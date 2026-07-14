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
// # M2 (LIVE)
//
// The Dispatcher held by Runner requires a fetcher.ChapterFetcher. In M2,
// suwayomi.NewFetcher is the real implementation. In main.go, runner.Start is
// called in a background goroutine once pm.Start succeeds; the runner's
// internals are fetcher-agnostic. Reconcile has been live since M1.
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
	"github.com/technobecet/tsundoku/internal/refresh"
	"github.com/technobecet/tsundoku/internal/sse"
	"github.com/technobecet/tsundoku/internal/suwayomi"
	"github.com/technobecet/tsundoku/internal/warmup"
)

// CycleEvent is the SSE payload broadcast at the start and end of every
// download cycle. It carries summary information so clients can display a
// "last synced" indicator.
type CycleEvent struct {
	// Flagged is the number of downloaded chapters detected as having a
	// strictly better source available (upgrade_available). Set on cycle.done;
	// zero on cycle.start.
	Flagged int `json:"flagged,omitempty"`

	// Upgraded is the number of upgrade operations actually performed in this
	// cycle. A chapter counted here was fetched from the better source and
	// returned to state=downloaded. Set on cycle.done; zero on cycle.start.
	Upgraded int `json:"upgraded,omitempty"`

	// Error is set when the cycle completed with an error.
	Error string `json:"error,omitempty"`
}

// Intervals supplies the runtime-tunable ticker periods. The Start / StartRefresh /
// StartExtensionCheck loops read these at the TOP OF EACH ITERATION (a dynamic
// timer, not a captured ticker), so an owner's change to the cadence via the
// settings API takes effect on the next cycle without a restart. *settings.Service
// and settings.Static both satisfy it; reads happen from the ticker goroutines, so
// implementations must be safe for concurrent use.
type Intervals interface {
	// DownloadInterval is the period between download/upgrade cycles.
	DownloadInterval(ctx context.Context) time.Duration
	// RefreshInterval is the period between discovery sweeps.
	RefreshInterval(ctx context.Context) time.Duration
	// ExtensionCheckInterval is the period between extension availability checks.
	// 0 = disabled (the job idles and re-reads the interval on its next pass for
	// hot-reload — no restart needed to re-enable it).
	ExtensionCheckInterval(ctx context.Context) time.Duration
	// WarmupInterval is the period between anti-bot session warm-up passes.
	// 0 = disabled (same idle-and-re-read hot-reload semantics as
	// ExtensionCheckInterval).
	WarmupInterval(ctx context.Context) time.Duration
	// TrackRetryInterval is the period between tracker-push retry-queue drain
	// passes (see StartTrackerRetry / internal/tracker/retry). Always-on (no
	// disable sentinel, unlike WarmupInterval/ExtensionCheckInterval) — the
	// retry queue must keep draining so a failed push is never abandoned
	// beyond its own attempt cap.
	TrackRetryInterval(ctx context.Context) time.Duration
}

// Runner orchestrates the chapter download/upgrade cycle, the discovery refresh
// sweep, and the on-demand disk reconciler. Create one with NewRunner.
type Runner struct {
	dispatcher *download.Dispatcher
	client     *ent.Client
	hub        *sse.Hub
	storage    string
	intervals  Intervals
	// trigger requests an immediate download cycle. Buffered (cap 1) so a
	// request coalesces: if one is already pending, further Trigger() calls are
	// dropped. Drained only by the Start loop, so all cycles run in one
	// goroutine and never overlap.
	trigger chan struct{}
	// notifier runs the best-effort new-readable-chapter pass at the end of each
	// download cycle (internal/notify). Optional (nil when unset) and held as a
	// narrow interface so job never imports internal/notify. Set via SetNotifier.
	notifier interface {
		NotifyNewChapters(context.Context) error
	}
}

// SetNotifier registers the post-cycle new-chapter notifier. Nil-safe: passing
// nil (or never calling it) leaves the cycle notifier-free. The notifier is
// best-effort — its error never affects RunDownloadCycle's result.
func (r *Runner) SetNotifier(n interface {
	NotifyNewChapters(context.Context) error
}) {
	r.notifier = n
}

