// Package flaresolverr_test exercises the Tsundoku-owned FlareSolverr settings
// HTTP handlers end-to-end through a real Echo instance (with RequireOwner +
// the central error middleware wired) against an ephemeral PostgreSQL
// instance (testdb, for the real settings.Service) and a fake suwayomi.Client
// (the best-effort mirror target). Tests require Docker.
package flaresolverr_test

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

	"github.com/technobecet/tsundoku/internal/database/testdb"
	handler "github.com/technobecet/tsundoku/internal/handler/flaresolverr"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	settingssvc "github.com/technobecet/tsundoku/internal/settings"
	suwayomicli "github.com/technobecet/tsundoku/internal/suwayomi"
)

const testSecret = "flaresolverr-handler-test-secret" //nolint:gosec // test fixture, not a real credential

// fakeSuwayomiClient is a suwayomi.Client double: only SetServerSettings is
// overridden (the mirror target); every other method would panic if called,
// which these handlers never do. It captures the last patch it received so
// tests can assert the mirror carries the freshly-saved Tsundoku state.
type fakeSuwayomiClient struct {
	suwayomicli.Client
	setErr    error
	setCalled bool
	lastPatch suwayomicli.SuwayomiSettingsPatch
}

func (f *fakeSuwayomiClient) SetServerSettings(_ context.Context, patch suwayomicli.SuwayomiSettingsPatch) error {
	f.setCalled = true
	f.lastPatch = patch
	return f.setErr
}

type testEnv struct {
	e     *echo.Echo
	fake  *fakeSuwayomiClient
	token string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	client := testdb.New(t)
	authSvc := auth.NewService(testSecret)
	fake := &fakeSuwayomiClient{}
	h := handler.NewHandler(settingssvc.NewService(client, settingssvc.Defaults{FlareSolverrTimeout: 60, FlareSolverrSessionTTL: 15}), fake)

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.GET("/flaresolverr/settings", h.Get)
	authed.PATCH("/flaresolverr/settings", h.Update)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}
	return &testEnv{e: e, fake: fake, token: token}
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

// TestGet_OK proves GET returns the six FlareSolverr defaults.
func TestGet_OK(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodGet, "/api/flaresolverr/settings", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("Get: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got handler.SettingsDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Enabled {
		t.Error("default Enabled = true, want false")
	}
	if got.URL != "" {
		t.Errorf("default URL = %q, want \"\"", got.URL)
	}
	if got.Timeout != 60 {
		t.Errorf("default Timeout = %d, want 60", got.Timeout)
	}
	if got.SessionTTL != 15 {
		t.Errorf("default SessionTTL = %d, want 15", got.SessionTTL)
	}
}

// TestGet_Unauthorized proves the route is behind RequireOwner.
func TestGet_Unauthorized(t *testing.T) {
	env := newTestEnv(t)
	r := httptest.NewRequest(http.MethodGet, "/api/flaresolverr/settings", nil)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Get without token: want 401, got %d", rec.Code)
	}
}

