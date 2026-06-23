// Package suwayomi — ingest service.
//
// This file (ingest.go) implements the Ingest service, which is the bridge
// between the Suwayomi client (Task 4) and the M1 chapter-ingest engine
// (chapter.IngestProviderChapters). It is the ONLY place that maps Suwayomi
// manga/chapter data into the Tsundoku data model; no other package duplicates
// this mapping.
//
// Design decisions:
//
//   - suwayomi_chapter_id is written via a post-ingest update loop rather than
//     by extending chapter.FetchedChapter. This keeps the M1 chapter package
//     entirely untouched and avoids coupling the M1 data type to a
//     Suwayomi-specific field.
//
//   - Series slug uses disk.Slugify (the same slugifier used by the M1 disk
//     reconciler), guaranteeing that a series created by ingest and one
//     reconstructed by disk.Reconcile after a DB wipe agree on identity.
//
//   - SeriesProvider is looked up by (series_id, provider) — the same key the
//     disk reconciler uses — so a provider previously created by reconcile is
//     reused rather than duplicated.
package suwayomi

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
)

// Ingest bridges the Suwayomi client and the M1 chapter-ingest engine.
// Create one with NewIngest and call AddSeries to populate the DB so the
// M1 download dispatcher has chapters to process.
type Ingest struct {
	client Client
	db     *ent.Client
}

// NewIngest constructs an Ingest backed by the given Suwayomi client and ent
// database client.
func NewIngest(client Client, db *ent.Client) *Ingest {
	return &Ingest{client: client, db: db}
}

// Search delegates to the underlying Suwayomi client, returning all manga
// matching query in the given source.
func (i *Ingest) Search(ctx context.Context, sourceID, query string) ([]Manga, error) {
	return i.client.Search(ctx, sourceID, query)
}

// AddSeries fetches all chapters for mangaID from Suwayomi, upserts the
// corresponding Series and SeriesProvider rows, delegates to the M1
// chapter.IngestProviderChapters for dedup/identity, then writes each
// ProviderChapter's suwayomi_chapter_id in a post-ingest update.
//
// Parameters:
//   - sourceName is stored as SeriesProvider.provider (e.g. "mangadex").
//   - mangaID is the Suwayomi-internal manga identifier.
//   - title is the manga's display title (used to derive the Series slug and
//     set Series.title). The caller is responsible for providing the correct
//     title; AddSeries does not call client.Search to look it up.
//
// The operation is idempotent: calling AddSeries again for the same manga
// produces no duplicate rows. The M1 dedup invariant guarantees that
// re-ingesting the same chapter list creates no new Chapter rows (result counts
// will be zero on a second call).
func (i *Ingest) AddSeries(
	ctx context.Context,
	sourceName string,
	mangaID int,
	title string,
) (chapter.IngestResult, error) {
	// 1. Fetch all chapters from Suwayomi via the fetchChapters mutation. This
	//    contacts the upstream source and populates Suwayomi's internal cache
	//    before we touch our own DB, so that a client failure does not leave
	//    partially-created rows. FetchChapters must be called (not MangaChapters)
	//    because after Search the manga exists in Suwayomi but chapters are not
	//    cached yet (they require an explicit source fetch first).
	swChapters, err := i.client.FetchChapters(ctx, mangaID)
	if err != nil {
		return chapter.IngestResult{}, fmt.Errorf("suwayomi.Ingest.AddSeries: fetch chapters for manga %d: %w", mangaID, err)
	}

	// 2. Upsert the Series row, keyed by slug = disk.Slugify(title).
	series, err := i.upsertSeries(ctx, title)
	if err != nil {
		return chapter.IngestResult{}, fmt.Errorf("suwayomi.Ingest.AddSeries: upsert series %q: %w", title, err)
	}

	// 3. Upsert the SeriesProvider row, keyed by (series_id, provider).
	//    title is passed so that SeriesProvider.Title is populated for downstream
	//    CBZ rendering (ComicInfo.Series → Komga series grouping).
	sp, err := i.upsertSeriesProvider(ctx, series.ID, sourceName, mangaID, title)
	if err != nil {
		return chapter.IngestResult{}, fmt.Errorf("suwayomi.Ingest.AddSeries: upsert series provider %q for series %s: %w", sourceName, series.ID, err)
	}

	// 4. Map Suwayomi chapters to the M1 FetchedChapter type.
	fetched := mapToFetchedChapters(swChapters)

	// 5. Delegate to the M1 ingest engine (dedup/identity — never duplicated).
	result, err := chapter.IngestProviderChapters(ctx, i.db, sp.ID, fetched)
	if err != nil {
		return chapter.IngestResult{}, fmt.Errorf("suwayomi.Ingest.AddSeries: ingest chapters for series provider %s: %w", sp.ID, err)
	}

	// 6. Post-ingest: write suwayomi_chapter_id on each ProviderChapter row.
	//    Keyed by (series_provider_id, chapter_key) — the same unique index the
	//    M1 ingest used to create/update the rows.
	if err := i.backfillSuwayomiChapterIDs(ctx, sp.ID, swChapters); err != nil {
		return chapter.IngestResult{}, fmt.Errorf("suwayomi.Ingest.AddSeries: backfill suwayomi_chapter_id for series provider %s: %w", sp.ID, err)
	}

	return result, nil
}

