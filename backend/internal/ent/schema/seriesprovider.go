package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// SeriesProvider holds the schema definition for the SeriesProvider entity: one
// (source, scanlator) pair followed for one series.
//
// NO JSON chapters blob may ever be added to this entity — nor anywhere else.
// Per-provider chapter availability lives in ProviderChapter rows, keyed
// UNIQUE(series_provider_id, chapter_key). That constraint is what makes
// deduplication STRUCTURAL: the database refuses a duplicate outright, so no
// application-layer "have we seen this chapter?" logic exists to drift, race, or be
// forgotten by a new code path. A blob would move that guarantee back into
// application code and silently lose it.
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
		// web_url is the fully-qualified, browser-clickable URL for this manga
		// (Mihon's HttpSource.getMangaUrl, surfaced end-to-end as
		// sourceengine.MangaEntry/MangaDetails.RealURL). Distinct from url (the
		// source-relative ADDRESSING key every request sends back) — web_url is
		// only ever meant to be opened in a browser (Komga ComicInfo <Web>,
		// "View on source" links for an adopted series). "" when the engine host
		// could not resolve one. Additive + defaulted ⇒ zero-data migration.
		field.String("web_url").Default(""),
		field.String("title").Default(""),
		field.Bool("metadata").Default(false),
		field.String("status").Default(""),
		field.Uint32("flags").Default(0),
		// importance ranks this source against the series' other sources.
		// HIGHER NUMBER = HIGHER PRIORITY. This is the INVERSE of a typical priority
		// queue and of the legacy Kaizoku.GO behaviour, so it is the direction a
		// newcomer is most likely to assume backwards. Never reverse it: the whole
		// candidate/upgrade engine reads it this way — disk.RenderChapter,
		// chapter.BestProviderChapter, download.buildFetchRef, series.ChapterTitles
		// and imports.Adopt all depend on this ordering.
		field.Int("importance").Default(0),
		// ignore_fractional marks this source, FOR THIS SERIES, as a fractional
		// re-uploader: a mirror that republishes whole chapter N as a lone "N.1"
		// under its own URL (Comic Asura does exactly this — 179 pages vs Asura's
		// 26). When set, the source contributes NO fractional-numbered chapters to
		// this series: they are dropped at ingest and excluded from candidacy.
		//
		// It is per (series, provider) and NOT a heuristic, deliberately. The engine
		// CANNOT tell a re-upload from a genuine side-chapter: a `.5` omake source
		// obviously also hosts the whole chapter, and `.5` is the MOST COMMON
		// fractional in a real library (825 chapters across 44 series). Any automatic
		// rule would have deleted all of them. So the owner ticks it per source, after
		// SEEING that source's fractional list (ProviderDTO.fractionalChapters).
		//
		// DELIBERATE FAIL-OPEN: a chapter with NO parsed number cannot be judged
		// fractional, so it is left alone — a source that publishes a re-upload under a
		// NULL-numbered chapter therefore evades the toggle. Orphaning every unnumbered
		// chapter would be the far worse failure, so both the engine
		// (chapter.dropIgnoredFractionalSources) and the downloads read model
		// (downloads.newUpgradeTargetIndex) keep such rows.
		//
		// Flipping it DELETES NOTHING (never-auto-delete): existing ProviderChapter
		// rows and downloaded CBZs stay; the source simply stops offering fractionals.
		// Additive + defaulted ⇒ zero-data migration.
		field.Bool("ignore_fractional").Default(false),
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
