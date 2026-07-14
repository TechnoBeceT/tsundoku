// Package settings_test exercises the settings HTTP handlers end-to-end through a
// real Echo instance (with RequireOwner wired) against an ephemeral PostgreSQL
// instance (testdb). Tests require Docker.
package settings_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	handler "github.com/technobecet/tsundoku/internal/handler/settings"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	settingssvc "github.com/technobecet/tsundoku/internal/settings"
)

const testSecret = "settings-handler-test-secret"

type testEnv struct {
	e      *echo.Echo
	client *ent.Client
	token  string
}

// testDefaults mirrors the config defaults so List responses are meaningful.
func testDefaults() settingssvc.Defaults {
	return settingssvc.Defaults{
		DownloadInterval:        15 * time.Minute,
		DownloadConcurrency:     5,
		RefreshInterval:         2 * time.Hour,
		RefreshConcurrency:      4,
		MaxRetries:              3,
		RetryBackoff:            time.Minute,
		StaleGraceDays:          14,
		ExtensionCheckInterval:  24 * time.Hour,
		WarmupInterval:          15 * time.Minute,
		WarmupSlowThresholdMs:   5000,
		SourcesFailureThreshold: 5,
		SourcesCooldown:         30 * time.Minute,
		SourcesMinRequestDelay:  500 * time.Millisecond,
	}
}

// newTestEnv stands up a fully-wired Echo with the settings routes behind
// RequireOwner (so the 401 proofs hit the real middleware), a settings.Service
// over a fresh testdb client, and a valid owner Bearer token.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	client := testdb.New(t)
	authSvc := auth.NewService(testSecret)
	h := handler.NewHandler(settingssvc.NewService(client, testDefaults()))

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.GET("/settings", h.List)
	authed.PATCH("/settings", h.Update)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}
	return &testEnv{e: e, client: client, token: token}
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

// TestList_OK proves GET returns the allowlist with defaults.
func TestList_OK(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodGet, "/api/settings", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("List: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got []settingssvc.SettingDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 17 {
		t.Fatalf("want 17 settings, got %d", len(got))
	}
	if got[0].Key != settingssvc.KeyDownloadInterval || got[0].Value != "15m0s" {
		t.Errorf("first row = %+v, want download_interval=15m0s", got[0])
	}
}

// TestList_Unauthorized proves the route is behind RequireOwner.
func TestList_Unauthorized(t *testing.T) {
	env := newTestEnv(t)
	r := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("List without token: want 401, got %d", rec.Code)
	}
}

// TestUpdate_OK proves a valid batch persists and the response reflects the new
// values (§16 round-trip), and a re-GET confirms persistence.
func TestUpdate_OK(t *testing.T) {
	env := newTestEnv(t)
	body := `{"settings":[{"key":"jobs.max_retries","value":"9"},{"key":"jobs.download_interval","value":"30m"}]}`
	rec := env.do(http.MethodPatch, "/api/settings", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("Update: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got []settingssvc.SettingDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byKey := map[string]string{}
	for _, s := range got {
		byKey[s.Key] = s.Value
	}
	if byKey[settingssvc.KeyMaxRetries] != "9" {
		t.Errorf("max_retries = %q, want 9", byKey[settingssvc.KeyMaxRetries])
	}
	if byKey[settingssvc.KeyDownloadInterval] != "30m0s" {
		t.Errorf("download_interval = %q, want 30m0s", byKey[settingssvc.KeyDownloadInterval])
	}

	// Re-GET confirms persistence.
	rec2 := env.do(http.MethodGet, "/api/settings", "")
	if !strings.Contains(rec2.Body.String(), `"value":"9"`) {
		t.Errorf("re-GET missing persisted max_retries=9: %s", rec2.Body.String())
	}
}

// TestUpdate_UnknownKey proves an unknown key is a 400 naming the key, and
// nothing is persisted.
func TestUpdate_UnknownKey(t *testing.T) {
	env := newTestEnv(t)
	body := `{"settings":[{"key":"jobs.secret","value":"1"}]}`
	rec := env.do(http.MethodPatch, "/api/settings", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Update unknown key: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "jobs.secret") {
		t.Errorf("400 message should name the bad key: %s", rec.Body.String())
	}
}

// TestUpdate_InvalidValue proves an out-of-bounds value is a 400 and the whole
// batch is rolled back (the valid sibling does not persist).
func TestUpdate_InvalidValue(t *testing.T) {
	env := newTestEnv(t)
	body := `{"settings":[{"key":"jobs.max_retries","value":"9"},{"key":"jobs.download_interval","value":"5s"}]}`
	rec := env.do(http.MethodPatch, "/api/settings", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Update invalid value: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	// Roll-back: max_retries still at default 3.
	rec2 := env.do(http.MethodGet, "/api/settings", "")
	if strings.Contains(rec2.Body.String(), `"value":"9"`) {
		t.Errorf("rejected batch leaked a partial write: %s", rec2.Body.String())
	}
}

// TestUpdate_EmptyBody proves an empty settings list is a 400.
func TestUpdate_EmptyBody(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPatch, "/api/settings", `{"settings":[]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Update empty: want 400, got %d", rec.Code)
	}
}
