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
		// provider_name is the source's human-readable display name (e.g.
		// "WebToon", "Comix"), captured at ingest from client.Sources(). It is
		// DISTINCT from provider (the numeric Suwayomi source-ID identity key) and
		// from title (the manga's per-source title). "" when the name could not be
		// resolved — the DTO layer then falls back to showing the id. Additive +
		// defaulted, so existing rows migrate with zero data change and backfill
		// their name on the next ingest/refresh sweep.
		field.String("provider_name").Optional().Default(""),
		field.String("scanlator").Default(""),
		field.String("language").Default(""),
		field.String("url").Default(""),
		field.String("title").Default(""),
		field.Bool("metadata").Default(false),
		field.String("status").Default(""),
		field.Uint32("flags").Default(0),
		field.Int("importance").Default(0),
		// cover_url is this source's thumbnail path (Suwayomi server-relative),
		// captured at ingest from the source manga. "" when none. Served via the
		// cover proxy; never loaded directly by the browser.
		field.String("cover_url").Default(""),
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
