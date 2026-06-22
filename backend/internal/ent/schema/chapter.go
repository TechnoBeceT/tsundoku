package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// Chapter is the single source of truth for one logical chapter of a series.
// Identity is (series_id, chapter_key) — enforced by a unique index — so a
// chapter can never be duplicated across providers. The M1 normalizer derives
// chapter_key from provider-supplied data and uses it for all dedup logic.
type Chapter struct {
	ent.Schema
}

// Fields of the Chapter.
func (Chapter) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		field.UUID("series_id", uuid.UUID{}),
		field.String("chapter_key"),
		// number stores the display/sort value for a chapter.
		// chapter_key (string), not number, is the identity used for dedup;
		// number is for display/sort. The M1 normalizer derives chapter_key.
		// Postgres column type is numeric to avoid float8 precision loss.
		field.Float("number").
			SchemaType(map[string]string{dialect.Postgres: "numeric"}).
			Optional().
			Nillable(),
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
