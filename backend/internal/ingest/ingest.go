// Package ingest — engine-agnostic chapter-ingest service.
//
// This file (ingest.go) implements the Ingest service, the bridge between the
// URL-addressed engine-host client (internal/sourceengine) and the M1
// chapter-ingest engine (chapter.IngestProviderChapters). It is the P2
// (Suwayomi-removal) successor to internal/suwayomi's Ingest — same job, but
// every source manga/chapter is addressed by a (sourceID, url) pair instead of
// a Suwayomi-internal numeric manga id. internal/suwayomi's own Ingest stays
// in place for imports/library until a later P2 slice repoints them; this
// package MUST NOT import internal/suwayomi (that dependency is exactly what
// this migration removes).
//
// Design decisions (carried over from internal/suwayomi/ingest.go):
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
//
//   - There is no engine-assigned chapter id to backfill: sourceengine.Chapter
//     is addressed purely by URL, and the download path already fetches pages
//     via ProviderChapter.URL (see download's buildFetchRef). The old
//     backfillSuwayomiChapterIDs step (and ProviderChapter.suwayomi_chapter_id)
//     has no equivalent here — it is simply not written by this package.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/pkg/chapterrange"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourcegate"
)

// ErrSourceCooledDown is returned by AddSeries when the source-politeness gate
// reports the target physical source is currently in circuit-breaker cooldown,
// so the adopt/attach fetch is refused rather than hammering a source that is
// likely blocking us. Callers (imports.Adopt, library.AddProvider) surface it up
// the stack. Nil gate ⇒ never returned (no gating).
var ErrSourceCooledDown = errors.New("source in circuit-breaker cooldown")

// Ingest bridges the engine-host client (internal/sourceengine) and the M1
// chapter-ingest engine. Create one with NewIngest and call AddSeries to
// populate the DB so the M1 download dispatcher has chapters to process.
//
// cache and gate are anti-ban de-amplification collaborators, both nil-safe:
//   - cache (*ChapterCache) memoizes the raw client.Chapters result so a
//     coverage→configure→adopt session fetches a source-manga ONCE (see
//     AddSeries / fetchForAdopt). A nil cache means every fetch hits upstream
//     (today's behaviour) — this is the case for the plain NewIngest used by
//     tests. Only the interactive adopt path is cached; the refresh sweep fetches
//     fresh (FetchChaptersUncached), so the cache never stales-out discovery.
//   - gate (*sourcegate.Service) is the per-physical-source circuit-breaker +
//     politeness delay; AddSeries routes its ONE upstream fetch through it (see
//     gatedUpstream). A nil gate means no gating. The refresh sweep instead
//     fetches via FetchChaptersUncached (no cache, no gate) and applies its own
//     gate around that pre-fetch, so the shared cache stays interactive-only.
type Ingest struct {
	client sourceengine.Client
	db     *ent.Client
	cache  *ChapterCache
	gate   *sourcegate.Service
}

// NewIngest constructs an Ingest backed by the given engine-host client and ent
// database client, with NO chapter cache and NO source-politeness gate — every
// AddSeries fetch hits upstream. This is the narrow constructor tests use;
// production wires the shared cache + gate via NewIngestWithGate.
func NewIngest(client sourceengine.Client, db *ent.Client) *Ingest {
	return &Ingest{client: client, db: db}
}

// NewIngestWithGate constructs an Ingest that SHARES the given chapter cache
// (so the discovery coverage path and this adopt path collapse onto one upstream
// fetch per source-manga) and routes its adopt/attach fetch through the given
// source-politeness gate. Either may be nil (falling back to NewIngest's
// behaviour for that collaborator). This is the production constructor
// (cmd/tsundoku/main.go) — it mirrors series.NewServiceWithStaleGrade: a wider
// constructor for the wired app, the narrow one kept for call sites that don't
// need the extra collaborators.
func NewIngestWithGate(client sourceengine.Client, db *ent.Client, cache *ChapterCache, gate *sourcegate.Service) *Ingest {
	return &Ingest{client: client, db: db, cache: cache, gate: gate}
}

