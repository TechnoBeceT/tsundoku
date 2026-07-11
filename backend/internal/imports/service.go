// Package imports — import-workflow service.
//
// This file implements Service, which is the domain layer for the manga import
// workflow: discovering available Suwayomi sources, searching across them, and
// previewing a manga's chapter list before adopting it into the Tsundoku
// library.
//
// Service is intentionally thin: it delegates source enumeration and search to
// suwayomi.Client, grouping to groupCandidates (match.go), and chapter preview
// to suwayomi.Client.FetchChapters. The ingest, db, and storage fields are
// declared now for Task 4 (adopt) but not used here.
package imports

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/metrics"
	"github.com/technobecet/tsundoku/internal/pkg/chapterrange"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// searchConcurrency is the maximum number of sources queried in parallel during
// Search. This bounds upstream load when many sources are installed.
const searchConcurrency = 8

// recordTimeout bounds the background post-fan-out metrics batch write so its
// goroutine always terminates even against a stuck DB. It is applied to a
// cancellation-detached context (context.WithoutCancel) so the write survives the
// client disconnect that would otherwise cancel the request context.
const recordTimeout = 10 * time.Second

// ErrSourceNotFound is returned by Browse when the requested sourceID is not in
// the live source list (client.Sources). The HTTP handler maps it to 404.
var ErrSourceNotFound = errors.New("source not found")

// Service provides the import workflow over a Suwayomi backend: source
// discovery, multi-source search with fuzzy grouping, and chapter inspection.
//
// Fields ingest, db, and storage are unused in Task 3 but declared here so
// Task 4 can extend this struct without changing the constructor signature.
type Service struct {
	client        suwayomi.Client
	ingest        *suwayomi.Ingest
	db            *ent.Client
	storage       string
	searchTimeout time.Duration
	recorder      metrics.Recorder

	// chapterCache memoizes the raw client.FetchChapters result so coverage
	// (SourceBreakdown) / InspectChapters / Adopt fetch a source-manga ONCE (Task
	// C2). It is the SAME instance the adopt-side suwayomi.Ingest holds, so a
	// coverage→configure→adopt session collapses onto a single upstream fetch. Nil
	// ⇒ no chapter caching (each call hits upstream — the plain NewService case).
	chapterCache *suwayomi.ChapterCache
	// searchCache memoizes Search fan-out results (Task C1). Nil ⇒ no search
	// caching (every Search fans out — the plain NewService case).
	searchCache *searchCache
}

// NewService constructs a Service backed by the given Suwayomi client.
//
// searchTimeout is the OVERALL deadline for one interactive Search fan-out (see
// Search) — it bounds the response below a CDN edge timeout and yields partial
// results instead of hanging on a slow anti-bot source. It is sourced from
// config (cfg.Suwayomi.SearchTimeout) and is DISTINCT from the per-request HTTP
// client timeout, which downloads keep generous.
//
// ingest, db, and storage back the adopt/import workflow and may be nil/empty
// for callers that only use the read-only discovery paths.
//
// recorder receives one batch of per-source search timings after each Search
// fan-out (see Search); it is best-effort and may be nil (recording is then
// skipped) for callers/tests that do not exercise metrics.
func NewService(client suwayomi.Client, ingest *suwayomi.Ingest, db *ent.Client, storage string, searchTimeout time.Duration, recorder metrics.Recorder) *Service {
	return &Service{
		client:        client,
		ingest:        ingest,
		db:            db,
		storage:       storage,
		searchTimeout: searchTimeout,
		recorder:      recorder,
	}
}

// NewServiceWithCaches is NewService plus the anti-ban de-amplification caches:
// chapterCache (SHARED with the adopt-side suwayomi.Ingest so coverage→adopt
// fetches a source-manga once — Task C2) and an internally-built search-result
// cache (Task C1). It is the production constructor (server.registerRoutes); the
// plain NewService (no caches) is kept so the many read-only/test call sites need
// no change. chapterCache may be nil (chapter caching then disabled). searchTTL
// supplies the search cache's lifetime PER-Get (jobs.search_cache_ttl, hot
// reload); a searchTTL returning 0 or less disables the search cache at runtime.
func NewServiceWithCaches(client suwayomi.Client, ingest *suwayomi.Ingest, db *ent.Client, storage string, searchTimeout time.Duration, recorder metrics.Recorder, chapterCache *suwayomi.ChapterCache, searchTTL func(context.Context) time.Duration) *Service {
	s := NewService(client, ingest, db, storage, searchTimeout, recorder)
	s.chapterCache = chapterCache
	s.searchCache = newSearchCache(searchTTL)
	return s
}

