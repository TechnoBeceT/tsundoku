package extensions_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"

	handler "github.com/technobecet/tsundoku/internal/handler/extensions"
	suwayomicli "github.com/technobecet/tsundoku/internal/suwayomi"
)

// boolPtr / strPtr build pointer payloads for the preference fixtures.
func boolPtr(b bool) *bool    { return &b }
func strPtr(s string) *string { return &s }

// switchPref / listPref / multiPref are seed preferences covering the variants a
// write can target (bool, string, string-array).
func switchPref(pos int, current bool) suwayomicli.SourcePreference {
	return suwayomicli.SourcePreference{
		Type: suwayomicli.PreferenceSwitch, Position: pos, Key: "dataSaver",
		Title: "Data saver", CurrentBool: boolPtr(current), DefaultBool: false,
	}
}

func listPref(pos int, current string) suwayomicli.SourcePreference {
	return suwayomicli.SourcePreference{
		Type: suwayomicli.PreferenceList, Position: pos, Key: "quality",
		Title: "Quality", CurrentString: strPtr(current), DefaultString: strPtr(""),
		Entries: []string{"Original", "Low"}, EntryValues: []string{"", ".256.jpg"},
	}
}

func multiPref(pos int, current []string) suwayomicli.SourcePreference {
	return suwayomicli.SourcePreference{
		Type: suwayomicli.PreferenceMultiSelect, Position: pos, Key: "rating",
		Title: "Rating", CurrentStringList: current,
		Entries: []string{"Safe", "Erotica"}, EntryValues: []string{"safe", "erotica"},
	}
}

// prefsFake builds a fakeClient seeded with one source and its preferences.
func prefsFake() *fakeClient {
	src := suwayomicli.Source{ID: "src-en", Name: "MangaDex", Lang: "en"}
	return &fakeClient{
		sources: []suwayomicli.Source{src},
		prefsBySource: map[string][]suwayomicli.SourcePreference{
			"src-en": {switchPref(0, true), listPref(1, ".256.jpg"), multiPref(2, []string{"safe"})},
		},
	}
}

// --- GET preferences ----------------------------------------------------------

