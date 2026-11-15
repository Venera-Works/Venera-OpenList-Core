package openlistcore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/go-resty/resty/v2"
)

type cachedLinkDriver struct {
	driver.Driver
	links int
	gets  int
}

func (d *cachedLinkDriver) Init(context.Context) error { return nil }
func (d *cachedLinkDriver) Drop(context.Context) error { return nil }
func (d *cachedLinkDriver) GetRootId() string          { return "0" }
func (d *cachedLinkDriver) Get(context.Context, string) (model.Obj, error) {
	d.gets++
	return &model.Object{ID: "file", Name: "book.cbz", Size: 10}, nil
}
func (d *cachedLinkDriver) List(context.Context, model.Obj, model.ListArgs) ([]model.Obj, error) {
	return []model.Obj{&model.Object{ID: "file", Name: "book.cbz", Size: 10}}, nil
}
func (d *cachedLinkDriver) Link(context.Context, model.Obj, model.LinkArgs) (*model.Link, error) {
	d.links++
	expiration := time.Minute
	return &model.Link{URL: fmt.Sprintf("https://files.example/book.cbz?v=%d", d.links), Expiration: &expiration}, nil
}

func TestEmbeddedLinkReusesCacheAndRefreshesExplicitly(t *testing.T) {
	constructor, err := op.GetDriver("Quark")
	if err != nil {
		t.Fatal(err)
	}
	fake := &cachedLinkDriver{Driver: constructor()}
	op.RegisterDriver(func() driver.Driver { return fake })
	defer op.RegisterDriver(constructor)
	c := &core{}
	if _, err := c.initialize(t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	defer c.close()
	if _, err := c.saveAccount("cached", "Quark", "cache", json.RawMessage(`{"cookie":"test"}`)); err != nil {
		t.Fatal(err)
	}
	call := func(method, p string, refresh bool) {
		t.Helper()
		raw, err := json.Marshal(fileArguments{AccountID: "cached", Path: p, Refresh: refresh})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.fileOperation(method, raw); err != nil {
			t.Fatal(err)
		}
	}
	call("list", "/", false)
	call("link", "/book.cbz", false)
	call("link", "/book.cbz", false)
	if fake.links != 1 || fake.gets != 0 {
		t.Fatalf("ordinary reads missed caches: links=%d gets=%d", fake.links, fake.gets)
	}
	call("link", "/book.cbz", true)
	if fake.links != 2 || fake.gets != 1 {
		t.Fatalf("expired link was not refreshed: links=%d gets=%d", fake.links, fake.gets)
	}
	call("list", "/", true)
	call("link", "/book.cbz", false)
	if fake.links != 3 || fake.gets != 1 {
		t.Fatalf("directory refresh did not invalidate link: links=%d gets=%d", fake.links, fake.gets)
	}
}

func TestRateLimitResponsePreservesRetryAfter(t *testing.T) {
	err := fmt.Errorf("list directory: %w", &errs.RateLimitError{RetryAfter: "120"})
	result := errorResponse(err)
	if result.Code != "rate_limited" || result.RetryAfter != "120" {
		t.Fatalf("unexpected rate limit response: %+v", result)
	}
	for _, message := range []string{"HTTP 429 Too Many Requests", "code: 123, message: 操作过于频繁"} {
		if result := errorResponse(fmt.Errorf("%s", message)); result.Code != "rate_limited" {
			t.Fatalf("did not classify %q: %+v", message, result)
		}
	}
	if result := errorResponse(fmt.Errorf("file 429.jpg not found")); result.Code == "rate_limited" {
		t.Fatal("filename was classified as rate limiting")
	}
}

func TestRegisteredDriversMatchSupportedAccounts(t *testing.T) {
	drivers := op.GetDriverInfoMap()
	if len(drivers) != len(allowedDrivers)+1 {
		t.Fatalf("registered %d drivers, want %d", len(drivers), len(allowedDrivers)+1)
	}
	for name := range allowedDrivers {
		if _, ok := drivers[name]; !ok {
			t.Errorf("supported driver %q is not registered", name)
		}
	}
	if _, ok := drivers["UC"]; !ok {
		t.Error("quark_uc did not register UC")
	}
	if _, ok := drivers["QuarkOpen"]; ok {
		t.Error("QuarkOpen is still registered")
	}
	for _, name := range []string{"UC", "QuarkOpen"} {
		if _, ok := allowedDrivers[name]; ok {
			t.Errorf("unsupported account driver %q is allowed", name)
		}
	}
}

func TestCancelBeforeTaskRegistration(t *testing.T) {
	core := &core{tasks: map[string]*taskState{}, pendingCancels: map[string]struct{}{}}
	if _, err := core.taskOperation("cancel", "cancelled-before-start", "account-a"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := core.contextFor("cancelled-before-start", "account-a")
	defer cancel()
	if ctx.Err() != context.Canceled {
		t.Fatalf("early cancellation was not applied: %v", ctx.Err())
	}
	other, cancelOther := core.contextFor("unrelated-task", "account-a")
	defer cancelOther()
	if other.Err() != nil {
		t.Fatalf("early cancellation affected another task: %v", other.Err())
	}
	core.finishTask("cancelled-before-start", "")
	if _, err := core.taskOperation("releaseTask", "cancelled-before-start", ""); err != nil {
		t.Fatal(err)
	}

	if _, err := core.taskOperation("cancel", "released-before-start", "account-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := core.taskOperation("releaseTask", "released-before-start", ""); err != nil {
		t.Fatal(err)
	}
	fresh, cancelFresh := core.contextFor("released-before-start", "account-a")
	defer cancelFresh()
	if fresh.Err() != nil {
		t.Fatalf("release did not clear pending cancellation: %v", fresh.Err())
	}
	core.finishTask("unrelated-task", "")
	core.finishTask("released-before-start", "")
	if _, err := core.taskOperation("releaseTask", "unrelated-task", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := core.taskOperation("releaseTask", "released-before-start", ""); err != nil {
		t.Fatal(err)
	}

	core.tasksMu.RLock()
	pending := len(core.pendingCancels)
	tasks := len(core.tasks)
	core.tasksMu.RUnlock()
	if pending != 0 {
		t.Fatalf("pending cancellations leaked: %d", pending)
	}
	if tasks != 0 {
		t.Fatalf("released tasks leaked: %d", tasks)
	}
}

func TestNormalizeAdditionKeepsValuesAndAddsDefaults(t *testing.T) {
	addition, err := normalizeAddition("Quark", json.RawMessage(`{"cookie":"test-cookie"}`))
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err := json.Unmarshal([]byte(addition), &values); err != nil {
		t.Fatal(err)
	}
	if values["cookie"] != "test-cookie" {
		t.Fatalf("authorization values changed: %#v", values)
	}
	if values["root_folder_id"] != "0" || values["order_by"] != "none" || values["order_direction"] != "asc" {
		t.Fatalf("missing Quark defaults: %#v", values)
	}
	for _, key := range []string{"use_online_api", "api_url_address"} {
		if _, ok := values[key]; ok {
			t.Errorf("obsolete Quark default %q is present", key)
		}
	}
}

func TestSaveAccountReplacesQuarkOpenAndKeepsStorage(t *testing.T) {
	c := &core{tasks: map[string]*taskState{}, pendingCancels: map[string]struct{}{}}
	if _, err := c.initialize(t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.close(); err != nil {
			t.Error(err)
		}
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/config" || r.Header.Get("Cookie") != "test-cookie" {
			t.Errorf("unexpected Quark initialization request: %s, cookie %q", r.URL.Path, r.Header.Get("Cookie"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":200,"code":0}`))
	}))
	t.Cleanup(server.Close)
	base.RestyClient.OnBeforeRequest(func(_ *resty.Client, r *resty.Request) error {
		r.URL = server.URL + "/config"
		return nil
	})
	old := model.Storage{
		MountPath:       "/venera/existing-quark",
		Driver:          "QuarkOpen",
		Remark:          accountRemark("legacy-quark", "Old Quark"),
		Addition:        `{"root_folder_id":"existing-root","app_id":"old-app"}`,
		Disabled:        true,
		CacheExpiration: 42,
		Sort:            model.Sort{OrderBy: "name", OrderDirection: "desc"},
	}
	if err := db.CreateStorage(&old); err != nil {
		t.Fatal(err)
	}
	result, err := c.saveAccount("legacy-quark", "Quark", "Quark", json.RawMessage(`{"cookie":"test-cookie","root_folder_id":"existing-root"}`))
	if err != nil {
		t.Fatal(err)
	}
	if values := result.(map[string]any); values["id"] != "legacy-quark" || values["driver"] != "Quark" {
		t.Fatalf("unexpected saved account: %#v", values)
	}
	updated, err := c.findAccount("legacy-quark")
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != old.ID || updated.MountPath != old.MountPath || updated.CacheExpiration != old.CacheExpiration || updated.Sort != old.Sort {
		t.Fatalf("existing storage configuration changed: %#v", updated)
	}
	if updated.Driver != "Quark" || updated.Disabled || updated.Status != op.WORK || accountName(*updated) != "Quark" {
		t.Fatalf("legacy account was not replaced: %#v", updated)
	}
	var addition map[string]any
	if err := json.Unmarshal([]byte(updated.Addition), &addition); err != nil {
		t.Fatal(err)
	}
	if addition["cookie"] != "test-cookie" || addition["root_folder_id"] != "existing-root" {
		t.Fatalf("account addition was not preserved: %#v", addition)
	}
	loaded, err := c.storage("legacy-quark")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Config().Name != "Quark" || loaded.GetStorage().ID != old.ID {
		t.Fatalf("Quark driver was not loaded for the existing storage: %#v", loaded.GetStorage())
	}
	if _, err := c.saveAccount("legacy-quark", "BaiduNetdisk", "Other", json.RawMessage(`{}`)); err == nil || err.Error() != "account driver cannot be changed" {
		t.Fatalf("unexpected result changing Quark to another driver: %v", err)
	}
}

func TestInvokeInitializeCloseAndReopen(t *testing.T) {
	directory := t.TempDir()
	initializeRequest := func(dataDir string) string {
		arguments, err := json.Marshal(map[string]string{"dataDir": dataDir, "proxy": ""})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(request{Method: "initialize", Arguments: arguments})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	assertRequestRoundTrip := func(dataDir string) {
		var decoded request
		if err := json.Unmarshal([]byte(initializeRequest(dataDir)), &decoded); err != nil {
			t.Fatal(err)
		}
		var arguments struct {
			DataDir string `json:"dataDir"`
		}
		if err := json.Unmarshal(decoded.Arguments, &arguments); err != nil {
			t.Fatal(err)
		}
		if arguments.DataDir != dataDir {
			t.Fatalf("data directory changed during JSON encoding: %q", arguments.DataDir)
		}
	}
	assertRequestRoundTrip(`C:\\OpenList\\quoted"directory`)
	initialize := func() map[string]any {
		raw := Invoke(initializeRequest(directory))
		var response response
		if err := json.Unmarshal([]byte(raw), &response); err != nil {
			t.Fatal(err)
		}
		if response.Error != nil {
			t.Fatalf("initialize failed: %#v", response.Error)
		}
		return response.Data.(map[string]any)
	}
	initialized := initialize()
	if initialized["ready"] != true {
		t.Fatal("initialize did not report ready")
	}
	drivers := initialized["drivers"].([]any)
	if len(drivers) != len(allowedDrivers) {
		t.Fatalf("initialize returned %d drivers, want %d", len(drivers), len(allowedDrivers))
	}
	for _, driver := range drivers {
		if _, ok := allowedDrivers[driver.(string)]; !ok {
			t.Errorf("initialize returned unsupported driver %q", driver)
		}
	}
	if raw := Invoke(`{"method":"accounts","arguments":{}}`); raw == "" {
		t.Fatal("accounts returned no response")
	}
	if raw := Invoke(`{"method":"close","arguments":{}}`); raw == "" {
		t.Fatal("close returned no response")
	}
	if initialize()["ready"] != true {
		t.Fatal("reopen did not report ready")
	}
	Invoke(`{"method":"close","arguments":{}}`)
}
