package baidu_netdisk

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/glebarez/sqlite"
	"github.com/go-resty/resty/v2"
	"gorm.io/gorm"
)

type tokenTestTransport func(*http.Request) (*http.Response, error)

func (f tokenTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newTokenTestDriver(t *testing.T, handler http.HandlerFunc) *BaiduNetdisk {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	previousConfig := conf.Conf
	conf.Conf = conf.DefaultConfig(t.TempDir())
	t.Cleanup(func() { conf.Conf = previousConfig })
	if err := db.InitWithError(database); err != nil {
		t.Fatal(err)
	}

	previousClient := base.RestyClient
	base.RestyClient = resty.New().SetTransport(tokenTestTransport(func(req *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		recorder.Header().Set("Content-Type", "application/json")
		handler(recorder, req)
		return recorder.Result(), nil
	}))
	t.Cleanup(func() { base.RestyClient = previousClient })

	d := &BaiduNetdisk{
		Storage: model.Storage{Driver: config.Name, MountPath: "/baidu-test"},
		Addition: Addition{
			UseOnlineAPI: true,
			APIAddress:   "https://baidu.test/refresh",
			AccessToken:  "expired-access",
			RefreshToken: "original-refresh",
		},
	}
	if err := db.CreateStorage(&d.Storage); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestRequestTokenRefreshConditions(t *testing.T) {
	for _, tc := range []struct {
		errno       int
		wantRefresh bool
	}{
		{errno: 111, wantRefresh: true},
		{errno: -6, wantRefresh: true},
		{errno: 20016, wantRefresh: true},
		{errno: 20017, wantRefresh: true},
		{errno: 31034},
	} {
		t.Run(strconv.Itoa(tc.errno), func(t *testing.T) {
			requests, refreshes := 0, 0
			d := newTokenTestDriver(t, func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == "/refresh" {
					refreshes++
					if req.URL.Query().Get("refresh_ui") != "original-refresh" {
						t.Error("刷新请求未使用原刷新令牌")
					}
					fmt.Fprint(w, `{"access_token":"refreshed-access","refresh_token":"refreshed-refresh"}`)
					return
				}
				requests++
				wantToken := "expired-access"
				if requests > 1 && tc.wantRefresh {
					wantToken = "refreshed-access"
				}
				if req.URL.Query().Get("access_token") != wantToken || req.URL.Query().Get("method") != "list" {
					t.Errorf("文件请求的令牌或回调参数不正确：%s", req.URL.RawQuery)
				}
				if requests == 1 {
					fmt.Fprintf(w, `{"errno":%d}`, tc.errno)
					return
				}
				fmt.Fprint(w, `{"errno":0,"list":[{"fs_id":42,"server_filename":"test.cbz"}]}`)
			})
			var result ListResp
			_, err := d.get("/xpan/file", map[string]string{"method": "list"}, &result)
			if err != nil || result.Errno != 0 || len(result.List) != 1 || result.List[0].FsId != 42 || result.List[0].ServerFilename != "test.cbz" {
				t.Fatalf("重试未恢复文件列表：%+v，错误 %v", result, err)
			}
			wantRefreshes := 0
			if tc.wantRefresh {
				wantRefreshes = 1
			}
			if requests != 2 || refreshes != wantRefreshes {
				t.Fatalf("请求/刷新次数 = %d/%d，预期 2/%d", requests, refreshes, wantRefreshes)
			}
			if tc.wantRefresh {
				stored, err := db.GetStorageById(d.ID)
				if err != nil {
					t.Fatal(err)
				}
				var addition Addition
				if err := json.Unmarshal([]byte(stored.Addition), &addition); err != nil {
					t.Fatal(err)
				}
				if addition.AccessToken != "refreshed-access" || addition.RefreshToken != "refreshed-refresh" {
					t.Fatal("刷新令牌未持久化")
				}
			} else if d.AccessToken != "expired-access" || d.RefreshToken != "original-refresh" {
				t.Fatal("非令牌错误改变了令牌")
			}
		})
	}
}

func TestRequestStopsWhenTokenRefreshFails(t *testing.T) {
	requests, refreshes := 0, 0
	d := newTokenTestDriver(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/refresh" {
			refreshes++
			fmt.Fprint(w, `{"text":"refresh rejected"}`)
			return
		}
		requests++
		fmt.Fprint(w, `{"errno":20016}`)
	})
	body, err := d.get("/xpan/file", map[string]string{"method": "list"}, nil)
	if err == nil || !strings.Contains(err.Error(), "refresh rejected") || body != nil {
		t.Fatalf("预期刷新错误和空响应，实际响应 %q，错误 %v", body, err)
	}
	if requests != 1 || refreshes != 1 || d.AccessToken != "expired-access" || d.RefreshToken != "original-refresh" {
		t.Fatalf("刷新失败后继续请求或改变了令牌：请求/刷新 %d/%d", requests, refreshes)
	}
}
