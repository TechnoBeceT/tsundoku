package suwayomi

import (
	"context"
	"fmt"
)

// This file (source_preferences.go) holds the per-source preference passthrough:
// reading a source's configurable preferences (the Tachiyomi/Mihon source
// settings — quality toggles, content-rating filters, blocked-group text, …)
// and writing a single one back. Like settings.go and extensions.go it is a PURE
// proxy — Tsundoku stores none of this; the preferences live entirely on
// whichever Suwayomi the client targets (BaseURL(), embed or external).
//
// Shapes confirmed by live introspection against Suwayomi v2.2.2100 (re-proven
// at merge by the build-tagged TestShape8_SourcePreferences):
//   - query    source(id: LongString!) { preferences { <union> } }
//   - union    Preference = CheckBoxPreference | SwitchPreference | ListPreference
//                          | MultiSelectListPreference | EditTextPreference
//   - mutation updateSourcePreference(input: { source: LongString!,
//              change: SourcePreferenceChangeInput! }) { preferences { <union> } }
//       SourcePreferenceChangeInput = { position: Int!, checkBoxState / switchState
//       / listState / multiSelectState / editTextState } — set EXACTLY one *State
//       field, matching the union variant at that POSITION. The mutation payload
//       already carries the FULL refreshed preferences[] (no separate re-read).
//
// FOOTGUNS (all live-confirmed, do not "fix"):
//   - Selection is POSITION-INDEXED, never key-indexed: SourcePreferenceChangeInput
//     has no key field; position is the sole selector and indexes directly into the
//     same array order source.preferences returns.
//   - currentValue / default clash across the union (Boolean vs String vs [String!]),
//     so they MUST be aliased per inline fragment or Suwayomi rejects the document
//     with a FieldsConflict validation error. key/title/summary (all String) and
//     entries/entryValues (both [String!]!) do NOT clash and share one response key.
//   - Sending the wrong *State field for the position's actual variant is rejected
//     by Suwayomi ("Expected change to <Variant>Compat"), and an out-of-range
//     position leaks a raw Kotlin "Index: N, Size: M" exception — both surface as
//     the ordinary upstream 502 (never re-interpreted into a friendlier 400, since
//     the message shape is a Suwayomi implementation detail, not a stable contract).

// PreferenceType is the union variant of a source preference. Its value is the
// GraphQL __typename verbatim, so a decoded node's Type is directly comparable to
// these constants and drives which SourcePreferenceChangeInput field a write sets.
type PreferenceType string

const (
	// PreferenceCheckBox is a boolean checkbox (writes checkBoxState).
	PreferenceCheckBox PreferenceType = "CheckBoxPreference"
	// PreferenceSwitch is a boolean switch — wire-identical to a checkbox, but
	// writes switchState (an Android checkbox-vs-switch UI distinction only).
	PreferenceSwitch PreferenceType = "SwitchPreference"
	// PreferenceList is a single-choice list (writes listState — one of EntryValues).
	PreferenceList PreferenceType = "ListPreference"
	// PreferenceMultiSelect is a multi-choice list (writes multiSelectState — a
	// subset of EntryValues; an empty selection is valid).
	PreferenceMultiSelect PreferenceType = "MultiSelectListPreference"
	// PreferenceEditText is a free-text field (writes editTextState — any string).
	PreferenceEditText PreferenceType = "EditTextPreference"
)

