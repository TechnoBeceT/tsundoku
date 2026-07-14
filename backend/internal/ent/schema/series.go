// Package schema holds the Ent schema definitions for all Tsundoku entities.
package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/metadata"
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
		// genres + tags are the Phase-1 metadata engine's classification tags —
		// UNION-merged across every provider a series was identified against
		// (internal/metadata.Merge; QCAT-228 never single-provider-wins on a
		// collection). GIN-indexed jsonb (see Indexes below) so a future
		// "filter by genre" query can use Postgres' jsonb containment operators
		// directly, with no join table. Optional/nillable ⇒ zero-data migration:
		// an existing row gets a NULL column, which decodes to a nil (empty)
		// slice — indistinguishable in Go from "no genres yet".
		field.JSON("genres", []string{}).Optional(),
		field.JSON("tags", []string{}).Optional(),
		// alt_titles / authors / links reuse the metadata engine's normalized
		// structs verbatim (internal/metadata.AltTitle / Author / Link) — the
		// schema never re-declares its own mirror of the pure engine's shapes,
		// so a mapper change and its DB column stay ONE definition (§2 DRY).
		field.JSON("alt_titles", []metadata.AltTitle{}).Optional(),
		field.JSON("authors", []metadata.Author{}).Optional(),
		field.JSON("links", []metadata.Link{}).Optional(),
		// year is the first-publication year the metadata engine resolved; 0 =
		// unknown, mirroring metadata.SeriesMetadata.Year's zero-is-unset
		// convention. Defaulted ⇒ zero-data migration.
		field.Int("year").Default(0),
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
		// metadata_source records WHICH metadata provider (AniList, MangaDex, …)
		// the rich fields above (genres/tags/alt_titles/authors/description/
		// status/year/links) were anchored on — the "anchor-then-aggregate"
		// primary from the Phase-1 metadata engine (spec/metadata-engine-phase1
		// §5). It SUPERSEDES metadata_provider_id's ROLE for rich-card display,
		// but that column is DELIBERATELY KEPT (not dropped) here: its M10
		// display-name/cover resolution stays untouched until a later,
		// owner-approved migration (spec §3). nil = not yet identified.
		field.JSON("metadata_source", &metadata.SourceRef{}).Optional(),
		// metadata_locked marks the series' rich metadata as OWNER-HAND-CURATED
		// (multi-select merge identify, spec/metadata-engine-phase1 §5 extension) —
		// it is set true ONLY by metadatasvc.Service.IdentifyMerge (the owner's
		// explicit multi-provider pick). AutoIdentify checks this FIRST and no-ops
		// on a locked series, so a manually-curated merge is never silently
		// clobbered by the next background pass. Default false ⇒ zero-data
		// migration; an existing series stays eligible for auto-identify exactly
		// as before.
		field.Bool("metadata_locked").Default(false),
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
		// cover_version is a short hash of the cover BYTES currently cached — the
		// content version the served URL carries (…/cover?v=<cover_version>).
		//
		// It must be derived from the BYTES, never from cover_source_url: that URL
		// is Suwayomi's id-derived thumbnail path (/api/v1/manga/{id}/thumbnail),
		// so it is stable even when the source republishes different art. The cover
		// endpoint answers `immutable` — a one-way door — and the ONLY lever that
		// can ever show the owner a changed image is a changed URL. A version that
		// tracks the URL instead of the bytes would pin a stale cover for a year
		// with no server-side remedy.
		//
		// Empty ⇒ nothing is cached for this series (or the index predates the
		// column): the DTO then emits an unversioned URL and the endpoint serves
		// revalidatable no-cache, never immutable.
		field.String("cover_version").Default(""),
		// cover_source records which provider the CURRENTLY CACHED cover's bytes
		// came from — a metadata provider or a library SeriesProvider
		// (SourceRef.Kind distinguishes them). Independent of metadata_source
		// (QCAT-228: the cover is chosen separately from the rich-metadata
		// merge). nil = the cache predates this feature (M10 cover proxy) or the
		// series has no cached cover yet.
		field.JSON("cover_source", &metadata.SourceRef{}).Optional(),
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
		// track_bindings is the inverse of TrackBinding.series — a series may be
		// bound to several native trackers (at most one per tracker). The DB-level
		// ON DELETE CASCADE is belt-and-braces alongside the M9 DeleteSeries
		// manual-tx cascade: if a series row is ever deleted outside that path, its
		// bindings never linger as orphans.
		edge.To("track_bindings", TrackBinding.Type).
			Annotations(entsql.Annotation{OnDelete: entsql.Cascade}),
	}
}

// Indexes of the Series.
func (Series) Indexes() []ent.Index {
	return []ent.Index{
		// GIN indexes on the jsonb genres/tags columns so a future "filter by
		// genre/tag" query can use Postgres' jsonb containment operators
		// (@>, ?, ?&, ?|) directly, without a join table.
		index.Fields("genres").Annotations(entsql.IndexType("GIN")),
		index.Fields("tags").Annotations(entsql.IndexType("GIN")),
	}
}
