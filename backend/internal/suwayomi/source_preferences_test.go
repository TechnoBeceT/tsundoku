package suwayomi_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// switchNode / listNode / multiNode / editNode / checkboxNode are canned
// Preference-union nodes keyed by the SAME per-fragment aliases the real query
// emits (see sourcePreferenceSelection), so the decode + variant mapping is fully
// asserted against the exact wire shape.
func switchNode() map[string]any {
	return map[string]any{
		"__typename": "SwitchPreference",
		"key":        "dataSaver_en",
		"title":      "Data saver",
		"summary":    "Load smaller images",
		"swCurrent":  true,
		"swDefault":  false,
	}
}

func listNode() map[string]any {
	return map[string]any{
		"__typename":  "ListPreference",
		"key":         "thumbnailQuality_en",
		"title":       "Thumbnail quality",
		"summary":     "",
		"lpCurrent":   ".512.jpg",
		"lpDefault":   "",
		"entries":     []string{"Original", "Medium", "Low"},
		"entryValues": []string{"", ".512.jpg", ".256.jpg"},
	}
}

func multiNode() map[string]any {
	return map[string]any{
		"__typename":  "MultiSelectListPreference",
		"key":         "contentRating_en",
		"title":       "Content rating",
		"summary":     "",
		"mslCurrent":  []string{"safe", "suggestive"},
		"mslDefault":  []string{"safe"},
		"entries":     []string{"Safe", "Suggestive", "Erotica"},
		"entryValues": []string{"safe", "suggestive", "erotica"},
	}
}

func editNode() map[string]any {
	return map[string]any{
		"__typename": "EditTextPreference",
		"key":        "blockedGroups_en",
		"title":      "Blocked groups",
		"summary":    "Comma-separated",
		"etCurrent":  nil,
		"etDefault":  nil,
	}
}

func checkboxNode() map[string]any {
	return map[string]any{
		"__typename": "CheckBoxPreference",
		"key":        "usePort443_en",
		"title":      "Use port 443",
		"summary":    "",
		"cbCurrent":  nil,
		"cbDefault":  true,
	}
}

