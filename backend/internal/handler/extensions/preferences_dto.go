package extensions

import (
	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// SourcePreferenceDTO is the JSON shape of one source preference. It is a
// camelCase mirror of sourceengine.Preference, flattened so the FE reads a
// single control per row keyed off `type`:
//   - CheckBoxPreference / SwitchPreferenceCompat: currentValue/default are booleans.
//   - ListPreference / EditTextPreference:         currentValue/default are strings
//     (List also carries entries/entryValues).
//   - MultiSelectListPreference:                   currentValue/default are string arrays.
//
// currentValue and default are `any` so each variant serialises to its natural
// JSON type (bool / string / array) or null when unset — the FE narrows on
// `type`. A preference write is addressed by `key` (see PreferenceUpdateRequest)
// — there is no position field (the engine host's SetPreferences is
// key-addressed, unlike the retired Suwayomi position-indexed write).
type SourcePreferenceDTO struct {
	// Type is the union variant (the androidx.preference class simpleName).
	Type string `json:"type"`
	// Key is the source-internal preference key — the write selector.
	Key string `json:"key"`
	// Title is the human-readable label ("" when null).
	Title string `json:"title"`
	// Summary is the human-readable help text ("" when null).
	Summary string `json:"summary"`
	// CurrentValue is the current value: bool | string | []string | null.
	CurrentValue any `json:"currentValue"`
	// Default is the default value: bool | string | []string | null.
	Default any `json:"default"`
	// Entries are the option labels (List/MultiSelect only; [] otherwise).
	Entries []string `json:"entries"`
	// EntryValues are the stored option values (List/MultiSelect only; [] otherwise).
	EntryValues []string `json:"entryValues"`
}

// SourcePreferencesGroupDTO is one source's preferences within the grouped GET
// response — a single extension backs one source per language.
//
// (QCAT-253, P2 Suwayomi-removal slice 5): `enabled` (the per-language
// enable/disable toggle) is RETIRED along with PATCH
// /api/suwayomi/sources/:sourceId/enabled — sourceengine has no server-side
// "disabled source" concept to proxy (see handler.go's package doc).
type SourcePreferencesGroupDTO struct {
	// SourceID is the engine host source id (the write body's sourceId).
	SourceID string `json:"sourceId"`
	// SourceName is the human-readable source name.
	SourceName string `json:"sourceName"`
	// Lang is the source's BCP-47 language tag.
	Lang string `json:"lang"`
	// Preferences are this source's configurable preferences, in array order.
	Preferences []SourcePreferenceDTO `json:"preferences"`
}

// SourcePreferencesBySourceDTO is the GET response: an extension's preferences
// grouped by the (per-language) source they belong to.
type SourcePreferencesBySourceDTO struct {
	// Sources are the extension's sources, each with its own preference list.
	Sources []SourcePreferencesGroupDTO `json:"sources"`
}

// nonNilStrings normalises a nil slice to an empty slice so a JSON array field is
// [] (not null) — entries/entryValues are always arrays on the wire.
func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// toSourcePreferenceDTO maps one client Preference into the HTTP DTO. It is
// the SINGLE mapper both preference endpoints route through, so no field is
// dropped on one path but not another (§16).
func toSourcePreferenceDTO(p sourceengine.Preference) SourcePreferenceDTO {
	return SourcePreferenceDTO{
		Type:         p.Type,
		Key:          p.Key,
		Title:        p.Title,
		Summary:      p.Summary,
		CurrentValue: p.CurrentValue,
		Default:      p.DefaultValue,
		Entries:      nonNilStrings(p.Entries),
		EntryValues:  nonNilStrings(p.EntryValues),
	}
}

// toSourcePreferenceDTOs maps a slice of preferences through the single mapper.
// The result is always non-nil so an empty list serialises to [] (not null).
func toSourcePreferenceDTOs(prefs []sourceengine.Preference) []SourcePreferenceDTO {
	out := make([]SourcePreferenceDTO, 0, len(prefs))
	for _, p := range prefs {
		out = append(out, toSourcePreferenceDTO(p))
	}
	return out
}
