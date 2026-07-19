package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// IgnoreScanlatorSource holds the schema definition for the IgnoreScanlatorSource
// entity: one row per engine-host source the owner has flagged "ignore
// scanlator" via the per-source toggle in the "Configure" dialog. Presence of a
// row = the flag is ON; absence = OFF (the default, and today's split-by-
// scanlator behaviour). Un-flagging deletes the row.
//
// WHY IT EXISTS: some sources (the Iken multisrc template family, e.g. Hive
// Scans) put the chapter UPLOADER into the scanlator field rather than a
// translation group. Tsundoku's scanlator-aware provider split then fragments
// such a source into fake per-uploader providers ([Hive Scans-Admin],
// [Hive Scans-Aero], …) instead of one [Hive Scans]. When the flag is ON,
// Tsundoku forces that source's chapter scanlator to "" at the ingest/adopt
// choke points, collapsing it back to a single [Source] provider (see
// internal/ignorescanlator + internal/ingest).
//
// This is a TSUNDOKU-ONLY interpretation flag, structurally identical to
// DisabledSource: the internal engine (Rensaio, via internal/sourceengine) has
// no server-side concept of it, so Tsundoku persists it itself and applies it at
// its own ingest layer. It is deliberately NOT engine topology —
// internal/enginetopo (seed + reconcile) never reads or writes this entity, so
// it is never pushed to the engine.
//
// Every field is additive/defaulted, so adding this entity is a zero-data
// migration: Ent auto-migrate creates the empty table and rows are created
// lazily the first time a source is flagged.
type IgnoreScanlatorSource struct {
	ent.Schema
}

// Fields of the IgnoreScanlatorSource.
func (IgnoreScanlatorSource) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		// source_id is the engine-host stable numeric source id and the stable
		// identity of the row. Unique — a source is flagged at most once.
		field.Int64("source_id").Unique(),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}

// Edges of the IgnoreScanlatorSource. None — an ignore-scanlator flag is a
// standalone Tsundoku-side fact keyed by the numeric source id, with no link to
// any other row (and deliberately not to SeriesProvider — flagging a source is
// apply-forward only and never touches an already-adopted series in Slice A).
func (IgnoreScanlatorSource) Edges() []ent.Edge {
	return nil
}
