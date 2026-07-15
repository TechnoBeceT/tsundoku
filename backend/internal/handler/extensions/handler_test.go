// Package extensions_test exercises the extension-management HTTP handlers
// end-to-end through a real Echo instance (with RequireOwner + the central
// error middleware wired) against the shared sourceengine/fake.Client. No
// engine host or DB is needed.
package extensions_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	handler "github.com/technobecet/tsundoku/internal/handler/extensions"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	sourceenginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
)

const testSecret = "extensions-handler-test-secret"

// seededExt is the shared fixture extension for the handler tests below.
func seededExt() sourceengine.Extension {
	repo := "https://repo.test/index.min.json"
	return sourceengine.Extension{
		PkgName:     "pkg.test.one",
		Name:        "Test One",
		Lang:        "en",
		VersionName: "1.0.0",
		VersionCode: 1,
		IconURL:     "https://cdn.test/icon.png",
		RepoURL:     &repo,
		IsInstalled: false,
		HasUpdate:   false,
		IsNsfw:      true,
		Sources:     []sourceengine.Source{{ID: 7, Name: "Test Source", Lang: "en"}},
	}
}

type testEnv struct {
	e     *echo.Echo
	fake  *sourceenginefake.Client
	token string
}

// newTestEnv wires an Echo instance whose Handler holds fc directly (nil
// durable store: db/cache/httpGet) — these tests exercise the pure proxy
// behaviour; the best-effort topology write-through is covered separately
// (writethrough_hook_test.go).
func newTestEnv(t *testing.T, fc *sourceenginefake.Client) *testEnv {
	t.Helper()
	authSvc := auth.NewService(testSecret)
	h := handler.NewHandler(fc, nil, nil, nil)

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.GET("/suwayomi/extensions", h.List)
	authed.POST("/suwayomi/extensions/refresh", h.Refresh)
	authed.GET("/suwayomi/extensions/repos", h.GetRepos)
	authed.PUT("/suwayomi/extensions/repos", h.SetRepos)
	authed.POST("/suwayomi/extensions/:pkgName/install", h.Install)
	authed.POST("/suwayomi/extensions/:pkgName/update", h.Update)
	authed.DELETE("/suwayomi/extensions/:pkgName", h.Uninstall)
	authed.GET("/suwayomi/extensions/:pkgName/preferences", h.Preferences)
	authed.PATCH("/suwayomi/extensions/:pkgName/preferences", h.SetPreference)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}
	return &testEnv{e: e, fake: fc, token: token}
}

func (env *testEnv) do(method, target, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("Authorization", "Bearer "+env.token)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	return rec
}

// noAuth issues a request without a Bearer token (for 401 assertions).
func (env *testEnv) noAuth(method, target, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	return rec
}

// --- List --------------------------------------------------------------------

// TestList_OK proves GET returns the DTO list with the isInstalled/isNsfw
// casing surviving to the JSON, and the repoUrl + embedded sources round-trip.
func TestList_OK(t *testing.T) {
	env := newTestEnv(t, sourceenginefake.New(sourceenginefake.WithExtensions([]sourceengine.Extension{seededExt()})))
	rec := env.do(http.MethodGet, "/api/suwayomi/extensions", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("List: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got []handler.ExtensionDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 extension, got %d", len(got))
	}
	assertSeededExtDTO(t, got[0])
	// Assert the raw JSON key casing explicitly (footgun: isInstalled).
	raw := rec.Body.String()
	for _, key := range []string{`"pkgName"`, `"isInstalled"`, `"hasUpdate"`, `"isNsfw"`, `"iconUrl"`, `"repoUrl"`, `"sources"`} {
		if !strings.Contains(raw, key) {
			t.Errorf("response missing expected JSON key %s: %s", key, raw)
		}
	}
}

// assertSeededExtDTO checks e matches seededExt's identity/repoUrl/isNsfw/
// sources. Split out purely to keep TestList_OK's cyclomatic complexity
// within the project's cyclop gate.
func assertSeededExtDTO(t *testing.T, e handler.ExtensionDTO) {
	t.Helper()
	if e.PkgName != "pkg.test.one" || e.RepoURL == nil || *e.RepoURL != "https://repo.test/index.min.json" {
		t.Errorf("identity/repoUrl mismatch: %+v", e)
	}
	if !e.IsNsfw {
		t.Errorf("isNsfw casing not preserved: %+v", e)
	}
	if len(e.Sources) != 1 || e.Sources[0].ID != "7" || e.Sources[0].Name != "Test Source" {
		t.Errorf("sources not round-tripped: %+v", e.Sources)
	}
}