// NewRunner creates a Runner that delegates to the given Dispatcher (which
// carries the ChapterFetcher seam), Ent client, SSE hub, and storage root.
//
// The Dispatcher must be constructed by the caller with the appropriate
// ChapterFetcher:
//   - Tests: use fetcher/fake.New().
//   - Production (M2+): use suwayomi.NewFetcher (already wired in main.go).
//
// NewRunner does not start any background goroutines; call Start to begin
// periodic download cycles. intervals supplies the runtime-tunable ticker
// periods, read at the top of each loop iteration (hot reload).
func NewRunner(dispatcher *download.Dispatcher, client *ent.Client, hub *sse.Hub, storage string, intervals Intervals) *Runner {
	return &Runner{
		dispatcher: dispatcher,
		client:     client,
		hub:        hub,
		storage:    storage,
		intervals:  intervals,
		trigger:    make(chan struct{}, 1),
	}
}

// RunDownloadCycle executes one full download + upgrade pass:
//  1. Broadcasts cycle.start.
//  2. Drains the dispatcher in BOUNDED PASSES (drainDownloads) — each call to
//     dispatcher.RunOnce dispatches only a bounded batch per source, so the
//     drain loop re-scans the actionable-chapter set between passes and picks
//     up chapters that became wanted mid-cycle (e.g. a fresh adopt) instead of
//     waiting out one giant unbounded drain.
//  3. Calls download.DetectUpgrades — flags any downloaded chapters that now
//     have a strictly better source.
//  4. Calls dispatcher.Upgrade for each newly-flagged chapter.
//  5. Calls dispatcher.DetectSupersededParts — fractional-part suppression:
//     supersedes split parts of a downloaded whole (and reverts when the whole
//     is gone or the setting is off).
//  6. Broadcasts cycle.done (with error info if any prior step failed).
//
// cycle.start/cycle.done fire exactly ONCE per RunDownloadCycle call — NOT once
// per bounded pass — so the SSE cadence is unchanged even though downloading now
// happens in several internal passes.
//
// Per-chapter errors are handled inside the dispatcher and upgrade engine
// (they record last_error and transition state machine appropriately).
// RunDownloadCycle only propagates hard infrastructure errors that prevented
// the cycle from completing at all.
func (r *Runner) RunDownloadCycle(ctx context.Context) error {
	r.broadcastCycle("cycle.start", CycleEvent{})

	slog.InfoContext(ctx, "job.Runner: download cycle started")

	// Step 1: drain all actionable chapters via bounded passes.
	if err := r.drainDownloads(ctx); err != nil {
		r.broadcastCycle("cycle.done", CycleEvent{Error: err.Error()})
		return fmt.Errorf("job.Runner.RunDownloadCycle: RunOnce: %w", err)
	}

	// Step 2: detect upgrade candidates among downloaded chapters. Exhausted
	// sources are excluded using the SAME per-source retry budget the dispatcher
	// applies, so an upgrade never targets a source that has failed out; the
	// GATED method form additionally excludes a source whose politeness
	// circuit-breaker is tripped, so a blocked higher source is never flagged.
	flagged, err := r.dispatcher.DetectUpgrades(ctx, r.dispatcher.MaxRetries(ctx))
	if err != nil {
		r.broadcastCycle("cycle.done", CycleEvent{Error: err.Error()})
		return fmt.Errorf("job.Runner.RunDownloadCycle: DetectUpgrades: %w", err)
	}

	// Step 3: upgrade each flagged chapter.
	upgraded := 0
	if flagged > 0 {
		upgraded, err = r.upgradeAll(ctx)
		if err != nil {
			r.broadcastCycle("cycle.done", CycleEvent{Flagged: flagged, Error: err.Error()})
			return fmt.Errorf("job.Runner.RunDownloadCycle: upgrade: %w", err)
		}
	}

	// Step 4: fractional-part suppression — supersede split parts of downloaded
	// wholes (and revert when the whole is gone or the setting is off).
	if _, _, serr := r.dispatcher.DetectSupersededParts(ctx); serr != nil {
		r.broadcastCycle("cycle.done", CycleEvent{Flagged: flagged, Upgraded: upgraded, Error: serr.Error()})
		return fmt.Errorf("job.Runner.RunDownloadCycle: DetectSupersededParts: %w", serr)
	}

	// Step 5: best-effort new-readable-chapter notification. It keys on
	// first_downloaded_at (upgrade-safe) and never affects the cycle result — a
	// notify failure is logged inside the pass and swallowed here.
	if r.notifier != nil {
		if nerr := r.notifier.NotifyNewChapters(ctx); nerr != nil {
			slog.WarnContext(ctx, "job.Runner: notify pass error (ignored)", "err", nerr)
		}
	}

	r.broadcastCycle("cycle.done", CycleEvent{Flagged: flagged, Upgraded: upgraded})
	slog.InfoContext(ctx, "job.Runner: download cycle finished",
		"flagged_upgrades", flagged,
		"upgraded", upgraded,
	)
	return nil
}

