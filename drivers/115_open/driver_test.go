package _115_open

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	sdk "github.com/OpenListTeam/115-sdk-go"
	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestEmbeddedTransportPreservesRateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "90")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	previous := base.HttpClient
	base.HttpClient = server.Client()
	defer func() { base.HttpClient = previous }()
	request, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := (embedded115Transport{}).RoundTrip(request)
	var limited *errs.RateLimitError
	if response != nil || !errors.As(err, &limited) || limited.RetryAfter != "90" {
		t.Fatalf("unexpected rate limit result: response=%v error=%v", response, err)
	}
}

var (
	setupDBOnce sync.Once
	setupDBErr  error
	rowSeq      int64
)

// 使用内存数据库验证真实持久化结果，不替换令牌保存逻辑。
func newTestDriver(t *testing.T, accessToken, refreshToken string) *Open115 {
	t.Helper()
	setupDBOnce.Do(func() {
		var gormDB *gorm.DB
		gormDB, setupDBErr = gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
		if setupDBErr != nil {
			return
		}
		conf.Conf = conf.DefaultConfig("testdata")
		setupDBErr = db.InitWithError(gormDB)
	})
	if setupDBErr != nil {
		t.Fatalf("failed to set up test database: %v", setupDBErr)
	}
	addition := Addition{AccessToken: accessToken, RefreshToken: refreshToken}
	addition.RootFolderID = "0"
	encoded, err := json.Marshal(addition)
	if err != nil {
		t.Fatal(err)
	}
	rowSeq++
	row := &model.Storage{
		Driver:    "115 Open",
		MountPath: fmt.Sprintf("/115open-test-%d", rowSeq),
		Addition:  string(encoded),
	}
	if err := db.CreateStorage(row); err != nil {
		t.Fatal(err)
	}
	d := &Open115{Storage: *row, Addition: addition}
	d.client = sdk.New(sdk.WithAccessToken(accessToken), sdk.WithRefreshToken(refreshToken))
	d.client.SetOnRefreshToken(d.onTokenRefreshed)
	return d
}

func persistedAddition(t *testing.T, id uint) Addition {
	t.Helper()
	storage, err := db.GetStorageById(id)
	if err != nil {
		t.Fatal(err)
	}
	var addition Addition
	if err := json.Unmarshal([]byte(storage.Addition), &addition); err != nil {
		t.Fatal(err)
	}
	return addition
}

func clientField(c *sdk.Client, name string) string {
	return reflect.ValueOf(c).Elem().FieldByName(name).String()
}

func TestOnTokenRefreshedPreservesUsableTokens(t *testing.T) {
	for _, tc := range []struct {
		name         string
		accessToken  string
		refreshToken string
		want         [2]string
	}{
		{"missing refresh token", "new-access", "", [2]string{"new-access", "old-refresh"}},
		{"missing access token", "", "new-refresh", [2]string{"old-access", "new-refresh"}},
		{"empty response", "", "", [2]string{"old-access", "old-refresh"}},
		{"rotated pair", "new-access", "new-refresh", [2]string{"new-access", "new-refresh"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newTestDriver(t, "old-access", "old-refresh")
			// 模拟 SDK 在调用刷新回调前已使用响应字段覆盖内部状态。
			d.client.SetAccessToken(tc.accessToken)
			d.client.SetRefreshToken(tc.refreshToken)
			d.onTokenRefreshed(tc.accessToken, tc.refreshToken)

			stored := persistedAddition(t, d.ID)
			for source, got := range map[string][2]string{
				"addition":   {d.Addition.AccessToken, d.Addition.RefreshToken},
				"database":   {stored.AccessToken, stored.RefreshToken},
				"sdk client": {clientField(d.client, "accessToken"), clientField(d.client, "refreshToken")},
			} {
				if got != tc.want {
					t.Errorf("%s tokens = %q, want %q", source, got, tc.want)
				}
			}
		})
	}
}

func TestInitRejectsMissingTokens(t *testing.T) {
	d := &Open115{}
	if err := d.Init(context.Background()); err == nil || err.Error() != "access token or refresh token is required" {
		t.Fatalf("unexpected initialization error: %v", err)
	}
	if d.client != nil {
		t.Fatal("SDK client created without credentials")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestInitUsesEmbeddedClientBeforeRequests(t *testing.T) {
	for _, tc := range []struct {
		name        string
		accessToken string
		refreshFail bool
		wantAccess  string
		wantPaths   []string
	}{
		{"existing access token", "old-access", false, "old-access", []string{"/open/user/info"}},
		{"refresh token only", "", false, "new-access", []string{"/open/refreshToken", "/open/user/info"}},
		{"refresh failure", "", true, "", []string{"/open/refreshToken"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newTestDriver(t, tc.accessToken, "old-refresh")
			previousEmbedded, previousClient := conf.Embedded, base.HttpClient
			conf.Embedded = true
			t.Cleanup(func() {
				conf.Embedded, base.HttpClient = previousEmbedded, previousClient
			})
			var paths []string
			// 拦截 SDK 的全部请求，验证首次刷新也走嵌入式传输，不连接 115。
			base.HttpClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				paths = append(paths, request.URL.Path)
				var body string
				switch request.URL.Path {
				case "/open/refreshToken":
					if err := request.ParseForm(); err != nil {
						return nil, err
					}
					if request.Method != http.MethodPost || request.Form.Get("refresh_token") != "old-refresh" {
						t.Errorf("unexpected refresh request: method=%s form=%v", request.Method, request.Form)
					}
					body = `{"code":0,"data":{"access_token":"new-access","refresh_token":"new-refresh"}}`
					if tc.refreshFail {
						body = `{"code":40140114,"message":"refresh rejected"}`
					}
				case "/open/user/info":
					if got := request.Header.Get("Authorization"); got != "Bearer "+tc.wantAccess {
						t.Errorf("authorization = %q, want %q", got, "Bearer "+tc.wantAccess)
					}
					body = `{"state":true,"data":{"user_id":1}}`
				default:
					return nil, fmt.Errorf("unexpected request: %s", request.URL)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(body)),
					Request:    request,
				}, nil
			})}

			err := d.Init(context.Background())
			if tc.refreshFail {
				if err == nil || !strings.Contains(err.Error(), "refresh rejected") {
					t.Errorf("refresh error = %v, want refresh rejected", err)
				}
			} else {
				if err != nil {
					t.Fatalf("initialization failed: %v", err)
				}
				stored := persistedAddition(t, d.ID)
				if stored.AccessToken != tc.wantAccess {
					t.Errorf("persisted access token = %q, want %q", stored.AccessToken, tc.wantAccess)
				}
				if tc.accessToken == "" && stored.RefreshToken != "new-refresh" {
					t.Errorf("persisted refresh token = %q, want new-refresh", stored.RefreshToken)
				}
			}
			if !reflect.DeepEqual(paths, tc.wantPaths) {
				t.Errorf("request paths = %v, want %v", paths, tc.wantPaths)
			}
		})
	}
}
