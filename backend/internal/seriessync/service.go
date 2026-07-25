// Package seriessync is the per-series INSTANT convergence layer (GAP-113): the
// glue that makes a single mutation which changes a series' download/upgrade
// candidate set — an adopt, a provider add/change/remove, a source re-rank — take
// effect for THAT ONE series right away, instead of waiting for the 2h whole-library
// refresh + detection sweep (job.Runner.StartRefresh).
//
// It owns no data-model logic of its own: it orchestrates the two scoped domain
// primitives — refresh.Service.RefreshSeries (re-fetch one series' feeds,
// upsert-only) and download.Dispatcher.DetectUpgradesForSeries (flag that series'
// freshly-upgradable chapters) — then Triggers a download cycle so the next pass
// drains anything new. The whole-library cadence is untouched; this is purely an
// ADDITIONAL instant layer on top of it.
//
// Every run is ASYNC and SINGLE-FLIGHT per series (the same shape as
// library.StartMatchDiskProvider): a mutation returns fast while the scoped work
// runs on a detached, disconnect-proof context, and a second trigger for a series
// already in flight is dropped (coalesced) — the in-flight run reflects current DB
// state. A change committed after that run began is picked up by the next mutation
// or, at the latest, the whole-library sweep.
package seriessync

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/refresh"
)

// seriesSyncTimeout bounds the detached background run a Sync/Detect kicks off. A
// single series' refresh fetches only its handful of provider feeds (far less than
// the whole-library sweep), but a slow Cloudflare source can still take tens of
// seconds per feed, so 10m gives comfortable headroom while guaranteeing the
// goroutine + its context can never leak. A var (not a const) so a test can shrink it.
var seriesSyncTimeout = 10 * time.Minute

// syncBlock, when non-nil, makes the background goroutine WAIT before running the
// real refresh/detect — a test-only seam (mirrors library.matchBlock) so the
// single-flight-guard test can hold the first run in flight deterministically while
// it fires a second. Nil in production: no wait.
var syncBlock chan struct{}

// Refresher re-syncs a single series' provider feeds, upsert-only. *refresh.Service
// satisfies it. Kept as an interface so the orchestrator is unit-testable with a fake.
type Refresher interface {
	RefreshSeries(ctx context.Context, seriesID uuid.UUID) (refresh.RefreshResult, error)
}

// Detector flags a single series' downloaded chapters that now have a strictly
// better source, and supplies the runtime max-retries the detection reads.
// *download.Dispatcher satisfies it (structural — no download import here).
type Detector interface {
	DetectUpgradesForSeries(ctx context.Context, seriesID uuid.UUID, maxRetries int) (int, error)
	MaxRetries(ctx context.Context) int
}

// Orchestrator runs the scoped refresh/detect for one series asynchronously, one
// run at a time per series. Construct it with NewOrchestrator and call SyncSeries /
// DetectSeries from a mutation site.
type Orchestrator struct {
	refresh  Refresher
	detector Detector
	trigger  func()

	// mu guards running, the per-series in-flight set. A series present in the map
	// has a scoped run in flight; a concurrent Sync/Detect for it is dropped.
	mu      sync.Mutex
	running map[uuid.UUID]struct{}
}

// NewOrchestrator builds an Orchestrator over the scoped refresh + detection
// primitives and the download-cycle trigger. trigger may be nil (the run still
// refreshes + detects; it just skips the immediate cycle nudge) — production wires
// runner.Trigger.
func NewOrchestrator(refresher Refresher, detector Detector, trigger func()) *Orchestrator {
	return &Orchestrator{
		refresh:  refresher,
		detector: detector,
		trigger:  trigger,
		running:  make(map[uuid.UUID]struct{}),
	}
}

// SyncSeries runs the FULL scoped convergence for seriesID in the background:
// RefreshSeries (re-fetch its feeds) THEN DetectUpgradesForSeries THEN a download
// trigger. Use it for a mutation that may bring NEW chapters or change the feed set
// — adopt, provider add/change/remove. Returns immediately; single-flight per series.
func (o *Orchestrator) SyncSeries(ctx context.Context, seriesID uuid.UUID) {
	o.dispatch(ctx, seriesID, true)
}

// DetectSeries runs ONLY scoped detection for seriesID in the background:
// DetectUpgradesForSeries THEN a download trigger — no feed re-fetch. Use it when
// the feeds are already current and only the WINNING source changed, i.e. a source
// re-rank / importance edit. Returns immediately; single-flight per series (shares
// the same latch as SyncSeries, so a re-rank never races a concurrent add's refresh).
func (o *Orchestrator) DetectSeries(ctx context.Context, seriesID uuid.UUID) {
	o.dispatch(ctx, seriesID, false)
}

// dispatch is the shared async, single-flight, disconnect-proof launcher. It
// detaches from the request context (context.WithoutCancel + a hard timeout, so a
// client disconnect the moment the handler returns can never cancel the scoped work)
// exactly like library.StartMatchDiskProvider.
func (o *Orchestrator) dispatch(ctx context.Context, seriesID uuid.UUID, withRefresh bool) {
	if !o.acquire(seriesID) {
		return
	}

	// Derive the detached context in the CALLER's goroutine (before the request ctx
	// dies) so the run always has a live context.
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), seriesSyncTimeout)

	// Snapshot the test-only block seam in the caller's goroutine (fully sequenced
	// with the caller — mirrors library.StartMatchDiskProvider).
	block := syncBlock

	go func() {
		defer cancel()
		defer o.release(seriesID)

		if block != nil {
			select {
			case <-block:
			case <-runCtx.Done():
			}
		}

		o.run(runCtx, seriesID, withRefresh)
	}()
}

// run performs the scoped work: an optional feed re-fetch, then detection, then a
// download trigger. A refresh error is logged and detection proceeds anyway (the
// feeds in the DB are still valid to detect over); a detection error is logged. The
// trigger fires regardless so a refresh that created new WANTED chapters still
// downloads promptly — coalescing makes an extra trigger free (mirrors
// job.Runner.runRefreshSweep: detect, then Trigger, errors swallowed).
func (o *Orchestrator) run(ctx context.Context, seriesID uuid.UUID, withRefresh bool) {
	if withRefresh {
		if _, err := o.refresh.RefreshSeries(ctx, seriesID); err != nil {
			slog.WarnContext(ctx, "seriessync: scoped refresh failed — proceeding to detection", "series_id", seriesID, "err", err)
		}
	}

	if _, err := o.detector.DetectUpgradesForSeries(ctx, seriesID, o.detector.MaxRetries(ctx)); err != nil {
		slog.WarnContext(ctx, "seriessync: scoped upgrade detection failed", "series_id", seriesID, "err", err)
	}

	if o.trigger != nil {
		o.trigger()
	}
}

// acquire claims the per-series single-flight latch, returning false when a run is
// already in flight for seriesID (the caller then drops — the in-flight run covers
// it). Different series run concurrently.
func (o *Orchestrator) acquire(seriesID uuid.UUID) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, running := o.running[seriesID]; running {
		return false
	}
	o.running[seriesID] = struct{}{}
	return true
}

// release frees the per-series latch (called from the background goroutine's defer).
func (o *Orchestrator) release(seriesID uuid.UUID) {
	o.mu.Lock()
	delete(o.running, seriesID)
	o.mu.Unlock()
}
