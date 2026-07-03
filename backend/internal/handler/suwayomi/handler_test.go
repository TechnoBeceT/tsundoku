// Package suwayomi_test exercises the Suwayomi settings-proxy HTTP handlers
// end-to-end through a real Echo instance (with RequireOwner + the central error
// middleware wired) against a fake suwayomi.Client. No Suwayomi or DB is needed.
package suwayomi_test

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

	handler "github.com/technobecet/tsundoku/internal/handler/suwayomi"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	suwayomicli "github.com/technobecet/tsundoku/internal/suwayomi"
)

const testSecret = "suwayomi-settings-handler-secret"

// fakeClient is a suwayomi.Client whose embedded nil interface satisfies every
// method at compile time; only ServerSettings + SetServerSettings are overridden
// (the rest are never called by these handlers and would panic if they were).
// It models a real Suwayomi: SetServerSettings applies the patch onto `state`,
// and ServerSettings returns `state` — so a PATCH round-trip is observable.
type fakeClient struct {
	suwayomicli.Client
	state     suwayomicli.SuwayomiSettings
	getErr    error
	setErr    error
	lastPatch suwayomicli.SuwayomiSettingsPatch
	setCalled bool
}

func (f *fakeClient) ServerSettings(context.Context) (suwayomicli.SuwayomiSettings, error) {
	if f.getErr != nil {
		return suwayomicli.SuwayomiSettings{}, f.getErr
	}
	return f.state, nil
}

func (f *fakeClient) SetServerSettings(_ context.Context, patch suwayomicli.SuwayomiSettingsPatch) error {
	f.setCalled = true
	f.lastPatch = patch
	if f.setErr != nil {
		return f.setErr
	}
	applyPatch(&f.state, patch)
	return nil
}

// setPtr copies *src into *dst only when src is non-nil (nil → untouched).
func setPtr[T any](dst *T, src *T) {
	if src != nil {
		*dst = *src
	}
}

// applyPatch mutates s with the non-nil fields of patch (mirrors how Suwayomi
// persists a partial update — only set fields change).
func applyPatch(s *suwayomicli.SuwayomiSettings, p suwayomicli.SuwayomiSettingsPatch) {
	setPtr(&s.FlareSolverrEnabled, p.FlareSolverrEnabled)
	setPtr(&s.FlareSolverrURL, p.FlareSolverrURL)
	setPtr(&s.FlareSolverrTimeout, p.FlareSolverrTimeout)
	setPtr(&s.FlareSolverrSessionName, p.FlareSolverrSessionName)
	setPtr(&s.FlareSolverrSessionTTL, p.FlareSolverrSessionTTL)
	setPtr(&s.FlareSolverrAsResponseFallback, p.FlareSolverrAsResponseFallback)
	setPtr(&s.SocksProxyEnabled, p.SocksProxyEnabled)
	setPtr(&s.SocksProxyVersion, p.SocksProxyVersion)
	setPtr(&s.SocksProxyHost, p.SocksProxyHost)
	setPtr(&s.SocksProxyPort, p.SocksProxyPort)
	setPtr(&s.SocksProxyUsername, p.SocksProxyUsername)
	setPtr(&s.SocksProxyPassword, p.SocksProxyPassword)
}