// TestClient_SourcePreferences_DecodesEveryVariant proves the union query decodes
// every variant into the flattened SourcePreference struct, each carrying its
// 0-based array position and its variant-specific payload fields.
func TestClient_SourcePreferences_DecodesEveryVariant(t *testing.T) {
	resp := graphqlResponse(t, map[string]any{
		"source": map[string]any{
			"preferences": []map[string]any{
				switchNode(), listNode(), multiNode(), editNode(), checkboxNode(),
			},
		},
	}, nil)
	srv := httptest.NewServer(captureGraphQL(t, nil, nil, resp))
	defer srv.Close()

	got, err := newTestClient(t, srv).SourcePreferences(context.Background(), "123")
	if err != nil {
		t.Fatalf("SourcePreferences: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("SourcePreferences: got %d prefs, want 5", len(got))
	}
	// Positions are assigned in array order.
	for i, p := range got {
		if p.Position != i {
			t.Errorf("pref %d: Position = %d, want %d", i, p.Position, i)
		}
	}
	assertSwitchPref(t, got[0])
	assertListPref(t, got[1])
	assertMultiPref(t, got[2])
	assertEditPref(t, got[3])
	assertCheckboxPref(t, got[4])
}

// assertSwitchPref checks the Switch variant decode (CurrentBool + DefaultBool).
func assertSwitchPref(t *testing.T, sw suwayomi.SourcePreference) {
	t.Helper()
	if sw.Type != suwayomi.PreferenceSwitch || sw.Key != "dataSaver_en" {
		t.Errorf("switch: type/key mismatch: %+v", sw)
	}
	if sw.CurrentBool == nil || !*sw.CurrentBool || sw.DefaultBool {
		t.Errorf("switch: bool payload mismatch: current=%v default=%v", sw.CurrentBool, sw.DefaultBool)
	}
}

// assertListPref checks the List variant decode (CurrentString + entries/values).
func assertListPref(t *testing.T, lp suwayomi.SourcePreference) {
	t.Helper()
	if lp.Type != suwayomi.PreferenceList || lp.CurrentString == nil || *lp.CurrentString != ".512.jpg" {
		t.Errorf("list: type/current mismatch: %+v", lp)
	}
	if len(lp.Entries) != 3 || len(lp.EntryValues) != 3 || lp.EntryValues[1] != ".512.jpg" {
		t.Errorf("list: entries/entryValues mismatch: %+v", lp)
	}
}

// assertMultiPref checks the MultiSelect variant decode (CurrentStringList).
func assertMultiPref(t *testing.T, msl suwayomi.SourcePreference) {
	t.Helper()
	if msl.Type != suwayomi.PreferenceMultiSelect || len(msl.CurrentStringList) != 2 || msl.CurrentStringList[0] != "safe" {
		t.Errorf("multiselect: payload mismatch: %+v", msl)
	}
}

// assertEditPref checks the EditText variant decode (null current/default → nil).
func assertEditPref(t *testing.T, et suwayomi.SourcePreference) {
	t.Helper()
	if et.Type != suwayomi.PreferenceEditText || et.CurrentString != nil || et.DefaultString != nil {
		t.Errorf("edittext: expected nil current/default, got %+v", et)
	}
}

// assertCheckboxPref checks the CheckBox variant decode (null current → nil; default=true).
func assertCheckboxPref(t *testing.T, cb suwayomi.SourcePreference) {
	t.Helper()
	if cb.Type != suwayomi.PreferenceCheckBox || cb.CurrentBool != nil || !cb.DefaultBool {
		t.Errorf("checkbox: payload mismatch: %+v", cb)
	}
}

// TestClient_SourcePreferences_SendsSourceID proves the query carries the source
// id under $sourceId (the LongString selector).
func TestClient_SourcePreferences_SendsSourceID(t *testing.T) {
	var vars map[string]any
	resp := graphqlResponse(t, map[string]any{
		"source": map[string]any{"preferences": []map[string]any{}},
	}, nil)
	srv := httptest.NewServer(captureGraphQL(t, nil, &vars, resp))
	defer srv.Close()

	if _, err := newTestClient(t, srv).SourcePreferences(context.Background(), "999"); err != nil {
		t.Fatalf("SourcePreferences: %v", err)
	}
	if vars["sourceId"] != "999" {
		t.Errorf("sourceId = %v, want 999", vars["sourceId"])
	}
}

// TestClient_SourcePreferences_PropagatesError proves a GraphQL error is surfaced.
func TestClient_SourcePreferences_PropagatesError(t *testing.T) {
	resp := graphqlResponse(t, nil, []map[string]any{{"message": "boom"}})
	srv := httptest.NewServer(captureGraphQL(t, nil, nil, resp))
	defer srv.Close()

	if _, err := newTestClient(t, srv).SourcePreferences(context.Background(), "1"); err == nil {
		t.Fatal("SourcePreferences: want error, got nil")
	}
}

// TestClient_SetSourcePreference_SendsCorrectStateField proves each PreferenceValue
// constructor sends EXACTLY the one *State field matching its variant, alongside
// the position — the "exactly one field" invariant that keeps Suwayomi from
// rejecting the write with "Expected change to X".
func TestClient_SetSourcePreference_SendsCorrectStateField(t *testing.T) {
	cases := []struct {
		name  string
		value suwayomi.PreferenceValue
		key   string
		want  any
	}{
		{"checkbox", suwayomi.BoolPreferenceValue(suwayomi.PreferenceCheckBox, true), "checkBoxState", true},
		{"switch", suwayomi.BoolPreferenceValue(suwayomi.PreferenceSwitch, false), "switchState", false},
		{"list", suwayomi.StringPreferenceValue(suwayomi.PreferenceList, ".256.jpg"), "listState", ".256.jpg"},
		{"edittext", suwayomi.StringPreferenceValue(suwayomi.PreferenceEditText, "scanlator-x"), "editTextState", "scanlator-x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var vars map[string]any
			resp := graphqlResponse(t, map[string]any{
				"updateSourcePreference": map[string]any{"preferences": []map[string]any{switchNode()}},
			}, nil)
			srv := httptest.NewServer(captureGraphQL(t, nil, &vars, resp))
			defer srv.Close()

			got, err := newTestClient(t, srv).SetSourcePreference(context.Background(), "42", 3, tc.value)
			if err != nil {
				t.Fatalf("SetSourcePreference(%s): %v", tc.name, err)
			}
			// The refreshed list from the mutation payload is returned directly.
			if len(got) != 1 {
				t.Fatalf("SetSourcePreference(%s): got %d prefs, want 1", tc.name, len(got))
			}
			if vars["source"] != "42" {
				t.Errorf("source = %v, want 42", vars["source"])
			}
			change, ok := vars["change"].(map[string]any)
			if !ok {
				t.Fatalf("change missing or wrong type: %v", vars["change"])
			}
			// position is JSON-decoded to float64.
			if change["position"] != float64(3) {
				t.Errorf("position = %v, want 3", change["position"])
			}
			// Exactly one state field (plus position) — two keys total.
			if len(change) != 2 {
				t.Fatalf("%s: change must carry position + exactly one state field, got %v", tc.name, change)
			}
			if change[tc.key] != tc.want {
				t.Errorf("%s: change[%q] = %v, want %v", tc.name, tc.key, change[tc.key], tc.want)
			}
		})
	}
}