// Sources returns all Suwayomi sources as SourceDTOs, excluding Suwayomi's
// built-in Local source (see isLocalSource) AND any source the owner has
// disabled (see isDisabledSource) — the per-language enable/disable toggle
// exposed via the extension "Configure" dialog. The Local source indexes
// files from Suwayomi's own on-disk localSourcePath rather than a real online
// provider, and its lang tag renders as the raw "LOCALSOURCELANG" string in
// the UI — it should never appear in the Discover picker or the Search
// "Limit to" filters (F1).
//
// Disabling only declutters these pickers; it does not affect a series
// already adopted from the disabled source (refresh.RefreshAll iterates
// SeriesProvider rows, not this list) and does not block a direct-by-id
// request (resolveSource/Browse, and Adopt/AddProvider's ingest calls, all
// bypass this filtered list — see resolveSources below for the equivalent
// Search/Browse-fan-out filter).
//
// client.Sources already selects each source's `meta` inline (see
// suwayomi/source_meta.go), so this filter costs no extra round trip.
func (s *Service) Sources(ctx context.Context) ([]SourceDTO, error) {
	srcs, err := s.client.Sources(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SourceDTO, 0, len(srcs))
	for _, src := range srcs {
		if excludedFromPicker(src) {
			continue
		}
		out = append(out, SourceDTO{ID: src.ID, Name: src.Name, Lang: src.Lang})
	}
	return out, nil
}

// isLocalSource reports whether src is Suwayomi's built-in Local source. The
// primary signal is the fixed id (suwayomi.LocalSourceID); the lang tag
// (case-insensitive "localsourcelang") is checked too as a defensive
// secondary signal, since it is the more stable identifier — if Suwayomi ever
// changes the id, matching on lang still catches it.
func isLocalSource(src suwayomi.Source) bool {
	return src.ID == suwayomi.LocalSourceID || strings.EqualFold(src.Lang, suwayomi.LocalSourceLang)
}

// isDisabledSource reports whether the owner has disabled src via the
// per-language enable/disable toggle (suwayomi.Source.Disabled, resolved from
// the source's isEnabled meta key — see suwayomi/source_meta.go).
func isDisabledSource(src suwayomi.Source) bool {
	return src.Disabled
}

// isBrokenSource reports whether src is a known-broken source Tsundoku must never
// touch — currently InfinityScans, whose captcha is broken (hitting it wastes
// requests + risks IP-blocks). Matched by NAME (case-insensitive). REMOVE this
// predicate (and its entry in excludedFromPicker) once the source's captcha works
// again.
func isBrokenSource(src suwayomi.Source) bool {
	return strings.EqualFold(src.Name, "InfinityScans")
}

// excludedFromPicker reports whether src must never appear in a Discover/
// Search/Browse source picker: Suwayomi's built-in Local source (F1), a
// source the owner has disabled, or a known-broken source (isBrokenSource).
// Shared by Sources() and resolveSources() so the exclusion rules can never
// drift apart.
func excludedFromPicker(src suwayomi.Source) bool {
	return isLocalSource(src) || isDisabledSource(src) || isBrokenSource(src)
}

// EnabledOnlineSources returns every Suwayomi source eligible for the warm-up
// pass: all installed sources MINUS the built-in Local source and any source the
// owner has disabled — the SAME exclusion rule Search's fan-out applies
// (excludedFromPicker). It is exported so the warm-up job (internal/warmup) warms
// exactly the source set Search hits, without duplicating the exclusion logic
// (§2 DRY). client is the live Suwayomi client (client.Sources already inlines
// each source's meta, so the disabled check costs no extra round trip).
func EnabledOnlineSources(ctx context.Context, client suwayomi.Client) ([]suwayomi.Source, error) {
	all, err := client.Sources(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]suwayomi.Source, 0, len(all))
	for _, src := range all {
		if excludedFromPicker(src) {
			continue
		}
		out = append(out, src)
	}
	return out, nil
}

