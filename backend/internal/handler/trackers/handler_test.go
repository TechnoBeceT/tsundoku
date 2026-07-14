// Package trackers_test exercises the Phase-3 tracker HTTP handlers
// end-to-end through a real Echo instance (with the RequireOwner middleware
// wired) against an ephemeral PostgreSQL instance (testdb) and fake
// tracker.Tracker doubles — no real network access. Tests require Docker.
package trackers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/ent/trackerconnection"
	handler "github.com/technobecet/tsundoku/internal/handler/trackers"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/tracker"
	"github.com/technobecet/tsundoku/internal/tracker/bind"
	"github.com/technobecet/tsundoku/internal/tracker/connect"
	"github.com/technobecet/tsundoku/internal/tracker/syncsvc"
)

const testSecret = "trackers-handler-test-secret"

// fakeOAuthTracker is an auth-code (MAL-shaped) tracker.Tracker test double:
// AuthURL/ExchangeCode succeed deterministically, and it deliberately does
// NOT implement tracker.CredentialLogin (so a credential-login call against
// it exercises connect.ErrCredentialLoginNotSupported). GetEntry/SaveEntry/
// DeleteEntry/Search are configurable per test via the *Fn fields, mirroring
// internal/tracker/bind's own test double.
type fakeOAuthTracker struct {
	id int

	getEntryFn    func(ctx context.Context, token, remoteID string) (*tracker.TrackEntry, error)
	saveEntryFn   func(ctx context.Context, token string, entry tracker.TrackEntry) (tracker.TrackEntry, error)
	deleteEntryFn func(ctx context.Context, token string, entry tracker.TrackEntry) error
	searchFn      func(ctx context.Context, token, query string) ([]tracker.TrackSearchResult, error)
}

func (f *fakeOAuthTracker) Key() string           { return "fake-oauth" }
func (f *fakeOAuthTracker) ID() int               { return f.id }
func (f *fakeOAuthTracker) Name() string          { return "Fake OAuth Tracker" }
func (f *fakeOAuthTracker) NeedsOAuth() bool      { return true }
func (f *fakeOAuthTracker) SupportsPrivate() bool { return false }

func (f *fakeOAuthTracker) AuthURL(state, _ string) (string, string, error) {
	return "https://fake.test/authorize?state=" + state, "verifier-xyz", nil
}

func (f *fakeOAuthTracker) ExchangeCode(_ context.Context, code, _, _ string) (tracker.TokenSet, error) {
	return tracker.TokenSet{Access: "access-" + code}, nil
}

func (f *fakeOAuthTracker) Refresh(context.Context, string) (tracker.TokenSet, error) {
	return tracker.TokenSet{}, tracker.ErrNoRefresh
}

func (f *fakeOAuthTracker) Search(ctx context.Context, token, query string) ([]tracker.TrackSearchResult, error) {
	if f.searchFn != nil {
		return f.searchFn(ctx, token, query)
	}
	return nil, nil
}

func (f *fakeOAuthTracker) GetEntry(ctx context.Context, token, remoteID string) (*tracker.TrackEntry, error) {
	if f.getEntryFn != nil {
		return f.getEntryFn(ctx, token, remoteID)
	}
	return nil, nil
}

func (f *fakeOAuthTracker) SaveEntry(ctx context.Context, token string, entry tracker.TrackEntry) (tracker.TrackEntry, error) {
	if f.saveEntryFn != nil {
		return f.saveEntryFn(ctx, token, entry)
	}
	return entry, nil
}

func (f *fakeOAuthTracker) UpdateEntry(_ context.Context, _ string, entry tracker.TrackEntry) (tracker.TrackEntry, error) {
	return entry, nil
}

func (f *fakeOAuthTracker) DeleteEntry(ctx context.Context, token string, entry tracker.TrackEntry) error {
	if f.deleteEntryFn != nil {
		return f.deleteEntryFn(ctx, token, entry)
	}
	return nil
}

var _ tracker.Tracker = (*fakeOAuthTracker)(nil)

// fakeCredentialTracker is a Kitsu/MangaUpdates-shaped tracker.Tracker test
// double: NeedsOAuth() is false, AuthURL/ExchangeCode always fail with
// tracker.ErrOAuthNotSupported, and LoginCredentials succeeds
// deterministically (implementing tracker.CredentialLogin).
type fakeCredentialTracker struct {
	id int
}

func (f *fakeCredentialTracker) Key() string           { return "fake-credential" }
func (f *fakeCredentialTracker) ID() int               { return f.id }
func (f *fakeCredentialTracker) Name() string          { return "Fake Credential Tracker" }
func (f *fakeCredentialTracker) NeedsOAuth() bool      { return false }
func (f *fakeCredentialTracker) SupportsPrivate() bool { return false }
func (f *fakeCredentialTracker) AuthURL(string, string) (string, string, error) {
	return "", "", tracker.ErrOAuthNotSupported
}
func (f *fakeCredentialTracker) ExchangeCode(context.Context, string, string, string) (tracker.TokenSet, error) {
	return tracker.TokenSet{}, tracker.ErrOAuthNotSupported
}
func (f *fakeCredentialTracker) Refresh(context.Context, string) (tracker.TokenSet, error) {
	return tracker.TokenSet{}, tracker.ErrNoRefresh
}
func (f *fakeCredentialTracker) LoginCredentials(_ context.Context, username, _ string) (tracker.TokenSet, error) {
	return tracker.TokenSet{Access: "access-" + username}, nil
}
func (f *fakeCredentialTracker) Search(context.Context, string, string) ([]tracker.TrackSearchResult, error) {
	return nil, nil
}
func (f *fakeCredentialTracker) GetEntry(context.Context, string, string) (*tracker.TrackEntry, error) {
	return nil, nil
}
func (f *fakeCredentialTracker) SaveEntry(_ context.Context, _ string, e tracker.TrackEntry) (tracker.TrackEntry, error) {
	return e, nil
}
func (f *fakeCredentialTracker) UpdateEntry(_ context.Context, _ string, e tracker.TrackEntry) (tracker.TrackEntry, error) {
	return e, nil
}
func (f *fakeCredentialTracker) DeleteEntry(context.Context, string, tracker.TrackEntry) error {
	return nil
}

