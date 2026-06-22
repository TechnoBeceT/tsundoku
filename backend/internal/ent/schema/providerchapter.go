package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// ProviderChapter holds the schema definition for the ProviderChapter entity.
type ProviderChapter struct {
	ent.Schema
}

// Fields of the ProviderChapter.
func (ProviderChapter) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		field.UUID("series_provider_id", uuid.UUID{}),
		field.String("chapter_key"),
		field.Float("number").Optional().Nillable(),
		field.String("name").Default(""),
		field.String("url").Default(""),
		field.Time("provider_upload_date").Optional().Nillable(),
		field.Int("provider_index").Default(0),
		field.Int("page_count").Optional().Nillable(),
	}
}

// Edges of the ProviderChapter.
func (ProviderChapter) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("series_provider", SeriesProvider.Type).
			Ref("provider_chapters").
			Field("series_provider_id").
			Required().
			Unique(),
	}
}

// Indexes of the ProviderChapter.
func (ProviderChapter) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("series_provider_id", "chapter_key").Unique(),
	}
}
