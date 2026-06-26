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
	"strings"
	"sync"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// searchConcurrency is the maximum number of sources queried in parallel during
// Search. This bounds upstream load when many sources are installed.
const searchConcurrency = 8

// ErrSourceNotFound is returned by Browse when the requested sourceID is not in
// the live source list (client.Sources). The HTTP handler maps it to 404.
var ErrSourceNotFound = errors.New("source not found")

// Service provides the import workflow over a Suwayomi backend: source
// discovery, multi-source search with fuzzy grouping, and chapter inspection.
//
// Fields ingest, db, and storage are unused in Task 3 but declared here so
// Task 4 can extend this struct without changing the constructor signature.
type Service struct {
	client  suwayomi.Client
	ingest  *suwayomi.Ingest
	db      *ent.Client
	storage string
}

// NewService constructs a Service backed by the given Suwayomi client.
// ingest, db, and storage are reserved for Task 4 (adopt workflow) and may be
// nil/empty for Task 3 callers.
func NewService(client suwayomi.Client, ingest *suwayomi.Ingest, db *ent.Client, storage string) *Service {
	return &Service{
		client:  client,
		ingest:  ingest,
		db:      db,
		storage: storage,
	}
}

// Sources returns all Suwayomi sources as SourceDTOs.
func (s *Service) Sources(ctx context.Context) ([]SourceDTO, error) {
	srcs, err := s.client.Sources(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SourceDTO, len(srcs))
	for i, src := range srcs {
		out[i] = SourceDTO{ID: src.ID, Name: src.Name, Lang: src.Lang}
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
// ID/name/lang. A nil ThumbnailURL becomes the empty string. Shared by the
// Search fan-out (searchOneSource) and the single-source Browse path so the
// Manga→Candidate mapping lives in exactly one place.
func newCandidate(src suwayomi.Source, m suwayomi.Manga) Candidate {
	thumb := ""
	if m.ThumbnailURL != nil {
		thumb = *m.ThumbnailURL
	}
	return Candidate{
		Source:       src.ID,
		SourceName:   src.Name,
		Lang:         src.Lang,
		MangaID:      m.ID,
		Title:        m.Title,
		URL:          m.URL,
		ThumbnailURL: thumb,
	}
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
	}
}

// Search fans out a query to all sources (or a subset when sourceIDs is
// non-nil), collects Candidates in parallel with bounded concurrency, groups
// them by title similarity, and returns []SearchGroupDTO.
//
// Per-source errors are logged at WARN level and skipped — the caller receives
// partial results from healthy sources with a nil error. This keeps a single
// misbehaving source from blocking the entire search response.
func (s *Service) Search(ctx context.Context, query string, sourceIDs []string) ([]SearchGroupDTO, error) {
	// Resolve the source set to query.
	sources, err := s.resolveSources(ctx, sourceIDs)
	if err != nil {
		return nil, err
	}

	// Fan out per-source searches with bounded concurrency.
	sem := make(chan struct{}, searchConcurrency)
	var mu sync.Mutex
	var candidates []Candidate

	g, gctx := errgroup.WithContext(ctx)
	for _, src := range sources {
		g.Go(func() error {
			// Acquire a concurrency slot; respect context cancellation so that
			// in-flight goroutines don't block indefinitely when the caller
			// cancels while all slots are taken.
			select {
			case sem <- struct{}{}:
			case <-gctx.Done():
				return gctx.Err()
			}
			defer func() { <-sem }()

			local, err := s.searchOneSource(gctx, src, query)
			if err != nil {
				slog.WarnContext(gctx, "imports: source search failed",
					"source", src.ID, "source_name", src.Name, "err", err)
				return nil // skip failing source; partial results are acceptable
			}

			mu.Lock()
			candidates = append(candidates, local...)
			mu.Unlock()
			return nil
		})
	}

	// Wait for all goroutines. Per-source fetch errors are logged and skipped
	// (goroutine returns nil), so partial results reach the caller. Context
	// cancellation or deadline expiry is returned via the semaphore acquire.
	if err := g.Wait(); err != nil {
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

// resolveSources returns the source list to query. When sourceIDs is non-nil
// and non-empty, it returns only those sources from the client whose IDs are in
// the set; otherwise it returns all client sources.
func (s *Service) resolveSources(ctx context.Context, sourceIDs []string) ([]suwayomi.Source, error) {
	all, err := s.client.Sources(ctx)
	if err != nil {
		return nil, err
	}

	if len(sourceIDs) == 0 {
		return all, nil
	}

	// Build a set for O(1) lookup.
	want := make(map[string]bool, len(sourceIDs))
	for _, id := range sourceIDs {
		want[id] = true
	}

	filtered := make([]suwayomi.Source, 0, len(sourceIDs))
	for _, src := range all {
		if want[src.ID] {
			filtered = append(filtered, src)
		}
	}
	return filtered, nil
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

// InspectChapters fetches the live chapter list for mangaID from sourceID and
// returns a lightweight preview as []ChapterInspectDTO.
//
// NOTE: This calls FetchChapters, which is a Suwayomi mutation — it contacts
// the upstream source and populates Suwayomi's internal chapter cache. This is
// intentional: it gives the user an up-to-date chapter count before adopting.
// For already-cached data use suwayomi.Client.MangaChapters instead.
func (s *Service) InspectChapters(ctx context.Context, _ string, mangaID int) ([]ChapterInspectDTO, error) {
	chapters, err := s.client.FetchChapters(ctx, mangaID)
	if err != nil {
		return nil, err
	}

	out := make([]ChapterInspectDTO, len(chapters))
	for i, ch := range chapters {
		out[i] = ChapterInspectDTO{
			Number: ch.Number,
			Name:   ch.Name,
		}
	}
	return out, nil
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

	// 5. Apply category when requested.
	if req.Category != "" {
		cat := entseries.Category(req.Category)
		if err := s.db.Series.UpdateOneID(series.ID).SetCategory(cat).Exec(ctx); err != nil {
			return uuid.UUID{}, fmt.Errorf("imports.Adopt: set category %q for series %s: %w", req.Category, series.ID, err)
		}
	}

	return series.ID, nil
}

// validateCategory returns nil when cat is empty (meaning "keep default") or
// when it is one of the legal entseries.Category enum values. A non-empty
// invalid value yields a wrapped error naming the invalid string.
func validateCategory(cat string) error {
	if cat == "" {
		return nil
	}
	if err := entseries.CategoryValidator(entseries.Category(cat)); err != nil {
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
		if _, err := s.ingest.AddSeries(ctx, p.Source, p.MangaID, req.Title); err != nil {
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
// by (seriesID, provider). The provider row is guaranteed to exist because
// ingestProviders just created or confirmed it; a failure here is a DB problem.
func (s *Service) setImportances(ctx context.Context, seriesID uuid.UUID, providers []AdoptProvider) error {
	for _, p := range providers {
		sp, err := s.db.SeriesProvider.Query().
			Where(
				entseriesprovider.SeriesID(seriesID),
				entseriesprovider.Provider(p.Source),
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
