// Package extensions_test exercises the Suwayomi extension-management HTTP
// handlers end-to-end through a real Echo instance (with RequireOwner + the
// central error middleware wired) against a fake suwayomi.Client. No Suwayomi or
// DB is needed.
package extensions_test

import (
	"context"
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
	suwayomicli "github.com/technobecet/tsundoku/internal/suwayomi"
)

const testSecret = "extensions-handler-test-secret"

// fakeClient is a suwayomi.Client whose embedded nil interface satisfies every
// method at compile time; only the five extension methods are overridden (the
// rest are never called by these handlers and would panic if they were).
//
// It models a real Suwayomi: a SetExtensionState toggles `installedFlip` so the
// re-read returns the FLIPPED isInstalled — proving the handler re-reads rather
// than echoing the request. SetExtensionRepos replaces `repos` so the PUT re-read
// returns the persisted list.
type fakeClient struct {
	suwayomicli.Client

	exts          []suwayomicli.Extension
	repos         []string
	fetched       []suwayomicli.Extension
	listErr       error
	setStateErr   error
	fetchErr      error
	getReposErr   error
	setReposErr   error
	installedFlip bool // when true, Extensions() flips IsInstalled on every entry

	// pageBytes backs Icon's coverproxy.Stream call. nil means "not configured";
	// Icon tests that don't reach PageBytes never trip this.
	pageBytes func(ctx context.Context, pageURL string) ([]byte, string, error)

	setStateCalled bool
	lastAction     suwayomicli.ExtensionAction
	lastPkgName    string
	setReposCalled bool
	lastRepos      []string

	// Per-source preference state (the M3 "Configure" endpoints).
	sources         []suwayomicli.Source
	sourcesErr      error
	prefsBySource   map[string][]suwayomicli.SourcePreference
	extSourcesErr   error
	prefsErr        error
	setPrefErr      error
	setPrefCalled   bool
	lastSetSourceID string
	lastSetPosition int
	lastSetValue    suwayomicli.PreferenceValue
	setPrefResult   []suwayomicli.SourcePreference

	// Per-language enable/disable toggle state.
	setEnabledErr       error
	setEnabledCalled    bool
	lastEnabledSourceID string
	lastEnabledValue    bool
}

// ExtensionSources returns the configured per-language sources (or the seeded error).
func (f *fakeClient) ExtensionSources(_ context.Context, _ string) ([]suwayomicli.Source, error) {
	if f.extSourcesErr != nil {
		return nil, f.extSourcesErr
	}
	return f.sources, nil
}

// Sources returns the configured source list (or the seeded error) — used by
// the enable/disable toggle's post-write re-read (§16).
func (f *fakeClient) Sources(context.Context) ([]suwayomicli.Source, error) {
	if f.sourcesErr != nil {
		return nil, f.sourcesErr
	}
	return f.sources, nil
}

// SetSourceEnabled models a real Suwayomi: it mutates the matching source's
// Disabled flag in `sources` so a post-write Sources() re-read observes the
// new state (proving the handler re-reads rather than echoing the request).
func (f *fakeClient) SetSourceEnabled(_ context.Context, sourceID string, enabled bool) error {
	f.setEnabledCalled = true
	f.lastEnabledSourceID = sourceID
	f.lastEnabledValue = enabled
	if f.setEnabledErr != nil {
		return f.setEnabledErr
	}
	for i := range f.sources {
		if f.sources[i].ID == sourceID {
			f.sources[i].Disabled = !enabled
		}
	}
	return nil
}

// SourcePreferences returns the configured preferences for sourceID (or the error).
func (f *fakeClient) SourcePreferences(_ context.Context, sourceID string) ([]suwayomicli.SourcePreference, error) {
	if f.prefsErr != nil {
		return nil, f.prefsErr
	}
	return f.prefsBySource[sourceID], nil
}

