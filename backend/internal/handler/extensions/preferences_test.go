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

// The source id every prefsFake fixture below addresses its preferences under.
const prefsSourceID int64 = 42

// switchPref / listPref / multiPref are seed preferences covering the variants
// a write can target (bool, string, string-array), keyed rather than
// positioned (QCAT-253, P2 Suwayomi-removal slice 5: the engine host's
// SetPreferences is key-addressed).
func switchPref(current bool) sourceengine.Preference {
	return sourceengine.Preference{
		Type: sourceengine.PreferenceSwitchCompat, Key: "dataSaver",
		Title: "Data saver", CurrentValue: current, DefaultValue: false,
	}
}

func listPref(current string) sourceengine.Preference {
	return sourceengine.Preference{
		Type: sourceengine.PreferenceList, Key: "quality",
		Title: "Quality", CurrentValue: current, DefaultValue: "",
		Entries: []string{"Original", "Low"}, EntryValues: []string{"", ".256.jpg"},
	}
}

func multiPref(current []string) sourceengine.Preference {
	var cv any
	if current != nil {
		cv = current
	}
	return sourceengine.Preference{
		Type: sourceengine.PreferenceMultiSelect, Key: "rating",
		Title: "Rating", CurrentValue: cv,
		Entries: []string{"Safe", "Erotica"}, EntryValues: []string{"safe", "erotica"},
	}
}

// prefsFake builds a sourceenginefake.Client seeded with one extension whose
// single source (id 42) carries the three variant preferences above.
func prefsFake() *sourceenginefake.Client {
	return sourceenginefake.New(
		sourceenginefake.WithExtensions([]sourceengine.Extension{
			{PkgName: "pkg.test", Sources: []sourceengine.Source{{ID: prefsSourceID, Name: "MangaDex", Lang: "en"}}},
		}),
		sourceenginefake.WithPreferences(prefsSourceID, []sourceengine.Preference{
			switchPref(true), listPref(".256.jpg"), multiPref([]string{"safe"}),
		}),
	)
}

// findDTOByKey returns the preference DTO whose Key matches key, failing the
// test if absent.
func findDTOByKey(t *testing.T, prefs []handler.SourcePreferenceDTO, key string) handler.SourcePreferenceDTO {
	t.Helper()
	for _, p := range prefs {
		if p.Key == key {
			return p
		}
	}
	t.Fatalf("no preference with key %q in %+v", key, prefs)
	return handler.SourcePreferenceDTO{}
}

// --- GET preferences ----------------------------------------------------------

// TestPreferences_OK proves GET groups an extension's preferences by source
// (resolved from Extensions()'s own embedded Sources — no separate lookup
// call) and maps every variant's untyped currentValue/default to its natural
// JSON type.
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
	if g.SourceID != "42" || g.SourceName != "MangaDex" || g.Lang != "en" {
		t.Errorf("group identity mismatch: %+v", g)
	}
	if len(g.Preferences) != 3 {
		t.Fatalf("want 3 preferences, got %d", len(g.Preferences))
	}
	assertVariantJSONTypes(t, g.Preferences)
}

// assertVariantJSONTypes checks the seeded switch/list/multi preferences mapped
// to their natural JSON types (bool / string + entries / array), addressed by
// key.
func assertVariantJSONTypes(t *testing.T, prefs []handler.SourcePreferenceDTO) {
	t.Helper()
	sw := findDTOByKey(t, prefs, "dataSaver")
	if b, ok := sw.CurrentValue.(bool); !ok || !b {
		t.Errorf("switch currentValue: want JSON true, got %v (%T)", sw.CurrentValue, sw.CurrentValue)
	}
	list := findDTOByKey(t, prefs, "quality")
	if s, ok := list.CurrentValue.(string); !ok || s != ".256.jpg" {
		t.Errorf("list currentValue: want \".256.jpg\", got %v", list.CurrentValue)
	}
	if len(list.EntryValues) != 2 {
		t.Errorf("list entryValues dropped: %+v", list)
	}
	multi := findDTOByKey(t, prefs, "rating")
	if multi.Type != sourceengine.PreferenceMultiSelect {
		t.Errorf("multiselect type: want %q, got %q", sourceengine.PreferenceMultiSelect, multi.Type)
	}
}

