package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/fetcher"
)

// ProviderChapter is the per-provider availability feed for a chapter.
// Identity is (series_provider_id, chapter_key) — enforced by a unique index —
// so a provider can never list the same chapter key twice. The M1 normalizer
// derives chapter_key from raw provider data before inserting rows.
type ProviderChapter struct {
	ent.Schema
}

// Fields of the ProviderChapter.
func (ProviderChapter) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		field.UUID("series_provider_id", uuid.UUID{}),
		field.String("chapter_key"),
		// number stores the display/sort value for a chapter.
		// chapter_key (string), not number, is the identity used for dedup;
		// number is for display/sort. The M1 normalizer derives chapter_key.
		// Postgres column type is numeric to avoid float8 precision loss.
		field.Float("number").
			SchemaType(map[string]string{dialect.Postgres: "numeric"}).
			Optional().
			Nillable(),
		field.String("name").Default(""),
		field.String("url").Default(""),
		// web_url is the fully-qualified, browser-clickable URL for this chapter
		// (Mihon's HttpSource.getChapterUrl, surfaced end-to-end as
		// sourceengine.Chapter.RealURL). Distinct from url (the source-relative
		// ADDRESSING key) — web_url feeds Komga's ComicInfo <Web> field (see
		// disk.newComicInfo / RenderMeta.WebURL) and is never used for
		// addressing. "" when the engine host could not resolve one. Additive +
		// defaulted ⇒ zero-data migration.
		field.String("web_url").Default(""),
		field.Time("provider_upload_date").Optional().Nillable(),
		field.Int("provider_index").Default(0),
		field.Int("page_count").Optional().Nillable(),
		// suwayomi_chapter_id is the Suwayomi-internal chapter identifier.
		// 0 (the zero value for Optional) means the ID is not yet known.
		// Populated by the M2 ingest service when a chapter is sourced from
		// Suwayomi; used by the download dispatcher to fetch page bytes.
		field.Int("suwayomi_chapter_id").Optional(),
		// attempts counts how many times the download dispatcher has tried to
		// fetch this chapter FROM THIS SOURCE and failed. It is the per-source
		// retry counter: once attempts reaches jobs.max_retries this source is
		// "exhausted" for this chapter and is dropped from the live-candidate set.
		// A chapter only becomes permanently_failed when EVERY source that offers
		// it is exhausted. Default 0 → every existing row is immediately a live
		// candidate (zero-data migration).
		field.Int("attempts").Default(0),
		// last_error records the most recent failure reason for THIS source's
		// attempt at THIS chapter (empty when it has never failed or after a reset).
		field.String("last_error").Default(""),
		// next_attempt_at is the per-source backoff gate: this source is not a live
		// candidate for this chapter again until now >= next_attempt_at. Nil means
		// "no cooldown pending" (never failed, or its cooldown has been cleared by a
		// success or an owner retry-reset).
		field.Time("next_attempt_at").Optional().Nillable(),
		// page_links caches the ordered RESOLVED page list for this chapter from
		// THIS source — each entry the (URL, ImageURL) pair the image-download call
		// needs. The download engine resolves it ONCE on the first attempt (the
		// source's Cloudflare-protected page-resolution step) and persists it here,
		// so every retry SKIPS re-resolution and drives the image loop from these
		// links directly. Cleared when a not_found image failure signals the links
		// have gone stale (re-resolved next attempt). Additive/optional ⇒ zero-data
		// migration (existing rows: nil = not yet resolved, resolved on first fetch).
		field.JSON("page_links", []fetcher.PageLink{}).Optional(),
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