var (
	_ tracker.Tracker         = (*fakeCredentialTracker)(nil)
	_ tracker.CredentialLogin = (*fakeCredentialTracker)(nil)
)

const (
	oauthTrackerID      = 901
	credentialTrackerID = 902
)

// fakeSyncService is a handler.SyncService test double for the Phase-4c
// UpdateTrack/SyncTracking routes — it never touches a real tracker, the DB,
// or the retry queue, keeping those two handlers' tests independent of the
// full syncsvc wiring (retry.Queue + SidecarSyncer + AutoUpdateTracker) none
// of the OTHER tracker-handler tests need. A nil *Fn defaults to a not-found
// sentinel (UpdateTrack) / an empty list (SyncNow) so a test that forgets to
// set it up fails loudly rather than silently succeeding.
type fakeSyncService struct {
	updateTrackFn func(ctx context.Context, recordID uuid.UUID, patch syncsvc.UpdatePatch) (*ent.TrackBinding, error)
	syncNowFn     func(ctx context.Context, seriesID uuid.UUID) ([]*ent.TrackBinding, error)
}

func (f *fakeSyncService) UpdateTrack(ctx context.Context, recordID uuid.UUID, patch syncsvc.UpdatePatch) (*ent.TrackBinding, error) {
	if f.updateTrackFn != nil {
		return f.updateTrackFn(ctx, recordID, patch)
	}
	return nil, syncsvc.ErrBindingNotFound
}

func (f *fakeSyncService) SyncNow(ctx context.Context, seriesID uuid.UUID) ([]*ent.TrackBinding, error) {
	if f.syncNowFn != nil {
		return f.syncNowFn(ctx, seriesID)
	}
	return nil, nil
}

var _ handler.SyncService = (*fakeSyncService)(nil)

// testEnv bundles the wired Echo app, the DB client, storage root, and a
// valid owner token.
type testEnv struct {
	e      *echo.Echo
	client *ent.Client
	token  string
	oauth  *fakeOAuthTracker
	cred   *fakeCredentialTracker
	sync   *fakeSyncService
}

// newTestEnv stands up a fully-wired Echo: the tracker routes registered
// behind RequireOwner, a connect.Service + bind.Service sharing ONE registry
// over the two fake trackers (an OAuth one and a credential one — mirrors
// the real AniList/MAL vs Kitsu/MangaUpdates split), a fake SyncService for
// the Phase-4c update/sync-now routes, and a valid owner Bearer token.
func newTestEnv(t *testing.T, publicURL string) *testEnv {
	t.Helper()

	client := testdb.New(t)
	authSvc := auth.NewService(testSecret)

	oauthT := &fakeOAuthTracker{id: oauthTrackerID}
	credT := &fakeCredentialTracker{id: credentialTrackerID}
	registry := tracker.NewRegistry(oauthT, credT)

	connectSvc := connect.NewService(client, registry, publicURL)
	bindSvc := bind.NewService(client, registry, t.TempDir())
	syncSvc := &fakeSyncService{}
	h := handler.NewHandler(client, registry, connectSvc, bindSvc, syncSvc)

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.GET("/trackers", h.List)
	authed.GET("/trackers/:id/auth-url", h.AuthURL)
	authed.POST("/trackers/:id/login/oauth", h.LoginOAuth)
	authed.POST("/trackers/:id/login/credentials", h.LoginCredentials)
	authed.POST("/trackers/:id/logout", h.Logout)
	authed.GET("/trackers/:id/search", h.Search)
	authed.GET("/series/:id/tracking", h.ListBindings)
	authed.POST("/series/:id/tracking", h.CreateBinding)
	authed.DELETE("/series/:id/tracking/:recordId", h.DeleteBinding)
	authed.POST("/series/:id/tracking/:recordId/refresh", h.RefreshBinding)
	authed.POST("/series/:id/tracking/:recordId/update", h.UpdateTrack)
	authed.POST("/series/:id/tracking/sync", h.SyncTracking)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}
	return &testEnv{e: e, client: client, token: token, oauth: oauthT, cred: credT, sync: syncSvc}
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

// seedSeries creates a minimal categorized series with no providers.
func seedSeries(ctx context.Context, t *testing.T, db *ent.Client, title, slug string) uuid.UUID {
	t.Helper()
	catID, err := category.IDByName(ctx, db, "Manga")
	if err != nil {
		t.Fatalf("category.IDByName: %v", err)
	}
	s := db.Series.Create().SetTitle(title).SetSlug(slug).SetCategoryID(catID).SaveX(ctx)
	return s.ID
}

// TestAuthz_AllRoutesReject401 asserts every tracker route is behind
// RequireOwner: an unauthenticated request is rejected before it ever
// reaches the handler (so a real DB call is never even attempted).
func TestAuthz_AllRoutesReject401(t *testing.T) {
	env := newTestEnv(t, "https://tsundoku.example")
	seriesID := uuid.New().String()
	recordID := uuid.New().String()

	routes := []struct {
		method, path string
	}{
		{http.MethodGet, "/api/trackers"},
		{http.MethodGet, fmt.Sprintf("/api/trackers/%d/auth-url", oauthTrackerID)},
		{http.MethodPost, fmt.Sprintf("/api/trackers/%d/login/oauth", oauthTrackerID)},
		{http.MethodPost, fmt.Sprintf("/api/trackers/%d/login/credentials", credentialTrackerID)},
		{http.MethodPost, fmt.Sprintf("/api/trackers/%d/logout", oauthTrackerID)},
		{http.MethodGet, fmt.Sprintf("/api/trackers/%d/search?q=x", oauthTrackerID)},
		{http.MethodGet, "/api/series/" + seriesID + "/tracking"},
		{http.MethodPost, "/api/series/" + seriesID + "/tracking"},
		{http.MethodDelete, "/api/series/" + seriesID + "/tracking/" + recordID},
		{http.MethodPost, "/api/series/" + seriesID + "/tracking/" + recordID + "/refresh"},
		{http.MethodPost, "/api/series/" + seriesID + "/tracking/" + recordID + "/update"},
		{http.MethodPost, "/api/series/" + seriesID + "/tracking/sync"},
	}
	for _, rt := range routes {
		r := httptest.NewRequest(rt.method, rt.path, nil)
		rec := httptest.NewRecorder()
		env.e.ServeHTTP(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: status = %d, want %d", rt.method, rt.path, rec.Code, http.StatusUnauthorized)
		}
	}
}

