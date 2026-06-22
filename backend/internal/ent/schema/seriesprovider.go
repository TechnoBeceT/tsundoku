package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// SeriesProvider holds the schema definition for the SeriesProvider entity.
type SeriesProvider struct {
	ent.Schema
}

// Fields of the SeriesProvider.
func (SeriesProvider) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		field.UUID("series_id", uuid.UUID{}),
		field.Int("suwayomi_id").Optional(),
		field.String("provider"),
		field.String("scanlator").Default(""),
		field.String("language").Default(""),
		field.String("url").Default(""),
		field.String("title").Default(""),
		field.Bool("metadata").Default(false),
		field.String("status").Default(""),
		field.Uint32("flags").Default(0),
		field.Int("importance").Default(0),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Edges of the SeriesProvider.
func (SeriesProvider) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("series", Series.Type).
			Ref("providers").
			Field("series_id").
			Required().
			Unique(),
		edge.To("provider_chapters", ProviderChapter.Type),
		edge.To("sync_state", SuwayomiSyncState.Type).Unique(),
		// satisfied_chapters is the back-reference for Chapter.satisfied_by.
		// It lets the M1 upgrade engine query "which chapters does this
		// SeriesProvider currently satisfy?" without a reverse table scan.
		edge.From("satisfied_chapters", Chapter.Type).
			Ref("satisfied_by"),
	}
}
