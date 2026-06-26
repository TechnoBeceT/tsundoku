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
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/technobecet/tsundoku/internal/ent"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	entsuwayomisyncstate "github.com/technobecet/tsundoku/internal/ent/suwayomisyncstate"
	"github.com/technobecet/tsundoku/internal/sse"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// Concurrency supplies the runtime-tunable parallel-refetch bound. RefreshAll
// reads it at the START of each sweep so an owner's change via the settings API
// applies to the next sweep without a restart. *settings.Service and
// settings.Static both satisfy it.
type Concurrency interface {
	// RefreshConcurrency is the maximum number of provider re-fetches that may run
	// in parallel in one sweep.
	RefreshConcurrency(ctx context.Context) int
}

// Service runs the discovery sweep. Create one with NewService and call
// RefreshAll on a schedule (job.Runner.StartRefresh) or on demand.
type Service struct {
	client      *ent.Client
	ingest      *suwayomi.Ingest
	hub         *sse.Hub
	concurrency Concurrency
}

// NewService constructs a Service. concurrency supplies the runtime-tunable
// parallel-refetch bound, read at the start of every sweep (hot reload).
func NewService(client *ent.Client, ingest *suwayomi.Ingest, hub *sse.Hub, concurrency Concurrency) *Service {
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
		// Skip completed series: a finished series has no new chapters, so polling
		// it is wasted work (and would freeze its sync state into false staleness).
		Where(entseries.Monitored(true), entseries.Completed(false)).
		WithProviders().
		All(ctx)
	if err != nil {
		return RefreshResult{}, fmt.Errorf("refresh.RefreshAll: query monitored series: %w", err)
	}

	s.broadcast("refresh.start", RefreshEvent{Monitored: len(seriesList)})

	// Build a flat work list of (series title, provider, manga id, provider id)
	// tuples, skipping providers whose suwayomi_id is unknown (0 — cannot fetch).
	type item struct {
		title      string
		provider   string
		mangaID    int
		providerID uuid.UUID
	}
	var items []item
	for _, sr := range seriesList {
		for _, p := range sr.Edges.Providers {
			if p.SuwayomiID == 0 {
				slog.WarnContext(ctx, "refresh: skipping provider with unknown suwayomi_id",
					"series", sr.Title, "provider", p.Provider)
				continue
			}
			items = append(items, item{title: sr.Title, provider: p.Provider, mangaID: p.SuwayomiID, providerID: p.ID})
		}
	}

	var mu sync.Mutex
	result := RefreshResult{SeriesRefreshed: len(seriesList)}

	g, gctx := errgroup.WithContext(ctx)
	// Read the parallel-refetch bound at use-time so a settings change applies to
	// this sweep (clamped >= 1 — a 0 limit would deadlock errgroup).
	g.SetLimit(s.refreshLimit(ctx))
	for _, it := range items {
		g.Go(func() error {
			res, addErr := s.ingest.AddSeries(gctx, it.provider, it.mangaID, it.title)

			// Persist polling health; upsertSyncState skips on ctx-cancel.
			if uerr := s.upsertSyncState(gctx, it.providerID, addErr); uerr != nil {
				slog.ErrorContext(gctx, "refresh: persist sync state failed",
					"series", it.title, "provider", it.provider, "err", uerr)
			}

			mu.Lock()
			defer mu.Unlock()
			if addErr != nil {
				// Context cancellation (shutdown/timeout) is not a provider error —
				// skip counting and logging to avoid false error inflation on clean exit.
				if errors.Is(addErr, context.Canceled) || errors.Is(addErr, context.DeadlineExceeded) {
					return nil
				}
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
	// Goroutines never return non-nil, so Wait never errors; parent-ctx
	// cancellation surfaces as context.Canceled in AddSeries and is skipped.
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

// refreshLimit resolves the runtime-tunable parallel-refetch bound at use-time,
// clamped to >= 1 (a 0 limit would deadlock the errgroup).
func (s *Service) refreshLimit(ctx context.Context) int {
	if limit := s.concurrency.RefreshConcurrency(ctx); limit >= 1 {
		return limit
	}
	return 1
}

// upsertSyncState records the outcome of refreshing one provider into its
// SuwayomiSyncState row, creating the row the first time. A nil syncErr means
// success (stamp last_synced_at, clear last_error); a non-nil syncErr records
// last_error. Context cancellation / deadline exceeded is silently skipped
// (clean shutdown, not a bookkeeping event). It never deletes anything.
func (s *Service) upsertSyncState(ctx context.Context, providerID uuid.UUID, syncErr error) error {
	// Skip on clean cancellation — this is shutdown, not a real fetch failure.
	if errors.Is(syncErr, context.Canceled) || errors.Is(syncErr, context.DeadlineExceeded) {
		return nil
	}
	now := time.Now().UTC()
	existing, err := s.client.SuwayomiSyncState.Query().
		Where(entsuwayomisyncstate.SeriesProviderID(providerID)).
		Only(ctx)
	if ent.IsNotFound(err) {
		c := s.client.SuwayomiSyncState.Create().SetSeriesProviderID(providerID)
		if syncErr == nil {
			c = c.SetLastSyncedAt(now)
		} else {
			c = c.SetLastError(syncErr.Error())
		}
		return c.Exec(ctx)
	}
	if err != nil {
		return fmt.Errorf("refresh.upsertSyncState: query %s: %w", providerID, err)
	}
	u := s.client.SuwayomiSyncState.UpdateOneID(existing.ID)
	if syncErr == nil {
		u = u.SetLastSyncedAt(now).SetLastError("")
	} else {
		u = u.SetLastError(syncErr.Error())
	}
	return u.Exec(ctx)
}