// drainDownloads repeatedly calls dispatcher.RunOnce — each call is ONE BOUNDED
// PASS that dispatches at most a batch per source — until a pass dispatches
// nothing (dispatched == 0) or ctx is cancelled. This keeps a single
// RunDownloadCycle call single-threaded and non-overlapping (the caller still
// serialises cycles) while letting a mid-cycle change to the actionable-chapter
// set (e.g. a fresh adopt inserting new wanted chapters) join within the same
// cycle: because RunOnce re-queries chapter.WantedChapters on every pass, a
// chapter that became wanted after pass N is visible to pass N+1.
//
// NO BUSY-SPIN: a pass dispatches 0 exactly when every remaining wanted/failed
// chapter has no live candidate this pass (no source, all sources exhausted, or
// every source on cooldown) — cooldown chapters are simply not re-selected until
// their backoff elapses on a LATER cycle, so the loop cannot spin forever on
// them. A hard error from RunOnce (chapter-list load failure) stops the drain
// immediately and is returned to the caller.
//
// now is snapshotted ONCE for the whole cycle (not re-read per pass) and passed
// to every RunOnceAt call: without this, a slow-timeout source's per-source
// backoff (next_attempt_at) set on an early pass could already be in the past
// by a later pass of the SAME cycle, re-qualifying the source and burning its
// whole retry budget in one cycle instead of one attempt per cycle.
func (r *Runner) drainDownloads(ctx context.Context) error {
	now := time.Now()
	for {
		dispatched, err := r.dispatcher.RunOnceAt(ctx, now)
		if err != nil {
			return err
		}
		if dispatched == 0 {
			return nil
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}

// upgradeAll runs the convergence-upgrade pass over every chapter in
// state=upgrade_available and returns how many upgrades completed without a hard
// error. The scheduling lives in the engine (download.Dispatcher.UpgradeAll), which
// groups the flagged chapters by their UPGRADE TARGET source and runs each source's
// queue in parallel with a PER-SOURCE concurrency cap — so a wave aimed at several
// sources is no longer serialised behind whichever source fills a single global
// pool, while no individual source is hit any harder than before (see UpgradeAll's
// doc comment for the full anti-ban argument).
//
// The runner keeps this thin wrapper because the cycle owns the error/count
// contract: individual upgrade failures are handled inside the engine (working copy
// kept, chapter returns to downloaded), so only hard infrastructure errors surface
// here and abort the cycle.
func (r *Runner) upgradeAll(ctx context.Context) (int, error) {
	return r.dispatcher.UpgradeAll(ctx)
}

// Start launches a background goroutine that calls RunDownloadCycle on a dynamic
// timer until ctx is cancelled. Each iteration re-reads the CURRENT download
// interval from the settings overlay (intervals.DownloadInterval) and waits that
// long, so a runtime change to the cadence takes effect on the very next cycle —
// no restart, no captured ticker. Errors from individual cycles are logged but
// do not stop the loop — a transient DB or network failure should not kill the
// runner permanently. Start returns immediately; the goroutine runs until
// ctx.Done() is closed.
//
// In main.go, Start is called in a background goroutine after pm.Start
// succeeds (Suwayomi is ready). If Suwayomi fails to start, downloads are
// simply suspended; the API server continues running. Reconcile does not use
// the fetcher and is live since M1.
func (r *Runner) Start(ctx context.Context) {
	go func() {
		for {
			timer := time.NewTimer(r.intervals.DownloadInterval(ctx))
			select {
			case <-ctx.Done():
				timer.Stop()
				slog.InfoContext(ctx, "job.Runner: download loop stopped (context cancelled)")
				return
			case <-timer.C:
				r.runDownloadCycleLogging(ctx, "download cycle error")
			case <-r.trigger:
				timer.Stop()
				r.runDownloadCycleLogging(ctx, "triggered download cycle error")
			}
		}
	}()
}

// runDownloadCycleLogging runs one download cycle and logs (without aborting the
// loop) any hard error, tagged with the given message so triggered vs ticked
// cycles are distinguishable in the logs.
func (r *Runner) runDownloadCycleLogging(ctx context.Context, msg string) {
	if err := r.RunDownloadCycle(ctx); err != nil {
		slog.ErrorContext(ctx, "job.Runner: "+msg, "err", err)
	}
}

// Trigger requests an immediate download cycle from the Start loop. It is
// non-blocking and coalescing: if a cycle is already pending, the request is
// dropped (the pending cycle will reflect current DB state). Safe to call from
// any goroutine — e.g. the Adopt / ReorderProviders handlers (M5 auto-converge)
// and the refresh ticker after a discovery sweep.
func (r *Runner) Trigger() {
	select {
	case r.trigger <- struct{}{}:
	default:
	}
}

// StartRefresh launches a background goroutine that runs the discovery sweep
// (svc.RefreshAll) on a dynamic timer until ctx is cancelled, then Triggers a
// download cycle so newly-discovered chapters download promptly instead of
// waiting for the download ticker. Each iteration re-reads the CURRENT refresh
// interval from the settings overlay (intervals.RefreshInterval), so a runtime
// change to the sweep cadence takes effect on the next cycle without a restart.
// After each successful sweep it calls healthCount to get the current number of
// unhealthy sources and broadcasts a health.summary SSE event so UI badges stay
// current without a manual refresh. If healthCount returns an error the broadcast
// is skipped for that cycle (the error is logged). Sweep errors are logged and
// never stop the loop. Returns immediately.
func (r *Runner) StartRefresh(ctx context.Context, svc *refresh.Service, healthCount func(context.Context) (int, error)) {
	go func() {
		for {
			timer := time.NewTimer(r.intervals.RefreshInterval(ctx))
			select {
			case <-ctx.Done():
				timer.Stop()
				slog.InfoContext(ctx, "job.Runner: refresh loop stopped (context cancelled)")
				return
			case <-timer.C:
				r.runRefreshSweep(ctx, svc, healthCount)
			}
		}
	}()
}

// runRefreshSweep performs one discovery sweep, triggers a download cycle, and
// broadcasts the health summary. Sweep / count errors are logged and swallowed so
// the refresh loop survives transient failures.
func (r *Runner) runRefreshSweep(ctx context.Context, svc *refresh.Service, healthCount func(context.Context) (int, error)) {
	res, err := svc.RefreshAll(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "job.Runner: refresh sweep error", "err", err)
		return
	}
	slog.InfoContext(ctx, "job.Runner: refresh sweep finished",
		"series", res.SeriesRefreshed,
		"new_chapters", res.NewChapters,
		"errors", res.Errors,
	)
	r.Trigger()
	if n, err := healthCount(ctx); err != nil {
		slog.ErrorContext(ctx, "refresh: health summary count failed", "err", err)
	} else {
		r.broadcastHealthSummary(n)
	}
}

// broadcastHealthSummary emits a health.summary SSE event with the count of
// series that have at least one stale or erroring source.
func (r *Runner) broadcastHealthSummary(unhealthy int) {
	raw, err := json.Marshal(struct {
		Unhealthy int `json:"unhealthy"`
	}{Unhealthy: unhealthy})
	if err != nil {
		// Defensive path: a single int field cannot fail to marshal.
		return
	}
	r.hub.Broadcast(sse.Event{Type: "health.summary", Data: json.RawMessage(raw)})
}

// StartExtensionCheck launches a background goroutine that periodically refreshes
// the Suwayomi extension catalog so available updates surface without a manual
// refresh. An interval of 0 disables the job — the goroutine idles for a fixed
// fallback period and then re-reads the setting, enabling hot-reload (setting a
// non-zero interval at runtime resumes the job without a restart). Mirrors
// StartRefresh's dynamic-timer (re-reads the interval at the top of each pass).
// Returns immediately.
func (r *Runner) StartExtensionCheck(ctx context.Context, sw suwayomi.Client) {
	go func() {
		const disabledRecheck = time.Hour
		for {
			iv := r.intervals.ExtensionCheckInterval(ctx)
			wait := iv
			if iv <= 0 {
				wait = disabledRecheck // disabled: idle, re-read later (hot reload)
			}
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				slog.InfoContext(ctx, "job.Runner: extension-check loop stopped (context cancelled)")
				return
			case <-timer.C:
				if iv <= 0 {
					continue // still disabled; re-read interval on the next pass
				}
				r.runExtensionCheck(ctx, sw)
			}
		}
	}()
}

