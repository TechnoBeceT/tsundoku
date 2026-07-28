// Package impersonate_test exercises the Tsundoku-owned impersonate-gateway
// settings HTTP handlers end-to-end through a real Echo instance (with
// RequireOwner + the central error middleware wired) against an ephemeral
// PostgreSQL instance (testdb, for the real settings.Service) and a fake
// sourceengine.Client (the best-effort mirror target). Tests require Docker.
package impersonate_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	handler "github.com/technobecet/tsundoku/internal/handler/impersonate"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	settingssvc "github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceengine"
)

const testSecret = "impersonate-handler-test-secret" //nolint:gosec // test fixture, not a real credential

// fakeEngineClient is a sourceengine.Client double: only SetImpersonate is
// overridden (the mirror target); every other method would panic if called,
// which this handler never does. It captures the last patch it received so
// tests can assert the mirror carries the freshly-saved Tsundoku state.
type fakeEngineClient struct {
	sourceengine.Client
	setErr    error
	setCalled bool
	lastPatch sourceengine.ImpersonatePatch
}

func (f *fakeEngineClient) SetImpersonate(_ context.Context, patch sourceengine.ImpersonatePatch) (sourceengine.ImpersonateConfig, error) {
	f.setCalled = true
	f.lastPatch = patch
	return sourceengine.ImpersonateConfig{}, f.setErr
}

type testEnv struct {
	e     *echo.Echo
	fake  *fakeEngineClient
	token string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	client := testdb.New(t)
	authSvc := auth.NewService(testSecret)
	fake := &fakeEngineClient{}
	h := handler.NewHandler(settingssvc.NewService(client, settingssvc.Defaults{}), fake)

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.GET("/impersonate", h.Get)
	authed.PUT("/impersonate", h.Update)

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

// TestGet_OK proves GET returns the impersonate defaults (off / blank).
func TestGet_OK(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodGet, "/api/impersonate", "")
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
}

// TestGet_Unauthorized proves the route is behind RequireOwner.
func TestGet_Unauthorized(t *testing.T) {
	env := newTestEnv(t)
	r := httptest.NewRequest(http.MethodGet, "/api/impersonate", nil)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Get without token: want 401, got %d", rec.Code)
	}
}