// searchOneSource performs a single-source search against the Suwayomi client
// and maps the results to Candidates. A nil error and nil slice is returned
// when the source fails — the caller logs the failure and skips the source so
// that partial results from healthy sources still reach the user.
func (s *Service) searchOneSource(ctx context.Context, src suwayomi.Source, query string) ([]Candidate, error) {
	results, err := s.client.Search(ctx, src.ID, query)
	if err != nil {
		return nil, err
	}

	// Map Manga results to Candidates tagged with source metadata.
	out := make([]Candidate, 0, len(results))
	for _, m := range results {
		out = append(out, newCandidate(src, m))
	}
	return out, nil
}

// newCandidate maps one suwayomi.Manga to a Candidate tagged with its source's
// ID/name/lang. Shared by the Search fan-out (searchOneSource) and the
// single-source Browse path so the Manga→Candidate mapping lives in exactly
// one place.
//
// ThumbnailURL is NOT Suwayomi's own thumbnailUrl forwarded verbatim (B2 fix):
// that value is Suwayomi-relative and 404s when rendered against Tsundoku's
// own origin. Instead it is Tsundoku's own cover-proxy path, resolved by
// thumbnailProxyPath.
func newCandidate(src suwayomi.Source, m suwayomi.Manga) Candidate {
	return Candidate{
		Source:       src.ID,
		SourceName:   src.Name,
		Lang:         src.Lang,
		MangaID:      m.ID,
		Title:        m.Title,
		URL:          m.URL,
		ThumbnailURL: thumbnailProxyPath(src.ID, m),
		Author:       strOrEmpty(m.Author),
		Artist:       strOrEmpty(m.Artist),
		Description:  strOrEmpty(m.Description),
		Genres:       nonNilStrings(m.Genre),
	}
}

// thumbnailProxyPath returns the Tsundoku-relative cover-proxy path for m
// within sourceID ("/api/sources/{sourceID}/manga/{mangaId}/cover"), or "" when
// the source provided no thumbnail at all (m.ThumbnailURL nil/empty) — the FE
// renders its initial-letter placeholder in that case. The proxy path always
// targets the SAME Suwayomi REST endpoint (/api/v1/manga/{id}/thumbnail;
// see handler/imports.MangaCover) regardless of what Suwayomi's own
// thumbnailUrl string said — only its presence/absence matters here.
func thumbnailProxyPath(sourceID string, m suwayomi.Manga) string {
	if m.ThumbnailURL == nil || *m.ThumbnailURL == "" {
		return ""
	}
	return fmt.Sprintf("/api/sources/%s/manga/%d/cover", sourceID, m.ID)
}

// strOrEmpty dereferences an optional Suwayomi metadata string, returning ""
// when nil rather than panicking or leaking a pointer into the DTO layer.
func strOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// nonNilStrings returns in unchanged when non-nil, else an empty (non-nil)
// slice — so a candidate's Genres always serialises as "[]", never "null"
// (matches the fleet convention for list-shaped DTO fields).
func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// newSearchCandidateDTO maps a Candidate to its wire DTO. Shared by Search
// (grouped) and Browse (flat) so the Candidate→DTO field copy is never
// duplicated — a dropped field here would surface in both endpoints.
func newSearchCandidateDTO(c Candidate) SearchCandidateDTO {
	return SearchCandidateDTO{
		Source:       c.Source,
		SourceName:   c.SourceName,
		Lang:         c.Lang,
		MangaID:      c.MangaID,
		Title:        c.Title,
		URL:          c.URL,
		ThumbnailURL: c.ThumbnailURL,
		Author:       c.Author,
		Artist:       c.Artist,
		Description:  c.Description,
		Genres:       c.Genres,
	}
}

