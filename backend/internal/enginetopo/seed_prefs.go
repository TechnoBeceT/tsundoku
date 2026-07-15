package enginetopo

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/technobecet/tsundoku/internal/ent"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	entsourcepreference "github.com/technobecet/tsundoku/internal/ent/sourcepreference"
	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// PreferenceSeedResult summarizes one SeedSourcePreferences pass: how many individual
// preference values were written, and how many SOURCES were skipped outright
// because listing their preferences failed (a per-source client error, not a
// per-preference one — see the doc comment below).
type PreferenceSeedResult struct {
	// Seeded is the count of individual (source_id, key) rows written
	// (created or updated) across every source that answered.
	Seeded int
	// SkippedSources is the count of distinct sources whose
	// sourceengine.Client.Preferences call errored; that source's
	// preferences are left untouched (partial success, never aborts the pass).
	SkippedSources int
}

// SeedSourcePreferences reads each library source's configurable preferences
// from the live engine and upserts them into the durable SourcePreference
// table (source_id, key) → (value, value_type), so a source's settings
// survive independently of whichever engine instance the client currently
// targets.
//
// The source set is every DISTINCT SeriesProvider.provider value already in
// the library. provider is the stable numeric engine source id string for a
// live-ingested row (see the source-identity-drift doc in the repo map); a
// disk-origin row instead stores a display NAME there and is silently
// skipped — there is no engine source to query preferences from, so it is
// not counted as a failure.
//
// Idempotent: re-running upserts each (source_id, key) row in place (no
// duplicate rows). Per-source failures are logged and skipped so one
// unreachable/erroring source can never abort the whole pass — partial
// success, matching the never-auto-delete/upsert-only conventions the rest
// of the ingest engine follows.
//
// CAPTURE-LATEST (deliberate — do NOT convert this to gap-fill): in Phase-1
// the ENGINE is a preference's only editor (there is no Tsundoku-side
// pref-editing path), so this pass CAPTURES-LATEST — re-reading the engine's
// current values every boot keeps the durable mirror fresh. Freezing the
// first-seen value (gap-fill) would let the mirror go stale the moment the
// owner edits a preference in the engine. (This becomes reconcile-aware in
// Phase-2, once Tsundoku can edit preferences too — see reconcile.go.)
//
// SECURITY: a source preference can hold a secret (e.g. a login-gated
// source's plaintext password — the engine has no password masking on the
// wire). Values are stored VERBATIM into the .Sensitive() value column,
// matching the ratified TrackerConnection plaintext-secrets model; this
// function does not attempt to detect or skip password-shaped preferences.
func SeedSourcePreferences(ctx context.Context, client sourceengine.Client, db *ent.Client) (PreferenceSeedResult, error) {
	providers, err := db.SeriesProvider.Query().
		Unique(true).
		Select(entseriesprovider.FieldProvider).
		Strings(ctx)
	if err != nil {
		return PreferenceSeedResult{}, fmt.Errorf("enginetopo.SeedSourcePreferences: query providers: %w", err)
	}

	var result PreferenceSeedResult
	for _, provider := range providers {
		sourceID, perr := strconv.ParseInt(provider, 10, 64)
		if perr != nil {
			// Disk-origin provider (a display name, not a numeric source id) —
			// nothing to seed from, and not a failure.
			continue
		}

		prefs, err := client.Preferences(ctx, sourceID)
		if err != nil {
			slog.WarnContext(ctx, "enginetopo: Preferences failed, skipping source",
				"source_id", sourceID, "err", err)
			result.SkippedSources++
			continue
		}

		for _, p := range prefs {
			if p.Key == "" {
				// A preference with no key cannot be addressed uniquely by
				// (source_id, key) — skip rather than risk merging distinct
				// preferences into one row.
				continue
			}
			value, valueType, ok := encodePreferenceValue(p)
			if !ok {
				// Unset current value (nil) — nothing to seed.
				continue
			}
			if err := upsertSourcePreference(ctx, db, sourceID, p.Key, value, valueType); err != nil {
				slog.WarnContext(ctx, "enginetopo: failed to persist source preference",
					"source_id", sourceID, "key", p.Key, "err", err)
				continue
			}
			result.Seeded++
		}
	}

	return result, nil
}