// TestUpdate_OK proves a valid update persists (§16 round-trip: the response
// AND a re-GET both reflect it) and attempts the engine mirror carrying the
// full post-save state.
func TestUpdate_OK(t *testing.T) {
	env := newTestEnv(t)
	body := `{"enabled":true,"url":"http://impersonate-gateway:8788"}`
	rec := env.do(http.MethodPut, "/api/impersonate", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("Update: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got handler.SettingsDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Enabled || got.URL != "http://impersonate-gateway:8788" {
		t.Fatalf("Update response = %+v, want the full submitted values", got)
	}

	// Re-GET confirms persistence, not just the response body.
	rec2 := env.do(http.MethodGet, "/api/impersonate", "")
	var got2 handler.SettingsDTO
	if err := json.Unmarshal(rec2.Body.Bytes(), &got2); err != nil {
		t.Fatalf("decode re-GET: %v", err)
	}
	if !reflect.DeepEqual(got2, got) {
		t.Errorf("re-GET = %+v, want it to match the Update response %+v", got2, got)
	}

	assertMirrorPatch(t, env.fake)
}

// assertMirrorPatch checks the engine mirror was attempted with the full
// resulting (post-save) state.
func assertMirrorPatch(t *testing.T, fake *fakeEngineClient) {
	t.Helper()
	if !fake.setCalled {
		t.Fatal("SetImpersonate was not called — the engine mirror never fired")
	}
	p := fake.lastPatch
	if p.Enabled == nil || !*p.Enabled {
		t.Error("mirror patch Enabled missing/false")
	}
	if p.URL == nil || *p.URL != "http://impersonate-gateway:8788" {
		t.Error("mirror patch URL missing/mismatched")
	}
}

// TestUpdate_SourceGatingSetRoundTrips proves the per-source gating set
// (GAP-131) round-trips through the endpoint as STRINGIFIED numeric source ids
// — the same wire convention Source.id already uses, so a 64-bit id survives a
// JSON client — is canonicalised (deduped + ascending), and reaches the engine
// mirror as real int64s.
func TestUpdate_SourceGatingSetRoundTrips(t *testing.T) {
	env := newTestEnv(t)
	body := `{"enabled":true,"url":"http://impersonate-gateway:8788","sourceIds":["1998416842837112832","42","42"]}`
	rec := env.do(http.MethodPut, "/api/impersonate", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("Update: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got handler.SettingsDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	assertSourceIDs(t, got.SourceIDs, []string{"42", "1998416842837112832"})

	// §16 round-trip: a re-GET returns the persisted, canonical set.
	rec2 := env.do(http.MethodGet, "/api/impersonate", "")
	var got2 handler.SettingsDTO
	if err := json.Unmarshal(rec2.Body.Bytes(), &got2); err != nil {
		t.Fatalf("decode re-GET: %v", err)
	}
	assertSourceIDs(t, got2.SourceIDs, []string{"42", "1998416842837112832"})

	// The engine mirror carries the ids as numbers, not names.
	if env.fake.lastPatch.SourceIDs == nil {
		t.Fatal("mirror patch SourceIDs missing")
	}
	ids := *env.fake.lastPatch.SourceIDs
	if len(ids) != 2 || ids[0] != 42 || ids[1] != 1998416842837112832 {
		t.Errorf("mirror patch SourceIDs = %v, want [42 1998416842837112832]", ids)
	}
}

// TestUpdate_SourceGatingSetClears proves an explicitly EMPTY array clears the
// selection (the fail-safe state: no source uses the gateway) rather than being
// treated as "field omitted".
func TestUpdate_SourceGatingSetClears(t *testing.T) {
	env := newTestEnv(t)
	if rec := env.do(http.MethodPut, "/api/impersonate", `{"sourceIds":["42"]}`); rec.Code != http.StatusOK {
		t.Fatalf("seed Update: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	rec := env.do(http.MethodPut, "/api/impersonate", `{"sourceIds":[]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear Update: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got handler.SettingsDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.SourceIDs) != 0 {
		t.Errorf("SourceIDs after clearing = %v, want empty", got.SourceIDs)
	}
	if env.fake.lastPatch.SourceIDs == nil || len(*env.fake.lastPatch.SourceIDs) != 0 {
		t.Errorf("mirror patch SourceIDs = %v, want an explicit empty set", env.fake.lastPatch.SourceIDs)
	}

	// The WIRE BYTES, not just the length. mirrorToEngine's nil→[]int64{}
	// conversion IS the clearing mechanism, and a length check cannot see it: a
	// *[]int64 pointing at a NIL slice also reads as len 0, but marshals to
	// `"sourceIds":null`, which the engine host's `req.sourceIds?.let { … }`
	// treats as "field omitted — leave the stored set alone". The engine would
	// then keep gating a source the owner un-ticked and go on writing corrupted
	// images, with every length-based assertion still green.
	raw, err := json.Marshal(env.fake.lastPatch)
	if err != nil {
		t.Fatalf("marshal mirror patch: %v", err)
	}
	const want = `{"enabled":false,"url":"","sourceIds":[]}`
	if string(raw) != want {
		t.Errorf("mirror patch marshals to %s, want %s", raw, want)
	}
}

// TestUpdate_SourceGatingSetRejectsNames proves a source NAME is a 400 and never
// reaches the engine mirror: only ids resolve on the engine side, and letting a
// name through would fork this boundary into two identity axes (the GAP-120
// drift class).
func TestUpdate_SourceGatingSetRejectsNames(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPut, "/api/impersonate", `{"sourceIds":["Hive Scans"]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Update with a source name: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if env.fake.setCalled {
		t.Error("SetImpersonate must not be attempted when the Tsundoku save was rejected")
	}
}

// TestGet_SourceGatingSetDefaultsEmpty proves the default is the EMPTY set — an
// unlisted source keeps the plain okhttp path, which is the whole point of the
// gating (GAP-131).
func TestGet_SourceGatingSetDefaultsEmpty(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodGet, "/api/impersonate", "")
	var got handler.SettingsDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.SourceIDs) != 0 {
		t.Errorf("default SourceIDs = %v, want empty", got.SourceIDs)
	}
}

// assertSourceIDs compares a DTO's source-id list against the expected canonical
// (deduped, ascending) form.
func assertSourceIDs(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("SourceIDs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("SourceIDs = %v, want %v", got, want)
		}
	}
}

// TestUpdate_MirrorFailureStillSaves proves an engine-mirror failure is
// swallowed: the Tsundoku save already succeeded, so the request still returns
// 200 with the persisted Tsundoku values.
func TestUpdate_MirrorFailureStillSaves(t *testing.T) {
	env := newTestEnv(t)
	env.fake.setErr = errors.New("engine: connection refused")

	rec := env.do(http.MethodPut, "/api/impersonate", `{"enabled":true}`)
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
		t.Fatal("SetImpersonate was not attempted")
	}

	// Persistence survives the mirror failure too.
	rec2 := env.do(http.MethodGet, "/api/impersonate", "")
	var got2 handler.SettingsDTO
	_ = json.Unmarshal(rec2.Body.Bytes(), &got2)
	if !got2.Enabled {
		t.Error("re-GET Enabled = false, want true")
	}
}

// TestUpdate_EmptyBody proves an empty PUT body is a 400 (no-op update
// rejected, fail-closed) and never reaches the engine mirror.
func TestUpdate_EmptyBody(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPut, "/api/impersonate", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Update empty body: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if env.fake.setCalled {
		t.Error("SetImpersonate must not be attempted when the Tsundoku save was rejected")
	}
}

// TestUpdate_InvalidURL proves a malformed URL is rejected 400 and never
// reaches the engine mirror.
func TestUpdate_InvalidURL(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPut, "/api/impersonate", `{"url":"not-a-url"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Update bad url: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if env.fake.setCalled {
		t.Error("SetImpersonate must not be attempted when the Tsundoku save was rejected")
	}
}

// TestUpdate_Unauthorized proves the route is behind RequireOwner.
func TestUpdate_Unauthorized(t *testing.T) {
	env := newTestEnv(t)
	r := httptest.NewRequest(http.MethodPut, "/api/impersonate", strings.NewReader(`{"enabled":true}`))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Update without token: want 401, got %d", rec.Code)
	}
}
