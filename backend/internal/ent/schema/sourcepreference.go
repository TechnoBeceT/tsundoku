package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// SourcePreference holds the schema definition for the SourcePreference entity:
// one per-source configuration setting (a Tachiyomi/Mihon source preference)
// that Tsundoku owns in its durable engine-topology store, so a source's
// settings survive independently of the Suwayomi engine.
//
// A preference is identified by (source_id, key) — the unique index enforces at
// most one value per key per source — so a source may carry many preferences but
// never two rows for the same key.
//
// Every field is additive/optional/defaulted, so adding this entity is a
// zero-data migration: Ent auto-migrate creates the empty table and rows are
// created lazily as source preferences are set.
type SourcePreference struct {
	ent.Schema
}

// Fields of the SourcePreference.
func (SourcePreference) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		// source_id is the Suwayomi source id this preference belongs to (64-bit on
		// the wire).
		field.Int64("source_id"),
		// key is the preference key within the source's settings namespace.
		field.String("key"),
		// value is the preference value, stored as a string (value_type records how
		// to interpret it).
		//
		// SECURITY: a source preference can hold a secret (e.g. an account password
		// or API token for a login-gated source), so it is stored PLAINTEXT at rest
		// under the single-owner homelab threat model — NO encryption (matches the
		// TrackerConnection token precedent; at-rest encryption is a future
		// hardening). Marked .Sensitive() so the value never leaks through the
		// generated String()/log path or JSON serialization; the at-rest storage and
		// Go read access are unaffected.
		field.String("value").Sensitive().Default(""),
		// value_type records how to interpret value (e.g. "string", "bool", "int",
		// "list") so a typed preference round-trips through the string column.
		field.String("value_type").Default(""),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Edges of the SourcePreference. None — a preference is keyed only by its source
// id, with no FK to any Tsundoku row.
func (SourcePreference) Edges() []ent.Edge {
	return nil
}

// Indexes of the SourcePreference.
func (SourcePreference) Indexes() []ent.Index {
	return []ent.Index{
		// At most one value per key per source: a source can carry many preferences,
		// but never two rows for the same key.
		index.Fields("source_id", "key").Unique(),
	}
}
