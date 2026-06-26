// Package schema holds the Ent schema definitions for all Tsundoku entities.
package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// Series holds the schema definition for the Series entity.
type Series struct {
	ent.Schema
}

// Fields of the Series.
func (Series) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		field.String("title"),
		field.String("slug").Unique(),
		field.String("cover_url").Default(""),
		field.String("description").Default(""),
		field.String("status").Default(""),
		// category drives the on-disk library layout (one top-level folder per
		// category, e.g. Manhwa/<Title>/) and Komga organisation. "Other" is the
		// safe default so existing rows and new imports need no data migration.
		field.Enum("category").
			Values("Manga", "Manhwa", "Manhua", "Comic", "Other").
			Default("Other"),
		// monitored gates the (M5) refresh poll; false = the owner is done with
		// this series and it is excluded from new-chapter checks.
		field.Bool("monitored").Default(true),
		// completed marks a finished series (story over, no more chapters
		// expected). Distinct from monitored: completed is a permanent fact
		// about the series; monitored=false is a temporary owner pause. The
		// two are orthogonal. A completed series is skipped by the refresh
		// sweep and excluded from source-health. Default false → existing rows
		// backfill via the column default (no data migration).
		field.Bool("completed").Default(false),
		// metadata_provider_id selects which SeriesProvider supplies the series'
		// DISPLAY name + cover. Nil = auto (highest-importance provider). NEVER
		// changes the canonical Series.title (slug/folder/Komga); display name +
		// cover are resolved on read.
		field.UUID("metadata_provider_id", uuid.UUID{}).Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Edges of the Series.
func (Series) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("providers", SeriesProvider.Type),
		edge.To("chapters", Chapter.Type),
	}
}
