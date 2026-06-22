// Package job provides the background job runner that orchestrates the chapter
// download/upgrade cycle and the on-demand disk reconciler.
//
// # Architecture
//
// Runner wraps three already-implemented engines from Tasks 5–7:
//   - download.Dispatcher (download + upgrade, Tasks 5 + 6)
//   - disk.Reconcile (lossless library scan, Task 7)
//
// It adds only the orchestration layer:
//   - RunDownloadCycle: one pass of RunOnce → DetectUpgrades → Upgrade per
//     flagged chapter, with cycle-level SSE events.
//   - Start: ticking goroutine that calls RunDownloadCycle every interval until
//     the context is cancelled.
//   - Reconcile: thin wrapper around disk.Reconcile for the on-demand trigger.
//
// # M2 seam
//
// The Dispatcher held by Runner requires a fetcher.ChapterFetcher. In M1 no
// production fetcher exists — only the test-only fetcher/fake. Callers must
// construct the Dispatcher with the real Suwayomi ChapterFetcher once M2 ships
// it. When the M2 fetcher is available, pass a Dispatcher built with it to
// NewRunner and call Start; the runner's internals are fetcher-agnostic.
//
// In main.go, Start is intentionally NOT called in M1 (no real fetcher).
// Reconcile IS wired and available immediately.
package job

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/sse"
)

// CycleEvent is the SSE payload broadcast at the start and end of every
// download cycle. It carries summary information so clients can display a
// "last synced" indicator.
type CycleEvent struct {
	// Chapters is the number of chapters that were actioned in this cycle.
	// Set on cycle.done; zero on cycle.start.
	Chapters int `json:"chapters,omitempty"`

	// Error is set when the cycle completed with an error.
	Error string `json:"error,omitempty"`
}

// Runner orchestrates the chapter download/upgrade cycle and the on-demand disk
// reconciler. Create one with NewRunner.
type Runner struct {
	dispatcher *download.Dispatcher
	client     *ent.Client
	hub        *sse.Hub
	storage    string
}

// NewRunner creates a Runner that delegates to the given Dispatcher (which
// carries the ChapterFetcher seam), Ent client, SSE hub, and storage root.
//
// The Dispatcher must be constructed by the caller with the appropriate
// ChapterFetcher:
//   - Tests: use fetcher/fake.New().
//   - M2 production: use the Suwayomi ChapterFetcher once it is implemented.
//
// NewRunner does not start any background goroutines; call Start to begin
// periodic download cycles.
func NewRunner(dispatcher *download.Dispatcher, client *ent.Client, hub *sse.Hub, storage string) *Runner {
	return &Runner{
		dispatcher: dispatcher,
		client:     client,
		hub:        hub,
		storage:    storage,
	}
}

// RunDownloadCycle executes one full download + upgrade pass:
//  1. Broadcasts cycle.start.
//  2. Calls dispatcher.RunOnce — downloads all wanted/retryable chapters.
//  3. Calls download.DetectUpgrades — flags any downloaded chapters that now
//     have a strictly better source.
//  4. Calls dispatcher.Upgrade for each newly-flagged chapter.
//  5. Broadcasts cycle.done (with error info if step 2 or 3 failed).
//
// Per-chapter errors are handled inside the dispatcher and upgrade engine
// (they record last_error and transition state machine appropriately).
// RunDownloadCycle only propagates hard infrastructure errors that prevented
// the cycle from completing at all.
func (r *Runner) RunDownloadCycle(ctx context.Context) error {
	r.broadcastCycle("cycle.start", CycleEvent{})

	slog.InfoContext(ctx, "job.Runner: download cycle started")

	// Step 1: download all actionable chapters.
	if err := r.dispatcher.RunOnce(ctx); err != nil {
		r.broadcastCycle("cycle.done", CycleEvent{Error: err.Error()})
		return fmt.Errorf("job.Runner.RunDownloadCycle: RunOnce: %w", err)
	}

	// Step 2: detect upgrade candidates among downloaded chapters.
	flagged, err := download.DetectUpgrades(ctx, r.client)
	if err != nil {
		r.broadcastCycle("cycle.done", CycleEvent{Error: err.Error()})
		return fmt.Errorf("job.Runner.RunDownloadCycle: DetectUpgrades: %w", err)
	}

	// Step 3: upgrade each flagged chapter.
	upgraded := 0
	if flagged > 0 {
		upgraded, err = r.upgradeAll(ctx)
		if err != nil {
			r.broadcastCycle("cycle.done", CycleEvent{Chapters: flagged, Error: err.Error()})
			return fmt.Errorf("job.Runner.RunDownloadCycle: upgrade: %w", err)
		}
	}

	r.broadcastCycle("cycle.done", CycleEvent{Chapters: flagged + upgraded})
	slog.InfoContext(ctx, "job.Runner: download cycle finished",
		"flagged_upgrades", flagged,
		"upgraded", upgraded,
	)
	return nil
}

