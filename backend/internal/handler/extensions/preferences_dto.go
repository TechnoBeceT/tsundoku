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
// `enabled` (the per-language enable/disable toggle) is resolved from Tsundoku's
// OWN disabled-source store (internal/disabledsource), NOT the engine (the
// internal engine has no server-side "disabled source" concept). It is the
// inverse of a DisabledSource row: a disabled source is hidden from the
// Discover/Search/Browse pickers (internal/imports) but keeps updating any
// series already adopted from it. Toggled via PATCH
// /api/sources/:sourceId/enabled; the FE hides a disabled group's preference
// block.
type SourcePreferencesGroupDTO struct {
	// SourceID is the engine host source id (the write body's sourceId).
	SourceID string `json:"sourceId"`
	// SourceName is the human-readable source name.
	SourceName string `json:"sourceName"`
	// Lang is the source's BCP-47 language tag.
	Lang string `json:"lang"`
	// Enabled is the per-language enable/disable state (Tsundoku-side; the
	// inverse of a DisabledSource row). Defaults to true when no disabled-flag
	// store is wired.
	Enabled bool `json:"enabled"`
	// IgnoreScanlator is the per-source "ignore scanlator" flag (Tsundoku-side;
	// presence of an IgnoreScanlatorSource row). true collapses this source's
	// per-uploader providers into one [Source] provider on future adopts (an
	// uploader-in-scanlator source, e.g. Hive Scans). Defaults to false when no
	// ignore-scanlator store is wired. The FE seeds its per-source toggle from it.
	IgnoreScanlator bool `json:"ignoreScanlator"`
	// Preferences are this source's configurable preferences, in array order.
	Preferences []SourcePreferenceDTO `json:"preferences"`
}

// SourceEnabledDTO is the response of PATCH /api/sources/:sourceId/enabled — the
// authoritative per-language enable/disable state after the write (re-read from
// Tsundoku's disabled-source store, never the request echo).
type SourceEnabledDTO struct {
	// SourceID is the engine host source id (stringified).
	SourceID string `json:"sourceId"`
	// Enabled is the enable/disable state as re-read after the write.
	Enabled bool `json:"enabled"`
}

// SourceIgnoreScanlatorDTO is the response of PATCH
// /api/sources/:sourceId/ignore-scanlator — the authoritative per-source
// ignore-scanlator state after the write (re-read from Tsundoku's
// ignore-scanlator store, never the request echo).
type SourceIgnoreScanlatorDTO struct {
	// SourceID is the engine host source id (stringified).
	SourceID string `json:"sourceId"`
	// IgnoreScanlator is the flag state as re-read after the write.
	IgnoreScanlator bool `json:"ignoreScanlator"`
	// Migration is the Slice-B on-enable migration summary — present ONLY when
	// flipping the flag ON with a collapser wired (it folds already-adopted
	// per-uploader providers into one [Source] provider + relabels their CBZs).
	// Absent (nil) when flipping OFF (one-way — no un-merge) or when no collapser
	// is configured. Surfaced by the FE so the owner sees what the toggle did (§16).
	Migration *ScanlatorMigrationDTO `json:"migration,omitempty"`
}

// ScanlatorMigrationDTO summarises the on-enable collapse migration: how many
// series were collapsed, how many per-uploader provider rows were folded in
// total, and how many series were skipped after an error (left for a re-run).
// Mirrors library.DedupAllProviders' (seriesProcessed, merged, skipped) shape.
type ScanlatorMigrationDTO struct {
	// SeriesProcessed is how many series had at least one per-uploader row folded.
	SeriesProcessed int `json:"seriesProcessed"`
	// Merged is the total number of per-uploader provider rows folded away.
	Merged int `json:"merged"`
	// Skipped is how many series errored during the sweep and were skipped.
	Skipped int `json:"skipped"`
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