// assertEq fails the test with a named message when got != want.
func assertEq[T comparable](t *testing.T, name string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", name, got, want)
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
	h := handler.NewHandler(fc)

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.GET("/suwayomi/settings", h.Get)
	authed.PATCH("/suwayomi/settings", h.Update)

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

func seededState() suwayomicli.SuwayomiSettings {
	return suwayomicli.SuwayomiSettings{
		FlareSolverrEnabled:            true,
		FlareSolverrURL:                "http://flare:8191",
		FlareSolverrTimeout:            60,
		FlareSolverrSessionName:        "sess",
		FlareSolverrSessionTTL:         15,
		FlareSolverrAsResponseFallback: true,
		SocksProxyEnabled:              true,
		SocksProxyVersion:              5,
		SocksProxyHost:                 "127.0.0.1",
		SocksProxyPort:                 "1080",
		SocksProxyUsername:             "user",
		SocksProxyPassword:             "pass",
	}
}

// TestGet_OK proves GET returns the grouped DTO with every field mapped.
func TestGet_OK(t *testing.T) {
	env := newTestEnv(t, &fakeClient{state: seededState()})
	rec := env.do(http.MethodGet, "/api/suwayomi/settings", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("Get: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got handler.SuwayomiSettingsDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.FlareSolverr.Enabled || got.FlareSolverr.URL != "http://flare:8191" || got.FlareSolverr.Timeout != 60 {
		t.Errorf("flareSolverr mismatch: %+v", got.FlareSolverr)
	}
	if got.SocksProxy.Version != 5 || got.SocksProxy.Port != "1080" || got.SocksProxy.Host != "127.0.0.1" {
		t.Errorf("socksProxy mismatch: %+v", got.SocksProxy)
	}
}

// TestGet_Unauthorized proves the route is behind RequireOwner.
func TestGet_Unauthorized(t *testing.T) {
	env := newTestEnv(t, &fakeClient{state: seededState()})
	r := httptest.NewRequest(http.MethodGet, "/api/suwayomi/settings", nil)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Get without token: want 401, got %d", rec.Code)
	}
}

// TestGet_Upstream502 proves an upstream Suwayomi failure is a 502 (no false-200).
func TestGet_Upstream502(t *testing.T) {
	env := newTestEnv(t, &fakeClient{getErr: errors.New("connection refused")})
	rec := env.do(http.MethodGet, "/api/suwayomi/settings", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("Get upstream fail: want 502, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestPatch_OK_RoundTrip proves a partial PATCH persists and the response
// reflects every sent field (§16 round-trip), and that only the provided fields
// were sent downstream (no-clobber).
func TestPatch_OK_RoundTrip(t *testing.T) {
	env := newTestEnv(t, &fakeClient{state: seededState()})
	body := `{"flareSolverr":{"url":"http://new:8191","timeout":120},"socksProxy":{"version":4,"port":"9050"}}`
	rec := env.do(http.MethodPatch, "/api/suwayomi/settings", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("Patch: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got handler.SuwayomiSettingsDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Every sent field round-trips (§16).
	assertEq(t, "flareSolverr.url", got.FlareSolverr.URL, "http://new:8191")
	assertEq(t, "flareSolverr.timeout", got.FlareSolverr.Timeout, 120)
	assertEq(t, "socksProxy.version", got.SocksProxy.Version, 4)
	assertEq(t, "socksProxy.port", got.SocksProxy.Port, "9050")
	// Untouched fields survive (no-clobber): the seeded host/session stay.
	assertEq(t, "socksProxy.host (untouched)", got.SocksProxy.Host, "127.0.0.1")
	assertEq(t, "flareSolverr.sessionName (untouched)", got.FlareSolverr.SessionName, "sess")
	// Only the provided fields were sent downstream (no-clobber at the wire).
	assertNoLeak(t, env.fake.lastPatch)
}

// assertNoLeak asserts the downstream patch carries the four fields PATCH_OK
// sent and none of the unset ones.
func assertNoLeak(t *testing.T, p suwayomicli.SuwayomiSettingsPatch) {
	t.Helper()
	leaked := p.FlareSolverrEnabled != nil || p.SocksProxyHost != nil || p.SocksProxyEnabled != nil
	if leaked {
		t.Errorf("unset fields leaked into downstream patch: %+v", p)
	}
	missing := p.FlareSolverrURL == nil || p.FlareSolverrTimeout == nil ||
		p.SocksProxyVersion == nil || p.SocksProxyPort == nil
	if missing {
		t.Errorf("provided fields missing from downstream patch: %+v", p)
	}
}

// TestPatch_Unauthorized proves PATCH is behind RequireOwner.
func TestPatch_Unauthorized(t *testing.T) {
	env := newTestEnv(t, &fakeClient{state: seededState()})
	r := httptest.NewRequest(http.MethodPatch, "/api/suwayomi/settings", strings.NewReader(`{"socksProxy":{"version":5}}`))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Patch without token: want 401, got %d", rec.Code)
	}
	if env.fake.setCalled {
		t.Error("SetServerSettings must not be called on an unauthorized request")
	}
}

// TestPatch_Validation400 table-tests the fail-closed validation cases. Each must
// be a 400 AND must not reach the downstream client.
func TestPatch_Validation400(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"bad socks version", `{"socksProxy":{"version":3}}`},
		{"bad socks port non-numeric", `{"socksProxy":{"port":"abc"}}`},
		{"bad socks port out of range", `{"socksProxy":{"port":"70000"}}`},
		{"bad flaresolverr url", `{"flareSolverr":{"url":"not a url"}}`},
		{"negative timeout", `{"flareSolverr":{"timeout":-1}}`},
		{"negative session ttl", `{"flareSolverr":{"sessionTtl":-5}}`},
		{"empty body", `{}`},
		{"empty groups", `{"flareSolverr":{},"socksProxy":{}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, &fakeClient{state: seededState()})
			rec := env.do(http.MethodPatch, "/api/suwayomi/settings", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("want 400, got %d (%s)", rec.Code, rec.Body.String())
			}
			if env.fake.setCalled {
				t.Error("validation failure must not call SetServerSettings")
			}
		})
	}
}

// TestPatch_AcceptsEmptyURLClear proves an explicit empty url clears the field
// (a valid partial update, not a validation error).
func TestPatch_AcceptsEmptyURLClear(t *testing.T) {
	env := newTestEnv(t, &fakeClient{state: seededState()})
	rec := env.do(http.MethodPatch, "/api/suwayomi/settings", `{"flareSolverr":{"url":""}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear url: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got handler.SuwayomiSettingsDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.FlareSolverr.URL != "" {
		t.Errorf("url not cleared: %q", got.FlareSolverr.URL)
	}
}

// TestPatch_SocksDisabledEmptyPort_OK proves the frontend's real request shape
// for a disabled/unconfigured SOCKS proxy — the full socksProxy group sent with
// port:"" alongside an unrelated FlareSolverr change — succeeds (200) rather
// than 400ing on the empty port, and that the empty port is treated as
// untouched (not forwarded to the downstream client, not clobbering the
// seeded port).
func TestPatch_SocksDisabledEmptyPort_OK(t *testing.T) {
	env := newTestEnv(t, &fakeClient{state: seededState()})
	body := `{"flareSolverr":{"timeout":90},"socksProxy":{"enabled":false,"version":5,"host":"","port":"","username":"","password":""}}`
	rec := env.do(http.MethodPatch, "/api/suwayomi/settings", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("Patch: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if env.fake.lastPatch.SocksProxyPort != nil {
		t.Errorf("empty socksProxy.port must not be forwarded downstream, got %q", *env.fake.lastPatch.SocksProxyPort)
	}
	var got handler.SuwayomiSettingsDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The seeded port survives untouched (empty port must not clobber it).
	assertEq(t, "socksProxy.port (untouched)", got.SocksProxy.Port, "1080")
	assertEq(t, "flareSolverr.timeout", got.FlareSolverr.Timeout, 90)
}

// TestPatch_InvalidJSON proves a malformed body is a 400.
func TestPatch_InvalidJSON(t *testing.T) {
	env := newTestEnv(t, &fakeClient{state: seededState()})
	rec := env.do(http.MethodPatch, "/api/suwayomi/settings", `{not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid json: want 400, got %d", rec.Code)
	}
}

// TestPatch_Upstream502 proves a downstream SetServerSettings failure is a 502.
func TestPatch_Upstream502(t *testing.T) {
	env := newTestEnv(t, &fakeClient{state: seededState(), setErr: errors.New("graphql rejected")})
	rec := env.do(http.MethodPatch, "/api/suwayomi/settings", `{"socksProxy":{"enabled":true}}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("upstream set fail: want 502, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestPatch_Upstream502OnReadBack proves a failure of the post-write read-back is
// also a 502 (the write happened but the §16 round-trip read failed).
func TestPatch_Upstream502OnReadBack(t *testing.T) {
	fc := &fakeClient{state: seededState()}
	env := newTestEnv(t, fc)
	// Make the read-back (second ServerSettings call) fail by flipping getErr
	// after the set is applied — simplest: set getErr now; the set still runs.
	fc.getErr = errors.New("connection reset")
	rec := env.do(http.MethodPatch, "/api/suwayomi/settings", `{"socksProxy":{"enabled":false}}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("read-back fail: want 502, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !fc.setCalled {
		t.Error("the write should have been attempted before the read-back")
	}
}
