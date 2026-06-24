// Package refresh implements the M5 discovery sweep: the recurring poll that
// re-fetches every monitored series' chapter list across all its providers to
// discover new releases. It is pure orchestration over the M2 ingest engine —
// it invents no new data mapping.
//
// The sweep is UPSERT-ONLY (it reuses suwayomi.Ingest.AddSeries) so it honors
// the never-auto-delete invariant: a chapter that disappears from a source
// listing on a later poll leaves its ProviderChapter row (and any rendered CBZ)
// untouched. Re-fetch never resets SeriesProvider.importance — only the create
// path sets it — so an owner re-rank survives every subsequent sweep.
package refresh

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/technobecet/tsundoku/internal/ent"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	"github.com/technobecet/tsundoku/internal/sse"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// Service runs the discovery sweep. Create one with NewService and call
// RefreshAll on a schedule (job.Runner.StartRefresh) or on demand.
type Service struct {
	client      *ent.Client
	ingest      *suwayomi.Ingest
	hub         *sse.Hub
	concurrency int
}

// NewService constructs a Service. concurrency bounds parallel provider
// re-fetches (each is a live upstream call); values < 1 are clamped to 1.
func NewService(client *ent.Client, ingest *suwayomi.Ingest, hub *sse.Hub, concurrency int) *Service {
	if concurrency < 1 {
		concurrency = 1
	}
	return &Service{client: client, ingest: ingest, hub: hub, concurrency: concurrency}
}

// RefreshResult summarises one sweep. SeriesRefreshed counts the monitored
// series considered; ProvidersRefreshed counts providers successfully
// re-fetched; NewChapters sums genuinely-new Chapter rows created; Errors counts
// providers whose re-fetch failed (and were skipped — the sweep continues).
type RefreshResult struct {
	SeriesRefreshed    int
	ProvidersRefreshed int
	NewChapters        int
	Errors             int
}

// RefreshAll sweeps every monitored series. For each of its providers (with a
// known suwayomi_id) it re-runs suwayomi.Ingest.AddSeries under bounded
// concurrency, discovering new chapters. Per-provider failures are logged and
// skipped (partial success). A hard error is returned only if the initial
// monitored-series query fails. Emits refresh.start before and refresh.done
// after the sweep.
func (s *Service) RefreshAll(ctx context.Context) (RefreshResult, error) {
	seriesList, err := s.client.Series.Query().
		Where(entseries.Monitored(true)).
		WithProviders().
		All(ctx)
	if err != nil {
		return RefreshResult{}, fmt.Errorf("refresh.RefreshAll: query monitored series: %w", err)
	}

	s.broadcast("refresh.start", RefreshEvent{Monitored: len(seriesList)})

	// Build a flat work list of (series title, provider, manga id) tuples,
	// skipping providers whose suwayomi_id is unknown (0 — cannot fetch).
	type item struct {
		title    string
		provider string
		mangaID  int
	}
	var items []item
	for _, sr := range seriesList {
		for _, p := range sr.Edges.Providers {
			if p.SuwayomiID == 0 {
				slog.WarnContext(ctx, "refresh: skipping provider with unknown suwayomi_id",
					"series", sr.Title, "provider", p.Provider)
				continue
			}
			items = append(items, item{title: sr.Title, provider: p.Provider, mangaID: p.SuwayomiID})
		}
	}

	var mu sync.Mutex
	result := RefreshResult{SeriesRefreshed: len(seriesList)}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(s.concurrency)
	for _, it := range items {
		it := it
		g.Go(func() error {
			res, addErr := s.ingest.AddSeries(gctx, it.provider, it.mangaID, it.title)
			mu.Lock()
			defer mu.Unlock()
			if addErr != nil {
				// Partial success: log + count, never abort the sweep.
				slog.ErrorContext(gctx, "refresh: provider re-fetch failed",
					"series", it.title, "provider", it.provider, "err", addErr)
				result.Errors++
				return nil
			}
			result.ProvidersRefreshed++
			result.NewChapters += res.NewChapters
			return nil
		})
	}
	// Goroutines never return non-nil, so Wait never errors; the only way gctx
	// cancels is parent-ctx cancellation, which surfaces as per-item AddSeries
	// errors counted above.
	_ = g.Wait()

	s.broadcast("refresh.done", RefreshEvent{
		Monitored:          len(seriesList),
		SeriesRefreshed:    result.SeriesRefreshed,
		ProvidersRefreshed: result.ProvidersRefreshed,
		NewChapters:        result.NewChapters,
		Errors:             result.Errors,
	})
	return result, nil
}
