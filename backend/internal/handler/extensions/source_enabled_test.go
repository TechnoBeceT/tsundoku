package extensions_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	handler "github.com/technobecet/tsundoku/internal/handler/extensions"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	sourceenginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
)

// --- Preferences group `enabled` field ---------------------------------------

// TestPreferences_EnabledReflectsDisabledStore proves each group's `enabled`
// flag is the inverse of the Tsundoku-side disabled-source store: a disabled
// source reports enabled=false so the FE can hide its preferences block, while
// an untouched source reports enabled=true.
func TestPreferences_EnabledReflectsDisabledStore(t *testing.T) {
	fc := sourceenginefake.New(
		sourceenginefake.WithExtensions([]sourceengine.Extension{{
			PkgName: "pkg.multi",
			Sources: []sourceengine.Source{
				{ID: 10, Name: "Webtoons", Lang: "en"},
				{ID: 11, Name: "Webtoons", Lang: "id"},
			},
		}}),
		sourceenginefake.WithPreferences(10, nil),
		sourceenginefake.WithPreferences(11, nil),
	)
	env := newTestEnv(t, fc)
	env.store.disabled[11] = true // language "id" disabled

	rec := env.do(http.MethodGet, "/api/suwayomi/extensions/pkg.multi/preferences", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("Preferences: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got handler.SourcePreferencesBySourceDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	enabledByID := map[string]bool{}
	for _, g := range got.Sources {
		enabledByID[g.SourceID] = g.Enabled
	}
	if !enabledByID["10"] {
		t.Errorf("source 10 must be enabled, got %+v", got.Sources)
	}
	if enabledByID["11"] {
		t.Errorf("disabled source 11 must report enabled=false, got %+v", got.Sources)
	}
}

// --- PATCH /api/sources/:sourceId/enabled ------------------------------------

// TestSetSourceEnabled_RoundTrip proves the toggle disables then re-enables a
// source, returning the authoritative re-read state each time and persisting it
// in the store (a subsequent GET reflects it).
func TestSetSourceEnabled_RoundTrip(t *testing.T) {
	env := newTestEnv(t, prefsFake())

	// Disable source 42.
	rec := env.do(http.MethodPatch, "/api/sources/42/enabled", `{"enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got handler.SourceEnabledDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.SourceID != "42" || got.Enabled {
		t.Fatalf("disable response: want {42, enabled=false}, got %+v", got)
	}
	if !env.store.disabled[42] {
		t.Errorf("store not updated: source 42 should be disabled")
	}

	// Re-enable it.
	rec = env.do(http.MethodPatch, "/api/sources/42/enabled", `{"enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("enable: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if !got.Enabled {
		t.Fatalf("enable response: want enabled=true, got %+v", got)
	}
	if env.store.disabled[42] {
		t.Errorf("store not updated: source 42 should be re-enabled")
	}
}

// TestSetSourceEnabled_MissingField400 proves a body without the required
// `enabled` field is a 400 (fail-closed — an absent field must not read as a
// disable).
func TestSetSourceEnabled_MissingField400(t *testing.T) {
	env := newTestEnv(t, prefsFake())
	rec := env.do(http.MethodPatch, "/api/sources/42/enabled", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing enabled: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestSetSourceEnabled_BadSourceID400 proves a non-numeric :sourceId is a 400.
func TestSetSourceEnabled_BadSourceID400(t *testing.T) {
	env := newTestEnv(t, prefsFake())
	rec := env.do(http.MethodPatch, "/api/sources/not-a-number/enabled", `{"enabled":false}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad sourceId: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestSetSourceEnabled_StoreError502 proves a store write failure is a 502, not
// a false 200.
func TestSetSourceEnabled_StoreError502(t *testing.T) {
	env := newTestEnv(t, prefsFake())
	env.store.setErr = errors.New("db write failed")
	rec := env.do(http.MethodPatch, "/api/sources/42/enabled", `{"enabled":false}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("store error: want 502, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestSetSourceEnabled_Unauthorized proves the route is behind RequireOwner.
func TestSetSourceEnabled_Unauthorized(t *testing.T) {
	env := newTestEnv(t, prefsFake())
	rec := env.noAuth(http.MethodPatch, "/api/sources/42/enabled", `{"enabled":false}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: want 401, got %d", rec.Code)
	}
}
