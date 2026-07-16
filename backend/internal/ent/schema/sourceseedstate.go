package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// SourceSeedState holds the schema definition for the SourceSeedState entity:
// one row per numeric engine source recording the OUTCOME of the last
// SeedSourcePreferences pass's PREFERENCE-READ for that source.
//
// It exists so the read-only topology status can POSITIVELY distinguish two
// states the SourcePreference row counts alone cannot tell apart:
//   - a source that was reached successfully but has zero non-default
//     preferences (benign — nothing to capture, no gap), vs
//   - a source whose client.Preferences READ errored (a real gap — a setting
//     we failed to harvest).
//
// Without this row the status could only INFER "sources without captured
// preferences" from a missing-count, lumping the benign-empty case in with the
// genuinely-failed one; here the failure is recorded explicitly.
//
// Every field is additive/optional/defaulted, so adding this entity is a
// zero-data migration: Ent auto-migrate creates the empty table and rows are
// created lazily as SeedSourcePreferences records each source's read outcome.
type SourceSeedState struct {
	ent.Schema
}

// Fields of the SourceSeedState.
func (SourceSeedState) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		// source_id is the numeric engine source id this seed-state row belongs to
		// (64-bit on the wire) — the unique index below keeps at most one row per
		// source.
		field.Int64("source_id"),
		// source_name is the denormalized SeriesProvider.provider_name, stored so a
		// human-readable gap note can name the source without a second lookup.
		field.String("source_name").Default(""),
		// prefs_read_ok records whether the last SeedSourcePreferences pass's
		// client.Preferences call for this source returned WITHOUT error.
		//
		// It reflects the READ call outcome ONLY — NOT whether any preferences
		// existed (a reached source with zero non-default prefs is still ok=true),
		// and NOT whether the individual per-preference row writes succeeded (those
		// are logged-and-skipped best-effort, independent of this flag).
		field.Bool("prefs_read_ok").Default(false),
		// last_error is the read error message from the most recent FAILED read,
		// cleared to "" once a later pass reads that source successfully. It is an
		// error string, not a secret, so it is deliberately NOT .Sensitive().
		field.String("last_error").Default(""),
		// prefs_read_at is when the read last SUCCEEDED (set only on a successful
		// read; left unchanged on a failure, so it preserves the last-good time).
		field.Time("prefs_read_at").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Edges of the SourceSeedState. None — a seed-state row is keyed only by its
// engine source id, with no FK to any Tsundoku row.
func (SourceSeedState) Edges() []ent.Edge {
	return nil
}

// Indexes of the SourceSeedState.
func (SourceSeedState) Indexes() []ent.Index {
	return []ent.Index{
		// At most one seed-state row per source: re-running SeedSourcePreferences
		// upserts the same row in place rather than appending a new one.
		index.Fields("source_id").Unique(),
	}
}