// TestList_IconURLPassesThroughRaw proves iconUrl is the engine host's OWN
// reported URL, served AS-IS — the retired Suwayomi icon proxy is gone
// (sourceengine has no PageBytes-shaped fetch to stream it through).
func TestList_IconURLPassesThroughRaw(t *testing.T) {
	env := newTestEnv(t, sourceenginefake.New(sourceenginefake.WithExtensions([]sourceengine.Extension{seededExt()})))
	rec := env.do(http.MethodGet, "/api/suwayomi/extensions", "")

	var got []handler.ExtensionDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 extension, got %d", len(got))
	}
	if got[0].IconURL != "https://cdn.test/icon.png" {
		t.Errorf("IconURL = %q, want the raw engine-reported URL unchanged", got[0].IconURL)
	}
}

// TestList_RepoURLNullWhenAbsent proves a nil sourceengine.Extension.RepoURL
// (sideloaded, no configured repo) serialises as JSON null, not "".
func TestList_RepoURLNullWhenAbsent(t *testing.T) {
	ext := seededExt()
	ext.RepoURL = nil
	env := newTestEnv(t, sourceenginefake.New(sourceenginefake.WithExtensions([]sourceengine.Extension{ext})))
	rec := env.do(http.MethodGet, "/api/suwayomi/extensions", "")
	if !strings.Contains(rec.Body.String(), `"repoUrl":null`) {
		t.Errorf("body = %s, want repoUrl:null", rec.Body.String())
	}
}

// TestList_EmptyIsArray proves an empty list serialises as [] (not null).
func TestList_EmptyIsArray(t *testing.T) {
	env := newTestEnv(t, sourceenginefake.New())
	rec := env.do(http.MethodGet, "/api/suwayomi/extensions", "")
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("empty list: want [], got %s", rec.Body.String())
	}
}

func TestList_Unauthorized(t *testing.T) {
	env := newTestEnv(t, sourceenginefake.New(sourceenginefake.WithExtensions([]sourceengine.Extension{seededExt()})))
	rec := env.noAuth(http.MethodGet, "/api/suwayomi/extensions", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("List no token: want 401, got %d", rec.Code)
	}
}

