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
	"log/slog"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// searchConcurrency is the maximum number of sources queried in parallel during
// Search. This bounds upstream load when many sources are installed.
const searchConcurrency = 8

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
		thumb := ""
		if m.ThumbnailURL != nil {
			thumb = *m.ThumbnailURL
		}
		out = append(out, Candidate{
			Source:       src.ID,
			SourceName:   src.Name,
			Lang:         src.Lang,
			MangaID:      m.ID,
			Title:        m.Title,
			ThumbnailURL: thumb,
		})
	}
	return out, nil
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
			cdtos[j] = SearchCandidateDTO{
				Source:       c.Source,
				SourceName:   c.SourceName,
				Lang:         c.Lang,
				MangaID:      c.MangaID,
				Title:        c.Title,
				ThumbnailURL: c.ThumbnailURL,
			}
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