// TestPreferences_NotFound proves an unknown pkgName (absent from Extensions())
// is a 404, not a false 200 or a panic.
func TestPreferences_NotFound(t *testing.T) {
	env := newTestEnv(t, prefsFake())
	rec := env.do(http.MethodGet, "/api/suwayomi/extensions/pkg.unknown/preferences", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("Preferences unknown pkgName: want 404, got %d (%s)", rec.Code, rec.Body.String())
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

// TestPreferences_ExtensionsUpstream502 proves a failure resolving the
// extension list (needed to find pkgName's sources) is a 502.
func TestPreferences_ExtensionsUpstream502(t *testing.T) {
	env := newTestEnv(t, sourceenginefake.New(sourceenginefake.WithError("Extensions", errors.New("engine down"))))
	rec := env.do(http.MethodGet, "/api/suwayomi/extensions/pkg.test/preferences", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("Preferences Extensions upstream: want 502, got %d", rec.Code)
	}
}

// TestPreferences_PreferencesUpstream502 proves a failure reading one source's
// preferences is a 502.
func TestPreferences_PreferencesUpstream502(t *testing.T) {
	env := newTestEnv(t, sourceenginefake.New(
		sourceenginefake.WithExtensions([]sourceengine.Extension{
			{PkgName: "pkg.test", Sources: []sourceengine.Source{{ID: prefsSourceID, Name: "MangaDex", Lang: "en"}}},
		}),
		sourceenginefake.WithError("Preferences", errors.New("source unreachable")),
	))
	rec := env.do(http.MethodGet, "/api/suwayomi/extensions/pkg.test/preferences", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("Preferences upstream: want 502, got %d", rec.Code)
	}
}

// --- PATCH preference ---------------------------------------------------------

// TestSetPreference_OK proves a write sends the correctly-typed value for the
// variant at the key and returns the authoritative refreshed list (§16).
func TestSetPreference_OK(t *testing.T) {
	fc := prefsFake()
	env := newTestEnv(t, fc)

	rec := env.do(http.MethodPatch, "/api/suwayomi/extensions/pkg.test/preferences",
		`{"sourceId":"42","key":"dataSaver","value":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("SetPreference: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if fc.CallCount("SetPreferences") != 1 {
		t.Fatalf("SetPreferences calls = %d, want 1", fc.CallCount("SetPreferences"))
	}
	var got []handler.SourcePreferenceDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 prefs back, got %d", len(got))
	}
	// The dispatched value must be the correctly-typed bool write for a switch
	// preference, not e.g. a string coercion of "false" — observed via the
	// refreshed list's JSON-typed currentValue.
	pref := findDTOByKey(t, got, "dataSaver")
	if b, ok := pref.CurrentValue.(bool); !ok || b {
		t.Errorf("refreshed switch currentValue: want JSON false, got %v (%T)", pref.CurrentValue, pref.CurrentValue)
	}
}

// TestSetPreference_ListValue proves a string value writes a list preference.
func TestSetPreference_ListValue(t *testing.T) {
	fc := prefsFake()
	env := newTestEnv(t, fc)

	rec := env.do(http.MethodPatch, "/api/suwayomi/extensions/pkg.test/preferences",
		`{"sourceId":"42","key":"quality","value":"low"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("SetPreference list: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got []handler.SourcePreferenceDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	pref := findDTOByKey(t, got, "quality")
	if s, ok := pref.CurrentValue.(string); !ok || s != "low" {
		t.Errorf("refreshed list currentValue: want \"low\", got %v (%T)", pref.CurrentValue, pref.CurrentValue)
	}
}

// TestSetPreference_MultiSelectValue proves an array value writes a multi-select.
func TestSetPreference_MultiSelectValue(t *testing.T) {
	fc := prefsFake()
	env := newTestEnv(t, fc)

	rec := env.do(http.MethodPatch, "/api/suwayomi/extensions/pkg.test/preferences",
		`{"sourceId":"42","key":"rating","value":["safe","erotica"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("SetPreference multiselect: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got []handler.SourcePreferenceDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	pref := findDTOByKey(t, got, "rating")
	list, ok := pref.CurrentValue.([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("refreshed multiselect currentValue: want a 2-element array, got %v (%T)", pref.CurrentValue, pref.CurrentValue)
	}
}

// TestSetPreference_TypeMismatch400 proves a value whose JSON type doesn't
// match the variant at that key is a 400 (a boolean sent to a list
// preference), and no write is dispatched.
func TestSetPreference_TypeMismatch400(t *testing.T) {
	fc := prefsFake()
	env := newTestEnv(t, fc)
	// "quality" is a ListPreference — a boolean value is invalid.
	rec := env.do(http.MethodPatch, "/api/suwayomi/extensions/pkg.test/preferences",
		`{"sourceId":"42","key":"quality","value":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("type mismatch: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if fc.CallCount("SetPreferences") != 0 {
		t.Error("a write was dispatched despite a type-mismatched value")
	}
}

// TestSetPreference_NullValue400 proves an explicit JSON `null` value is
// rejected with the same "value required" 400 as an absent value (M3-1).
// Without this guard, `null` (4 bytes) passes the "value present" length gate
// and then json.Unmarshal("null", &dst) succeeds leaving a zero value —
// silently clearing the preference instead of failing closed.
func TestSetPreference_NullValue400(t *testing.T) {
	fc := prefsFake()
	env := newTestEnv(t, fc)
	rec := env.do(http.MethodPatch, "/api/suwayomi/extensions/pkg.test/preferences",
		`{"sourceId":"42","key":"dataSaver","value":null}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("null value: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if fc.CallCount("SetPreferences") != 0 {
		t.Error("a write was dispatched despite a null value")
	}
}

// TestSetPreference_UnknownKey400 proves a key absent from the source's live
// preference list is a clean 400 (not a raw engine-host 502), and no write is
// dispatched.
func TestSetPreference_UnknownKey400(t *testing.T) {
	fc := prefsFake()
	env := newTestEnv(t, fc)
	rec := env.do(http.MethodPatch, "/api/suwayomi/extensions/pkg.test/preferences",
		`{"sourceId":"42","key":"doesNotExist","value":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown key: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if fc.CallCount("SetPreferences") != 0 {
		t.Error("a write was dispatched for an unknown key")
	}
}

// TestSetPreference_MissingFields400 proves blank/unparseable sourceId,
// missing key, and missing value are all 400s.
func TestSetPreference_MissingFields400(t *testing.T) {
	cases := map[string]string{
		"blank sourceId":       `{"sourceId":"","key":"dataSaver","value":true}`,
		"non-numeric sourceId": `{"sourceId":"not-a-number","key":"dataSaver","value":true}`,
		"missing key":          `{"sourceId":"42","value":true}`,
		"missing value":        `{"sourceId":"42","key":"dataSaver"}`,
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

// TestSetPreference_ReadUpstream502 proves a failure of the pre-write
// Preferences read (needed to resolve the variant at the key) is a 502.
func TestSetPreference_ReadUpstream502(t *testing.T) {
	env := newTestEnv(t, sourceenginefake.New(sourceenginefake.WithError("Preferences", errors.New("source down"))))
	rec := env.do(http.MethodPatch, "/api/suwayomi/extensions/pkg.test/preferences",
		`{"sourceId":"42","key":"dataSaver","value":true}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("read upstream failure: want 502, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestSetPreference_Upstream502 proves an engine-host write failure is a 502.
func TestSetPreference_Upstream502(t *testing.T) {
	fc := sourceenginefake.New(
		sourceenginefake.WithPreferences(prefsSourceID, []sourceengine.Preference{switchPref(true)}),
		sourceenginefake.WithError("SetPreferences", errors.New("engine rejected")),
	)
	env := newTestEnv(t, fc)
	rec := env.do(http.MethodPatch, "/api/suwayomi/extensions/pkg.test/preferences",
		`{"sourceId":"42","key":"dataSaver","value":true}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("upstream write failure: want 502, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestSetPreference_Unauthorized proves the write route is behind RequireOwner.
func TestSetPreference_Unauthorized(t *testing.T) {
	env := newTestEnv(t, prefsFake())
	rec := env.noAuth(http.MethodPatch, "/api/suwayomi/extensions/pkg.test/preferences",
		`{"sourceId":"42","key":"dataSaver","value":true}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("SetPreference no token: want 401, got %d", rec.Code)
	}
}