// upgradeAll loads all chapters in state=upgrade_available and calls
// dispatcher.Upgrade for each one. Returns the count of upgrade calls made.
// Individual upgrade failures are handled inside dispatcher.Upgrade (it records
// last_error and restores state=downloaded); only DB-load errors are propagated.
func (r *Runner) upgradeAll(ctx context.Context) (int, error) {
	chapters, err := r.client.Chapter.Query().
		Where(entchapter.StateEQ(entchapter.StateUpgradeAvailable)).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("job.Runner.upgradeAll: query upgrade_available chapters: %w", err)
	}

	count := 0
	for _, ch := range chapters {
		if err := r.dispatcher.Upgrade(ctx, ch.ID); err != nil {
			// Hard error from Upgrade — propagate so the cycle can record it.
			// Defensive path: Upgrade normally returns nil even on fetch/render
			// failures (it handles them internally). Only infrastructure errors
			// like DB-load failures reach here.
			return count, fmt.Errorf("job.Runner.upgradeAll: upgrade chapter %s: %w", ch.ID, err)
		}
		count++
	}
	return count, nil
}

// Start launches a background goroutine that calls RunDownloadCycle every
// interval until ctx is cancelled. Errors from individual cycles are logged but
// do not stop the ticker — a transient DB or network failure should not kill
// the runner permanently. Start returns immediately; the goroutine runs until
// ctx.Done() is closed.
//
// # M2 wiring note
//
// In M1 production this method is NOT called because no real ChapterFetcher
// exists. It is called from tests (using fetcher/fake) and will be wired in
// main.go once M2 supplies the Suwayomi fetcher. The reconcile entrypoint
// (Reconcile) does not use the fetcher and is available immediately.
func (r *Runner) Start(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				slog.InfoContext(ctx, "job.Runner: ticker stopped (context cancelled)")
				return
			case <-ticker.C:
				if err := r.RunDownloadCycle(ctx); err != nil {
					slog.ErrorContext(ctx, "job.Runner: download cycle error",
						"err", err,
					)
				}
			}
		}
	}()
}

// Reconcile wraps disk.Reconcile: it scans the storage root and idempotently
// upserts Series, SeriesProvider, and Chapter rows. This is the on-demand
// reconcile trigger exposed for the HTTP handler layer (M3/M5).
//
// It does not require a ChapterFetcher and is wired live in M1 production.
func (r *Runner) Reconcile(ctx context.Context) (disk.ReconcileResult, error) {
	result, err := disk.Reconcile(ctx, r.client, r.storage)
	if err != nil {
		return result, fmt.Errorf("job.Runner.Reconcile: %w", err)
	}
	return result, nil
}

// broadcastCycle emits a cycle-level SSE event. JSON-encoding failures are
// silently discarded — a missing SSE event is preferable to crashing the runner.
func (r *Runner) broadcastCycle(eventType string, data CycleEvent) {
	raw, err := json.Marshal(data)
	if err != nil {
		// Defensive path: CycleEvent contains only int and string fields;
		// Marshal should never fail. Document as unreachable.
		return
	}
	r.hub.Broadcast(sse.Event{
		Type: eventType,
		Data: json.RawMessage(raw),
	})
}
