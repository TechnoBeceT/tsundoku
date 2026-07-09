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
//   - SeriesProvider is looked up by (series_id, provider, scanlator) so that
//     two rows for the same source but different scanlation groups coexist as
//     independent providers (see AddSeries doc comment). A provider previously
//     created by the disk reconciler (which always uses scanlator="") is
//     reused rather than duplicated.
package suwayomi

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
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
//   - scanlator selects which scanlation group's chapters this provider row
//     tracks. "" means "all chapters from this source, regardless of
//     scanlator" (today's behavior, and what an untagged chapter matches). A
//     non-empty value keeps ONLY chapters whose Chapter.Scanlator
//     case-insensitively equals it — see mapToFetchedChapters. A provider is
//     therefore identified by (series, sourceName, scanlator): the same
//     source can be added twice under two different scanlators and the two
//     SeriesProvider rows coexist independently (see upsertSeriesProvider).
//
// The operation is idempotent: calling AddSeries again for the same
// (manga, scanlator) produces no duplicate rows. The M1 dedup invariant
// guarantees that re-ingesting the same chapter list creates no new Chapter
// rows (result counts will be zero on a second call).
func (i *Ingest) AddSeries(
	ctx context.Context,
	sourceName string,
	mangaID int,
	title string,
	scanlator string,
) (chapter.IngestResult, error) {
	// 1. Fetch all chapters from Suwayomi via the fetchChapters mutation. This
	//    contacts the upstream source and populates Suwayomi's internal cache
	//    before we touch our own DB, so that a client failure does not leave
	//    partially-created rows. FetchChapters must be called (not MangaChapters)
	//    because after Search the manga exists in Suwayomi but chapters are not
	//    cached yet (they require an explicit source fetch first). The result is
	//    UNFILTERED — it holds every scanlator's chapters; filtering happens in
	//    mapToFetchedChapters below.
	swChapters, err := i.client.FetchChapters(ctx, mangaID)
	if err != nil {
		return chapter.IngestResult{}, fmt.Errorf("suwayomi.Ingest.AddSeries: fetch chapters for manga %d: %w", mangaID, err)
	}

	// Resolve the source's display name ONCE (best-effort; "" when unresolved) —
	// reused for the defensive scanlator collapse below AND stored as
	// provider_name in upsertSeriesProvider, so ingest makes a single Sources()
	// call per AddSeries.
	providerName := i.resolveProviderName(ctx, sourceName)

	// Defensive scanlator collapse (mirrors the FE collapseUntaggedScanlator, but
	// enforced at the ingest chokepoint so NO surface — a stale FE, a direct API
	// call, a future caller — can leak). SourceBreakdown labels a source's
	// UNTAGGED chapters (Chapter.Scanlator == "") under the SOURCE NAME; if that
	// bucket is passed uncollapsed, filterByScanlator matches ZERO chapters and
	// creates a phantom 0-chapter provider (the source-identity scanlator-leak).
	// Treating "scanlator == source display name" as "" keeps the source's own
	// (untagged) chapters. It never mis-collapses a DISTINCT scanlator on an
	// aggregator (e.g. Comix hosting "Asura Scans"): that scanlator differs from
	// the aggregator's own name.
	if scanlator != "" && providerName != "" && strings.EqualFold(scanlator, providerName) {
		scanlator = ""
	}

	// 2. Upsert the Series row, keyed by slug = disk.Slugify(title).
	series, err := i.upsertSeries(ctx, title)
	if err != nil {
		return chapter.IngestResult{}, fmt.Errorf("suwayomi.Ingest.AddSeries: upsert series %q: %w", title, err)
	}

	// 3. Upsert the SeriesProvider row, keyed by (series_id, provider, scanlator).
	//    MangaMeta is called inside upsertSeriesProvider to populate the source's
	//    own title and cover URL — distinct from the canonical series title above.
	sp, err := i.upsertSeriesProvider(ctx, series.ID, sourceName, mangaID, scanlator, providerName)
	if err != nil {
		return chapter.IngestResult{}, fmt.Errorf("suwayomi.Ingest.AddSeries: upsert series provider %q (scanlator %q) for series %s: %w", sourceName, scanlator, series.ID, err)
	}

	// 4. Map Suwayomi chapters to the M1 FetchedChapter type, filtered to this
	//    provider's scanlator (see mapToFetchedChapters).
	fetched := mapToFetchedChapters(swChapters, scanlator)

	// 5. Delegate to the M1 ingest engine (dedup/identity — never duplicated).
	result, err := chapter.IngestProviderChapters(ctx, i.db, sp.ID, fetched)
	if err != nil {
		return chapter.IngestResult{}, fmt.Errorf("suwayomi.Ingest.AddSeries: ingest chapters for series provider %s: %w", sp.ID, err)
	}

	// 6. Post-ingest: write suwayomi_chapter_id on each ProviderChapter row.
	//    Keyed by (series_provider_id, chapter_key) — the same unique index the
	//    M1 ingest used to create/update the rows. Filtered the same way as
	//    step 4 so this loop only touches rows that belong to this provider.
	if err := i.backfillSuwayomiChapterIDs(ctx, sp.ID, filterByScanlator(swChapters, scanlator)); err != nil {
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
		// Link a freshly-created series to the configured default category so the
		// app invariant (every series has a category) holds even before an adopt
		// caller pins a specific category. The default is owner-chosen (is_default),
		// not the hardcoded "Other". A re-fetch of an existing series keeps
		// whatever category it already has.
		cat, catErr := category.ResolveDefault(ctx, i.db)
		if catErr != nil {
			return nil, fmt.Errorf("resolve default category for series %q: %w", slug, catErr)
		}
		created, createErr := i.db.Series.Create().
			SetTitle(title).
			SetSlug(slug).
			SetCategoryID(cat.ID).
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

// upsertSeriesProvider finds the SeriesProvider row by (series_id, provider,
// scanlator) or creates it. Matching on scanlator too means the same source
// can be attached twice under two different scanlation groups — e.g.
// (seriesID, "mangadex", "") and (seriesID, "mangadex", "Reset Scans") are
// DISTINCT rows, each with its own independent ProviderChapter feed and
// importance ranking. It fetches the source's own metadata via MangaMeta so
// that each SeriesProvider row carries the title and cover URL as the source
// knows them — independent of the canonical Series.title set by the caller. It
// also resolves the source's human-readable display name (provider_name) so the
// UI can show "WebToon" instead of the numeric source id stored in provider.
// On find it refreshes suwayomi_id, title, provider_name, and cover_url in case
// the manga (or the source name) was updated upstream — so a pre-existing row
// backfills its provider_name on the next ingest/refresh. Returns the existing
// or newly created row.
func (i *Ingest) upsertSeriesProvider(
	ctx context.Context,
	seriesID uuid.UUID,
	provider string,
	suwayomiMangaID int,
	scanlator string,
	providerName string,
) (*ent.SeriesProvider, error) {
	// Fetch the source's own title and cover so SeriesProvider reflects what
	// this specific source knows about the manga, not the canonical adopt title.
	meta, err := i.client.MangaMeta(ctx, suwayomiMangaID)
	if err != nil {
		return nil, fmt.Errorf("manga meta (series=%s provider=%q manga=%d): %w", seriesID, provider, suwayomiMangaID, err)
	}
	srcTitle := meta.Title
	cover := ""
	if meta.ThumbnailURL != nil {
		cover = *meta.ThumbnailURL
	}

	// providerName is the source's human-readable display name (e.g. "WebToon"),
	// resolved ONCE by the caller (AddSeries) and passed in so it is stored durably
	// alongside the numeric id. Runs on both create and update, so an existing row
	// backfills its name on the next refresh sweep. Best-effort: "" when unresolved
	// (the DTO falls back to the id), and a "" must never clobber a stored good name.

	existing, existErr := i.db.SeriesProvider.Query().
		Where(
			entseriesprovider.SeriesID(seriesID),
			entseriesprovider.Provider(provider),
			entseriesprovider.Scanlator(scanlator),
		).
		First(ctx)
	if existErr != nil && !ent.IsNotFound(existErr) {
		// Defensive path: reachable only on DB connection loss or cancelled context.
		return nil, fmt.Errorf("query (series=%s provider=%q scanlator=%q): %w", seriesID, provider, scanlator, existErr)
	}

	// Self-heal a row broken by the pre-fix scanlator-leak (see
	// existingOrSelfHealTwin): adopt it so the update path below repairs it in
	// place instead of Create()ing a duplicate on the next refresh sweep.
	existing, existErr = i.existingOrSelfHealTwin(ctx, existing, seriesID, provider, scanlator, providerName)
	if existErr != nil {
		return nil, existErr
	}

	if existing != nil {
		// Keep suwayomi_id, source title and cover fresh in case the manga was
		// re-added from a different Suwayomi instance or updated upstream.
		// SetScanlator is idempotent on the exact-match path (existing.Scanlator
		// already == scanlator) and REPAIRS a self-healed broken twin (source-name
		// → "").
		update := i.db.SeriesProvider.UpdateOne(existing).
			SetScanlator(scanlator).
			SetSuwayomiID(suwayomiMangaID).
			SetTitle(srcTitle).
			SetCoverURL(cover)
		// Only refresh provider_name when we actually resolved one — a transient
		// Sources() failure yields "" and must not clobber a previously-stored
		// good name (it would flicker back to the raw id until the next sweep).
		if providerName != "" {
			update.SetProviderName(providerName)
		}
		updated, updateErr := update.Save(ctx)
		if updateErr != nil {
			// Defensive path: reachable only on DB connection loss mid-operation.
			return nil, fmt.Errorf("update (series=%s provider=%q scanlator=%q): %w", seriesID, provider, scanlator, updateErr)
		}
		return updated, nil
	}

	created, createErr := i.db.SeriesProvider.Create().
		SetSeriesID(seriesID).
		SetProvider(provider).
		SetProviderName(providerName).
		SetScanlator(scanlator).
		SetSuwayomiID(suwayomiMangaID).
		SetTitle(srcTitle).
		SetCoverURL(cover).
		// importance=0 is the schema default; multi-source ranking is M3/M4.
		Save(ctx)
	if createErr != nil {
		// Defensive path: reachable on DB connection loss or a concurrent INSERT
		// that races with the query above.
		return nil, fmt.Errorf("create (series=%s provider=%q scanlator=%q): %w", seriesID, provider, scanlator, createErr)
	}
	return created, nil
}

// existingOrSelfHealTwin returns `existing` unchanged when it is already set or
// when there is nothing to self-heal (the collapsed scanlator is non-empty, or no
// source display name resolved). Otherwise it looks for a row broken by the
// pre-fix scanlator-leak — same (series, provider) but scanlator == the source's
// own display name (which the AddSeries collapse turned into "", so the
// exact-match lookup missed it) — and returns it so upsertSeriesProvider repairs
// it IN PLACE (repoints scanlator to "", repopulates its empty feed, keeps the
// owner's importance) rather than Create()ing a duplicate + resetting importance
// to 0 on the next refresh sweep. Returns (nil, nil) when no twin exists. No
// unique (series,provider,scanlator) index — query-then-update; race-benign for a
// single owner.
func (i *Ingest) existingOrSelfHealTwin(ctx context.Context, existing *ent.SeriesProvider, seriesID uuid.UUID, provider, scanlator, providerName string) (*ent.SeriesProvider, error) {
	if existing != nil || scanlator != "" || providerName == "" {
		return existing, nil
	}
	broken, err := i.db.SeriesProvider.Query().
		Where(
			entseriesprovider.SeriesID(seriesID),
			entseriesprovider.Provider(provider),
			entseriesprovider.ScanlatorEqualFold(providerName),
		).
		First(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query broken scanlator twin (series=%s provider=%q name=%q): %w", seriesID, provider, providerName, err)
	}
	return broken, nil
}

// resolveProviderName returns the human-readable display name for a source id
// (the value stored in SeriesProvider.provider) by looking it up in the live
// source list. It is best-effort and NEVER returns an error: if the source list
// cannot be fetched or the id is not present, it returns "" and the caller stores
// an empty provider_name, leaving the DTO layer to fall back to the raw id. A
// missing display name must never fail an ingest. One Sources() call per
// AddSeries (AddSeries handles a single provider), so no per-chapter fan-out.
func (i *Ingest) resolveProviderName(ctx context.Context, sourceID string) string {
	sources, err := i.client.Sources(ctx)
	if err != nil {
		return ""
	}
	for _, src := range sources {
		if src.ID == sourceID {
			return src.Name
		}
	}
	return ""
}

// filterByScanlator returns the subset of chs that belong to scanlator.
//
//   - scanlator == "" means "no filtering" — every chapter matches, which is
//     today's (pre-scanlator) behavior and is regression-critical to preserve.
//   - A non-empty scanlator keeps ONLY chapters whose Chapter.Scanlator is
//     case-insensitively equal to it (strings.EqualFold, mirroring Kaizoku's
//     workers.go:1311 comparison). An untagged chapter (Scanlator == "") never
//     matches a named scanlator — it belongs only to the "" (all-chapters)
//     provider, which is intentional and must not be special-cased away.
func filterByScanlator(chs []Chapter, scanlator string) []Chapter {
	if scanlator == "" {
		return chs
	}
	filtered := make([]Chapter, 0, len(chs))
	for _, ch := range chs {
		if strings.EqualFold(ch.Scanlator, scanlator) {
			filtered = append(filtered, ch)
		}
	}
	return filtered
}

// mapToFetchedChapters converts a slice of Suwayomi Chapter DTOs to the M1
// FetchedChapter type, first filtering to the given scanlator (see
// filterByScanlator). The mapping is lossless for the fields that the M1
// ingest engine uses; suwayomi_chapter_id is NOT included here — it is written
// in a separate post-ingest update (backfillSuwayomiChapterIDs).
func mapToFetchedChapters(chs []Chapter, scanlator string) []chapter.FetchedChapter {
	chs = filterByScanlator(chs, scanlator)
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
