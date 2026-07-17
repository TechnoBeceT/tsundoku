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
			Values("wanted", "downloading", "downloaded", "upgrade_available", "upgrading", "failed", "permanently_failed", "superseded", "ignored").
			Default("wanted"),
		field.UUID("satisfied_by_provider_id", uuid.UUID{}).Optional().Nillable(),
		field.Int("satisfied_importance").Optional().Nillable(),
		field.Int("page_count").Optional().Nillable(),
		field.String("filename").Default(""),
		field.Time("download_date").Optional().Nillable(),
		// first_downloaded_at is when this chapter FIRST became readable — written
		// exactly once, on the first successful download, and NEVER rewritten.
		//
		// It exists because download_date CANNOT answer that question: a
		// Library-Convergence upgrade re-fetches an OLD chapter from a better source
		// and rewrites download_date, so MAX(download_date) floats a series to the
		// top of "recently updated" with nothing new to read.
		//
		// Nillable: chapters imported from disk have no known arrival time until
		// reconcile fills it from the CBZ's mtime (a later task).
		field.Time("first_downloaded_at").Optional().Nillable(),
		field.Int("retries").Default(0),
		field.Time("next_attempt_at").Optional().Nillable(),
		field.String("last_error").Default(""),
		field.String("error_category").Default(""),
		// Reading-progress fields (in-app reader). All additive/defaulted so an
		// existing DB migrates with zero data work: every current chapter reads as
		// unread, page 0, never-read-at. read/last_read_page are pure owner UI state
		// (like monitored/completed on Series) — NOT disk/sidecar-represented and
		// never folder- or download-determining.
		field.Bool("read").Default(false),
		field.Int("last_read_page").Default(0),
		// read_at is nil until the chapter is first marked read; it means "when the
		// owner marked this chapter read", so SetProgress clears it when read flips
		// back to false.
		field.Time("read_at").Optional().Nillable(),
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