// AddSeries fetches all chapters for the manga at url on sourceID, upserts the
// corresponding Series and SeriesProvider rows, then delegates to the M1
// chapter.IngestProviderChapters for dedup/identity.
//
// Parameters:
//   - sourceID is the engine-host's stable numeric source identifier, stored
//     (stringified) as SeriesProvider.provider.
//   - url is the source-relative manga URL — the stable key the engine host
//     addresses this manga by (there is no manga-id lookup in the URL-addressed
//     model).
//   - title is the manga's display title (used to derive the Series slug and
//     set Series.title). The caller is responsible for providing the correct
//     title; AddSeries does not search for it.
//   - scanlator selects which scanlation group's chapters this provider row
//     tracks. "" means "all chapters from this source, regardless of
//     scanlator" (today's behavior, and what an untagged chapter matches). A
//     non-empty value keeps ONLY chapters whose Chapter.Scanlator
//     case-insensitively equals it — see mapToFetchedChapters. A provider is
//     therefore identified by (series, sourceID, scanlator): the same source
//     can be added twice under two different scanlators and the two
//     SeriesProvider rows coexist independently (see upsertSeriesProvider).
//
// The operation is idempotent: calling AddSeries again for the same
// (url, scanlator) produces no duplicate rows. The M1 dedup invariant
// guarantees that re-ingesting the same chapter list creates no new Chapter
// rows (result counts will be zero on a second call).
func (i *Ingest) AddSeries(
	ctx context.Context,
	sourceID int64,
	url string,
	title string,
	scanlator string,
) (chapter.IngestResult, error) {
	// Resolve the source's display name ONCE (best-effort; "" when unresolved) —
	// reused as the source-politeness gate key below AND passed into
	// addSeriesWithChapters for the scanlator collapse + provider_name storage,
	// so a whole AddSeries makes a single Sources() call.
	providerName := i.resolveProviderName(ctx, sourceID)

	// 1. Fetch all chapters from the engine host, THROUGH the source-politeness
	//    gate and the shared chapter cache. This contacts the upstream source
	//    before we touch our own DB, so that a client failure does not leave
	//    partially-created rows. The result is UNFILTERED — it holds every
	//    scanlator's chapters; filtering happens in mapToFetchedChapters below.
	//    On a cache hit NO upstream request is made, so the gate is
	//    legitimately bypassed (there is nothing to throttle).
	swChapters, err := i.fetchForAdopt(ctx, sourceID, url, title, providerName)
	if err != nil {
		return chapter.IngestResult{}, fmt.Errorf("ingest.Ingest.AddSeries: fetch chapters for source %d url %q: %w", sourceID, url, err)
	}

	return i.addSeriesWithChapters(ctx, sourceID, url, title, scanlator, providerName, swChapters)
}

// AddSeriesUngated is AddSeries for a DELIBERATE, one-shot OWNER-initiated attach
// (library.AddProvider / library.MatchDiskProvider — the "Link chapters / Match
// source" action). It performs the SAME ingest as AddSeries, but its upstream
// fetch BYPASSES the circuit-breaker cooldown refusal: it never returns
// ErrSourceCooledDown.
//
// The source-politeness gate (sourcegate) exists to throttle BULK background
// sweeps (the refresh sweep, the download/upgrade dispatcher) so an unattended
// deployment cannot hammer a source into a Cloudflare block. A single, explicit
// owner click is not that traffic shape — refusing it because unrelated
// background failures happened to trip the breaker is exactly the phantom
// "source not found" bug this method fixes. The politeness delay (Wait) and the
// success/failure bookkeeping are STILL applied, so an owner attach stays polite
// and a success even clears the breaker; only the cooldown REFUSAL is skipped.
//
// This is deliberately NOT used by any background path — the sweep and the
// dispatcher MUST stay gated (anti-ban). Only the owner attach/match path calls
// it.
func (i *Ingest) AddSeriesUngated(
	ctx context.Context,
	sourceID int64,
	url string,
	title string,
	scanlator string,
) (chapter.IngestResult, error) {
	providerName := i.resolveProviderName(ctx, sourceID)

	swChapters, err := i.fetchForAttach(ctx, sourceID, url, title, providerName)
	if err != nil {
		return chapter.IngestResult{}, fmt.Errorf("ingest.Ingest.AddSeriesUngated: fetch chapters for source %d url %q: %w", sourceID, url, err)
	}

	return i.addSeriesWithChapters(ctx, sourceID, url, title, scanlator, providerName, swChapters)
}