// TestUpdate_OK proves a valid partial update persists (§16 round-trip: the
// response AND a re-GET both reflect it) and attempts the Suwayomi mirror
// carrying the full post-save state.
func TestUpdate_OK(t *testing.T) {
	env := newTestEnv(t)
	body := `{"enabled":true,"url":"http://10.0.1.17:8191","timeout":90,"sessionName":"tsundoku","sessionTtl":30,"asResponseFallback":true}`
	rec := env.do(http.MethodPatch, "/api/flaresolverr/settings", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("Update: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got handler.SettingsDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	assertFullySubmittedValues(t, got)

	// Re-GET confirms persistence, not just the response body.
	rec2 := env.do(http.MethodGet, "/api/flaresolverr/settings", "")
	var got2 handler.SettingsDTO
	if err := json.Unmarshal(rec2.Body.Bytes(), &got2); err != nil {
		t.Fatalf("decode re-GET: %v", err)
	}
	if got2 != got {
		t.Errorf("re-GET = %+v, want it to match the Update response %+v", got2, got)
	}

	assertMirrorPatch(t, env.fake)
}

// assertFullySubmittedValues checks the Update response reflects every field
// TestUpdate_OK submitted (split out purely to keep that test's own
// cyclomatic complexity low).
func assertFullySubmittedValues(t *testing.T, got handler.SettingsDTO) {
	t.Helper()
	if !got.Enabled || got.URL != "http://10.0.1.17:8191" || got.Timeout != 90 ||
		got.SessionName != "tsundoku" || got.SessionTTL != 30 || !got.AsResponseFallback {
		t.Fatalf("Update response = %+v, want the full submitted values", got)
	}
}

// assertMirrorPatch checks the Suwayomi mirror was attempted with the full
// resulting (post-save) state.
func assertMirrorPatch(t *testing.T, fake *fakeSuwayomiClient) {
	t.Helper()
	if !fake.setCalled {
		t.Fatal("SetServerSettings was not called — the Suwayomi mirror never fired")
	}
	p := fake.lastPatch
	if p.FlareSolverrEnabled == nil || !*p.FlareSolverrEnabled {
		t.Error("mirror patch FlareSolverrEnabled missing/false")
	}
	if p.FlareSolverrURL == nil || *p.FlareSolverrURL != "http://10.0.1.17:8191" {
		t.Error("mirror patch FlareSolverrURL missing/mismatched")
	}
	if p.FlareSolverrSessionName == nil || *p.FlareSolverrSessionName != "tsundoku" {
		t.Error("mirror patch FlareSolverrSessionName missing/mismatched")
	}
}

// TestUpdate_MirrorFailureStillSaves proves a Suwayomi-mirror failure is
// swallowed: the Tsundoku save already succeeded, so the request still
// returns 200 with the persisted Tsundoku values.
func TestUpdate_MirrorFailureStillSaves(t *testing.T) {
	env := newTestEnv(t)
	env.fake.setErr = errors.New("suwayomi: connection refused")

	rec := env.do(http.MethodPatch, "/api/flaresolverr/settings", `{"enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("Update with mirror failure: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got handler.SettingsDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Enabled {
		t.Error("Update response Enabled = false, want true (Tsundoku save must persist despite mirror failure)")
	}
	if !env.fake.setCalled {
		t.Fatal("SetServerSettings was not attempted")
	}

	// Persistence survives the mirror failure too.
	rec2 := env.do(http.MethodGet, "/api/flaresolverr/settings", "")
	var got2 handler.SettingsDTO
	_ = json.Unmarshal(rec2.Body.Bytes(), &got2)
	if !got2.Enabled {
		t.Error("re-GET Enabled = false, want true")
	}
}

// TestUpdate_EmptyBody proves an empty PATCH body is a 400 (no-op update
// rejected, fail-closed).
func TestUpdate_EmptyBody(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPatch, "/api/flaresolverr/settings", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Update empty body: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if env.fake.setCalled {
		t.Error("SetServerSettings must not be attempted when the Tsundoku save was rejected")
	}
}

// TestUpdate_InvalidURL proves a malformed URL is rejected 400 and never
// reaches the Suwayomi mirror.
func TestUpdate_InvalidURL(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPatch, "/api/flaresolverr/settings", `{"url":"not-a-url"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Update bad url: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if env.fake.setCalled {
		t.Error("SetServerSettings must not be attempted when the Tsundoku save was rejected")
	}
}

// TestUpdate_Unauthorized proves the route is behind RequireOwner.
func TestUpdate_Unauthorized(t *testing.T) {
	env := newTestEnv(t)
	r := httptest.NewRequest(http.MethodPatch, "/api/flaresolverr/settings", strings.NewReader(`{"enabled":true}`))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Update without token: want 401, got %d", rec.Code)
	}
}
