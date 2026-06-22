package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// Chapter holds the schema definition for the Chapter entity.
type Chapter struct {
	ent.Schema
}

// Fields of the Chapter.
func (Chapter) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		field.UUID("series_id", uuid.UUID{}),
		field.String("chapter_key"),
		field.Float("number").Optional().Nillable(),
		field.Enum("state").
			Values("wanted", "downloading", "downloaded", "upgrade_available", "upgrading", "failed", "permanently_failed").
			Default("wanted"),
		field.UUID("satisfied_by_provider_id", uuid.UUID{}).Optional().Nillable(),
		field.Int("satisfied_importance").Optional().Nillable(),
		field.Int("page_count").Optional().Nillable(),
		field.String("filename").Default(""),
		field.Time("download_date").Optional().Nillable(),
		field.Int("retries").Default(0),
		field.Time("next_attempt_at").Optional().Nillable(),
		field.String("last_error").Default(""),
		field.String("error_category").Default(""),
	}
}

// Edges of the Chapter.
func (Chapter) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("series", Series.Type).
			Ref("chapters").
			Field("series_id").
			Required().
			Unique(),
		edge.To("satisfied_by", SeriesProvider.Type).
			Field("satisfied_by_provider_id").
			Unique(),
	}
}

// Indexes of the Chapter.
func (Chapter) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("series_id", "chapter_key").Unique(),
	}
}