// TestList_ReportsEveryRegisteredTracker asserts GET /api/trackers lists
// BOTH registered trackers with their connect status, and — the auth-url-
// on-demand design (spec §4) — succeeds even though the OAuth tracker's
// AuthURL would fail closed for THIS instance's blank public URL: List
// never calls AuthURL/stashes a login, it only reads TrackerConnection rows.
func TestList_ReportsEveryRegisteredTracker(t *testing.T) {
	env := newTestEnv(t, "") // blank public URL — AuthURL would ErrPublicURLNotConfigured

	rec := env.do(http.MethodGet, "/api/trackers", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var out []handler.TrackerDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2", len(out))
	}
	for _, dto := range out {
		if dto.IsLoggedIn {
			t.Errorf("tracker %d: IsLoggedIn = true, want false (nobody has logged in yet)", dto.ID)
		}
	}
	if out[0].ID != oauthTrackerID || !out[0].NeedsOAuth {
		t.Errorf("out[0] = %+v, want id=%d needsOAuth=true", out[0], oauthTrackerID)
	}
	if out[1].ID != credentialTrackerID || out[1].NeedsOAuth {
		t.Errorf("out[1] = %+v, want id=%d needsOAuth=false", out[1], credentialTrackerID)
	}
}

// TestAuthURL_Success asserts a fresh authorize URL is built on demand and
// carries the tracker's own AuthURL output.
func TestAuthURL_Success(t *testing.T) {
	env := newTestEnv(t, "https://tsundoku.example")

	rec := env.do(http.MethodGet, fmt.Sprintf("/api/trackers/%d/auth-url", oauthTrackerID), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var out handler.TrackerAuthURLDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(out.AuthURL, "https://fake.test/authorize?state=") {
		t.Errorf("AuthURL = %q, want the fake tracker's authorize URL", out.AuthURL)
	}
}

// TestAuthURL_UnknownTracker asserts an unregistered tracker id is a 404.
func TestAuthURL_UnknownTracker(t *testing.T) {
	env := newTestEnv(t, "https://tsundoku.example")

	rec := env.do(http.MethodGet, "/api/trackers/9999/auth-url", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", rec.Code, rec.Body.String())
	}
}

// TestAuthURL_PublicURLNotConfigured asserts a blank instance public URL is
// a 400 (the whole OAuth subsystem stays dormant per spec §2).
func TestAuthURL_PublicURLNotConfigured(t *testing.T) {
	env := newTestEnv(t, "")

	rec := env.do(http.MethodGet, fmt.Sprintf("/api/trackers/%d/auth-url", oauthTrackerID), "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
}

// TestAuthURL_CredentialTrackerNotOAuth asserts asking for an authorize URL
// on a credential-only tracker (Kitsu/MangaUpdates-shaped) is a 400.
func TestAuthURL_CredentialTrackerNotOAuth(t *testing.T) {
	env := newTestEnv(t, "https://tsundoku.example")

	rec := env.do(http.MethodGet, fmt.Sprintf("/api/trackers/%d/auth-url", credentialTrackerID), "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
}

// TestLoginOAuth_Success drives the full AuthURL → callback → LoginOAuth
// round-trip and asserts the refreshed TrackerDTO reflects the new login.
func TestLoginOAuth_Success(t *testing.T) {
	env := newTestEnv(t, "https://tsundoku.example")

	authRec := env.do(http.MethodGet, fmt.Sprintf("/api/trackers/%d/auth-url", oauthTrackerID), "")
	var authOut handler.TrackerAuthURLDTO
	if err := json.Unmarshal(authRec.Body.Bytes(), &authOut); err != nil {
		t.Fatalf("decode auth-url: %v", err)
	}
	state := stateFromAuthURL(t, authOut.AuthURL)
	callbackURL := "https://tsundoku.example/auth/tracker/callback?state=" + state + "&code=abc123"

	body := `{"callbackUrl":"` + callbackURL + `"}`
	rec := env.do(http.MethodPost, fmt.Sprintf("/api/trackers/%d/login/oauth", oauthTrackerID), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var out handler.TrackerDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.IsLoggedIn || out.ID != oauthTrackerID {
		t.Errorf("TrackerDTO = %+v, want isLoggedIn=true id=%d", out, oauthTrackerID)
	}
}

// stateFromAuthURL extracts the "state" query parameter AuthURL embedded in
// its returned authorize URL.
func stateFromAuthURL(t *testing.T, authURL string) string {
	t.Helper()
	idx := strings.Index(authURL, "state=")
	if idx == -1 {
		t.Fatalf("authURL %q carries no state", authURL)
	}
	return authURL[idx+len("state="):]
}

// TestLoginOAuth_MissingCallbackURL asserts an empty callbackUrl is a 400
// before the service is ever called.
func TestLoginOAuth_MissingCallbackURL(t *testing.T) {
	env := newTestEnv(t, "https://tsundoku.example")

	rec := env.do(http.MethodPost, fmt.Sprintf("/api/trackers/%d/login/oauth", oauthTrackerID), `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
}

// TestLoginOAuth_InvalidState asserts an unrecognized/expired state is a 400.
func TestLoginOAuth_InvalidState(t *testing.T) {
	env := newTestEnv(t, "https://tsundoku.example")

	body := `{"callbackUrl":"https://tsundoku.example/auth/tracker/callback?state=bogus&code=abc"}`
	rec := env.do(http.MethodPost, fmt.Sprintf("/api/trackers/%d/login/oauth", oauthTrackerID), body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
}

// TestLoginCredentials_Success asserts a direct username/password login
// succeeds and the refreshed TrackerDTO reflects it — the password is never
// echoed back anywhere in the response.
func TestLoginCredentials_Success(t *testing.T) {
	env := newTestEnv(t, "https://tsundoku.example")

	body := `{"username":"owner@example.test","password":"hunter2"}`
	rec := env.do(http.MethodPost, fmt.Sprintf("/api/trackers/%d/login/credentials", credentialTrackerID), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "hunter2") {
		t.Fatalf("response body leaks the password: %s", rec.Body.String())
	}

	var out handler.TrackerDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.IsLoggedIn || out.Username != "owner@example.test" {
		t.Errorf("TrackerDTO = %+v, want isLoggedIn=true username=owner@example.test", out)
	}
}

// TestLoginCredentials_MissingFields asserts an empty username/password is a
// 400 before the service is ever called.
func TestLoginCredentials_MissingFields(t *testing.T) {
	env := newTestEnv(t, "https://tsundoku.example")

	cases := []string{
		`{"username":"","password":"p"}`,
		`{"username":"u","password":""}`,
		`{}`,
	}
	for _, body := range cases {
		rec := env.do(http.MethodPost, fmt.Sprintf("/api/trackers/%d/login/credentials", credentialTrackerID), body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want 400", body, rec.Code)
		}
	}
}

// TestLoginCredentials_OAuthTrackerNotSupported asserts calling the
// credential-login endpoint against an OAuth-only tracker is a 400.
func TestLoginCredentials_OAuthTrackerNotSupported(t *testing.T) {
	env := newTestEnv(t, "https://tsundoku.example")

	body := `{"username":"u","password":"p"}`
	rec := env.do(http.MethodPost, fmt.Sprintf("/api/trackers/%d/login/credentials", oauthTrackerID), body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
}

// TestLogout_Success asserts a logout removes the connection and is
// idempotent (a second logout is still a 204, never a 404).
func TestLogout_Success(t *testing.T) {
	env := newTestEnv(t, "https://tsundoku.example")
	loginCredentials(t, env, credentialTrackerID, "owner", "pw")

	rec := env.do(http.MethodPost, fmt.Sprintf("/api/trackers/%d/logout", credentialTrackerID), "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body: %s", rec.Code, rec.Body.String())
	}

	// Idempotent: a second logout is still 204.
	rec2 := env.do(http.MethodPost, fmt.Sprintf("/api/trackers/%d/logout", credentialTrackerID), "")
	if rec2.Code != http.StatusNoContent {
		t.Fatalf("second logout: status = %d, want 204; body: %s", rec2.Code, rec2.Body.String())
	}

	listRec := env.do(http.MethodGet, "/api/trackers", "")
	var out []handler.TrackerDTO
	if err := json.Unmarshal(listRec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, dto := range out {
		if dto.ID == credentialTrackerID && dto.IsLoggedIn {
			t.Errorf("tracker %d still isLoggedIn=true after Logout", credentialTrackerID)
		}
	}
}

// TestLogout_UnknownTracker asserts an unregistered tracker id is a 404.
func TestLogout_UnknownTracker(t *testing.T) {
	env := newTestEnv(t, "https://tsundoku.example")

	rec := env.do(http.MethodPost, "/api/trackers/9999/logout", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", rec.Code, rec.Body.String())
	}
}

// loginCredentials is the shared setup for tests that need a connected
// credential-based account before exercising Search/Bind/Unbind/Refresh.
func loginCredentials(t *testing.T, env *testEnv, trackerID int, username, password string) {
	t.Helper()
	body := fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)
	rec := env.do(http.MethodPost, fmt.Sprintf("/api/trackers/%d/login/credentials", trackerID), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("loginCredentials setup: status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
}

// loginOAuth is the shared setup for tests that need a connected OAuth-based
// account (the fakeOAuthTracker) before exercising Search/Bind/Unbind/
// Refresh: it drives the real AuthURL → callback → LoginOAuth round-trip
// through the HTTP layer (not a DB shortcut), so it also re-proves the login
// flow on every caller.
func loginOAuth(t *testing.T, env *testEnv, trackerID int) {
	t.Helper()
	authRec := env.do(http.MethodGet, fmt.Sprintf("/api/trackers/%d/auth-url", trackerID), "")
	if authRec.Code != http.StatusOK {
		t.Fatalf("loginOAuth setup: auth-url status = %d, want 200; body: %s", authRec.Code, authRec.Body.String())
	}
	var authOut handler.TrackerAuthURLDTO
	if err := json.Unmarshal(authRec.Body.Bytes(), &authOut); err != nil {
		t.Fatalf("decode auth-url: %v", err)
	}
	state := stateFromAuthURL(t, authOut.AuthURL)
	callbackURL := "https://tsundoku.example/auth/tracker/callback?state=" + state + "&code=abc123"
	body := `{"callbackUrl":"` + callbackURL + `"}`
	rec := env.do(http.MethodPost, fmt.Sprintf("/api/trackers/%d/login/oauth", trackerID), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("loginOAuth setup: login status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
}

// TestSearch_Success asserts an authed tracker search round-trips through
// the TrackSearchResultDTO mapper.
func TestSearch_Success(t *testing.T) {
	env := newTestEnv(t, "https://tsundoku.example")
	loginOAuth(t, env, oauthTrackerID)
	env.oauth.searchFn = func(context.Context, string, string) ([]tracker.TrackSearchResult, error) {
		return []tracker.TrackSearchResult{{RemoteID: "1", Title: "Solo Leveling", TotalChapters: 179}}, nil
	}

	rec := env.do(http.MethodGet, fmt.Sprintf("/api/trackers/%d/search?q=solo", oauthTrackerID), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var out []handler.TrackSearchResultDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 1 || out[0].RemoteID != "1" || out[0].Title != "Solo Leveling" || out[0].TotalChapters != 179 {
		t.Errorf("results = %+v, want one Solo Leveling hit", out)
	}
}

// TestSearch_MissingQuery asserts an empty/absent ?q is a 400 before the
// service is ever called.
func TestSearch_MissingQuery(t *testing.T) {
	env := newTestEnv(t, "https://tsundoku.example")

	rec := env.do(http.MethodGet, fmt.Sprintf("/api/trackers/%d/search", oauthTrackerID), "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
}

// TestSearch_TrackerNotConnected asserts searching a registered-but-never-
// logged-in tracker is a 400.
func TestSearch_TrackerNotConnected(t *testing.T) {
	env := newTestEnv(t, "https://tsundoku.example")

	rec := env.do(http.MethodGet, fmt.Sprintf("/api/trackers/%d/search?q=x", oauthTrackerID), "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
}

// TestSearch_UnknownTracker asserts an unregistered tracker id is a 404.
func TestSearch_UnknownTracker(t *testing.T) {
	env := newTestEnv(t, "https://tsundoku.example")

	rec := env.do(http.MethodGet, "/api/trackers/9999/search?q=x", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", rec.Code, rec.Body.String())
	}
}

// TestListBindings_EmptyForUnboundSeries asserts a series with no bindings
// yields an empty (never null) array.
func TestListBindings_EmptyForUnboundSeries(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t, "https://tsundoku.example")
	id := seedSeries(ctx, t, env.client, "Unbound Series", "unbound-series")

	rec := env.do(http.MethodGet, "/api/series/"+id.String()+"/tracking", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("body = %s, want []", rec.Body.String())
	}
}

// TestListBindings_SeriesNotFound asserts an unknown series id is a 404.
func TestListBindings_SeriesNotFound(t *testing.T) {
	env := newTestEnv(t, "https://tsundoku.example")

	rec := env.do(http.MethodGet, "/api/series/"+uuid.New().String()+"/tracking", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", rec.Code, rec.Body.String())
	}
}

// TestCreateBinding_Success asserts a bind persists a TrackBinding row
// (resolving GetEntry's fields, incl. the tracker's display name) and
// TestListBindings then reflects it.
func TestCreateBinding_Success(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t, "https://tsundoku.example")
	loginOAuth(t, env, oauthTrackerID)
	id := seedSeries(ctx, t, env.client, "Solo Leveling", "solo-leveling")
	env.oauth.getEntryFn = func(_ context.Context, _, remoteID string) (*tracker.TrackEntry, error) {
		return &tracker.TrackEntry{RemoteID: remoteID, Status: "current", Progress: 12, TotalChapters: 179, Score: 8}, nil
	}

	body := fmt.Sprintf(`{"trackerId":%d,"remoteId":"7224"}`, oauthTrackerID)
	rec := env.do(http.MethodPost, "/api/series/"+id.String()+"/tracking", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", rec.Code, rec.Body.String())
	}
	var out handler.TrackBindingDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	assertCreatedBindingFields(t, out)

	listRec := env.do(http.MethodGet, "/api/series/"+id.String()+"/tracking", "")
	var list []handler.TrackBindingDTO
	if err := json.Unmarshal(listRec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || list[0].ID != out.ID {
		t.Fatalf("list after bind = %+v, want exactly the created binding", list)
	}
}

// assertCreatedBindingFields fails the test unless dto carries exactly the
// field values TestCreateBinding_Success's fake GetEntry returned —
// extracted so the driving test stays under the fleet's per-function
// cyclomatic-complexity budget.
func assertCreatedBindingFields(t *testing.T, dto handler.TrackBindingDTO) {
	t.Helper()
	if dto.RemoteID != "7224" || dto.Status != "current" || dto.LastChapterRead != 12 ||
		dto.TotalChapters != 179 || dto.Score != 8 || dto.TrackerName != "Fake OAuth Tracker" {
		t.Errorf("TrackBindingDTO = %+v", dto)
	}
}

// TestCreateBinding_PrivateFlag asserts the request's optional `private`
// field threads all the way through validateBind → bindSvc.Bind → the
// fresh SaveEntry call on the not-yet-tracked path (GetEntry finds nothing,
// so SaveEntry registers a brand new remote entry). Covers both an explicit
// true and an absent (defaults false) body.
func TestCreateBinding_PrivateFlag(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"explicit true", fmt.Sprintf(`{"trackerId":%d,"remoteId":"7225","private":true}`, oauthTrackerID), true},
		{"absent defaults false", fmt.Sprintf(`{"trackerId":%d,"remoteId":"7226"}`, oauthTrackerID), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			env := newTestEnv(t, "https://tsundoku.example")
			loginOAuth(t, env, oauthTrackerID)
			id := seedSeries(ctx, t, env.client, "Private Flag "+tc.name, "private-flag-"+tc.name)

			var capturedEntry tracker.TrackEntry
			env.oauth.saveEntryFn = func(_ context.Context, _ string, entry tracker.TrackEntry) (tracker.TrackEntry, error) {
				capturedEntry = entry
				entry.LibraryID = "new-lib"
				return entry, nil
			}

			rec := env.do(http.MethodPost, "/api/series/"+id.String()+"/tracking", tc.body)
			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201; body: %s", rec.Code, rec.Body.String())
			}
			if capturedEntry.Private != tc.want {
				t.Errorf("SaveEntry entry.Private = %v, want %v", capturedEntry.Private, tc.want)
			}
		})
	}
}

// TestCreateBinding_MissingFields asserts a zero trackerId / blank remoteId
// is a 400 before the service is ever called.
func TestCreateBinding_MissingFields(t *testing.T) {
	env := newTestEnv(t, "https://tsundoku.example")
	id := uuid.New().String()

	cases := []string{
		`{"trackerId":0,"remoteId":"1"}`,
		fmt.Sprintf(`{"trackerId":%d,"remoteId":""}`, oauthTrackerID),
		`{}`,
	}
	for _, body := range cases {
		rec := env.do(http.MethodPost, "/api/series/"+id+"/tracking", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want 400", body, rec.Code)
		}
	}
}

// TestCreateBinding_TrackerNotFound asserts an unregistered trackerId is a
// 404.
func TestCreateBinding_TrackerNotFound(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t, "https://tsundoku.example")
	id := seedSeries(ctx, t, env.client, "No Tracker", "no-tracker")

	body := `{"trackerId":9999,"remoteId":"1"}`
	rec := env.do(http.MethodPost, "/api/series/"+id.String()+"/tracking", body)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", rec.Code, rec.Body.String())
	}
}

// TestCreateBinding_TrackerNotConnected asserts binding to a registered but
// never-logged-in tracker is a 400.
func TestCreateBinding_TrackerNotConnected(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t, "https://tsundoku.example")
	id := seedSeries(ctx, t, env.client, "Not Connected", "not-connected")

	body := fmt.Sprintf(`{"trackerId":%d,"remoteId":"1"}`, oauthTrackerID)
	rec := env.do(http.MethodPost, "/api/series/"+id.String()+"/tracking", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
}

// TestCreateBinding_SeriesNotFound asserts an unknown series id is a 404
// even with a fully connected tracker.
func TestCreateBinding_SeriesNotFound(t *testing.T) {
	env := newTestEnv(t, "https://tsundoku.example")
	loginOAuth(t, env, oauthTrackerID)

	body := fmt.Sprintf(`{"trackerId":%d,"remoteId":"1"}`, oauthTrackerID)
	rec := env.do(http.MethodPost, "/api/series/"+uuid.New().String()+"/tracking", body)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", rec.Code, rec.Body.String())
	}
}

// createBinding is the shared setup for the DeleteBinding/RefreshBinding
// tests below: a bound series + a live TrackBinding record.
func createBinding(t *testing.T, env *testEnv, seriesID uuid.UUID, remoteID string) handler.TrackBindingDTO {
	t.Helper()
	body := fmt.Sprintf(`{"trackerId":%d,"remoteId":%q}`, oauthTrackerID, remoteID)
	rec := env.do(http.MethodPost, "/api/series/"+seriesID.String()+"/tracking", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("createBinding setup: status = %d, want 201; body: %s", rec.Code, rec.Body.String())
	}
	var out handler.TrackBindingDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// TestDeleteBinding_Success asserts an unbind removes the row and the series
// then lists zero bindings.
func TestDeleteBinding_Success(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t, "https://tsundoku.example")
	loginOAuth(t, env, oauthTrackerID)
	id := seedSeries(ctx, t, env.client, "To Unbind", "to-unbind")
	binding := createBinding(t, env, id, "r1")

	rec := env.do(http.MethodDelete, "/api/series/"+id.String()+"/tracking/"+binding.ID, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body: %s", rec.Code, rec.Body.String())
	}

	listRec := env.do(http.MethodGet, "/api/series/"+id.String()+"/tracking", "")
	if strings.TrimSpace(listRec.Body.String()) != "[]" {
		t.Errorf("list after Unbind = %s, want []", listRec.Body.String())
	}
}

// TestDeleteBinding_InvalidDeleteRemote asserts a non-boolean ?deleteRemote
// value is a 400.
func TestDeleteBinding_InvalidDeleteRemote(t *testing.T) {
	env := newTestEnv(t, "https://tsundoku.example")
	id := uuid.New().String()
	recordID := uuid.New().String()

	rec := env.do(http.MethodDelete, "/api/series/"+id+"/tracking/"+recordID+"?deleteRemote=bogus", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
}

// TestDeleteBinding_BindingNotFound asserts an unknown recordId is a 404.
func TestDeleteBinding_BindingNotFound(t *testing.T) {
	env := newTestEnv(t, "https://tsundoku.example")

	rec := env.do(http.MethodDelete, "/api/series/"+uuid.New().String()+"/tracking/"+uuid.New().String(), "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", rec.Code, rec.Body.String())
	}
}

// TestRefreshBinding_Success asserts a refresh re-pulls the remote entry and
// returns the updated TrackBindingDTO.
func TestRefreshBinding_Success(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t, "https://tsundoku.example")
	loginOAuth(t, env, oauthTrackerID)
	id := seedSeries(ctx, t, env.client, "To Refresh", "to-refresh")
	env.oauth.getEntryFn = func(_ context.Context, _, remoteID string) (*tracker.TrackEntry, error) {
		return &tracker.TrackEntry{RemoteID: remoteID, Status: "current", Progress: 5}, nil
	}
	binding := createBinding(t, env, id, "r2")

	env.oauth.getEntryFn = func(_ context.Context, _, remoteID string) (*tracker.TrackEntry, error) {
		return &tracker.TrackEntry{RemoteID: remoteID, Status: "completed", Progress: 20, TotalChapters: 20}, nil
	}
	rec := env.do(http.MethodPost, "/api/series/"+id.String()+"/tracking/"+binding.ID+"/refresh", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var out handler.TrackBindingDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Status != "completed" || out.LastChapterRead != 20 || out.TotalChapters != 20 {
		t.Errorf("TrackBindingDTO after refresh = %+v", out)
	}
}

// TestRefreshBinding_BindingNotFound asserts an unknown recordId is a 404.
func TestRefreshBinding_BindingNotFound(t *testing.T) {
	env := newTestEnv(t, "https://tsundoku.example")

	rec := env.do(http.MethodPost, "/api/series/"+uuid.New().String()+"/tracking/"+uuid.New().String()+"/refresh", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", rec.Code, rec.Body.String())
	}
}

// assertUpdateTrackPatch fails the test unless patch carries exactly the
// field values TestUpdateTrack_Success's request body set — extracted so
// the driving test's fake stays under the fleet's per-function
// cyclomatic-complexity budget (mirrors assertCreatedBindingFields above).
func assertUpdateTrackPatch(t *testing.T, patch syncsvc.UpdatePatch) {
	t.Helper()
	if patch.Status == nil || *patch.Status != "completed" {
		t.Errorf("patch.Status = %v, want *completed", patch.Status)
	}
	if patch.LastChapterRead == nil || *patch.LastChapterRead != 100 {
		t.Errorf("patch.LastChapterRead = %v, want *100", patch.LastChapterRead)
	}
	if patch.Score == nil || *patch.Score != 9 {
		t.Errorf("patch.Score = %v, want *9", patch.Score)
	}
	if patch.Private == nil || !*patch.Private {
		t.Errorf("patch.Private = %v, want *true", patch.Private)
	}
}

// TestUpdateTrack_Success asserts a manual tracking-sheet edit is routed to
// the SyncService with the parsed patch and the refreshed TrackBindingDTO
// carries the fields the fake returned.
func TestUpdateTrack_Success(t *testing.T) {
	env := newTestEnv(t, "https://tsundoku.example")
	seriesID := uuid.New()
	recordID := uuid.New()

	env.sync.updateTrackFn = func(_ context.Context, gotID uuid.UUID, patch syncsvc.UpdatePatch) (*ent.TrackBinding, error) {
		if gotID != recordID {
			t.Errorf("recordID = %s, want %s", gotID, recordID)
		}
		assertUpdateTrackPatch(t, patch)
		return &ent.TrackBinding{
			ID:              recordID,
			SeriesID:        seriesID,
			TrackerID:       oauthTrackerID,
			RemoteID:        "42",
			Status:          "completed",
			LastChapterRead: 100,
			Score:           9,
			Private:         true,
		}, nil
	}

	body := `{"status":"completed","lastChapterRead":100,"score":9,"private":true}`
	rec := env.do(http.MethodPost, "/api/series/"+seriesID.String()+"/tracking/"+recordID.String()+"/update", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var out handler.TrackBindingDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.ID != recordID.String() || out.Status != "completed" || out.LastChapterRead != 100 ||
		out.Score != 9 || !out.Private || out.TrackerName != "Fake OAuth Tracker" {
		t.Errorf("TrackBindingDTO = %+v", out)
	}
}

// TestUpdateTrack_UnknownRecordId asserts a SyncService ErrBindingNotFound
// maps to 404 (mirrors RefreshBinding's own not-found mapping).
func TestUpdateTrack_UnknownRecordId(t *testing.T) {
	env := newTestEnv(t, "https://tsundoku.example")
	env.sync.updateTrackFn = func(context.Context, uuid.UUID, syncsvc.UpdatePatch) (*ent.TrackBinding, error) {
		return nil, syncsvc.ErrBindingNotFound
	}

	body := `{"status":"completed"}`
	rec := env.do(http.MethodPost, "/api/series/"+uuid.New().String()+"/tracking/"+uuid.New().String()+"/update", body)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", rec.Code, rec.Body.String())
	}
}

// TestUpdateTrack_EmptyBody asserts an all-nil patch is a 400 before the
// SyncService is ever called (the fake's zero-value updateTrackFn returns
// ErrBindingNotFound, so a 400 here proves validation ran first).
func TestUpdateTrack_EmptyBody(t *testing.T) {
	env := newTestEnv(t, "https://tsundoku.example")

	rec := env.do(http.MethodPost, "/api/series/"+uuid.New().String()+"/tracking/"+uuid.New().String()+"/update", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
}

// TestUpdateTrack_InvalidRecordId asserts a malformed recordId path segment
// is a 400 before the body is even bound.
func TestUpdateTrack_InvalidRecordId(t *testing.T) {
	env := newTestEnv(t, "https://tsundoku.example")

	rec := env.do(http.MethodPost, "/api/series/"+uuid.New().String()+"/tracking/not-a-uuid/update", `{"status":"completed"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
}

// TestSyncTracking_Success asserts the endpoint forwards seriesID to
// SyncNow and renders the returned binding set.
func TestSyncTracking_Success(t *testing.T) {
	env := newTestEnv(t, "https://tsundoku.example")
	seriesID := uuid.New()
	b1 := &ent.TrackBinding{ID: uuid.New(), SeriesID: seriesID, TrackerID: oauthTrackerID, RemoteID: "1", Status: "current"}
	b2 := &ent.TrackBinding{ID: uuid.New(), SeriesID: seriesID, TrackerID: credentialTrackerID, RemoteID: "2", Status: "completed"}
	env.sync.syncNowFn = func(_ context.Context, gotID uuid.UUID) ([]*ent.TrackBinding, error) {
		if gotID != seriesID {
			t.Errorf("seriesID = %s, want %s", gotID, seriesID)
		}
		return []*ent.TrackBinding{b1, b2}, nil
	}

	rec := env.do(http.MethodPost, "/api/series/"+seriesID.String()+"/tracking/sync", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var out []handler.TrackBindingDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 2 || out[0].ID != b1.ID.String() || out[1].ID != b2.ID.String() {
		t.Fatalf("list = %+v, want the 2 fake bindings", out)
	}
}

// TestSyncTracking_BadSeriesId asserts a malformed :id path segment is a 400
// before the SyncService is ever called.
func TestSyncTracking_BadSeriesId(t *testing.T) {
	env := newTestEnv(t, "https://tsundoku.example")

	rec := env.do(http.MethodPost, "/api/series/not-a-uuid/tracking/sync", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
}

// setConnectionScoreFormat overwrites trackerID's TrackerConnection row's
// score_format directly via Ent — simulating an AccountInfoProvider tracker
// (real AniList) having captured a non-default format at login, which none
// of this package's fake trackers implement (see fakeOAuthTracker's doc
// comment: lookupAccountInfo always no-ops for it, so score_format is ""
// after a normal loginOAuth call in these tests).
func setConnectionScoreFormat(ctx context.Context, t *testing.T, db *ent.Client, trackerID int, format string) {
	t.Helper()
	n, err := db.TrackerConnection.Update().
		Where(trackerconnection.TrackerID(trackerID)).
		SetScoreFormat(format).
		Save(ctx)
	if err != nil {
		t.Fatalf("setConnectionScoreFormat: %v", err)
	}
	if n != 1 {
		t.Fatalf("setConnectionScoreFormat: updated %d rows, want 1", n)
	}
}

// TestCreateBinding_ScoreFormatFromConnection asserts a binding's
// scoreFormat reflects the connected account's OWN captured score_format
// (rather than a fixed 0-10 assumption — the score-scale bug this feature
// fixes) and that GET /api/series/:id/tracking (the batch
// resolveScoreFormats path) reports the SAME value for the same binding.
func TestCreateBinding_ScoreFormatFromConnection(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t, "https://tsundoku.example")
	loginOAuth(t, env, oauthTrackerID)
	setConnectionScoreFormat(ctx, t, env.client, oauthTrackerID, "POINT_10_DECIMAL")
	id := seedSeries(ctx, t, env.client, "Score Scale", "score-scale")
	env.oauth.getEntryFn = func(_ context.Context, _, remoteID string) (*tracker.TrackEntry, error) {
		return &tracker.TrackEntry{RemoteID: remoteID, Status: "current", Score: 7.5}, nil
	}

	body := fmt.Sprintf(`{"trackerId":%d,"remoteId":"1"}`, oauthTrackerID)
	rec := env.do(http.MethodPost, "/api/series/"+id.String()+"/tracking", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", rec.Code, rec.Body.String())
	}
	var out handler.TrackBindingDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.ScoreFormat != "POINT_10_DECIMAL" || out.Score != 7.5 {
		t.Fatalf("TrackBindingDTO = %+v, want scoreFormat POINT_10_DECIMAL / score 7.5", out)
	}

	listRec := env.do(http.MethodGet, "/api/series/"+id.String()+"/tracking", "")
	var list []handler.TrackBindingDTO
	if err := json.Unmarshal(listRec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || list[0].ScoreFormat != "POINT_10_DECIMAL" {
		t.Fatalf("list after bind = %+v, want scoreFormat POINT_10_DECIMAL", list)
	}
}

// TestCreateBinding_ScoreFormatDefaultsWhenConnectionBlank asserts a
// connected account whose score_format is still "" (the normal state for
// every fake tracker here — none implement AccountInfoProvider) falls back
// to defaultScoreFormat(trackerId), never a blindly-assumed 0-10 scale.
// oauthTrackerID is not one of the real tracker.ID* constants, so the
// documented fallback for an unrecognized id is "".
func TestCreateBinding_ScoreFormatDefaultsWhenConnectionBlank(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t, "https://tsundoku.example")
	loginOAuth(t, env, oauthTrackerID)
	id := seedSeries(ctx, t, env.client, "Blank Format", "blank-format")

	body := fmt.Sprintf(`{"trackerId":%d,"remoteId":"1"}`, oauthTrackerID)
	rec := env.do(http.MethodPost, "/api/series/"+id.String()+"/tracking", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", rec.Code, rec.Body.String())
	}
	var out handler.TrackBindingDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.ScoreFormat != "" {
		t.Errorf("ScoreFormat = %q, want \"\" (oauthTrackerID has no default mapping)", out.ScoreFormat)
	}
}

// TestUpdateTrack_ScoreFormatFromConnection asserts the manual-edit endpoint
// resolves scoreFormat the same way as CreateBinding, even though the
// binding itself comes back from the (fake) SyncService rather than a real
// bind.Service call.
func TestUpdateTrack_ScoreFormatFromConnection(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t, "https://tsundoku.example")
	loginOAuth(t, env, oauthTrackerID)
	setConnectionScoreFormat(ctx, t, env.client, oauthTrackerID, "POINT_5")
	seriesID := uuid.New()
	recordID := uuid.New()
	env.sync.updateTrackFn = func(context.Context, uuid.UUID, syncsvc.UpdatePatch) (*ent.TrackBinding, error) {
		return &ent.TrackBinding{ID: recordID, SeriesID: seriesID, TrackerID: oauthTrackerID, Score: 4}, nil
	}

	body := `{"score":4}`
	rec := env.do(http.MethodPost, "/api/series/"+seriesID.String()+"/tracking/"+recordID.String()+"/update", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var out handler.TrackBindingDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.ScoreFormat != "POINT_5" || out.Score != 4 {
		t.Fatalf("TrackBindingDTO = %+v, want scoreFormat POINT_5 / score 4", out)
	}
}

// TestSyncTracking_ScoreFormatBatchResolvesPerTracker asserts the batch path
// (resolveScoreFormats) resolves EACH binding's tracker independently in a
// mixed set: one binding's tracker has a captured score_format, the other's
// connection has none — proving the map-per-request-not-per-binding shape
// still returns the right value per row, not one value smeared across all
// of them.
func TestSyncTracking_ScoreFormatBatchResolvesPerTracker(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t, "https://tsundoku.example")
	loginOAuth(t, env, oauthTrackerID)
	setConnectionScoreFormat(ctx, t, env.client, oauthTrackerID, "POINT_3")
	seriesID := uuid.New()
	b1 := &ent.TrackBinding{ID: uuid.New(), SeriesID: seriesID, TrackerID: oauthTrackerID, RemoteID: "1"}
	b2 := &ent.TrackBinding{ID: uuid.New(), SeriesID: seriesID, TrackerID: credentialTrackerID, RemoteID: "2"}
	env.sync.syncNowFn = func(context.Context, uuid.UUID) ([]*ent.TrackBinding, error) {
		return []*ent.TrackBinding{b1, b2}, nil
	}

	rec := env.do(http.MethodPost, "/api/series/"+seriesID.String()+"/tracking/sync", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var out []handler.TrackBindingDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("list = %+v, want 2 bindings", out)
	}
	if out[0].ScoreFormat != "POINT_3" {
		t.Errorf("out[0].ScoreFormat (oauth, connected+captured) = %q, want POINT_3", out[0].ScoreFormat)
	}
	if out[1].ScoreFormat != "" {
		t.Errorf("out[1].ScoreFormat (credential, never logged in) = %q, want \"\"", out[1].ScoreFormat)
	}
}
