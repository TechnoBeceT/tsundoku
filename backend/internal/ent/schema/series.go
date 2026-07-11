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
		// category_id is the FK to the series' Category (the inverse side of the
		// category edge below). It drives the on-disk library layout (one
		// top-level folder per category, e.g. Manhwa/<Title>/) and Komga
		// organisation.
		//
		// MIGRATION SAFETY — this column is intentionally OPTIONAL (nullable) at
		// the DB level even though every series MUST have a category by app
		// invariant. A required NOT NULL FK cannot be added to an already-populated
		// series table without a static default (categories have random UUIDs), so
		// it would break Ent auto-migration on an existing DB. Instead the column
		// is added nullable, then the startup seed+backfill (category.EnsureDefaults
		// + category.BackfillSeries) links every legacy row to its same-named
		// Category, and every create path (ingest, adopt, reconcile) always sets a
		// category. The invariant is therefore enforced in code, not by a NOT NULL
		// constraint that would make the migration unsafe.
		field.UUID("category_id", uuid.UUID{}).Optional(),
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
		// cover_file + cover_source_url are the FAST INDEX over the on-disk cover
		// cache: the filename inside the series folder ("cover.webp") and the
		// provider cover_url those bytes were fetched from (the cache key).
		//
		// They exist purely for SPEED. The tsundoku.json sidecar remains the
		// durable rebuild seed (a DB loss is repaired by disk.Reconcile, which
		// restores both columns from it) — but reading the sidecar to serve ONE
		// cover means parsing every chapter's provenance, over NFS, on every
		// request. With these columns the warm path is one DB row + one os.ReadFile.
		//
		// Both are additive + defaulted ⇒ zero-data migration. An EXISTING library
		// (covers already on disk, columns empty) must NOT be treated as uncached:
		// series.CoverBytes falls back to the sidecar and BACKFILLS these columns,
		// so no source is ever re-hit for a cover that is already local.
		field.String("cover_file").Default(""),
		field.String("cover_source_url").Default(""),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Edges of the Series.
func (Series) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("providers", SeriesProvider.Type),
		edge.To("chapters", Chapter.Type),
		// category is the (app-)required link to the series' Category. Modelled as
		// the inverse of Category.series, storing the FK in category_id. Unique
		// (a series has exactly one category) but NOT .Required() at the schema
		// level — see the category_id field comment for the migration-safety
		// reasoning that keeps it nullable in the DB.
		edge.From("category", Category.Type).
			Ref("series").
			Field("category_id").
			Unique(),
	}
}
