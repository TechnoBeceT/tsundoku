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
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/technobecet/tsundoku/internal/ent"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	entsuwayomisyncstate "github.com/technobecet/tsundoku/internal/ent/suwayomisyncstate"
	"github.com/technobecet/tsundoku/internal/sourcegate"
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
	gate        *sourcegate.Service
}

// NewService constructs a Service. concurrency supplies the runtime-tunable
// parallel-refetch bound, read at the start of every sweep (hot reload). gate
// is the source-politeness circuit-breaker + delay (internal/sourcegate),
// consulted per provider before re-fetching it — see RefreshAll. gate may be
// nil (no gate configured): every gate-consulting call site treats a nil gate
// as "always available, no delay" (today's pre-politeness behaviour), so
// passing nil is a safe default for callers that do not need the gate.
func NewService(client *ent.Client, ingest *suwayomi.Ingest, hub *sse.Hub, concurrency Concurrency, gate *sourcegate.Service) *Service {
	return &Service{client: client, ingest: ingest, hub: hub, concurrency: concurrency, gate: gate}
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

	now := time.Now()
	items := s.buildRefreshItems(ctx, seriesList, now)

	var mu sync.Mutex
	result := RefreshResult{SeriesRefreshed: len(seriesList)}

	g, gctx := errgroup.WithContext(ctx)
	// Read the parallel-refetch bound at use-time so a settings change applies to
	// this sweep (clamped >= 1 — a 0 limit would deadlock errgroup).
	g.SetLimit(s.refreshLimit(ctx))
	for _, it := range items {
		g.Go(func() error {
			// Politeness delay before the fetch — the runtime-tunable minimum gap
			// between successive requests to this physical source.
			s.gateWait(gctx, it.sourceKey)
			res, addErr := s.ingest.AddSeries(gctx, it.provider, it.mangaID, it.title, it.scanlator)

			// Persist polling health; upsertSyncState skips on ctx-cancel.
			if uerr := s.upsertSyncState(gctx, it.providerID, addErr); uerr != nil {
				slog.ErrorContext(gctx, "refresh: persist sync state failed",
					"series", it.title, "provider", it.provider, "err", uerr)
			}

			mu.Lock()
			defer mu.Unlock()
			if addErr != nil {
				// Context cancellation (shutdown/timeout) is not a provider error —
				// skip counting, logging, AND gate bookkeeping to avoid false error
				// inflation (or a false trip) on clean exit.
				if errors.Is(addErr, context.Canceled) || errors.Is(addErr, context.DeadlineExceeded) {
					return nil
				}
				// Partial success: log + count, never abort the sweep.
				slog.ErrorContext(gctx, "refresh: provider re-fetch failed",
					"series", it.title, "provider", it.provider, "err", addErr)
				result.Errors++
				s.gateRecordFailure(gctx, it.sourceKey, addErr, now)
				return nil
			}
			result.ProvidersRefreshed++
			result.NewChapters += res.NewChapters
			s.gateRecordSuccess(gctx, it.sourceKey)
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

// refreshItem is one (series, provider) pair queued for re-fetch by a sweep.
type refreshItem struct {
	title      string
	provider   string
	mangaID    int
	providerID uuid.UUID
	sourceKey  string
	// scanlator is the STORED scanlator of this SeriesProvider row (set at
	// create time — see suwayomi.Ingest.upsertSeriesProvider). It MUST be
	// passed back into AddSeries so a re-fetch updates this SAME row instead
	// of find-or-creating a fresh scanlator=="" one: AddSeries keys
	// SeriesProvider on (series, provider, scanlator), and a mismatched
	// scanlator here would silently split one provider into two.
	scanlator string
}

// buildRefreshItems flattens every monitored series' providers into a work
// list, skipping a provider whose suwayomi_id is unknown (0 — cannot fetch)
// OR whose physical source is currently cooled down by the source-politeness
// gate (a tripped source is excluded from the sweep entirely this cycle,
// mirroring the download dispatcher's candidacy exclusion). Extracted from
// RefreshAll to keep its cyclomatic complexity low.
func (s *Service) buildRefreshItems(ctx context.Context, seriesList []*ent.Series, now time.Time) []refreshItem {
	var items []refreshItem
	for _, sr := range seriesList {
		for _, p := range sr.Edges.Providers {
			if p.SuwayomiID == 0 {
				slog.WarnContext(ctx, "refresh: skipping provider with unknown suwayomi_id",
					"series", sr.Title, "provider", p.Provider)
				continue
			}
			key := sourceKey(p)
			if !s.gateAvailable(ctx, key, now) {
				slog.WarnContext(ctx, "refresh: skipping provider — source cooled down by politeness gate",
					"series", sr.Title, "provider", p.Provider, "source_key", key)
				continue
			}
			items = append(items, refreshItem{title: sr.Title, provider: p.Provider, mangaID: p.SuwayomiID, providerID: p.ID, sourceKey: key, scanlator: p.Scanlator})
		}
	}
	return items
}

// sourceKey returns the physical-source identity used to key the
// source-politeness gate for a SeriesProvider: its display name
// (provider_name) when known, else its raw provider id, trimmed. It mirrors
// download.canonicalSourceKey — kept as a small local copy rather than a
// cross-package import so refresh does not need to know about the download
// engine's internals for this one shared concept.
func sourceKey(sp *ent.SeriesProvider) string {
	name := sp.ProviderName
	if name == "" {
		name = sp.Provider
	}
	return strings.TrimSpace(name)
}

// gateAvailable reports whether sourceKey's circuit-breaker currently permits
// access. A nil gate (no gate configured) is always available.
func (s *Service) gateAvailable(ctx context.Context, sourceKey string, now time.Time) bool {
	if s.gate == nil {
		return true
	}
	return s.gate.IsAvailable(ctx, sourceKey, now)
}

// gateWait enforces the politeness delay for sourceKey before a fetch. A nil
// gate is a no-op.
func (s *Service) gateWait(ctx context.Context, sourceKey string) {
	if s.gate == nil {
		return
	}
	s.gate.Wait(ctx, sourceKey)
}

// gateRecordSuccess reports a successful re-fetch from sourceKey to the
// breaker. A nil gate is a no-op.
func (s *Service) gateRecordSuccess(ctx context.Context, sourceKey string) {
	if s.gate == nil {
		return
	}
	s.gate.RecordSuccess(ctx, sourceKey)
}

// gateRecordFailure reports a failed re-fetch from sourceKey to the breaker. A
// nil gate is a no-op.
func (s *Service) gateRecordFailure(ctx context.Context, sourceKey string, cause error, now time.Time) {
	if s.gate == nil {
		return
	}
	s.gate.RecordFailure(ctx, sourceKey, cause, now)
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
