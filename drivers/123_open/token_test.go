package _123_open

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/go-resty/resty/v2"
)

func TestOfficialRefreshErrors(t *testing.T) {
	previousClient := base.RestyClient
	base.RestyClient = resty.New()
	t.Cleanup(func() { base.RestyClient = previousClient })

	for _, tc := range []struct {
		name        string
		status      int
		contentType string
		body        string
		want        string
	}{
		{"official API error", 500, "application/json", `{"text":"refresh token rejected"}`, "refresh token rejected"},
		{"OAuth error", 401, "application/json", `{"error_description":"invalid grant"}`, "invalid grant"},
		{"rate limit", 429, "application/json", `{"message":"rate limited"}`, "rate limited"},
		{"non-JSON error", 503, "text/html", `<html>unavailable</html>`, "503 Service Unavailable"},
		{"HTTP error with tokens", 500, "application/json", `{"access_token":"unexpected-access","refresh_token":"unexpected-refresh","expires_in":3600}`, "500 Internal Server Error"},
		{"error in success response", 200, "application/json", `{"text":"authorization rejected"}`, "authorization rejected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("refresh_ui") != "original-refresh" ||
					r.URL.Query().Get("server_use") != "true" ||
					r.URL.Query().Get("driver_txt") != "123cloud_oa" {
					t.Error("incorrect official API refresh parameters")
				}
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			driver := &Open123{
				Addition: Addition{
					UseOnlineAPI: true,
					APIAddress:   server.URL,
					AccessToken:  "original-access",
					RefreshToken: "original-refresh",
				},
				tm: &tokenManager{},
			}
			err := driver.flushAccessToken()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
			if driver.AccessToken != "original-access" || driver.RefreshToken != "original-refresh" {
				t.Fatal("failed refresh changed stored tokens")
			}
		})
	}
}
