package extensions

import (
	suwayomicli "github.com/technobecet/tsundoku/internal/suwayomi"
)

// SourcePreferenceDTO is the JSON shape of one source preference. It is a
// camelCase mirror of suwayomi.SourcePreference, flattened so the FE reads a
// single control per row keyed off `type`:
//   - CheckBoxPreference / SwitchPreference: currentValue/default are booleans.
//   - ListPreference / EditTextPreference:   currentValue/default are strings
//     (List also carries entries/entryValues).
//   - MultiSelectListPreference:             currentValue/default are string arrays.
//
// currentValue and default are `any` so each variant serialises to its natural
// JSON type (bool / string / array) or null when unset — the FE narrows on `type`.
// position is the 0-based array index and is the ONLY selector a write may use
// (the write mutation is position-indexed); it must be taken from a FRESH read,
// never cached, since the array order can shift server-side.
type SourcePreferenceDTO struct {
	// Type is the union variant (Suwayomi __typename verbatim).
	Type string `json:"type"`
	// Position is the 0-based index used as the write selector.
	Position int `json:"position"`
	// Key is the source-internal preference key ("" when null).
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
type SourcePreferencesGroupDTO struct {
	// SourceID is the Suwayomi source id (the write body's sourceId).
	SourceID string `json:"sourceId"`
	// SourceName is the human-readable source name.
	SourceName string `json:"sourceName"`
	// Lang is the source's BCP-47 language tag.
	Lang string `json:"lang"`
	// Enabled is the per-language enable/disable toggle — the inverse of
	// suwayomicli.Source.Disabled: a disabled source is hidden from Tsundoku's
	// Discover/Search/Browse source lists but keeps updating any series
	// already adopted from it. Toggled via PATCH
	// /api/suwayomi/sources/:sourceId/enabled.
	Enabled bool `json:"enabled"`
	// Preferences are this source's configurable preferences, in array order.
	Preferences []SourcePreferenceDTO `json:"preferences"`
}

// SourceEnabledDTO is the response of PATCH /api/suwayomi/sources/:sourceId/enabled
// — the authoritative per-language enable/disable state after the write
// (§16 round-trip: re-read via suwayomicli.Client.Sources, never the request echo).
type SourceEnabledDTO struct {
	// SourceID is the Suwayomi source id.
	SourceID string `json:"sourceId"`
	// Enabled is the enable/disable state as re-read after the write.
	Enabled bool `json:"enabled"`
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

// currentAndDefault resolves a preference's currentValue + default to their
// natural JSON-typed values (bool / string / []string / null) based on the
// variant. A nil pointer or nil slice serialises to JSON null; a non-nil empty
// slice serialises to [] — preserving the "unset vs cleared" distinction.
func currentAndDefault(p suwayomicli.SourcePreference) (current, def any) {
	switch p.Type {
	case suwayomicli.PreferenceCheckBox, suwayomicli.PreferenceSwitch:
		return p.CurrentBool, p.DefaultBool
	case suwayomicli.PreferenceList, suwayomicli.PreferenceEditText:
		return p.CurrentString, p.DefaultString
	case suwayomicli.PreferenceMultiSelect:
		return p.CurrentStringList, p.DefaultStringList
	default:
		return nil, nil
	}
}

// toSourcePreferenceDTO maps one client SourcePreference into the HTTP DTO. It is
// the SINGLE mapper both preference endpoints route through, so no field is
// dropped on one path but not another (§16).
func toSourcePreferenceDTO(p suwayomicli.SourcePreference) SourcePreferenceDTO {
	current, def := currentAndDefault(p)
	return SourcePreferenceDTO{
		Type:         string(p.Type),
		Position:     p.Position,
		Key:          p.Key,
		Title:        p.Title,
		Summary:      p.Summary,
		CurrentValue: current,
		Default:      def,
		Entries:      nonNilStrings(p.Entries),
		EntryValues:  nonNilStrings(p.EntryValues),
	}
}

// toSourcePreferenceDTOs maps a slice of preferences through the single mapper.
// The result is always non-nil so an empty list serialises to [] (not null).
func toSourcePreferenceDTOs(prefs []suwayomicli.SourcePreference) []SourcePreferenceDTO {
	out := make([]SourcePreferenceDTO, 0, len(prefs))
	for _, p := range prefs {
		out = append(out, toSourcePreferenceDTO(p))
	}
	return out
}