// Search fans out a query to all sources (or a subset when sourceIDs is
// non-nil), collects Candidates in parallel with bounded concurrency, groups
// them by title similarity, and returns []SearchGroupDTO.
//
// Per-source errors are logged at WARN level and skipped — the caller receives
// partial results from healthy sources with a nil error. This keeps a single
// misbehaving source from blocking the entire search response.
//
// The whole fan-out is additionally bounded by an OVERALL deadline
// (s.searchTimeout). Interactive search fans out to every installed source in
// parallel, and a Cloudflare-protected source can hang for a long time solving
// an anti-bot challenge; without a bound the response can exceed a CDN edge's
// ~100s cut-off (e.g. Cloudflare's 524) and the user sees a gateway error
// instead of any results. When the deadline fires, sources that have not
// answered are simply DROPPED and whatever completed is returned as partial
// results — the exact same partial-results contract the per-source skip above
// already honours. The deadline is never surfaced as an error (a slow source is
// not a failed search).
func (s *Service) Search(ctx context.Context, query string, sourceIDs []string) ([]SearchGroupDTO, error) {
	// Short-TTL memo (Task C1): a repeated identical (query, sources) search
	// within the TTL returns the prior result and does ZERO upstream fan-out — the
	// heaviest anti-bot amplifier. Nil cache (plain NewService) ⇒ always fan out.
	if s.searchCache == nil {
		return s.searchUncached(ctx, query, sourceIDs)
	}
	return s.searchCache.Get(ctx, query, sourceIDs, func() ([]SearchGroupDTO, error) {
		return s.searchUncached(ctx, query, sourceIDs)
	})
}

// searchUncached is the live Search fan-out: it always queries upstream sources.
// Search wraps it with the short-TTL result cache; every doc note on Search's
// deadline / partial-results contract applies here.
func (s *Service) searchUncached(ctx context.Context, query string, sourceIDs []string) ([]SearchGroupDTO, error) {
	// Resolve the source set to query.
	sources, err := s.resolveSources(ctx, sourceIDs)
	if err != nil {
		return nil, err
	}

	// Bound the whole fan-out below the CDN edge timeout so a hung source yields
	// partial results rather than a gateway error.
	sctx, cancel := context.WithTimeout(ctx, s.searchTimeout)
	defer cancel()

	// Fan out per-source searches with bounded concurrency.
	sem := make(chan struct{}, searchConcurrency)
	var mu sync.Mutex
	var candidates []Candidate
	// samples accumulates one timing per source that actually ran (acquired a
	// slot and called the client), success or failure. It is recorded ONCE after
	// the fan-out (see below), not per goroutine, so metrics writes never race
	// the deadline or add latency to the fan-out.
	var samples []metrics.Sample

	g, gctx := errgroup.WithContext(sctx)
	for _, src := range sources {
		g.Go(func() error {
			// Acquire a concurrency slot; on deadline/cancel just drop this
			// source (return nil) so partial results survive — the caller must
			// never see the deadline as an error. A source dropped HERE never
			// ran, so it contributes no timing sample.
			select {
			case sem <- struct{}{}:
			case <-gctx.Done():
				return nil
			}
			defer func() { <-sem }()

			// Measure the whole call. A source that hangs until the deadline
			// returns a context error here with a latency ~= searchTimeout —
			// exactly the slow datapoint that must be recorded (that is why the
			// batch below uses a deadline-detached context, not gctx).
			start := time.Now()
			local, err := s.searchOneSource(gctx, src, query)
			latency := time.Since(start)

			mu.Lock()
			samples = append(samples, metrics.Sample{
				SourceID: src.ID, SourceName: src.Name, Latency: latency, Err: err,
			})
			if err == nil {
				candidates = append(candidates, local...)
			}
			mu.Unlock()

			if err != nil {
				slog.WarnContext(gctx, "imports: source search failed",
					"source", src.ID, "source_name", src.Name, "err", err)
			}
			return nil // per-source failures and the deadline both drop that source
		})
	}

	// Every goroutine returns nil (per-source failures and the overall deadline
	// are both treated as "drop that source"), so g.Wait never surfaces an
	// error — it just joins all goroutines. Once it returns, every mutex-guarded
	// write has happened-before, so candidates and samples are the complete sets
	// as of the deadline and are safe to read.
	_ = g.Wait()

	// Record the batch AFTER the fan-out on a deadline-detached, short-bounded
	// context: a source dropped at the 85s deadline still records its slow
	// latency (sctx is cancelled by now, so recording on it would drop exactly
	// the datapoints that flag a source slow).
	s.recordSamples(ctx, samples)

	// If the PARENT request context was cancelled (client disconnected / navigated
	// away), do NOT return — and therefore do NOT let Search cache — a truncated
	// result. A cancelled fan-out drops out early, so candidates may hold only a
	// few sources (or none); caching that would poison the shared searchCache for
	// the whole TTL and serve the empty/partial result to unrelated later callers.
	// This is deliberately distinct from our OWN searchTimeout firing (that bounds
	// sctx while ctx stays live — the documented partial-results contract, which IS
	// safe to cache): only a cancelled PARENT ctx short-circuits here.
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Group candidates by title similarity using the Task 2 matcher.
	groups := groupCandidates(candidates)

	// Map []Group → []SearchGroupDTO.
	out := make([]SearchGroupDTO, len(groups))
	for i, grp := range groups {
		cdtos := make([]SearchCandidateDTO, len(grp.Candidates))
		for j, c := range grp.Candidates {
			cdtos[j] = newSearchCandidateDTO(c)
		}
		out[i] = SearchGroupDTO{Title: grp.Title, Candidates: cdtos}
	}
	return out, nil
}