// SourcePreference is the Tsundoku-side view of one source preference, flattened
// from the GraphQL Preference union into a single tagged struct (mirrors how
// Extension flattens its node). Only the fields relevant to Type are populated:
//   - CheckBox / Switch: CurrentBool (nil when unset) + DefaultBool.
//   - List / EditText:   CurrentString + DefaultString (both nilable) — List also
//     carries Entries (labels) + EntryValues (stored values).
//   - MultiSelect:       CurrentStringList + DefaultStringList + Entries + EntryValues.
type SourcePreference struct {
	// Type is the union variant; it selects which payload fields are meaningful
	// and which SourcePreferenceChangeInput field a write must set.
	Type PreferenceType
	// Position is the 0-based index into the source's preferences array. It is
	// NOT a wire field — Tsundoku assigns it while decoding, because the write
	// mutation is position-indexed and needs it back.
	Position int
	// Key is the source-internal preference key (e.g. "thumbnailQuality_en"); ""
	// when the source reports null.
	Key string
	// Title is the human-readable label; "" when null.
	Title string
	// Summary is the human-readable help text; "" when null.
	Summary string

	// CurrentBool is the current value for CheckBox/Switch; nil when unset.
	CurrentBool *bool
	// DefaultBool is the default for CheckBox/Switch (Boolean! on the wire).
	DefaultBool bool

	// CurrentString is the current value for List/EditText; nil when unset/null.
	CurrentString *string
	// DefaultString is the default for List/EditText; nil when null.
	DefaultString *string

	// CurrentStringList is the current selection for MultiSelect; nil when null.
	CurrentStringList []string
	// DefaultStringList is the default selection for MultiSelect; nil when null.
	DefaultStringList []string

	// Entries are the human-readable option labels (List/MultiSelect only).
	Entries []string
	// EntryValues are the stored option values matching Entries by index
	// (List/MultiSelect only); a listState/multiSelectState write must use these.
	EntryValues []string
}

// PreferenceValue is a single typed preference write, built via one of the three
// constructors below. It carries the variant KIND alongside the value so that
// changeMap sends EXACTLY the one SourcePreferenceChangeInput field matching the
// position's variant (mirrors extensions.go's extensionPatch "exactly one field"
// discipline). It is opaque so a caller cannot hand-build an ambiguous value.
type PreferenceValue struct {
	kind      PreferenceType
	boolVal   *bool
	stringVal *string
	listVal   []string
}

// BoolPreferenceValue builds a write for a CheckBox or Switch preference. kind
// MUST be PreferenceCheckBox or PreferenceSwitch (it selects checkBoxState vs
// switchState); any other kind is rejected by changeMap at send time.
func BoolPreferenceValue(kind PreferenceType, v bool) PreferenceValue {
	return PreferenceValue{kind: kind, boolVal: &v}
}

// StringPreferenceValue builds a write for a List or EditText preference. kind
// MUST be PreferenceList or PreferenceEditText (it selects listState vs
// editTextState). v is passed through to Suwayomi as-is — Suwayomi, not
// Tsundoku, is the authority on what's valid: for a List preference it expects
// one of the preference's EntryValues, but Tsundoku does not check membership
// here (an invalid value surfaces as the ordinary upstream error).
func StringPreferenceValue(kind PreferenceType, v string) PreferenceValue {
	return PreferenceValue{kind: kind, stringVal: &v}
}

// MultiSelectPreferenceValue builds a write for a MultiSelectList preference. An
// empty (or nil) slice is a valid "clear all selections" write; the values must
// be a subset of the preference's EntryValues.
func MultiSelectPreferenceValue(v []string) PreferenceValue {
	return PreferenceValue{kind: PreferenceMultiSelect, listVal: v}
}

// boolStateField returns the SourcePreferenceChangeInput field name for a boolean
// preference: checkBoxState for a checkbox, switchState for a switch (the two are
// wire-identical apart from which field the write must set).
func boolStateField(kind PreferenceType) string {
	if kind == PreferenceCheckBox {
		return "checkBoxState"
	}
	return "switchState"
}

// stringStateField returns the SourcePreferenceChangeInput field name for a string
// preference: listState for a list, editTextState for a free-text field.
func stringStateField(kind PreferenceType) string {
	if kind == PreferenceList {
		return "listState"
	}
	return "editTextState"
}

// changeMap builds the SourcePreferenceChangeInput map for this value at the
// given array position: always the position, plus EXACTLY the one *State field
// matching the value's kind. A kind whose matching value was never set (a
// programmer error) is rejected rather than sending an empty/ambiguous change.
func (v PreferenceValue) changeMap(position int) (map[string]any, error) {
	m := map[string]any{"position": position}
	switch v.kind {
	case PreferenceCheckBox, PreferenceSwitch:
		if v.boolVal == nil {
			return nil, fmt.Errorf("suwayomi: %s value missing", v.kind)
		}
		m[boolStateField(v.kind)] = *v.boolVal
	case PreferenceList, PreferenceEditText:
		if v.stringVal == nil {
			return nil, fmt.Errorf("suwayomi: %s value missing", v.kind)
		}
		m[stringStateField(v.kind)] = *v.stringVal
	case PreferenceMultiSelect:
		// An empty selection is valid (clear all); normalise nil → [] so the
		// GraphQL [String!] variable is an empty array, never a JSON null.
		list := v.listVal
		if list == nil {
			list = []string{}
		}
		m["multiSelectState"] = list
	default:
		return nil, fmt.Errorf("suwayomi: unknown preference kind %q", v.kind)
	}
	return m, nil
}