// SetSourcePreference records the write and returns setPrefResult (the authoritative
// refreshed list the handler must echo back, §16), or the seeded error.
func (f *fakeClient) SetSourcePreference(_ context.Context, sourceID string, position int, value suwayomicli.PreferenceValue) ([]suwayomicli.SourcePreference, error) {
	f.setPrefCalled = true
	f.lastSetSourceID = sourceID
	f.lastSetPosition = position
	f.lastSetValue = value
	if f.setPrefErr != nil {
		return nil, f.setPrefErr
	}
	return f.setPrefResult, nil
}

// PageBytes backs the icon proxy (coverproxy.Stream calls this). Only Icon
// tests configure it; every other test leaves it nil (unreached).
func (f *fakeClient) PageBytes(ctx context.Context, pageURL string) ([]byte, string, error) {
	if f.pageBytes != nil {
		return f.pageBytes(ctx, pageURL)
	}
	return nil, "", errors.New("PageBytes: not configured")
}

func (f *fakeClient) Extensions(context.Context) ([]suwayomicli.Extension, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if !f.installedFlip {
		return f.exts, nil
	}
	out := make([]suwayomicli.Extension, len(f.exts))
	for i, e := range f.exts {
		e.IsInstalled = !e.IsInstalled
		out[i] = e
	}
	return out, nil
}

func (f *fakeClient) SetExtensionState(_ context.Context, pkgName string, action suwayomicli.ExtensionAction) error {
	f.setStateCalled = true
	f.lastPkgName = pkgName
	f.lastAction = action
	return f.setStateErr
}

func (f *fakeClient) FetchExtensions(context.Context) ([]suwayomicli.Extension, error) {
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	return f.fetched, nil
}

func (f *fakeClient) ExtensionRepos(context.Context) ([]string, error) {
	if f.getReposErr != nil {
		return nil, f.getReposErr
	}
	return f.repos, nil
}

func (f *fakeClient) SetExtensionRepos(_ context.Context, repos []string) error {
	f.setReposCalled = true
	f.lastRepos = repos
	if f.setReposErr != nil {
		return f.setReposErr
	}
	f.repos = repos
	return nil
}

func seededExt() suwayomicli.Extension {
	return suwayomicli.Extension{
		PkgName:     "pkg.test.one",
		Name:        "Test One",
		Lang:        "en",
		VersionName: "1.0.0",
		VersionCode: 1,
		IconURL:     "http://suwayomi/icon.png",
		Repo:        "https://repo.test/index.min.json",
		IsInstalled: false,
		HasUpdate:   false,
		IsNsfw:      true,
		IsObsolete:  true,
	}
}

type testEnv struct {
	e     *echo.Echo
	fake  *fakeClient
	token string
}

func newTestEnv(t *testing.T, fc *fakeClient) *testEnv {
	t.Helper()
	authSvc := auth.NewService(testSecret)
	// nil durable store (db/cache/httpGet): these tests exercise the pure proxy
	// behaviour; the best-effort topology write-through is covered separately
	// (writethrough_test.go).
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
	authed.GET("/suwayomi/extensions/:pkgName/icon", h.Icon)
	authed.GET("/suwayomi/extensions/:pkgName/preferences", h.Preferences)
	authed.PATCH("/suwayomi/extensions/:pkgName/preferences", h.SetPreference)
	authed.PATCH("/suwayomi/sources/:sourceId/enabled", h.SetSourceEnabled)

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

// TestList_OK proves GET returns the DTO list with the isInstalled/isObsolete
// casing surviving to the JSON.
func TestList_OK(t *testing.T) {
	env := newTestEnv(t, &fakeClient{exts: []suwayomicli.Extension{seededExt()}})
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
	e := got[0]
	if e.PkgName != "pkg.test.one" || e.Repo != "https://repo.test/index.min.json" {
		t.Errorf("identity/repo mismatch: %+v", e)
	}
	if !e.IsNsfw || !e.IsObsolete {
		t.Errorf("isNsfw/isObsolete casing not preserved: %+v", e)
	}
	// Assert the raw JSON key casing explicitly (footgun: isInstalled, isObsolete).
	raw := rec.Body.String()
	for _, key := range []string{`"pkgName"`, `"isInstalled"`, `"isObsolete"`, `"hasUpdate"`, `"isNsfw"`, `"iconUrl"`} {
		if !strings.Contains(raw, key) {
			t.Errorf("response missing expected JSON key %s: %s", key, raw)
		}
	}
}

// TestList_IconURLIsProxyPath proves the DTO rewrites IconURL to the Tsundoku
// same-origin icon proxy path — the raw cross-origin Suwayomi URL the fixture
// seeds must never reach the client (M1 bugfix). Split from TestList_OK to
// keep that test's cyclomatic complexity within the project's gate.
func TestList_IconURLIsProxyPath(t *testing.T) {
	env := newTestEnv(t, &fakeClient{exts: []suwayomicli.Extension{seededExt()}})
	rec := env.do(http.MethodGet, "/api/suwayomi/extensions", "")

	var got []handler.ExtensionDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 extension, got %d", len(got))
	}
	const wantIconURL = "/api/suwayomi/extensions/pkg.test.one/icon"
	if got[0].IconURL != wantIconURL {
		t.Errorf("IconURL = %q, want proxy path %q (raw Suwayomi URL must not leak)", got[0].IconURL, wantIconURL)
	}
}