// runExtensionCheck calls FetchExtensions and broadcasts an extensions.checked SSE
// event with the count of extensions that have updates available. A FetchExtensions
// error is logged and the broadcast is skipped for that cycle.
func (r *Runner) runExtensionCheck(ctx context.Context, sw suwayomi.Client) {
	exts, err := sw.FetchExtensions(ctx)
	if err != nil {
		slog.WarnContext(ctx, "job.Runner: extension check failed", "err", err)
		return
	}
	updates := 0
	for _, e := range exts {
		if e.HasUpdate {
			updates++
		}
	}
	r.broadcastExtensionsChecked(updates)
}

// broadcastExtensionsChecked emits an extensions.checked SSE event carrying the
// count of extensions with an available update.
func (r *Runner) broadcastExtensionsChecked(updates int) {
	raw, err := json.Marshal(struct {
		UpdatesAvailable int `json:"updatesAvailable"`
	}{UpdatesAvailable: updates})
	if err != nil {
		// Defensive path: a single int field cannot fail to marshal.
		return
	}
	r.hub.Broadcast(sse.Event{Type: "extensions.checked", Data: json.RawMessage(raw)})
}

// StartWarmup launches a background goroutine that periodically warms anti-bot
// Suwayomi sessions so interactive search stays fast. The FIRST pass warms EVERY
// enabled source (WarmAll, a seed); every pass after warms only the slow /
// never-measured ones (WarmSlow). An interval of 0 disables the job — the
// goroutine idles for a fixed fallback period then re-reads the setting, enabling
// hot-reload. Returns immediately.
//
// The seed pass runs at the TOP of the loop (immediately at boot), THEN the loop
// waits the interval — so a fresh restart warms sources right away instead of
// leaving a full-interval cold window before the first pass. The period between
// later passes is unchanged (interval + pass duration); only the first pass moved
// to t=0. The interval is re-read every iteration (a dynamic timer), so a runtime
// change to the cadence — including enabling a disabled job — takes effect on the
// next pass without a restart.
func (r *Runner) StartWarmup(ctx context.Context, svc *warmup.Service) {
	go func() {
		const disabledRecheck = time.Hour
		seeded := false
		for {
			iv := r.intervals.WarmupInterval(ctx)
			// Run the pass at the top when enabled (seed-at-boot on the first
			// iteration); a disabled job runs no pass and only idles to re-read.
			wait := disabledRecheck
			if iv > 0 {
				seeded = r.runWarmupPass(ctx, svc, seeded)
				wait = iv
			}
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				slog.InfoContext(ctx, "job.Runner: warm-up loop stopped (context cancelled)")
				return
			case <-timer.C:
			}
		}
	}()
}

// runWarmupPass runs one warm-up pass and returns the new "seeded" state. Until a
// seed (WarmAll) has completed successfully it runs WarmAll; thereafter it runs
// WarmSlow. A failed seed leaves seeded=false so the next pass retries the seed.
func (r *Runner) runWarmupPass(ctx context.Context, svc *warmup.Service, seeded bool) bool {
	if !seeded {
		n, err := svc.WarmAll(ctx)
		if err != nil {
			slog.WarnContext(ctx, "job.Runner: warm-up seed failed", "err", err)
			return false
		}
		slog.InfoContext(ctx, "job.Runner: warm-up seed complete", "warmed", n)
		return true
	}
	n, err := svc.WarmSlow(ctx)
	if err != nil {
		slog.WarnContext(ctx, "job.Runner: warm-up (slow) failed", "err", err)
		return true
	}
	slog.InfoContext(ctx, "job.Runner: warm-up (slow) complete", "warmed", n)
	return true
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
