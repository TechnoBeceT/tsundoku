package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// DisabledSource holds the schema definition for the DisabledSource entity: one
// row per engine-host source the owner has DISABLED via the per-language
// enable/disable toggle (the "Configure" dialog's per-source Switch). Presence
// of a row = disabled; absence = enabled. Re-enabling deletes the row.
//
// This is a TSUNDOKU-ONLY UI/picker flag. The internal engine (Rensaio, via
// internal/sourceengine) has no server-side "disabled source" concept to store
// or reconcile, so Tsundoku persists the flag itself and applies the filter in
// internal/imports (Discover/Search/Browse pickers). It is deliberately NOT
// engine topology — internal/enginetopo (seed + reconcile) never reads or
// writes this entity, so it is never pushed to the engine.
//
// Every field is additive/defaulted, so adding this entity is a zero-data
// migration: Ent auto-migrate creates the empty table and rows are created
// lazily the first time a source is disabled.
type DisabledSource struct {
	ent.Schema
}

// Fields of the DisabledSource.
func (DisabledSource) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		// source_id is the engine-host stable numeric source id and the stable
		// identity of the row. Unique — a source is disabled at most once.
		field.Int64("source_id").Unique(),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}

// Edges of the DisabledSource. None — a disabled-source flag is a standalone
// Tsundoku-side fact keyed by the numeric source id, with no link to any other
// row (and deliberately not to SeriesProvider — disabling a source never
// touches an already-adopted series).
func (DisabledSource) Edges() []ent.Edge {
	return nil
}