// TestList_EmptyIsArray proves an empty list serialises as [] (not null).
func TestList_EmptyIsArray(t *testing.T) {
	env := newTestEnv(t, &fakeClient{})
	rec := env.do(http.MethodGet, "/api/suwayomi/extensions", "")
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("empty list: want [], got %s", rec.Body.String())
	}
}

func TestList_Unauthorized(t *testing.T) {
	env := newTestEnv(t, &fakeClient{exts: []suwayomicli.Extension{seededExt()}})
	rec := env.noAuth(http.MethodGet, "/api/suwayomi/extensions", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("List no token: want 401, got %d", rec.Code)
	}
}

func TestList_Upstream502(t *testing.T) {
	env := newTestEnv(t, &fakeClient{listErr: errors.New("connection refused")})
	rec := env.do(http.MethodGet, "/api/suwayomi/extensions", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("List upstream fail: want 502, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// --- Refresh -----------------------------------------------------------------

func TestRefresh_OK(t *testing.T) {
	ext := seededExt()
	ext.PkgName = "pkg.fetched"
	env := newTestEnv(t, &fakeClient{fetched: []suwayomicli.Extension{ext}})
	rec := env.do(http.MethodPost, "/api/suwayomi/extensions/refresh", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("Refresh: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got []handler.ExtensionDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got) != 1 || got[0].PkgName != "pkg.fetched" {
		t.Errorf("Refresh returned wrong list: %+v", got)
	}
}

func TestRefresh_Unauthorized(t *testing.T) {
	env := newTestEnv(t, &fakeClient{})
	rec := env.noAuth(http.MethodPost, "/api/suwayomi/extensions/refresh", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Refresh no token: want 401, got %d", rec.Code)
	}
}

func TestRefresh_Upstream502(t *testing.T) {
	env := newTestEnv(t, &fakeClient{fetchErr: errors.New("graphql error")})
	rec := env.do(http.MethodPost, "/api/suwayomi/extensions/refresh", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("Refresh upstream fail: want 502, got %d", rec.Code)
	}
}

// --- Mutating actions (install/update/uninstall) -----------------------------

// TestActions_RoundTrip proves each mutating endpoint calls SetExtensionState
// with the right action+pkgName, then RE-READS (the fake flips isInstalled on
// re-read) so the response reflects the flipped state — proving a re-read, not an
// echo of the request.
func TestActions_RoundTrip(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		action suwayomicli.ExtensionAction
	}{
		{"install", http.MethodPost, "/api/suwayomi/extensions/pkg.test.one/install", suwayomicli.ExtensionInstall},
		{"update", http.MethodPost, "/api/suwayomi/extensions/pkg.test.one/update", suwayomicli.ExtensionUpdate},
		{"uninstall", http.MethodDelete, "/api/suwayomi/extensions/pkg.test.one", suwayomicli.ExtensionUninstall},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// seededExt has IsInstalled=false; installedFlip makes the re-read true.
			env := newTestEnv(t, &fakeClient{exts: []suwayomicli.Extension{seededExt()}, installedFlip: true})
			rec := env.do(tc.method, tc.path, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: want 200, got %d (%s)", tc.name, rec.Code, rec.Body.String())
			}
			if !env.fake.setStateCalled {
				t.Fatal("SetExtensionState was not called")
			}
			if env.fake.lastAction != tc.action {
				t.Errorf("action = %q, want %q", env.fake.lastAction, tc.action)
			}
			if env.fake.lastPkgName != "pkg.test.one" {
				t.Errorf("pkgName = %q, want pkg.test.one", env.fake.lastPkgName)
			}
			var got []handler.ExtensionDTO
			_ = json.Unmarshal(rec.Body.Bytes(), &got)
			if len(got) != 1 || !got[0].IsInstalled {
				t.Errorf("response not from re-read (isInstalled should be flipped to true): %+v", got)
			}
		})
	}
}

func TestActions_Unauthorized(t *testing.T) {
	cases := []struct {
		method, path string
	}{
		{http.MethodPost, "/api/suwayomi/extensions/pkg.test.one/install"},
		{http.MethodPost, "/api/suwayomi/extensions/pkg.test.one/update"},
		{http.MethodDelete, "/api/suwayomi/extensions/pkg.test.one"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			env := newTestEnv(t, &fakeClient{exts: []suwayomicli.Extension{seededExt()}})
			rec := env.noAuth(tc.method, tc.path, "")
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("want 401, got %d", rec.Code)
			}
			if env.fake.setStateCalled {
				t.Error("SetExtensionState must not be called on an unauthorized request")
			}
		})
	}
}

