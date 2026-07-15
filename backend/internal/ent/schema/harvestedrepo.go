package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// HarvestedRepo holds the schema definition for the HarvestedRepo entity: one
// extension-repository URL that Tsundoku has harvested into its own durable
// engine-topology store (the set of repos it knows about, independent of the
// Suwayomi engine's transient in-memory list).
//
// `url` is the stable identity — a re-harvest upserts by it — so the table is a
// de-duplicated set of repo URLs.
//
// Every field is additive/optional/defaulted, so adding this entity is a
// zero-data migration: Ent auto-migrate creates the empty table and rows are
// created lazily as repos are harvested.
type HarvestedRepo struct {
	ent.Schema
}

// Fields of the HarvestedRepo.
func (HarvestedRepo) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		// url is the extension-repository URL and the stable identity of the row.
		// Unique — the store holds each repo exactly once.
		field.String("url").Unique(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Edges of the HarvestedRepo. None — a harvested repo is a standalone fact keyed
// by its URL, with no link to any other Tsundoku row.
func (HarvestedRepo) Edges() []ent.Edge {
	return nil
}