// upsertSeries finds the Series row by slug or creates it, then updates the
// title in case it has changed. Returns the existing or newly created row.
func (i *Ingest) upsertSeries(ctx context.Context, title string) (*ent.Series, error) {
	slug := disk.Slugify(title)

	existing, err := i.db.Series.Query().
		Where(entseries.Slug(slug)).
		Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		// Defensive path: reachable only on DB connection loss or cancelled context.
		return nil, fmt.Errorf("query by slug %q: %w", slug, err)
	}
	if ent.IsNotFound(err) {
		created, createErr := i.db.Series.Create().
			SetTitle(title).
			SetSlug(slug).
			Save(ctx)
		if createErr != nil {
			// Defensive path: reachable on DB connection loss or a concurrent
			// INSERT that wins the slug unique constraint race.
			return nil, fmt.Errorf("create series %q: %w", slug, createErr)
		}
		return created, nil
	}

	// Update title in case it has changed (slug stays fixed).
	updated, updateErr := i.db.Series.UpdateOne(existing).
		SetTitle(title).
		Save(ctx)
	if updateErr != nil {
		// Defensive path: reachable only on DB connection loss mid-operation.
		return nil, fmt.Errorf("update series %q: %w", slug, updateErr)
	}
	return updated, nil
}

// upsertSeriesProvider finds the SeriesProvider row by (series_id, provider)
// or creates it. On find it updates suwayomi_id and title in case they changed.
// title is the manga display title from Suwayomi; it is stored so that downstream
// CBZ rendering can populate ComicInfo.Series for Komga series grouping.
// Returns the existing or newly created row.
func (i *Ingest) upsertSeriesProvider(
	ctx context.Context,
	seriesID uuid.UUID,
	provider string,
	suwayomiMangaID int,
	title string,
) (*ent.SeriesProvider, error) {
	existing, err := i.db.SeriesProvider.Query().
		Where(
			entseriesprovider.SeriesID(seriesID),
			entseriesprovider.Provider(provider),
		).
		First(ctx)
	if err != nil && !ent.IsNotFound(err) {
		// Defensive path: reachable only on DB connection loss or cancelled context.
		return nil, fmt.Errorf("query (series=%s provider=%q): %w", seriesID, provider, err)
	}
	if existing != nil {
		// Keep suwayomi_id and title fresh in case the manga was re-added from a
		// different Suwayomi instance or the title has been corrected upstream.
		updated, updateErr := i.db.SeriesProvider.UpdateOne(existing).
			SetSuwayomiID(suwayomiMangaID).
			SetTitle(title).
			Save(ctx)
		if updateErr != nil {
			// Defensive path: reachable only on DB connection loss mid-operation.
			return nil, fmt.Errorf("update (series=%s provider=%q): %w", seriesID, provider, updateErr)
		}
		return updated, nil
	}

	created, createErr := i.db.SeriesProvider.Create().
		SetSeriesID(seriesID).
		SetProvider(provider).
		SetSuwayomiID(suwayomiMangaID).
		SetTitle(title).
		// importance=0 is the schema default; multi-source ranking is M3/M4.
		Save(ctx)
	if createErr != nil {
		// Defensive path: reachable on DB connection loss or a concurrent INSERT
		// that races with the query above.
		return nil, fmt.Errorf("create (series=%s provider=%q): %w", seriesID, provider, createErr)
	}
	return created, nil
}

// mapToFetchedChapters converts a slice of Suwayomi Chapter DTOs to the M1
// FetchedChapter type. The mapping is lossless for the fields that the M1
// ingest engine uses; suwayomi_chapter_id is NOT included here — it is written
// in a separate post-ingest update (backfillSuwayomiChapterIDs).
func mapToFetchedChapters(chs []Chapter) []chapter.FetchedChapter {
	out := make([]chapter.FetchedChapter, len(chs))
	for idx, ch := range chs {
		// Suwayomi returns PageCount=0 when pages have not been fetched yet; pass
		// nil rather than a misleading zero so the M1 ingest stores no page count.
		var pc *int
		if ch.PageCount > 0 {
			pc = &chs[idx].PageCount
		}
		out[idx] = chapter.FetchedChapter{
			Number:        ch.Number,
			Name:          ch.Name,
			URL:           ch.URL,
			ProviderIndex: ch.Index,
			PageCount:     pc,
			UploadDate:    ch.UploadDate,
		}
	}
	return out
}

// backfillSuwayomiChapterIDs writes suwayomi_chapter_id on each ProviderChapter
// row that was created or updated by IngestProviderChapters. The key used is
// (series_provider_id, chapter_key) derived via chapter.NormalizeChapterKey —
// the same normalizer the M1 ingest used, so the keys are guaranteed to match.
//
// Rows that already have the correct suwayomi_chapter_id are written again;
// this is idempotent and avoids a read-before-write on each row.
func (i *Ingest) backfillSuwayomiChapterIDs(
	ctx context.Context,
	seriesProviderID uuid.UUID,
	chs []Chapter,
) error {
	for _, ch := range chs {
		key := chapter.NormalizeChapterKey(ch.Number, ch.Name)
		if err := i.db.ProviderChapter.Update().
			Where(
				entproviderchapter.SeriesProviderID(seriesProviderID),
				entproviderchapter.ChapterKey(key),
			).
			SetSuwayomiChapterID(ch.ID).
			Exec(ctx); err != nil {
			// Defensive path: reachable only on DB connection loss between ingest
			// and this update — the row must exist because IngestProviderChapters
			// just created or confirmed it.
			return fmt.Errorf("set suwayomi_chapter_id for key %q: %w", key, err)
		}
	}
	return nil
}