// gqlPreferenceNode is the aliased decode target for one Preference union node.
// Every field that clashes across variants (currentValue/default) has a
// per-variant alias so the single query is FieldsConflict-free; key/title/summary
// and entries/entryValues are shared response keys (identical types across
// variants). __typename discriminates which alias set is meaningful.
type gqlPreferenceNode struct {
	Typename string  `json:"__typename"`
	Key      *string `json:"key"`
	Title    *string `json:"title"`
	Summary  *string `json:"summary"`

	// CheckBoxPreference
	CbCurrent *bool `json:"cbCurrent"`
	CbDefault *bool `json:"cbDefault"`
	// SwitchPreference
	SwCurrent *bool `json:"swCurrent"`
	SwDefault *bool `json:"swDefault"`
	// ListPreference
	LpCurrent *string `json:"lpCurrent"`
	LpDefault *string `json:"lpDefault"`
	// MultiSelectListPreference
	MslCurrent []string `json:"mslCurrent"`
	MslDefault []string `json:"mslDefault"`
	// EditTextPreference
	EtCurrent *string `json:"etCurrent"`
	EtDefault *string `json:"etDefault"`

	// Shared by List + MultiSelect (both [String!]! — same type, one response key).
	Entries     []string `json:"entries"`
	EntryValues []string `json:"entryValues"`
}

// derefString returns *p or "" when p is nil (collapses a null key/title/summary).
func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// derefBool returns *p or false when p is nil (a null Boolean! default is treated
// as false, though Suwayomi types default NON_NULL so this only guards decode).
func derefBool(p *bool) bool {
	if p == nil {
		return false
	}
	return *p
}

// toSourcePreference maps a decoded node to the flattened struct at the given
// array position, populating only the fields meaningful for its variant.
func (n gqlPreferenceNode) toSourcePreference(position int) SourcePreference {
	p := SourcePreference{
		Type:     PreferenceType(n.Typename),
		Position: position,
		Key:      derefString(n.Key),
		Title:    derefString(n.Title),
		Summary:  derefString(n.Summary),
	}
	switch p.Type {
	case PreferenceCheckBox:
		p.CurrentBool = n.CbCurrent
		p.DefaultBool = derefBool(n.CbDefault)
	case PreferenceSwitch:
		p.CurrentBool = n.SwCurrent
		p.DefaultBool = derefBool(n.SwDefault)
	case PreferenceList:
		p.CurrentString = n.LpCurrent
		p.DefaultString = n.LpDefault
		p.Entries = n.Entries
		p.EntryValues = n.EntryValues
	case PreferenceMultiSelect:
		p.CurrentStringList = n.MslCurrent
		p.DefaultStringList = n.MslDefault
		p.Entries = n.Entries
		p.EntryValues = n.EntryValues
	case PreferenceEditText:
		p.CurrentString = n.EtCurrent
		p.DefaultString = n.EtDefault
	}
	return p
}

// mapPreferenceNodes converts decoded nodes to []SourcePreference, assigning each
// its 0-based array position (the selector the write mutation needs back).
func mapPreferenceNodes(nodes []gqlPreferenceNode) []SourcePreference {
	out := make([]SourcePreference, len(nodes))
	for i, n := range nodes {
		out[i] = n.toSourcePreference(i)
	}
	return out
}

// sourcePreferenceSelection is the shared Preference-union selection used by both
// the read query and the write mutation payload, so the two never drift. Every
// clashing field (currentValue/default) is aliased per fragment (see the
// FieldsConflict footgun above); shared-type fields are selected once per fragment.
const sourcePreferenceSelection = `
    __typename
    ... on CheckBoxPreference { key title summary cbCurrent: currentValue cbDefault: default }
    ... on SwitchPreference { key title summary swCurrent: currentValue swDefault: default }
    ... on ListPreference { key title summary lpCurrent: currentValue lpDefault: default entries entryValues }
    ... on MultiSelectListPreference { key title summary mslCurrent: currentValue mslDefault: default entries entryValues }
    ... on EditTextPreference { key title summary etCurrent: currentValue etDefault: default }`