// AddSeriesWithChapters is AddSeries WITHOUT the upstream fetch: it ingests the
// caller-supplied raw (all-scanlators) chapter list for this source-manga. The
// refresh sweep uses it to fetch a source-manga ONCE (via FetchChaptersUncached)
// and ingest every (source, scanlator) provider that shares it from that single
// result, instead of re-fetching per scanlator. Because the caller already
// performed (and gated) the fetch, this path applies NO gate — gating it again
// would double-Wait a source that was already throttled for the pre-fetch.
//
// raw must be the UNFILTERED list (every scanlator); it is filtered to this
// provider's scanlator by mapToFetchedChapters / filterByScanlator, exactly as
// AddSeries does.
func (i *Ingest) AddSeriesWithChapters(
	ctx context.Context,
	sourceID int64,
	url string,
	title string,
	scanlator string,
	raw []sourceengine.Chapter,
) (chapter.IngestResult, error) {
	providerName := i.resolveProviderName(ctx, sourceID)
	return i.addSeriesWithChapters(ctx, sourceID, url, title, scanlator, providerName, raw)
}

// addSeriesWithChapters is the shared ingest body for AddSeries and
// AddSeriesWithChapters: given an already-fetched raw chapter list and the
// already-resolved provider display name, it upserts the Series + SeriesProvider
// rows and delegates dedup/identity to the M1 chapter engine. It performs NO
// upstream chapter fetch.
func (i *Ingest) addSeriesWithChapters(
	ctx context.Context,
	sourceID int64,
	url string,
	title string,
	scanlator string,
	providerName string,
	swChapters []sourceengine.Chapter,
) (chapter.IngestResult, error) {
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
		return chapter.IngestResult{}, fmt.Errorf("ingest.Ingest.AddSeries: upsert series %q: %w", title, err)
	}

	// 3. Upsert the SeriesProvider row, keyed by (series_id, provider, scanlator).
	//    MangaDetails is called inside upsertSeriesProvider to populate the
	//    source's own title and cover — distinct from the canonical series title
	//    above.
	sp, err := i.upsertSeriesProvider(ctx, series.ID, sourceID, url, scanlator, providerName)
	if err != nil {
		return chapter.IngestResult{}, fmt.Errorf("ingest.Ingest.AddSeries: upsert series provider %d (scanlator %q) for series %s: %w", sourceID, scanlator, series.ID, err)
	}

	// 3a. Owner gate: a source flagged as a fractional re-uploader for THIS series
	//     contributes NO fractional chapters to its feed. Applied to the RAW slice
	//     so the ingest mapping (step 4) stays in lockstep with what the sweep
	//     actually intended to ingest.
	//
	//     Upsert-only semantics are untouched: fractional rows ingested BEFORE the
	//     flag was ticked are NOT deleted (never-auto-delete) — they are simply
	//     never refreshed here, and never dispatched. Un-ticking restores the
	//     source at once.
	if sp.IgnoreFractional {
		swChapters = dropFractional(swChapters)
	}

	// 4. Map engine chapters to the M1 FetchedChapter type, filtered to this
	//    provider's scanlator (see mapToFetchedChapters).
	fetched := mapToFetchedChapters(swChapters, scanlator)

	// 5. Delegate to the M1 ingest engine (dedup/identity — never duplicated).
	result, err := chapter.IngestProviderChapters(ctx, i.db, sp.ID, fetched)
	if err != nil {
		return chapter.IngestResult{}, fmt.Errorf("ingest.Ingest.AddSeries: ingest chapters for series provider %s: %w", sp.ID, err)
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
// (seriesID, "7537715367149829912", "") and (seriesID, "7537715367149829912",
// "Reset Scans") are DISTINCT rows, each with its own independent
// ProviderChapter feed and importance ranking. It fetches the source's own
// metadata via MangaDetails so that each SeriesProvider row carries the title
// and cover as the source knows them — independent of the canonical
// Series.title set by the caller. SeriesProvider.URL is set to the CALLER'S
// url argument, never derived from the MangaDetails response (the engine host
// has no id→lookup; url is the only stable key we have).
// On find it refreshes title, provider_name, cover_url, and url in case the
// manga (or the source name) was updated upstream. Returns the existing or
// newly created row.
func (i *Ingest) upsertSeriesProvider(
	ctx context.Context,
	seriesID uuid.UUID,
	sourceID int64,
	url string,
	scanlator string,
	providerName string,
) (*ent.SeriesProvider, error) {
	// Fetch the source's own title and cover so SeriesProvider reflects what
	// this specific source knows about the manga, not the canonical adopt title.
	meta, err := i.client.MangaDetails(ctx, sourceID, url)
	if err != nil {
		return nil, fmt.Errorf("manga details (series=%s source=%d url=%q): %w", seriesID, sourceID, url, err)
	}
	srcTitle := meta.Title
	cover := meta.ThumbnailURL
	// webURL is the fully-qualified, browser-clickable manga URL (distinct
	// from the url PARAMETER above, which is the source-relative addressing
	// key) — stored so an adopted series' "View on source" link and Komga's
	// ComicInfo <Web> field work without a live engine call.
	webURL := meta.RealURL

	provider := providerKey(sourceID)

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
		// Keep source title, cover, and url fresh in case the manga was
		// re-added from a different engine host or updated upstream.
		// SetScanlator is idempotent on the exact-match path (existing.Scanlator
		// already == scanlator) and REPAIRS a self-healed broken twin (source-name
		// → "").
		update := i.db.SeriesProvider.UpdateOne(existing).
			SetScanlator(scanlator).
			SetURL(url)
		applyOptionalSeriesProviderFields(update, srcTitle, cover, webURL, providerName)
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
		SetTitle(srcTitle).
		SetCoverURL(cover).
		SetURL(url).
		SetWebURL(webURL).
		// importance=0 is the schema default; multi-source ranking is M3/M4.
		Save(ctx)
	if createErr != nil {
		// Defensive path: reachable on DB connection loss or a concurrent INSERT
		// that races with the query above.
		return nil, fmt.Errorf("create (series=%s provider=%q scanlator=%q): %w", seriesID, provider, scanlator, createErr)
	}
	return created, nil
}

// applyOptionalSeriesProviderFields guards the four MangaDetails-sourced
// SeriesProvider.Update fields that must NEVER be blanked by a transient
// empty engine response: title/cover/webURL are only set when non-empty (a
// blank MangaDetails hiccup must not overwrite a previously-stored good
// value), and providerName only when a Sources() lookup actually resolved
// one (a transient failure yields "" and must not clobber a stored name).
// Extracted from upsertSeriesProvider's update branch to keep that
// function's cyclomatic complexity within the fleet lint budget (§2 DRY is a
// side benefit, not the primary reason).
func applyOptionalSeriesProviderFields(update *ent.SeriesProviderUpdateOne, srcTitle, cover, webURL, providerName string) {
	if srcTitle != "" {
		update.SetTitle(srcTitle)
	}
	if cover != "" {
		update.SetCoverURL(cover)
	}
	if webURL != "" {
		update.SetWebURL(webURL)
	}
	if providerName != "" {
		update.SetProviderName(providerName)
	}
}

// providerKey renders sourceID as the string stored in SeriesProvider.provider
// — the engine-host's stable numeric source id, stringified. Kept as a named
// helper (rather than an inline strconv call at every call site) so the
// provider-identity string format has exactly one definition.
func providerKey(sourceID int64) string {
	return strconv.FormatInt(sourceID, 10)
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
// (the value stored in SeriesProvider.provider, stringified) by looking it up
// in the live source list. It is best-effort and NEVER returns an error: if
// the source list cannot be fetched or the id is not present, it returns ""
// and the caller stores an empty provider_name, leaving the DTO layer to fall
// back to the raw id. A missing display name must never fail an ingest. One
// Sources() call per AddSeries (AddSeries handles a single provider), so no
// per-chapter fan-out.
func (i *Ingest) resolveProviderName(ctx context.Context, sourceID int64) string {
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

// FetchChaptersUncached returns the raw, unfiltered chapter list for the manga
// at url on sourceID straight from the client, deliberately BYPASSING the
// shared chapter cache. It is the refresh sweep's single per-source-manga
// pre-fetch: the sweep already dedups each (source, manga) to ONE fetch per
// sweep via its own grouping, and it needs a FRESH result every sweep so the
// long, interactive cache TTL can never stale-out new-chapter discovery. The
// sweep applies its OWN source-politeness gate (Wait + breaker bookkeeping)
// around this call, so it is also deliberately UNGATED here (no double-Wait).
// Keeping refresh off the shared cache is what lets that cache be a
// long-lived, interactive-only memo. mangaTitle is passed through to the
// engine host's chapter-number recognition; "" is safe when unknown.
func (i *Ingest) FetchChaptersUncached(ctx context.Context, sourceID int64, url string, mangaTitle string) ([]sourceengine.Chapter, error) {
	return i.client.Chapters(ctx, sourceID, url, mangaTitle)
}

// fetchForAdopt returns the raw chapter list for the adopt/attach path: cached
// AND, on a cache miss, gated. The gate wraps the ONE real upstream fetch, so a
// cache hit makes no request and correctly skips the gate. title feeds the
// engine host's chapter-number recognition on a cache miss (a cache hit never
// re-fetches, so it has no effect then) AND is part of the cache key itself
// (see chaptersThroughCache) — a title="" discovery preview populated by
// imports.Service never masquerades as this call's real-title result.
func (i *Ingest) fetchForAdopt(ctx context.Context, sourceID int64, url string, title string, providerName string) ([]sourceengine.Chapter, error) {
	return i.chaptersThroughCache(ctx, sourceID, url, title, func() ([]sourceengine.Chapter, error) {
		return i.gatedUpstream(ctx, sourceID, url, title, providerName, true)
	})
}

// fetchForAttach is fetchForAdopt for the one-shot OWNER attach path
// (AddSeriesUngated): cached, and on a cache miss fetched with the SAME
// politeness delay + breaker bookkeeping BUT with the cooldown refusal skipped
// (respectCooldown=false — see gatedUpstream). A cache hit still makes no request
// (and correctly skips the gate entirely), exactly like fetchForAdopt.
func (i *Ingest) fetchForAttach(ctx context.Context, sourceID int64, url string, title string, providerName string) ([]sourceengine.Chapter, error) {
	return i.chaptersThroughCache(ctx, sourceID, url, title, func() ([]sourceengine.Chapter, error) {
		return i.gatedUpstream(ctx, sourceID, url, title, providerName, false)
	})
}

// chaptersThroughCache routes fetch through the shared chapter cache when one is
// wired, else calls fetch directly (nil cache = today's uncached behaviour).
// title is threaded into the cache key (see chapterCacheKey's doc comment) so a
// fetch made under one mangaTitle can never be served back for a different one.
func (i *Ingest) chaptersThroughCache(ctx context.Context, sourceID int64, url string, title string, fetch func() ([]sourceengine.Chapter, error)) ([]sourceengine.Chapter, error) {
	if i.cache == nil {
		return fetch()
	}
	return i.cache.Get(ctx, sourceID, url, title, fetch)
}

// gatedUpstream performs exactly ONE client.Chapters call wrapped in the
// source-politeness gate: refuse when the source's breaker is in cooldown
// (ErrSourceCooledDown), enforce the politeness delay, then record the outcome
// so the breaker converges. A nil gate makes every step a no-op (ungated fetch).
// The gate key is the physical-source identity (provider display name else the
// stringified source id, trimmed) — the SAME key refresh/download use, so one
// source's breaker state is shared across every path. title feeds the engine
// host's chapter-number recognition.
//
// respectCooldown gates ONLY the cooldown REFUSAL: true for the background
// adopt/sweep paths (a tripped breaker refuses the fetch); false for the one-shot
// OWNER attach (AddSeriesUngated) — the politeness delay + breaker bookkeeping
// still run, but a tripped breaker never blocks a deliberate owner click.
func (i *Ingest) gatedUpstream(ctx context.Context, sourceID int64, url string, title string, providerName string, respectCooldown bool) ([]sourceengine.Chapter, error) {
	key := gateKey(providerName, sourceID)
	now := time.Now()
	if respectCooldown && !i.gateAvailable(ctx, key, now) {
		return nil, fmt.Errorf("%w: %s", ErrSourceCooledDown, key)
	}
	i.gateWait(ctx, key)
	chs, err := i.client.Chapters(ctx, sourceID, url, title)
	if err != nil {
		i.gateRecordFailure(ctx, key, err, now)
		return nil, err
	}
	i.gateRecordSuccess(ctx, key)
	return chs, nil
}

// gateKey is the physical-source identity used to key the source-politeness gate
// for an ingest fetch: the resolved display name when known, else the
// stringified source id, trimmed. Mirrors refresh.sourceKey / download's
// canonical source key so the breaker is keyed consistently across every
// source-access path.
func gateKey(providerName string, sourceID int64) string {
	name := providerName
	if name == "" {
		name = providerKey(sourceID)
	}
	return strings.TrimSpace(name)
}

// gateAvailable reports whether key's breaker permits access. Nil gate ⇒ true.
func (i *Ingest) gateAvailable(ctx context.Context, key string, now time.Time) bool {
	if i.gate == nil {
		return true
	}
	return i.gate.IsAvailable(ctx, key, now)
}

// gateWait enforces the politeness delay before a fetch. Nil gate ⇒ no-op.
func (i *Ingest) gateWait(ctx context.Context, key string) {
	if i.gate == nil {
		return
	}
	i.gate.Wait(ctx, key)
}

// gateRecordSuccess reports a successful fetch to the breaker. Nil gate ⇒ no-op.
func (i *Ingest) gateRecordSuccess(ctx context.Context, key string) {
	if i.gate == nil {
		return
	}
	i.gate.RecordSuccess(ctx, key)
}

// gateRecordFailure reports a failed fetch to the breaker. Nil gate ⇒ no-op.
func (i *Ingest) gateRecordFailure(ctx context.Context, key string, cause error, now time.Time) {
	if i.gate == nil {
		return
	}
	i.gate.RecordFailure(ctx, key, cause, now)
}

// filterByScanlator returns the subset of chs that belong to scanlator.
//
//   - scanlator == "" means "no filtering" — every chapter matches, which is
//     today's (pre-scanlator) behavior and is regression-critical to preserve.
//   - A non-empty scanlator keeps ONLY chapters whose Chapter.Scanlator is
//     case-insensitively equal to it (strings.EqualFold). An untagged chapter
//     (Scanlator == "") never matches a named scanlator — it belongs only to
//     the "" (all-chapters) provider, which is intentional and must not be
//     special-cased away.
func filterByScanlator(chs []sourceengine.Chapter, scanlator string) []sourceengine.Chapter {
	if scanlator == "" {
		return chs
	}
	filtered := make([]sourceengine.Chapter, 0, len(chs))
	for _, ch := range chs {
		if strings.EqualFold(ch.Scanlator, scanlator) {
			filtered = append(filtered, ch)
		}
	}
	return filtered
}

// dropFractional removes fractional-numbered chapters (5.1, 5.5 …) from a raw
// source chapter list. Used ONLY for a provider whose ignore_fractional flag the
// owner has ticked — a mirror that republishes whole chapter N as a lone "N.1"
// under its own URL (Comic Asura does exactly this: 179 pages vs the original's
// 26).
//
// A chapter carrying the engine host's "unparsed number" sentinel (see
// hasParsedNumber) is KEPT: it cannot be judged fractional, and dropping it
// would silently lose a chapter whose only fault is an unparseable number.
func dropFractional(chs []sourceengine.Chapter) []sourceengine.Chapter {
	out := make([]sourceengine.Chapter, 0, len(chs))
	for _, ch := range chs {
		if hasParsedNumber(ch) && chapterrange.IsFractional(ch.Number) {
			continue
		}
		out = append(out, ch)
	}
	return out
}

// hasParsedNumber reports whether ch carries a genuine parsed chapter number.
// sourceengine.Chapter.Number is a non-nullable float64 (unlike the old
// Suwayomi client's *float64), but the underlying Mihon source library still
// uses ITS OWN sentinel, -1, for "could not parse a number from this chapter"
// (see engine-host's SChapter.chapter_number, forwarded verbatim onto the wire
// — engine-host performs no null-translation, unlike Suwayomi's GraphQL layer,
// which used to send an actual null for the same case). Any negative value is
// therefore treated as "no number", exactly like the old nil-Number case, so
// chapter identity for these chapters still falls back to the name-based key
// (see NormalizeChapterKey) instead of every quirky chapter colliding onto a
// single literal "-1" chapter_key.
func hasParsedNumber(ch sourceengine.Chapter) bool {
	return ch.Number >= 0
}

// mapToFetchedChapters converts a slice of engine-host Chapter DTOs to the M1
// FetchedChapter type, first filtering to the given scanlator (see
// filterByScanlator).
//
// Field mapping deltas versus the old Suwayomi mapping (sourceengine.Chapter
// carries no per-chapter engine id, no explicit provider index, and no page
// count):
//   - ProviderIndex is the chapter's REVERSED 0-based position in the
//     (already scanlator-filtered) slice — the engine host does not report
//     one, so Go derives it. The engine host's raw list is newest-first
//     (index 0 = newest chapter — see SourceCalls.chapters), and Suwayomi's
//     own Chapter.kt assigns its equivalent field, sourceOrder, from that same
//     raw list REVERSED (`uniqueChapters.reversed().forEachIndexed`, P2
//     mapper-audit M6) so the OLDEST chapter gets the LOWEST index and the
//     NEWEST the HIGHEST — the convention ProviderIndex's own doc comment
//     ("used for ordering when numeric chapter numbers are absent or
//     ambiguous") assumes. Mirroring the direction here means a future
//     ProviderIndex-based tiebreak (unnumbered chapters, where Number can't
//     order them) agrees with Suwayomi's, instead of running backwards.
//   - PageCount is always nil: it is not known at ingest time, only once a
//     chapter is actually downloaded/rendered, so passing nil (rather than a
//     misleading literal 0) means the M1 ingest stores no page count here.
//   - Number is nil for a chapter carrying the engine host's unparsed-number
//     sentinel (see hasParsedNumber); otherwise it is a pointer to the engine's
//     value.
//   - UploadDate converts the engine's epoch-milliseconds int64 (0 = omitted)
//     to a *time.Time, nil when omitted.
func mapToFetchedChapters(chs []sourceengine.Chapter, scanlator string) []chapter.FetchedChapter {
	chs = filterByScanlator(chs, scanlator)
	out := make([]chapter.FetchedChapter, len(chs))
	for idx, ch := range chs {
		var num *float64
		if hasParsedNumber(ch) {
			n := ch.Number
			num = &n
		}
		var uploadDate *time.Time
		if ch.UploadDate > 0 {
			t := time.UnixMilli(ch.UploadDate)
			uploadDate = &t
		}
		out[idx] = chapter.FetchedChapter{
			Number: num,
			Name:   ch.Name,
			URL:    ch.URL,
			WebURL: ch.RealURL,
			// Reversed: the raw slice is newest-first (index 0 = newest), so the
			// oldest chapter (the LAST element) must get index 0 to match
			// Suwayomi's sourceOrder convention — see the doc comment above.
			ProviderIndex: len(chs) - 1 - idx,
			PageCount:     nil,
			UploadDate:    uploadDate,
		}
	}
	return out
}
