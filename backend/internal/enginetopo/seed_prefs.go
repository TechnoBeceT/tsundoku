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
	"github.com/technobecet/tsundoku/internal/suwayomi"
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
	// suwayomi.Client.SourcePreferences call errored; that source's
	// preferences are left untouched (partial success, never aborts the pass).
	SkippedSources int
}

// SeedSourcePreferences reads each library source's configurable preferences
// from the live engine and upserts them into the durable SourcePreference
// table (source_id, key) → (value, value_type), so a source's settings
// survive independently of whichever Suwayomi instance the client currently
// targets.
//
// The source set is every DISTINCT SeriesProvider.provider value already in
// the library. provider is the stable numeric Suwayomi source id string for a
// live-ingested row (see the source-identity-drift doc in the repo map); a
// disk-origin row instead stores a display NAME there and is silently
// skipped — there is no engine source to query preferences from, so it is
// not counted as a failure.
//
// Idempotent: re-running upserts each (source_id, key) row in place (no
// duplicate rows). Per-source failures are logged and skipped so one
// unreachable/erroring source can never abort the whole pass — partial
// success, matching the never-auto-delete/upsert-only conventions the rest
// of the ingest engine follows (see BackfillProviderURLs).
//
// ASYMMETRY vs SeedEngineConfig (deliberate — do NOT convert this to gap-fill):
// SeedEngineConfig gap-fills because config is TSUNDOKU-OWNED (capture once, then
// Tsundoku is authoritative — see its doc comment). Source preferences are the
// opposite: in Phase-1 the ENGINE is their only editor (there is no Tsundoku-side
// pref-editing path), so this pass CAPTURES-LATEST — re-reading the engine's
// current values every boot keeps the durable mirror fresh. Freezing the
// first-seen value (gap-fill) would let the mirror go stale the moment the owner
// edits a preference in the engine. (This becomes reconcile-aware in Phase-2,
// once Tsundoku can edit preferences too.)
//
// SECURITY: a source preference can hold a secret (e.g. a login-gated
// source's plaintext password — Suwayomi has no password masking on the
// wire, confirmed live). Values are stored VERBATIM into the .Sensitive()
// value column, matching the ratified TrackerConnection plaintext-secrets
// model; this function does not attempt to detect or skip password-shaped
// preferences.
func SeedSourcePreferences(ctx context.Context, client suwayomi.Client, db *ent.Client) (PreferenceSeedResult, error) {
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

		prefs, err := client.SourcePreferences(ctx, provider)
		if err != nil {
			slog.WarnContext(ctx, "enginetopo: SourcePreferences failed, skipping source",
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
				// Unset current value (nil pointer) — nothing to seed.
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

// encodePreferenceValue converts a decoded suwayomi.SourcePreference's
// CURRENT value into the (value, value_type) pair the SourcePreference row
// stores. ok is false when the preference has no current value set (every
// *current field is nil/empty per its variant) — there is nothing to seed
// for an unconfigured preference. value_type is the PreferenceType constant
// verbatim, so a later read knows how to reinterpret value.
//
//   - CheckBox / Switch  → "true"/"false".
//   - List / EditText    → the string as-is (this is the path a plaintext
//     password preference takes — see the SECURITY note on SeedSourcePreferences).
//   - MultiSelect        → a JSON array string (e.g. `["a","b"]`).
func encodePreferenceValue(p suwayomi.SourcePreference) (value string, valueType string, ok bool) {
	switch p.Type {
	case suwayomi.PreferenceCheckBox, suwayomi.PreferenceSwitch:
		if p.CurrentBool == nil {
			return "", "", false
		}
		return strconv.FormatBool(*p.CurrentBool), string(p.Type), true
	case suwayomi.PreferenceList, suwayomi.PreferenceEditText:
		if p.CurrentString == nil {
			return "", "", false
		}
		return *p.CurrentString, string(p.Type), true
	case suwayomi.PreferenceMultiSelect:
		if p.CurrentStringList == nil {
			return "", "", false
		}
		encoded, err := json.Marshal(p.CurrentStringList)
		if err != nil {
			// Structurally unreachable: a []string always marshals.
			return "", "", false
		}
		return string(encoded), string(p.Type), true
	default:
		return "", "", false
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