// sourcePreferencesQuery reads one source's preferences array (in-order).
const sourcePreferencesQuery = `
query SourcePreferences($sourceId: LongString!) {
  source(id: $sourceId) {
    preferences {` + sourcePreferenceSelection + `
    }
  }
}`

// updateSourcePreferenceMutation writes one preference by position and returns the
// FULL refreshed preferences array in the same round trip (no re-read needed).
const updateSourcePreferenceMutation = `
mutation UpdateSourcePreference($source: LongString!, $change: SourcePreferenceChangeInput!) {
  updateSourcePreference(input: { source: $source, change: $change }) {
    preferences {` + sourcePreferenceSelection + `
    }
  }
}`

// extensionSourcesQuery resolves an extension's sources via the ExtensionType.source
// link — the natural "one extension → N language sources" traversal that drives a
// per-extension Configure UI.
const extensionSourcesQuery = `
query ExtensionSources($pkgName: String!) {
  extension(pkgName: $pkgName) {
    source {
      nodes {
        id
        name
        lang
      }
    }
  }
}`

// gqlSourcePreferencesData is the typed `data` shape for the read query.
type gqlSourcePreferencesData struct {
	Source struct {
		Preferences []gqlPreferenceNode `json:"preferences"`
	} `json:"source"`
}

// gqlUpdateSourcePreferenceData is the typed `data` shape for the write mutation.
type gqlUpdateSourcePreferenceData struct {
	UpdateSourcePreference struct {
		Preferences []gqlPreferenceNode `json:"preferences"`
	} `json:"updateSourcePreference"`
}

// gqlExtensionSourcesData is the typed `data` shape for the extension→sources query.
type gqlExtensionSourcesData struct {
	Extension struct {
		Source struct {
			Nodes []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
				Lang string `json:"lang"`
			} `json:"nodes"`
		} `json:"source"`
	} `json:"extension"`
}

// SourcePreferences reads the configurable preferences of the source identified by
// sourceID, in array order (each carries its Position). A transport or GraphQL
// error is surfaced; an empty list (a source with no preferences) is valid.
func (c *httpClient) SourcePreferences(ctx context.Context, sourceID string) ([]SourcePreference, error) {
	vars := map[string]any{"sourceId": sourceID}
	var data gqlSourcePreferencesData
	if err := c.doGraphQL(ctx, sourcePreferencesQuery, vars, &data); err != nil {
		return nil, err
	}
	return mapPreferenceNodes(data.Source.Preferences), nil
}

// SetSourcePreference writes value to the preference at position in sourceID's
// preferences array and returns the FULL refreshed list (the mutation payload
// carries it, so no re-read is needed). value must match the variant at that
// position (built via the BoolPreferenceValue / StringPreferenceValue /
// MultiSelectPreferenceValue constructor for that type); a mismatch or an
// out-of-range position is surfaced as the ordinary upstream error.
func (c *httpClient) SetSourcePreference(ctx context.Context, sourceID string, position int, value PreferenceValue) ([]SourcePreference, error) {
	change, err := value.changeMap(position)
	if err != nil {
		return nil, err
	}
	vars := map[string]any{"source": sourceID, "change": change}
	var data gqlUpdateSourcePreferenceData
	if err := c.doGraphQL(ctx, updateSourcePreferenceMutation, vars, &data); err != nil {
		return nil, err
	}
	return mapPreferenceNodes(data.UpdateSourcePreference.Preferences), nil
}

// ExtensionSources lists the sources an installed extension provides (one per
// language) via the ExtensionType.source link. It drives the "which source(s)
// does this extension back → configure each" flow. A transport or GraphQL error
// is surfaced.
func (c *httpClient) ExtensionSources(ctx context.Context, pkgName string) ([]Source, error) {
	vars := map[string]any{"pkgName": pkgName}
	var data gqlExtensionSourcesData
	if err := c.doGraphQL(ctx, extensionSourcesQuery, vars, &data); err != nil {
		return nil, err
	}
	nodes := data.Extension.Source.Nodes
	out := make([]Source, len(nodes))
	for i, n := range nodes {
		out[i] = Source{ID: n.ID, Name: n.Name, Lang: n.Lang}
	}
	return out, nil
}
