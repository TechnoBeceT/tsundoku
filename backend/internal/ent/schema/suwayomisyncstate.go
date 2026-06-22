package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// SuwayomiSyncState holds the schema definition for the SuwayomiSyncState entity.
type SuwayomiSyncState struct {
	ent.Schema
}

// Fields of the SuwayomiSyncState.
func (SuwayomiSyncState) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		field.UUID("series_provider_id", uuid.UUID{}),
		field.Time("last_synced_at").Optional().Nillable(),
		field.String("last_error").Default(""),
		field.String("state").Default(""),
	}
}

// Edges of the SuwayomiSyncState.
func (SuwayomiSyncState) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("series_provider", SeriesProvider.Type).
			Ref("sync_state").
			Field("series_provider_id").
			Required().
			Unique(),
	}
}

// Indexes of the SuwayomiSyncState.
func (SuwayomiSyncState) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("series_provider_id"),
	}
}