// recordSamples writes the per-source search timings collected during a fan-out
// as ONE metrics batch, in the BACKGROUND so a slow metrics write can never add
// latency to the search response (fast search under the CDN cutoff is the whole
// point of the feature). The batch runs on a context derived from the ORIGINAL
// request context with cancellation stripped (context.WithoutCancel) and bounded
// by recordTimeout, so it survives both the search deadline (sctx) and a client
// disconnect yet always terminates. Recording is best-effort and skipped when no
// recorder is wired (nil) or there is nothing to record. Handing `samples` to the
// goroutine is race-free: g.Wait() has returned, so it is no longer mutated.
func (s *Service) recordSamples(ctx context.Context, samples []metrics.Sample) {
	if s.recorder == nil || len(samples) == 0 {
		return
	}
	go func() {
		recCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
		defer cancel()
		s.recorder.RecordBatch(recCtx, samples)
	}()
}

// resolveSources returns the source list to query, always excluding Suwayomi's
// built-in Local source (see isLocalSource — the same F1 exclusion Sources()
// applies) AND any source the owner has disabled (see isDisabledSource,
// mirroring Sources()'s filter) — a disabled source is never fanned out to by
// Search, even when the caller names it explicitly in sourceIDs (the picker
// that supplies sourceIDs is itself built from the filtered Sources() list, so
// this only matters for a stale/hand-crafted request). When sourceIDs is
// non-nil and non-empty, it returns only those sources from the client whose
// IDs are in the set; otherwise it returns all (non-Local, enabled) client
// sources. Naming Local's id explicitly does not resurrect it (N1) — a client
// cannot search it by id any more than by leaving the filter empty; the same
// reasoning applies to a disabled source's id.
func (s *Service) resolveSources(ctx context.Context, sourceIDs []string) ([]suwayomi.Source, error) {
	all, err := s.client.Sources(ctx)
	if err != nil {
		return nil, err
	}

	// nil want ⇒ match every id (the "no filter" case); a non-nil want is an
	// O(1)-lookup allowlist built once, up front.
	want := sourceIDSet(sourceIDs)

	out := make([]suwayomi.Source, 0, len(all))
	for _, src := range all {
		if excludedFromPicker(src) || !want.matches(src.ID) {
			continue
		}
		out = append(out, src)
	}
	return out, nil
}

// sourceIDFilter is an O(1)-lookup allowlist of source IDs; a nil filter
// matches every id (used when the caller passed no explicit sourceIDs).
type sourceIDFilter map[string]bool