// TestActions_SetState502 proves an upstream failure of SetExtensionState is 502
// and no re-read masks it.
func TestActions_SetState502(t *testing.T) {
	env := newTestEnv(t, &fakeClient{exts: []suwayomicli.Extension{seededExt()}, setStateErr: errors.New("graphql rejected")})
	rec := env.do(http.MethodPost, "/api/suwayomi/extensions/pkg.test.one/install", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("setState upstream fail: want 502, got %d", rec.Code)
	}
}

// TestActions_ReadBack502 proves a failure of the post-write re-read is also a 502.
func TestActions_ReadBack502(t *testing.T) {
	env := newTestEnv(t, &fakeClient{listErr: errors.New("connection reset")})
	rec := env.do(http.MethodPost, "/api/suwayomi/extensions/pkg.test.one/install", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("read-back fail: want 502, got %d", rec.Code)
	}
	if !env.fake.setStateCalled {
		t.Error("the write should have been attempted before the read-back")
	}
}

// TestActions_BlankPkgName400 proves a whitespace-only pkgName (a "%20" path
// segment) is rejected with a 400 before any client call — exercising the
// validatePkgName empty-after-trim branch.
func TestActions_BlankPkgName400(t *testing.T) {
	env := newTestEnv(t, &fakeClient{exts: []suwayomicli.Extension{seededExt()}})
	rec := env.do(http.MethodPost, "/api/suwayomi/extensions/%20/install", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("blank pkgName: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if env.fake.setStateCalled {
		t.Error("validation failure must not call SetExtensionState")
	}
}

// --- Repos -------------------------------------------------------------------

func TestGetRepos_OK(t *testing.T) {
	env := newTestEnv(t, &fakeClient{repos: []string{"https://a.test/i.json"}})
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
	env := newTestEnv(t, &fakeClient{})
	rec := env.do(http.MethodGet, "/api/suwayomi/extensions/repos", "")
	if !strings.Contains(rec.Body.String(), `"repos":[]`) {
		t.Errorf("empty repos: want repos:[], got %s", rec.Body.String())
	}
}

func TestGetRepos_Unauthorized(t *testing.T) {
	env := newTestEnv(t, &fakeClient{repos: []string{"https://a.test/i.json"}})
	rec := env.noAuth(http.MethodGet, "/api/suwayomi/extensions/repos", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GetRepos no token: want 401, got %d", rec.Code)
	}
}

func TestGetRepos_Upstream502(t *testing.T) {
	env := newTestEnv(t, &fakeClient{getReposErr: errors.New("boom")})
	rec := env.do(http.MethodGet, "/api/suwayomi/extensions/repos", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("GetRepos upstream fail: want 502, got %d", rec.Code)
	}
}

// TestSetRepos_RoundTrip proves PUT replaces the list and re-reads it (the
// response reflects the persisted state, not the request echo).
func TestSetRepos_RoundTrip(t *testing.T) {
	env := newTestEnv(t, &fakeClient{repos: []string{"https://old.test/i.json"}})
	body := `{"repos":["https://new1.test/i.json","https://new2.test/i.json"]}`
	rec := env.do(http.MethodPut, "/api/suwayomi/extensions/repos", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("SetRepos: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !env.fake.setReposCalled {
		t.Fatal("SetExtensionRepos was not called")
	}
	var got handler.ExtensionReposDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Repos) != 2 || got.Repos[0] != "https://new1.test/i.json" {
		t.Errorf("SetRepos round-trip: unexpected %+v", got)
	}
}

// TestSetRepos_EmptyClears proves an explicit empty array is accepted (clear-all).
func TestSetRepos_EmptyClears(t *testing.T) {
	env := newTestEnv(t, &fakeClient{repos: []string{"https://old.test/i.json"}})
	rec := env.do(http.MethodPut, "/api/suwayomi/extensions/repos", `{"repos":[]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("SetRepos empty: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !env.fake.setReposCalled || len(env.fake.lastRepos) != 0 {
		t.Errorf("empty clear not passed through: called=%v repos=%v", env.fake.setReposCalled, env.fake.lastRepos)
	}
	var got handler.ExtensionReposDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Repos == nil || len(got.Repos) != 0 {
		t.Errorf("cleared repos should serialise as []: %+v", got)
	}
}

// TestSetRepos_TrimsAndPasses proves whitespace is trimmed before reaching the client.
func TestSetRepos_TrimsAndPasses(t *testing.T) {
	env := newTestEnv(t, &fakeClient{})
	rec := env.do(http.MethodPut, "/api/suwayomi/extensions/repos", `{"repos":["  https://a.test/i.json  "]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("SetRepos trim: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if len(env.fake.lastRepos) != 1 || env.fake.lastRepos[0] != "https://a.test/i.json" {
		t.Errorf("repo not trimmed: %v", env.fake.lastRepos)
	}
}

func TestSetRepos_Unauthorized(t *testing.T) {
	env := newTestEnv(t, &fakeClient{})
	rec := env.noAuth(http.MethodPut, "/api/suwayomi/extensions/repos", `{"repos":["https://a.test/i.json"]}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("SetRepos no token: want 401, got %d", rec.Code)
	}
	if env.fake.setReposCalled {
		t.Error("SetExtensionRepos must not be called on an unauthorized request")
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
			env := newTestEnv(t, &fakeClient{})
			rec := env.do(http.MethodPut, "/api/suwayomi/extensions/repos", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("want 400, got %d (%s)", rec.Code, rec.Body.String())
			}
			if env.fake.setReposCalled {
				t.Error("validation failure must not call SetExtensionRepos")
			}
		})
	}
}

// TestSetRepos_InvalidJSON proves a malformed body is a 400.
func TestSetRepos_InvalidJSON(t *testing.T) {
	env := newTestEnv(t, &fakeClient{})
	rec := env.do(http.MethodPut, "/api/suwayomi/extensions/repos", `{not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid json: want 400, got %d", rec.Code)
	}
}

// TestSetRepos_Upstream502 proves a downstream SetExtensionRepos failure is a 502.
func TestSetRepos_Upstream502(t *testing.T) {
	env := newTestEnv(t, &fakeClient{setReposErr: errors.New("graphql rejected")})
	rec := env.do(http.MethodPut, "/api/suwayomi/extensions/repos", `{"repos":["https://a.test/i.json"]}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("SetRepos upstream fail: want 502, got %d", rec.Code)
	}
}

// TestSetRepos_ReadBack502 proves a failure of the post-write read-back is a 502.
func TestSetRepos_ReadBack502(t *testing.T) {
	env := newTestEnv(t, &fakeClient{getReposErr: errors.New("connection reset")})
	rec := env.do(http.MethodPut, "/api/suwayomi/extensions/repos", `{"repos":["https://a.test/i.json"]}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("read-back fail: want 502, got %d", rec.Code)
	}
	if !env.fake.setReposCalled {
		t.Error("the write should have been attempted before the read-back")
	}
}

// --- Icon (M1 bugfix: extension icon proxy) -----------------------------------

// TestIcon_OK proves the handler looks the extension up by pkgName among
// Extensions(), then streams THAT entry's own IconURL via coverproxy.Stream —
// the raw bytes + a Content-Type resolved from the sniffed extension.
func TestIcon_OK(t *testing.T) {
	ext := seededExt()
	ext.PkgName = "eu.kanade.tachiyomi.extension.all.example"
	ext.IconURL = "/api/v1/extension/icon/tachiyomi-all.example-v1.0.0.apk"
	pngBytes := []byte{0x89, 0x50, 0x4E, 0x47}

	var gotURL string
	env := newTestEnv(t, &fakeClient{
		exts: []suwayomicli.Extension{ext},
		pageBytes: func(_ context.Context, pageURL string) ([]byte, string, error) {
			gotURL = pageURL
			return pngBytes, "png", nil
		},
	})
	rec := env.do(http.MethodGet, "/api/suwayomi/extensions/eu.kanade.tachiyomi.extension.all.example/icon", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("Icon: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Icon: Content-Type = %q, want image/png", ct)
	}
	if rec.Body.String() != string(pngBytes) {
		t.Error("Icon: body does not match the fetched bytes")
	}
	if gotURL != ext.IconURL {
		t.Errorf("Icon: streamed URL = %q, want the extension's own IconURL %q", gotURL, ext.IconURL)
	}
}

// TestIcon_NotFound proves an unknown pkgName (absent from Extensions()) is a
// 404, not a false 200 or a panic.
func TestIcon_NotFound(t *testing.T) {
	env := newTestEnv(t, &fakeClient{exts: []suwayomicli.Extension{seededExt()}})
	rec := env.do(http.MethodGet, "/api/suwayomi/extensions/pkg.unknown/icon", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("Icon unknown pkgName: want 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestIcon_ExtensionsUpstream502 proves a failure of the Extensions() lookup
// itself (needed to resolve pkgName → IconURL) is a 502.
func TestIcon_ExtensionsUpstream502(t *testing.T) {
	env := newTestEnv(t, &fakeClient{listErr: errors.New("connection refused")})
	rec := env.do(http.MethodGet, "/api/suwayomi/extensions/pkg.test.one/icon", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("Icon Extensions() fail: want 502, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestIcon_PageBytesFail502 proves a Suwayomi icon-fetch failure (the extension
// is found, but coverproxy.Stream's PageBytes call fails) is a 502, never a
// false 200.
func TestIcon_PageBytesFail502(t *testing.T) {
	env := newTestEnv(t, &fakeClient{
		exts: []suwayomicli.Extension{seededExt()},
		pageBytes: func(context.Context, string) ([]byte, string, error) {
			return nil, "", errors.New("suwayomi down")
		},
	})
	rec := env.do(http.MethodGet, "/api/suwayomi/extensions/pkg.test.one/icon", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("Icon PageBytes fail: want 502, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestIcon_BlankPkgName400 proves a whitespace-only pkgName is rejected before
// any client call (mirrors TestActions_BlankPkgName400).
func TestIcon_BlankPkgName400(t *testing.T) {
	env := newTestEnv(t, &fakeClient{exts: []suwayomicli.Extension{seededExt()}})
	rec := env.do(http.MethodGet, "/api/suwayomi/extensions/%20/icon", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Icon blank pkgName: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestIcon_Unauthorized proves the route is behind RequireOwner.
func TestIcon_Unauthorized(t *testing.T) {
	env := newTestEnv(t, &fakeClient{exts: []suwayomicli.Extension{seededExt()}})
	rec := env.noAuth(http.MethodGet, "/api/suwayomi/extensions/pkg.test.one/icon", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Icon no token: want 401, got %d", rec.Code)
	}
}