// encodePreferenceValue converts a decoded sourceengine.Preference's untyped
// CurrentValue into the (value, value_type) pair the SourcePreference row
// stores. ok is false when the preference has no current value set
// (CurrentValue == nil, or a type-mismatched/unrecognised Type) — there is
// nothing to seed for an unconfigured preference. value_type is the Type
// string verbatim, so a later read knows how to reinterpret value.
//
//   - CheckBox / SwitchCompat → "true"/"false".
//   - List / EditText         → the string as-is (this is the path a
//     plaintext password preference takes — see the SECURITY note on
//     SeedSourcePreferences).
//   - MultiSelect             → a JSON array string (e.g. `["a","b"]`).
//
// CurrentValue arrives already JSON-decoded (the engine host wire-encodes it
// as its natural JSON type), so a bool/string/[]string type assertion is
// enough — no further parsing. A []string CANNOT arrive as-is from
// encoding/json (JSON arrays decode into []any), so MultiSelect additionally
// accepts []any and coerces each element to a string.
func encodePreferenceValue(p sourceengine.Preference) (value string, valueType string, ok bool) {
	switch p.Type {
	case sourceengine.PreferenceCheckBox, sourceengine.PreferenceSwitchCompat:
		b, isBool := p.CurrentValue.(bool)
		if !isBool {
			return "", "", false
		}
		return strconv.FormatBool(b), p.Type, true
	case sourceengine.PreferenceList, sourceengine.PreferenceEditText:
		s, isString := p.CurrentValue.(string)
		if !isString {
			return "", "", false
		}
		return s, p.Type, true
	case sourceengine.PreferenceMultiSelect:
		list, ok := stringSlice(p.CurrentValue)
		if !ok {
			return "", "", false
		}
		encoded, err := json.Marshal(list)
		if err != nil {
			// Structurally unreachable: a []string always marshals.
			return "", "", false
		}
		return string(encoded), p.Type, true
	default:
		return "", "", false
	}
}

// stringSlice coerces an untyped preference CurrentValue into a []string,
// accepting both a concrete []string (the shape a test fixture or a future
// typed client might hand in) and the []any a JSON array decodes into over
// the wire (each element must itself be a string, else the whole value is
// rejected rather than partially coerced).
func stringSlice(v any) ([]string, bool) {
	switch vv := v.(type) {
	case []string:
		return vv, true
	case []any:
		out := make([]string, len(vv))
		for i, e := range vv {
			s, ok := e.(string)
			if !ok {
				return nil, false
			}
			out[i] = s
		}
		return out, true
	default:
		return nil, false
	}
}

// upsertSourcePreference writes one (source_id, key) → (value, value_type)
// row: creates it the first time, overwrites value+value_type thereafter.
// SourcePreference's uniqueness is a 2-column composite index, so — like
// settings.Service.upsertSettingTx over the single-column Settings table —
// this is a plain query-then-write, not a generated ON CONFLICT upsert.
func upsertSourcePreference(ctx context.Context, db *ent.Client, sourceID int64, key, value, valueType string) error {
	existing, err := db.SourcePreference.Query().
		Where(entsourcepreference.SourceID(sourceID), entsourcepreference.Key(key)).
		Only(ctx)
	if ent.IsNotFound(err) {
		return db.SourcePreference.Create().
			SetSourceID(sourceID).
			SetKey(key).
			SetValue(value).
			SetValueType(valueType).
			Exec(ctx)
	}
	if err != nil {
		return fmt.Errorf("query existing source preference: %w", err)
	}
	return db.SourcePreference.UpdateOneID(existing.ID).
		SetValue(value).
		SetValueType(valueType).
		Exec(ctx)
}