// sourceIDSet builds a sourceIDFilter from ids, or nil when ids is empty.
func sourceIDSet(ids []string) sourceIDFilter {
	if len(ids) == 0 {
		return nil
	}
	want := make(sourceIDFilter, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	return want
}

// matches reports whether id passes the filter: true for every id when want
// is nil, else membership in the allowlist.
func (want sourceIDFilter) matches(id string) bool {
	if want == nil {
		return true
	}
	return want[id]
}

// Browse returns one page of a single source's catalog listing (Popular or
// Latest) as a flat BrowseResultDTO. Unlike Search there is no cross-source
// fan-out or grouping — Popular/Latest are per-source listings.
//
// It first resolves sourceID against the live source list (to obtain the
// source's Name/Lang for tagging candidates); an unknown sourceID yields
// ErrSourceNotFound (→ 404). A client.Browse failure is returned verbatim — the
// request is single-source, so a source/upstream failure IS the whole request
// (no partial-results carve-out like Search). page is 1-based.
func (s *Service) Browse(ctx context.Context, sourceID string, t suwayomi.BrowseType, page int) (BrowseResultDTO, error) {
	src, err := s.resolveSource(ctx, sourceID)
	if err != nil {
		return BrowseResultDTO{}, err
	}

	res, err := s.client.Browse(ctx, sourceID, t, page)
	if err != nil {
		return BrowseResultDTO{}, err
	}

	manga := make([]SearchCandidateDTO, len(res.Mangas))
	for i, m := range res.Mangas {
		manga[i] = newSearchCandidateDTO(newCandidate(src, m))
	}
	return BrowseResultDTO{Manga: manga, HasNextPage: res.HasNextPage, Page: page}, nil
}

// resolveSource returns the single source whose ID equals sourceID from the live
// client source list, or ErrSourceNotFound when absent. Browse needs the
// resolved source's Name/Lang to tag its candidates.
func (s *Service) resolveSource(ctx context.Context, sourceID string) (suwayomi.Source, error) {
	all, err := s.client.Sources(ctx)
	if err != nil {
		return suwayomi.Source{}, err
	}
	for _, src := range all {
		if src.ID == sourceID {
			return src, nil
		}
	}
	return suwayomi.Source{}, ErrSourceNotFound
}

// fetchChapters returns the raw, unfiltered chapter list for (sourceID, mangaID)
// through the shared chapter cache (Task C2) when one is wired, else straight
// from the client. It is the single point the read-only discovery paths
// (SourceBreakdown, InspectChapters) fetch chapters, so they share their result
// with each other AND with the adopt-side suwayomi.Ingest (same cache instance)
// — a coverage→configure→adopt session triggers ONE upstream FetchChapters.
func (s *Service) fetchChapters(ctx context.Context, sourceID string, mangaID int) ([]suwayomi.Chapter, error) {
	fetch := func() ([]suwayomi.Chapter, error) {
		return s.client.FetchChapters(ctx, mangaID)
	}
	if s.chapterCache == nil {
		return fetch()
	}
	return s.chapterCache.Get(ctx, sourceID, mangaID, fetch)
}

// InspectChapters fetches the live chapter list for mangaID from sourceID and
// returns a lightweight preview as []ChapterInspectDTO.
//
// NOTE: On a cache MISS this fetches via the FetchChapters Suwayomi mutation —
// which contacts the upstream source and populates Suwayomi's internal chapter
// cache — giving the user an up-to-date chapter count before adopting. Within the
// short chapter-cache TTL (Task C2) a repeat call for the same source-manga reuses
// the memoized list and makes NO upstream request (an anti-ban de-amplification;
// the count is at most a few minutes stale). For already-cached data use
// suwayomi.Client.MangaChapters instead.
func (s *Service) InspectChapters(ctx context.Context, sourceID string, mangaID int) ([]ChapterInspectDTO, error) {
	chapters, err := s.fetchChapters(ctx, sourceID, mangaID)
	if err != nil {
		return nil, err
	}

	out := make([]ChapterInspectDTO, len(chapters))
	for i, ch := range chapters {
		out[i] = ChapterInspectDTO{
			Number:    ch.Number,
			Name:      ch.Name,
			Scanlator: ch.Scanlator,
		}
	}
	return out, nil
}

// SourceBreakdown groups a source-manga's live chapter feed by scanlator so
// the adopt UI can auto-split a source into per-scanlator rows with counts +
// display ranges. A chapter's group key is its own Scanlator when non-empty,
// else the source's own Name (mirrors Kaizoku's "untagged → source name"
// convention — an aggregator source may tag some chapters and leave others
// untagged, and the untagged ones are attributed to the source itself).
//
// Ranges reuses the shared coverage helper (chapterrange.FormatChapterRanges) — the
// run-collapsing walk is never duplicated. Only chapters with a non-nil
// Number contribute to a group's Ranges/Count coverage input; Total counts
// every chapter regardless.
//
// An unknown sourceID yields ErrSourceNotFound (→ 404, mirrors Browse/
// MangaDetails); a client.FetchChapters failure is returned verbatim (the
// caller maps it to a 502, mirroring Details' upstream mapping).
func (s *Service) SourceBreakdown(ctx context.Context, sourceID string, mangaID int) (SourceBreakdownDTO, error) {
	src, err := s.resolveSource(ctx, sourceID)
	if err != nil {
		return SourceBreakdownDTO{}, err
	}

	chapters, err := s.fetchChapters(ctx, sourceID, mangaID)
	if err != nil {
		return SourceBreakdownDTO{}, err
	}

	type group struct {
		numbers []float64
		count   int
	}
	groups := make(map[string]*group)
	order := make([]string, 0)
	for _, ch := range chapters {
		key := ch.Scanlator
		if key == "" {
			key = src.Name
		}
		g, ok := groups[key]
		if !ok {
			g = &group{}
			groups[key] = g
			order = append(order, key)
		}
		g.count++
		if ch.Number != nil {
			g.numbers = append(g.numbers, *ch.Number)
		}
	}

	scanlators := make([]ScanlatorCoverageDTO, 0, len(order))
	for _, key := range order {
		g := groups[key]
		scanlators = append(scanlators, ScanlatorCoverageDTO{
			Scanlator: key,
			Count:     g.count,
			Ranges:    chapterrange.FormatChapterRanges(g.numbers),
		})
	}
	sort.Slice(scanlators, func(i, j int) bool {
		if scanlators[i].Count != scanlators[j].Count {
			return scanlators[i].Count > scanlators[j].Count
		}
		return scanlators[i].Scanlator < scanlators[j].Scanlator
	})

	return SourceBreakdownDTO{Total: len(chapters), Scanlators: scanlators}, nil
}

// MangaDetails FORCES a live details fetch for (sourceID, mangaID) via
// suwayomi.Client.FetchMangaDetails and returns the enriched candidate as a
// SearchCandidateDTO — the SAME shape Search/Browse return, so the caller (the
// Discover hover preview) can merge the response straight into an existing
// candidate. sourceID resolves the source's Name/Lang (reusing resolveSource,
// the same helper Browse uses); an unknown sourceID yields ErrSourceNotFound
// (→ 404). A client.FetchMangaDetails failure is returned verbatim — the
// caller maps it to a 502 (this is a genuine upstream/source fetch, not a
// Tsundoku validation problem).
//
// This is deliberately on-demand, single-manga only: calling it once per
// Search/Browse result would multiply upstream requests by the page size.
func (s *Service) MangaDetails(ctx context.Context, sourceID string, mangaID int) (SearchCandidateDTO, error) {
	src, err := s.resolveSource(ctx, sourceID)
	if err != nil {
		return SearchCandidateDTO{}, err
	}

	m, err := s.client.FetchMangaDetails(ctx, mangaID)
	if err != nil {
		return SearchCandidateDTO{}, err
	}

	return newSearchCandidateDTO(newCandidate(src, m)), nil
}

// Adopt groups one or more (source, manga) candidates under a single canonical
// title and merges them into ONE Series with N importance-ranked providers,
// ingesting all their chapters.
//
// Preconditions (enforced by the HTTP handler, not validated here):
//   - len(req.Providers) >= 1 — the service assumes at least one provider.
//
// Algorithm:
//  1. Validate req.Category early (before any DB writes) so an invalid category
//     surfaces cleanly rather than leaving orphaned rows.
//  2. For each provider p: call ingest.AddSeries with the canonical req.Title so
//     that all providers attach to the SAME Series slug. On error, wrap with the
//     list of sources already attached in this call and return (§16 no-silent-partial).
//  3. Load the Series by slug = disk.Slugify(req.Title).
//  4. For each provider p: find its SeriesProvider by (series_id, provider) and
//     set its Importance.
//  5. If req.Category != "" apply it to the Series (validated in step 1).
//  6. Return the series UUID.
func (s *Service) Adopt(ctx context.Context, req AdoptRequest) (uuid.UUID, error) {
	// 1. Validate category up front to avoid creating rows for an invalid request.
	if err := validateCategory(req.Category); err != nil {
		return uuid.UUID{}, err
	}

	// 2. Ingest each provider under the shared canonical title.
	if err := s.ingestProviders(ctx, req); err != nil {
		return uuid.UUID{}, err
	}

	// 3. Load the Series by slug.
	series, err := s.loadSeriesBySlug(ctx, req.Title)
	if err != nil {
		return uuid.UUID{}, err
	}

	// 4. Set importance on each SeriesProvider.
	if err := s.setImportances(ctx, series.ID, req.Providers); err != nil {
		return uuid.UUID{}, err
	}

	// 5. Apply category when requested. ingest.AddSeries already linked the new
	//    series to the configured default category (is_default) on create, so an
	//    empty req.Category keeps that default; a named category is find-or-created
	//    (a brand-new owner category lands here) and linked by id.
	if req.Category != "" {
		cat, err := category.FindOrCreate(ctx, s.db, req.Category)
		if err != nil {
			return uuid.UUID{}, fmt.Errorf("imports.Adopt: resolve category %q for series %s: %w", req.Category, series.ID, err)
		}
		if err := s.db.Series.UpdateOneID(series.ID).SetCategoryID(cat.ID).Exec(ctx); err != nil {
			return uuid.UUID{}, fmt.Errorf("imports.Adopt: set category %q for series %s: %w", req.Category, series.ID, err)
		}
	}

	return series.ID, nil
}

// validateCategory returns nil when cat is empty (meaning "keep the configured
// default") or when it is a filesystem-safe category name (it becomes a folder).
// A non-empty invalid value yields a wrapped error naming the invalid string.
func validateCategory(cat string) error {
	if cat == "" {
		return nil
	}
	if _, err := category.ValidateName(cat); err != nil {
		return fmt.Errorf("imports.Adopt: invalid category %q: %w", cat, err)
	}
	return nil
}

// ingestProviders calls ingest.AddSeries for every provider in req, all under
// req.Title so they attach to the same slug-derived Series. On the first error
// it returns a wrapped message that names every source successfully attached
// before the failure (§16 no-silent-partial). No rollback is performed.
func (s *Service) ingestProviders(ctx context.Context, req AdoptRequest) error {
	attached := make([]string, 0, len(req.Providers))
	for _, p := range req.Providers {
		// p.Scanlator selects which scanlation group's chapters this provider
		// tracks; "" means "all chapters from this source" (see
		// suwayomi.Ingest.AddSeries).
		if _, err := s.ingest.AddSeries(ctx, p.Source, p.MangaID, req.Title, p.Scanlator); err != nil {
			if len(attached) > 0 {
				return fmt.Errorf(
					"imports.Adopt: provider %q failed (providers already attached: %s): %w",
					p.Source, strings.Join(attached, ", "), err,
				)
			}
			return fmt.Errorf("imports.Adopt: provider %q failed: %w", p.Source, err)
		}
		attached = append(attached, p.Source)
	}
	return nil
}

// loadSeriesBySlug looks up the Series whose slug matches disk.Slugify(title).
// This is guaranteed to succeed after a successful ingestProviders call because
// AddSeries creates the Series row; a failure here means a DB-level problem.
func (s *Service) loadSeriesBySlug(ctx context.Context, title string) (*ent.Series, error) {
	slug := disk.Slugify(title)
	series, err := s.db.Series.Query().
		Where(entseries.Slug(slug)).
		Only(ctx)
	if err != nil {
		// Defensive path: reachable only on a DB connection loss after ingest
		// already created the row (slug is guaranteed to exist at this point).
		return nil, fmt.Errorf("imports.Adopt: load series by slug %q: %w", slug, err)
	}
	return series, nil
}

// setImportances updates the Importance field on each SeriesProvider identified
// by (seriesID, provider, scanlator) — a SeriesProvider row's identity is the
// full triple (see suwayomi.Ingest.upsertSeriesProvider), so matching on
// provider alone would be WRONG once two scanlator rows share the same
// provider name: e.g. adopting the same source under two scanlators with
// different importances would otherwise both resolve to whichever row
// First(ctx) happens to return, silently clobbering one of them. The provider
// row is guaranteed to exist because ingestProviders just created or
// confirmed it; a failure here is a DB problem.
func (s *Service) setImportances(ctx context.Context, seriesID uuid.UUID, providers []AdoptProvider) error {
	for _, p := range providers {
		sp, err := s.db.SeriesProvider.Query().
			Where(
				entseriesprovider.SeriesID(seriesID),
				entseriesprovider.Provider(p.Source),
				entseriesprovider.Scanlator(p.Scanlator),
			).
			First(ctx)
		if err != nil {
			// Defensive path: reachable only on DB connection loss — the row must
			// exist because ingestProviders just created or confirmed it.
			return fmt.Errorf("imports.Adopt: find provider %q for series %s: %w", p.Source, seriesID, err)
		}
		if err := s.db.SeriesProvider.UpdateOne(sp).SetImportance(p.Importance).Exec(ctx); err != nil {
			// Defensive path: reachable only on DB connection loss mid-operation.
			return fmt.Errorf("imports.Adopt: set importance for provider %q: %w", p.Source, err)
		}
	}
	return nil
}