// TestPreferences_OK proves GET groups an extension's preferences by source and
// maps every variant's typed currentValue/default to its natural JSON type.
func TestPreferences_OK(t *testing.T) {
	env := newTestEnv(t, prefsFake())
	rec := env.do(http.MethodGet, "/api/suwayomi/extensions/pkg.test/preferences", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("Preferences: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got handler.SourcePreferencesBySourceDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Sources) != 1 {
		t.Fatalf("want 1 source group, got %d", len(got.Sources))
	}
	g := got.Sources[0]
	if g.SourceID != "src-en" || g.SourceName != "MangaDex" || g.Lang != "en" {
		t.Errorf("group identity mismatch: %+v", g)
	}
	if len(g.Preferences) != 3 {
		t.Fatalf("want 3 preferences, got %d", len(g.Preferences))
	}
	assertVariantJSONTypes(t, g.Preferences)
}

// assertVariantJSONTypes checks the seeded switch/list/multi preferences mapped to
// their natural JSON types (bool / string + entries / array + position).
func assertVariantJSONTypes(t *testing.T, prefs []handler.SourcePreferenceDTO) {
	t.Helper()
	// Switch: currentValue is a JSON bool.
	if b, ok := prefs[0].CurrentValue.(bool); !ok || !b {
		t.Errorf("switch currentValue: want JSON true, got %v (%T)", prefs[0].CurrentValue, prefs[0].CurrentValue)
	}
	// List: currentValue is a JSON string; entries/entryValues present.
	if s, ok := prefs[1].CurrentValue.(string); !ok || s != ".256.jpg" {
		t.Errorf("list currentValue: want \".256.jpg\", got %v", prefs[1].CurrentValue)
	}
	if len(prefs[1].EntryValues) != 2 {
		t.Errorf("list entryValues dropped: %+v", prefs[1])
	}
	// Position round-trips (the write selector).
	if prefs[2].Position != 2 {
		t.Errorf("multiselect position: want 2, got %d", prefs[2].Position)
	}
}

// TestPreferences_Unauthorized proves the route is behind RequireOwner.
func TestPreferences_Unauthorized(t *testing.T) {
	env := newTestEnv(t, prefsFake())
	rec := env.noAuth(http.MethodGet, "/api/suwayomi/extensions/pkg.test/preferences", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Preferences no token: want 401, got %d", rec.Code)
	}
}

// TestPreferences_Upstream502 proves a Suwayomi failure resolving sources is a 502.
func TestPreferences_Upstream502(t *testing.T) {
	fc := prefsFake()
	fc.extSourcesErr = errors.New("suwayomi down")
	env := newTestEnv(t, fc)
	rec := env.do(http.MethodGet, "/api/suwayomi/extensions/pkg.test/preferences", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("Preferences upstream: want 502, got %d", rec.Code)
	}
}

// --- PATCH preference ---------------------------------------------------------

// TestSetPreference_OK proves a write sends the correctly-typed value for the
// variant at the position and returns the authoritative refreshed list (§16).
func TestSetPreference_OK(t *testing.T) {
	fc := prefsFake()
	// The authoritative post-write list the fake echoes back: switch now false.
	fc.setPrefResult = []suwayomicli.SourcePreference{switchPref(0, false), listPref(1, ".256.jpg"), multiPref(2, []string{"safe"})}
	env := newTestEnv(t, fc)

	rec := env.do(http.MethodPatch, "/api/suwayomi/extensions/pkg.test/preferences",
		`{"sourceId":"src-en","position":0,"value":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("SetPreference: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !fc.setPrefCalled || fc.lastSetSourceID != "src-en" || fc.lastSetPosition != 0 {
		t.Errorf("write not dispatched correctly: called=%v src=%q pos=%d", fc.setPrefCalled, fc.lastSetSourceID, fc.lastSetPosition)
	}
	// The dispatched value must be the correctly-typed bool write for a switch
	// preference, not e.g. a string coercion of "false".
	wantValue := suwayomicli.BoolPreferenceValue(suwayomicli.PreferenceSwitch, false)
	if !reflect.DeepEqual(fc.lastSetValue, wantValue) {
		t.Errorf("dispatched value: got %+v, want %+v", fc.lastSetValue, wantValue)
	}
	var got []handler.SourcePreferenceDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The response is the refreshed list — the switch flipped to false (§16).
	if len(got) != 3 {
		t.Fatalf("want 3 prefs back, got %d", len(got))
	}
	if b, ok := got[0].CurrentValue.(bool); !ok || b {
		t.Errorf("refreshed switch currentValue: want false, got %v", got[0].CurrentValue)
	}
}

// TestSetPreference_ListValue proves a string value writes a list preference.
func TestSetPreference_ListValue(t *testing.T) {
	fc := prefsFake()
	fc.setPrefResult = []suwayomicli.SourcePreference{listPref(1, ".256.jpg")}
	env := newTestEnv(t, fc)

	rec := env.do(http.MethodPatch, "/api/suwayomi/extensions/pkg.test/preferences",
		`{"sourceId":"src-en","position":1,"value":".256.jpg"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("SetPreference list: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if fc.lastSetPosition != 1 {
		t.Errorf("want position 1, got %d", fc.lastSetPosition)
	}
	// The dispatched value must be the correctly-typed string write for a list
	// preference, not e.g. left as raw JSON.
	wantValue := suwayomicli.StringPreferenceValue(suwayomicli.PreferenceList, ".256.jpg")
	if !reflect.DeepEqual(fc.lastSetValue, wantValue) {
		t.Errorf("dispatched value: got %+v, want %+v", fc.lastSetValue, wantValue)
	}
}

// TestSetPreference_MultiSelectValue proves an array value writes a multi-select.
func TestSetPreference_MultiSelectValue(t *testing.T) {
	fc := prefsFake()
	fc.setPrefResult = []suwayomicli.SourcePreference{multiPref(2, []string{"safe", "erotica"})}
	env := newTestEnv(t, fc)

	rec := env.do(http.MethodPatch, "/api/suwayomi/extensions/pkg.test/preferences",
		`{"sourceId":"src-en","position":2,"value":["safe","erotica"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("SetPreference multiselect: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if fc.lastSetPosition != 2 {
		t.Errorf("want position 2, got %d", fc.lastSetPosition)
	}
	// The dispatched value must be the correctly-typed string-slice write for a
	// multi-select preference.
	wantValue := suwayomicli.MultiSelectPreferenceValue([]string{"safe", "erotica"})
	if !reflect.DeepEqual(fc.lastSetValue, wantValue) {
		t.Errorf("dispatched value: got %+v, want %+v", fc.lastSetValue, wantValue)
	}
}

// TestSetPreference_TypeMismatch400 proves a value whose JSON type doesn't match
// the variant at that position is a 400 (a bool sent to a list preference), and
// no write is dispatched.
func TestSetPreference_TypeMismatch400(t *testing.T) {
	fc := prefsFake()
	env := newTestEnv(t, fc)
	// position 1 is a ListPreference — a boolean value is invalid.
	rec := env.do(http.MethodPatch, "/api/suwayomi/extensions/pkg.test/preferences",
		`{"sourceId":"src-en","position":1,"value":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("type mismatch: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if fc.setPrefCalled {
		t.Error("a write was dispatched despite a type-mismatched value")
	}
}

// TestSetPreference_NullValue400 proves an explicit JSON `null` value is
// rejected with the same "value required" 400 as an absent value (M3-1). Without
// this guard, `null` (4 bytes) passes the "value present" length gate and then
// json.Unmarshal("null", &dst) succeeds leaving a zero value — silently
// clearing the preference instead of failing closed.
func TestSetPreference_NullValue400(t *testing.T) {
	fc := prefsFake()
	env := newTestEnv(t, fc)
	rec := env.do(http.MethodPatch, "/api/suwayomi/extensions/pkg.test/preferences",
		`{"sourceId":"src-en","position":0,"value":null}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("null value: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if fc.setPrefCalled {
		t.Error("a write was dispatched despite a null value")
	}
}

// TestSetPreference_OutOfRange400 proves a position past the end of the list is a
// clean 400 (not a raw Suwayomi 502), and no write is dispatched.
func TestSetPreference_OutOfRange400(t *testing.T) {
	fc := prefsFake()
	env := newTestEnv(t, fc)
	rec := env.do(http.MethodPatch, "/api/suwayomi/extensions/pkg.test/preferences",
		`{"sourceId":"src-en","position":99,"value":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("out of range: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if fc.setPrefCalled {
		t.Error("a write was dispatched for an out-of-range position")
	}
}

// TestSetPreference_MissingFields400 proves blank sourceId / missing position are 400s.
func TestSetPreference_MissingFields400(t *testing.T) {
	cases := map[string]string{
		"blank sourceId":   `{"sourceId":"","position":0,"value":true}`,
		"missing position": `{"sourceId":"src-en","value":true}`,
		"missing value":    `{"sourceId":"src-en","position":0}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			env := newTestEnv(t, prefsFake())
			rec := env.do(http.MethodPatch, "/api/suwayomi/extensions/pkg.test/preferences", body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s: want 400, got %d (%s)", name, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestSetPreference_Upstream502 proves a Suwayomi write failure is a 502.
func TestSetPreference_Upstream502(t *testing.T) {
	fc := prefsFake()
	fc.setPrefErr = errors.New("Expected change to SwitchPreferenceCompat")
	env := newTestEnv(t, fc)
	rec := env.do(http.MethodPatch, "/api/suwayomi/extensions/pkg.test/preferences",
		`{"sourceId":"src-en","position":0,"value":true}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("upstream write failure: want 502, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestSetPreference_Unauthorized proves the write route is behind RequireOwner.
func TestSetPreference_Unauthorized(t *testing.T) {
	env := newTestEnv(t, prefsFake())
	rec := env.noAuth(http.MethodPatch, "/api/suwayomi/extensions/pkg.test/preferences",
		`{"sourceId":"src-en","position":0,"value":true}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("SetPreference no token: want 401, got %d", rec.Code)
	}
}
