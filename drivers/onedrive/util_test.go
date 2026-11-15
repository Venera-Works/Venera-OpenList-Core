package onedrive

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

func newTokenTestDriver(t *testing.T, handler http.HandlerFunc) *Onedrive {
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

	d := &Onedrive{
		Storage: model.Storage{Driver: config.Name, MountPath: "/onedrive-test"},
		Addition: Addition{
			Region:       "global",
			UseOnlineAPI: true,
			APIAddress:   "https://onedrive.test/refresh",
			RefreshToken: "original-refresh",
		},
		AccessToken: "expired-access",
	}
	if err := db.CreateStorage(&d.Storage); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestRequestRefreshRetriesOnce(t *testing.T) {
	for _, tc := range []struct {
		name            string
		rejectRefreshed bool
		noRetry         bool
		wantError       bool
		wantRequests    int
		wantRefreshes   int
	}{
		{name: "refresh_recovers", wantRequests: 2, wantRefreshes: 1},
		{name: "refreshed_token_rejected", rejectRefreshed: true, wantError: true, wantRequests: 2, wantRefreshes: 1},
		{name: "refresh_disabled", noRetry: true, wantError: true, wantRequests: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
				if req.Header.Get("X-Test") != "preserved" {
					t.Error("重试丢失请求回调")
				}
				wantToken := "Bearer expired-access"
				if requests > 1 {
					wantToken = "Bearer refreshed-access"
				}
				if req.Header.Get("Authorization") != wantToken {
					t.Errorf("Authorization = %q，预期 %q", req.Header.Get("Authorization"), wantToken)
				}
				// 回归时用不同错误终止第三次请求，避免测试无限递归。
				if requests > 2 {
					w.WriteHeader(http.StatusBadRequest)
					fmt.Fprint(w, `{"error":{"code":"UnexpectedRetry","message":"unexpected retry"}}`)
					return
				}
				if requests == 1 || tc.rejectRefreshed {
					w.WriteHeader(http.StatusUnauthorized)
					fmt.Fprint(w, `{"error":{"code":"InvalidAuthenticationToken","message":"token rejected"}}`)
					return
				}
				fmt.Fprint(w, `{"id":"root"}`)
			})
			var result struct {
				ID string `json:"id"`
			}
			body, err := d.Request(d.GetMetaUrl(false, "/"), http.MethodGet, func(req *resty.Request) {
				req.SetHeader("X-Test", "preserved")
			}, &result, tc.noRetry)
			if tc.wantError {
				if err == nil || err.Error() != "token rejected" || body != nil {
					t.Fatalf("预期令牌错误和空响应，实际响应 %q，错误 %v", body, err)
				}
			} else if err != nil || result.ID != "root" || string(body) != `{"id":"root"}` {
				t.Fatalf("刷新后未恢复响应：%q，结果 %+v，错误 %v", body, result, err)
			}
			if requests != tc.wantRequests || refreshes != tc.wantRefreshes {
				t.Fatalf("请求/刷新次数 = %d/%d，预期 %d/%d", requests, refreshes, tc.wantRequests, tc.wantRefreshes)
			}
			if refreshes != 0 {
				stored, err := db.GetStorageById(d.ID)
				if err != nil {
					t.Fatal(err)
				}
				var addition Addition
				if err := json.Unmarshal([]byte(stored.Addition), &addition); err != nil {
					t.Fatal(err)
				}
				if d.AccessToken != "refreshed-access" || addition.RefreshToken != "refreshed-refresh" {
					t.Fatal("刷新结果未更新访问令牌并持久化刷新令牌")
				}
			}
		})
	}
}

func TestGetDriveRefreshesExpiredToken(t *testing.T) {
	type contextKey struct{}
	for _, sharepoint := range []bool{false, true} {
		t.Run(fmt.Sprintf("sharepoint_%t", sharepoint), func(t *testing.T) {
			requests, refreshes := 0, 0
			wantPath := "/v1.0/me/drive"
			if sharepoint {
				wantPath = "/v1.0/sites/test-site/drive"
			}
			d := newTokenTestDriver(t, func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == "/refresh" {
					refreshes++
					fmt.Fprint(w, `{"access_token":"refreshed-access","refresh_token":"refreshed-refresh"}`)
					return
				}
				requests++
				if req.Method != http.MethodGet || req.URL.Path != wantPath || req.Context().Value(contextKey{}) != "quota" {
					t.Errorf("容量请求的路径、方法或上下文不正确：%s %s", req.Method, req.URL.Path)
				}
				if req.Header.Get("Authorization") != "Bearer refreshed-access" {
					w.WriteHeader(http.StatusUnauthorized)
					fmt.Fprint(w, `{"error":{"code":"InvalidAuthenticationToken","message":"expired token"}}`)
					return
				}
				fmt.Fprint(w, `{"id":"drive","quota":{"total":1000,"used":250}}`)
			})
			d.IsSharepoint, d.SiteId = sharepoint, "test-site"
			ctx := context.WithValue(context.Background(), contextKey{}, "quota")
			drive, err := d.getDrive(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if drive.ID != "drive" || drive.Quota.Total != 1000 || drive.Quota.Used != 250 {
				t.Fatalf("容量结果不正确：%+v", drive)
			}
			if requests != 2 || refreshes != 1 {
				t.Fatalf("请求/刷新次数 = %d/%d，预期 2/1", requests, refreshes)
			}
		})
	}
}