// TestClient_SetSourcePreference_MultiSelectSendsArray proves a multi-select write
// sends multiSelectState as an array, and an empty selection still sends an
// (empty) array — clear-all semantics — never a JSON null.
func TestClient_SetSourcePreference_MultiSelectSendsArray(t *testing.T) {
	var vars map[string]any
	resp := graphqlResponse(t, map[string]any{
		"updateSourcePreference": map[string]any{"preferences": []map[string]any{multiNode()}},
	}, nil)
	srv := httptest.NewServer(captureGraphQL(t, nil, &vars, resp))
	defer srv.Close()

	value := suwayomi.MultiSelectPreferenceValue([]string{"safe", "erotica"})
	if _, err := newTestClient(t, srv).SetSourcePreference(context.Background(), "42", 0, value); err != nil {
		t.Fatalf("SetSourcePreference: %v", err)
	}
	change := vars["change"].(map[string]any)
	arr, ok := change["multiSelectState"].([]any)
	if !ok || len(arr) != 2 || arr[0] != "safe" {
		t.Errorf("multiSelectState = %v, want [safe erotica]", change["multiSelectState"])
	}
}

// TestClient_SetSourcePreference_EmptyMultiSelectSendsEmptyArray proves a nil/empty
// selection is still sent as an empty array (clear all), not omitted or null.
func TestClient_SetSourcePreference_EmptyMultiSelectSendsEmptyArray(t *testing.T) {
	var vars map[string]any
	resp := graphqlResponse(t, map[string]any{
		"updateSourcePreference": map[string]any{"preferences": []map[string]any{multiNode()}},
	}, nil)
	srv := httptest.NewServer(captureGraphQL(t, nil, &vars, resp))
	defer srv.Close()

	value := suwayomi.MultiSelectPreferenceValue(nil)
	if _, err := newTestClient(t, srv).SetSourcePreference(context.Background(), "42", 0, value); err != nil {
		t.Fatalf("SetSourcePreference: %v", err)
	}
	change := vars["change"].(map[string]any)
	arr, ok := change["multiSelectState"].([]any)
	if !ok || len(arr) != 0 {
		t.Errorf("empty multiSelectState = %v, want empty array", change["multiSelectState"])
	}
}

// TestClient_SetSourcePreference_PropagatesError proves a GraphQL error (e.g. the
// "Expected change to X" type-mismatch or an out-of-range position) is surfaced.
func TestClient_SetSourcePreference_PropagatesError(t *testing.T) {
	resp := graphqlResponse(t, nil, []map[string]any{{"message": "Index: 999, Size: 12"}})
	srv := httptest.NewServer(captureGraphQL(t, nil, nil, resp))
	defer srv.Close()

	value := suwayomi.BoolPreferenceValue(suwayomi.PreferenceSwitch, true)
	if _, err := newTestClient(t, srv).SetSourcePreference(context.Background(), "1", 999, value); err == nil {
		t.Fatal("SetSourcePreference: want error, got nil")
	}
}

// TestClient_ExtensionSources_MapsNodes proves the extension→sources traversal
// decodes each SourceNode into a Source (id/name/lang).
func TestClient_ExtensionSources_MapsNodes(t *testing.T) {
	var vars map[string]any
	resp := graphqlResponse(t, map[string]any{
		"extension": map[string]any{
			"source": map[string]any{
				"nodes": []map[string]any{
					{"id": "111", "name": "MangaDex", "lang": "en"},
					{"id": "222", "name": "MangaDex", "lang": "ja"},
				},
			},
		},
	}, nil)
	srv := httptest.NewServer(captureGraphQL(t, nil, &vars, resp))
	defer srv.Close()

	got, err := newTestClient(t, srv).ExtensionSources(context.Background(), "eu.kanade.tachiyomi.extension.all.mangadex")
	if err != nil {
		t.Fatalf("ExtensionSources: %v", err)
	}
	if vars["pkgName"] != "eu.kanade.tachiyomi.extension.all.mangadex" {
		t.Errorf("pkgName = %v", vars["pkgName"])
	}
	if len(got) != 2 || got[0].ID != "111" || got[1].Lang != "ja" {
		t.Errorf("ExtensionSources: unexpected result %+v", got)
	}
}

// TestClient_ExtensionSources_PropagatesError proves a GraphQL error is surfaced.
func TestClient_ExtensionSources_PropagatesError(t *testing.T) {
	resp := graphqlResponse(t, nil, []map[string]any{{"message": "boom"}})
	srv := httptest.NewServer(captureGraphQL(t, nil, nil, resp))
	defer srv.Close()

	if _, err := newTestClient(t, srv).ExtensionSources(context.Background(), "pkg"); err == nil {
		t.Fatal("ExtensionSources: want error, got nil")
	}
}