func TestList_Upstream502(t *testing.T) {
	env := newTestEnv(t, sourceenginefake.New(sourceenginefake.WithError("Extensions", errors.New("connection refused"))))
	rec := env.do(http.MethodGet, "/api/suwayomi/extensions", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("List upstream fail: want 502, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// --- Refresh -----------------------------------------------------------------

func TestRefresh_OK(t *testing.T) {
	ext := seededExt()
	ext.PkgName = "pkg.fetched"
	env := newTestEnv(t, sourceenginefake.New(sourceenginefake.WithExtensions([]sourceengine.Extension{ext})))
	rec := env.do(http.MethodPost, "/api/suwayomi/extensions/refresh", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("Refresh: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got []handler.ExtensionDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got) != 1 || got[0].PkgName != "pkg.fetched" {
		t.Errorf("Refresh returned wrong list: %+v", got)
	}
	if env.fake.CallCount("RefreshExtensions") != 1 {
		t.Errorf("RefreshExtensions calls = %d, want 1", env.fake.CallCount("RefreshExtensions"))
	}
}

func TestRefresh_Unauthorized(t *testing.T) {
	env := newTestEnv(t, sourceenginefake.New())
	rec := env.noAuth(http.MethodPost, "/api/suwayomi/extensions/refresh", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Refresh no token: want 401, got %d", rec.Code)
	}
}

func TestRefresh_Upstream502(t *testing.T) {
	env := newTestEnv(t, sourceenginefake.New(sourceenginefake.WithError("RefreshExtensions", errors.New("engine error"))))
	rec := env.do(http.MethodPost, "/api/suwayomi/extensions/refresh", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("Refresh upstream fail: want 502, got %d", rec.Code)
	}
}

// --- Mutating actions (install/update/uninstall) -----------------------------

// TestActions_RoundTrip proves each mutating endpoint calls the matching
// engine-host method and returns ITS OWN refreshed list in one call — no
// separate re-read is needed (unlike the retired Suwayomi shape): install/
// uninstall flip IsInstalled on the fake's stored extension, proving the
// response reflects post-mutation state, not a request echo.
func TestActions_RoundTrip(t *testing.T) {
	cases := []struct {
		name          string
		method        string
		path          string
		wantMethod    string
		wantInstalled bool
	}{
		{"install", http.MethodPost, "/api/suwayomi/extensions/pkg.test.one/install", "InstallExtension", true},
		{"update", http.MethodPost, "/api/suwayomi/extensions/pkg.test.one/update", "UpdateExtension", false},
		{"uninstall", http.MethodDelete, "/api/suwayomi/extensions/pkg.test.one", "UninstallExtension", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// seededExt has IsInstalled=false; install flips it to true.
			env := newTestEnv(t, sourceenginefake.New(sourceenginefake.WithExtensions([]sourceengine.Extension{seededExt()})))
			rec := env.do(tc.method, tc.path, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: want 200, got %d (%s)", tc.name, rec.Code, rec.Body.String())
			}
			if env.fake.CallCount(tc.wantMethod) != 1 {
				t.Fatalf("%s calls = %d, want 1", tc.wantMethod, env.fake.CallCount(tc.wantMethod))
			}
			var got []handler.ExtensionDTO
			_ = json.Unmarshal(rec.Body.Bytes(), &got)
			if len(got) != 1 || got[0].PkgName != "pkg.test.one" {
				t.Fatalf("response not from re-read: %+v", got)
			}
			if got[0].IsInstalled != tc.wantInstalled {
				t.Errorf("isInstalled = %v, want %v", got[0].IsInstalled, tc.wantInstalled)
			}
		})
	}
}

func TestActions_Unauthorized(t *testing.T) {
	cases := []struct {
		method, path, wantMethod string
	}{
		{http.MethodPost, "/api/suwayomi/extensions/pkg.test.one/install", "InstallExtension"},
		{http.MethodPost, "/api/suwayomi/extensions/pkg.test.one/update", "UpdateExtension"},
		{http.MethodDelete, "/api/suwayomi/extensions/pkg.test.one", "UninstallExtension"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			env := newTestEnv(t, sourceenginefake.New(sourceenginefake.WithExtensions([]sourceengine.Extension{seededExt()})))
			rec := env.noAuth(tc.method, tc.path, "")
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("want 401, got %d", rec.Code)
			}
			if env.fake.CallCount(tc.wantMethod) != 0 {
				t.Errorf("%s must not be called on an unauthorized request", tc.wantMethod)
			}
		})
	}
}

// TestActions_Upstream502 proves an engine-host failure on the mutating call
// itself is a 502 (there is no separate write/read-back split any more — the
// engine host returns the refreshed list from the SAME call).
func TestActions_Upstream502(t *testing.T) {
	env := newTestEnv(t, sourceenginefake.New(
		sourceenginefake.WithExtensions([]sourceengine.Extension{seededExt()}),
		sourceenginefake.WithError("InstallExtension", errors.New("engine rejected")),
	))
	rec := env.do(http.MethodPost, "/api/suwayomi/extensions/pkg.test.one/install", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("install upstream fail: want 502, got %d", rec.Code)
	}
}

// TestActions_BlankPkgName400 proves a whitespace-only pkgName (a "%20" path
// segment) is rejected with a 400 before any client call — exercising the
// validatePkgName empty-after-trim branch.
func TestActions_BlankPkgName400(t *testing.T) {
	env := newTestEnv(t, sourceenginefake.New(sourceenginefake.WithExtensions([]sourceengine.Extension{seededExt()})))
	rec := env.do(http.MethodPost, "/api/suwayomi/extensions/%20/install", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("blank pkgName: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if env.fake.CallCount("InstallExtension") != 0 {
		t.Error("validation failure must not call InstallExtension")
	}
}

// --- Repos -------------------------------------------------------------------

func TestGetRepos_OK(t *testing.T) {
	env := newTestEnv(t, sourceenginefake.New(sourceenginefake.WithRepos([]string{"https://a.test/i.json"})))
	rec := env.do(http.MethodGet, "/api/suwayomi/extensions/repos", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GetRepos: want 200, got %d", rec.Code)
	}
	var got handler.ExtensionReposDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Repos) != 1 || got.Repos[0] != "https://a.test/i.json" {
		t.Errorf("GetRepos: unexpected %+v", got)
	}
}

// TestGetRepos_EmptyIsArray proves a nil repo list serialises as [].
func TestGetRepos_EmptyIsArray(t *testing.T) {
	env := newTestEnv(t, sourceenginefake.New())
	rec := env.do(http.MethodGet, "/api/suwayomi/extensions/repos", "")
	if !strings.Contains(rec.Body.String(), `"repos":[]`) {
		t.Errorf("empty repos: want repos:[], got %s", rec.Body.String())
	}
}

func TestGetRepos_Unauthorized(t *testing.T) {
	env := newTestEnv(t, sourceenginefake.New(sourceenginefake.WithRepos([]string{"https://a.test/i.json"})))
	rec := env.noAuth(http.MethodGet, "/api/suwayomi/extensions/repos", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GetRepos no token: want 401, got %d", rec.Code)
	}
}

func TestGetRepos_Upstream502(t *testing.T) {
	env := newTestEnv(t, sourceenginefake.New(sourceenginefake.WithError("Repos", errors.New("boom"))))
	rec := env.do(http.MethodGet, "/api/suwayomi/extensions/repos", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("GetRepos upstream fail: want 502, got %d", rec.Code)
	}
}

// TestSetRepos_RoundTrip proves PUT replaces the list and the response
// reflects the engine host's own echoed-back list (SetRepos already returns
// it — no separate re-read call).
func TestSetRepos_RoundTrip(t *testing.T) {
	env := newTestEnv(t, sourceenginefake.New(sourceenginefake.WithRepos([]string{"https://old.test/i.json"})))
	body := `{"repos":["https://new1.test/i.json","https://new2.test/i.json"]}`
	rec := env.do(http.MethodPut, "/api/suwayomi/extensions/repos", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("SetRepos: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if env.fake.CallCount("SetRepos") != 1 {
		t.Fatal("SetRepos was not called")
	}
	var got handler.ExtensionReposDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Repos) != 2 || got.Repos[0] != "https://new1.test/i.json" {
		t.Errorf("SetRepos round-trip: unexpected %+v", got)
	}
}

// TestSetRepos_EmptyClears proves an explicit empty array is accepted (clear-all).
func TestSetRepos_EmptyClears(t *testing.T) {
	env := newTestEnv(t, sourceenginefake.New(sourceenginefake.WithRepos([]string{"https://old.test/i.json"})))
	rec := env.do(http.MethodPut, "/api/suwayomi/extensions/repos", `{"repos":[]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("SetRepos empty: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got handler.ExtensionReposDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Repos == nil || len(got.Repos) != 0 {
		t.Errorf("cleared repos should serialise as []: %+v", got)
	}
}

// TestSetRepos_TrimsAndPasses proves whitespace is trimmed before reaching the client.
func TestSetRepos_TrimsAndPasses(t *testing.T) {
	env := newTestEnv(t, sourceenginefake.New())
	rec := env.do(http.MethodPut, "/api/suwayomi/extensions/repos", `{"repos":["  https://a.test/i.json  "]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("SetRepos trim: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got handler.ExtensionReposDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Repos) != 1 || got.Repos[0] != "https://a.test/i.json" {
		t.Errorf("repo not trimmed: %v", got.Repos)
	}
}

func TestSetRepos_Unauthorized(t *testing.T) {
	env := newTestEnv(t, sourceenginefake.New())
	rec := env.noAuth(http.MethodPut, "/api/suwayomi/extensions/repos", `{"repos":["https://a.test/i.json"]}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("SetRepos no token: want 401, got %d", rec.Code)
	}
	if env.fake.CallCount("SetRepos") != 0 {
		t.Error("SetRepos must not be called on an unauthorized request")
	}
}

// TestSetRepos_Validation400 table-tests the fail-closed validation cases. Each
// must be a 400 AND must not reach the downstream client.
func TestSetRepos_Validation400(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"missing repos key", `{}`},
		{"null repos", `{"repos":null}`},
		{"blank entry", `{"repos":["   "]}`},
		{"relative url", `{"repos":["/not/absolute"]}`},
		{"non-http scheme", `{"repos":["ftp://a.test/i.json"]}`},
		{"no host", `{"repos":["http://"]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, sourceenginefake.New())
			rec := env.do(http.MethodPut, "/api/suwayomi/extensions/repos", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("want 400, got %d (%s)", rec.Code, rec.Body.String())
			}
			if env.fake.CallCount("SetRepos") != 0 {
				t.Error("validation failure must not call SetRepos")
			}
		})
	}
}

// TestSetRepos_InvalidJSON proves a malformed body is a 400.
func TestSetRepos_InvalidJSON(t *testing.T) {
	env := newTestEnv(t, sourceenginefake.New())
	rec := env.do(http.MethodPut, "/api/suwayomi/extensions/repos", `{not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid json: want 400, got %d", rec.Code)
	}
}

// TestSetRepos_Upstream502 proves a downstream SetRepos failure is a 502.
func TestSetRepos_Upstream502(t *testing.T) {
	env := newTestEnv(t, sourceenginefake.New(sourceenginefake.WithError("SetRepos", errors.New("engine rejected"))))
	rec := env.do(http.MethodPut, "/api/suwayomi/extensions/repos", `{"repos":["https://a.test/i.json"]}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("SetRepos upstream fail: want 502, got %d", rec.Code)
	}
}
