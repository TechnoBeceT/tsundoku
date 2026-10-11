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
//   - RunDownloadCycle: one bounded source-window drain with cycle-level events.
//   - Start: independent, start-based source windows sharing one global fetch
//     bound. A slow source does not delay discovery or another source's renewal.
//   - StartRefresh: the non-overlapping true-period discovery loop.
//   - SetRuntimeReconciler: a bounded durable runtime-intent scan dispatched
//     asynchronously from that same download cadence through one epoch-owned,
//     cap-1 worker. Multiple live Start loops share it, shutdown joins it, and
//     retries coalesce without delaying chapter cycles.
//   - ScheduleSnapshot: the race-free read model behind GET /api/engine/schedule
//     (schedule.go) — is a cycle running, and when may the next one start.
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
	"sync"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/refresh"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sse"
	"github.com/technobecet/tsundoku/internal/warmup"
)

// CycleEvent is the SSE payload broadcast at the start and end of every
// download cycle. It carries summary information so clients can display a
// "last synced" indicator.
type CycleEvent struct {
	// Flagged is the number of downloaded chapters detected as having a
	// strictly better source available (upgrade_available). It is NO LONGER set by
	// the download cycle — upgrade detection moved to the post-refresh pass
	// (runUpgradeDetection) — so cycle.done always omits it. The field is retained
	// for SSE wire compatibility.
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
	// dropped. The one admitted coordinator discovers ready work; physical-source
	// windows never overlap themselves.
	trigger chan struct{}
	// downloadOwner serializes coordinator lifetimes, including manual drains.
	// A second Start waits for the first owner's shutdown rather than creating
	// another set of source/global permits.
	downloadOwner    chan struct{}
	downloadMu       sync.Mutex
	downloadDone     chan struct{}
	downloadCancel   context.CancelFunc
	downloadStopping bool
	// notifier runs the best-effort new-readable-chapter pass after a drain or
	// a completed-work batch on the download cadence (internal/notify). Optional (nil when unset) and held as a
	// narrow interface so job never imports internal/notify. Set via SetNotifier.
	notifier interface {
		NotifyNewChapters(context.Context) error
	}
	// breakers reads every source's circuit-breaker state for the sources.summary
	// alert counts. Optional (nil when unset — the summary is then a no-op); set
	// via SetBreakerSnapshotter. Held as the narrow BreakerSnapshotter interface
	// (see sources_summary.go).
	breakers BreakerSnapshotter
	// schedule is the concurrency-safe cycle-schedule snapshot the download and
	// refresh loops publish into and ScheduleSnapshot reads (see schedule.go).
	// Its zero value is ready to use — a Runner whose loops were never started
	// reports an unscheduled, not-running schedule.
	schedule schedule
	// healer runs the post-sweep source-identity-drift self-heal (provider_heal.go).
	// Optional (nil ⇒ the pass is skipped) and guarded by healMu because it is
	// wired from the route layer AFTER the refresh loop has already started — see
	// SetProviderHealer.
	healMu sync.RWMutex
	healer ProviderHealer
	// runtimeReconciler retries durable global and per-source engine-runtime
	// revisions when the existing download cadence requests a pass. It is wired
	// before Start and owned by one Start-epoch worker, keeping retries
	// single-flight without redefining the download loop's running state.
	runtimeReconciler interface {
		ReconcilePending(context.Context) error
	}
	// runtimeMu owns the retry generation and current Start epoch. Multiple live
	// Start loops in one epoch share one consumer; shutdown advances the epoch so
	// old loops cannot signal a replacement generation.
	runtimeMu         sync.Mutex
	runtimeGeneration *runtimeRetryGeneration
	runtimeEpoch      uint64
	runtimeStarts     int
	runtimeStopping   bool
	runtimeStopped    bool
	runtimeStopDone   chan struct{}
	runtimePending    bool
}

type runtimeRetryGeneration struct {
	ctx    context.Context
	cancel context.CancelFunc
	retry  chan struct{}
}

// runtimeRetryRegistration balances one Start lifecycle. Its epoch is used
// only when that Start exits; cadence requests deliberately never carry it.
type runtimeRetryRegistration struct {
	epoch uint64
}

// SetNotifier registers the post-cycle new-chapter notifier. Nil-safe: passing
// nil (or never calling it) leaves the cycle notifier-free. The notifier is
// best-effort — its error never affects RunDownloadCycle's result.
func (r *Runner) SetNotifier(n interface {
	NotifyNewChapters(context.Context) error
}) {
	r.notifier = n
}

// SetRuntimeReconciler registers the bounded pending-runtime scan requested
// asynchronously by each download-cycle start. It must be called during
// construction, before Start.
func (r *Runner) SetRuntimeReconciler(reconciler interface {
	ReconcilePending(context.Context) error
}) {
	r.runtimeReconciler = reconciler
}

// NewRunner creates a Runner that delegates to the given Dispatcher (which
// carries the ChapterFetcher seam), Ent client, SSE hub, and storage root.
//
// The Dispatcher must be constructed by the caller with the appropriate
// ChapterFetcher:
//   - Tests: use fetcher/fake.New().
//   - Production: use sourceengine.NewFetcher (already wired in main.go).
//
// NewRunner does not start any background goroutines; call Start to begin
// periodic download cycles. intervals supplies the runtime-tunable ticker
// periods, read at the top of each loop iteration (hot reload).
func NewRunner(dispatcher *download.Dispatcher, client *ent.Client, hub *sse.Hub, storage string, intervals Intervals) *Runner {
	return &Runner{
		dispatcher:    dispatcher,
		client:        client,
		hub:           hub,
		storage:       storage,
		intervals:     intervals,
		trigger:       make(chan struct{}, 1),
		downloadOwner: make(chan struct{}, 1),
	}
}

// RunDownloadCycle performs one bounded drain with independent source windows.
// Downloads and upgrades share each source's allowance; newly wanted work from
// other sources can join while an existing window is still running. Periodic
// allowance renewal belongs to Start, so this manual drain remains bounded.
func (r *Runner) RunDownloadCycle(ctx context.Context) error {
	return r.runSourceSchedule(ctx, false)
}

func (r *Runner) finishDownloadWork(ctx context.Context) error {
	if _, _, err := r.dispatcher.DetectSupersededParts(ctx); err != nil {
		return err
	}
	if r.notifier != nil {
		if err := r.notifier.NotifyNewChapters(ctx); err != nil {
			slog.WarnContext(ctx, "job.Runner: notify pass error (ignored)", "err", err)
		}
	}
	return nil
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
//
// downloadsConsumed is the drain pass's per-source dispatch tally, forwarded so the
// upgrade pass caps each target source to the remainder of its shared per-cycle
// fetch budget (see download.Dispatcher.UpgradeAll). globalSem is the same
// all-sources concurrency semaphore the drain used, so upgrade fetches count against
// the one aggregate budget for the cycle.
func (r *Runner) upgradeAll(ctx context.Context, downloadsConsumed map[string]int, globalSem *semaphore.Weighted) (int, error) {
	return r.dispatcher.UpgradeAll(ctx, downloadsConsumed, globalSem)
}

// Start launches the source-window coordinator. Each idle physical source may
// renew its allowance on its own start-based download interval; busy sources
// never overlap. Coalesced triggers discover ready work while other sources run.
// Refresh keeps its separate, non-overlapping periodLoop.
func (r *Runner) Start(ctx context.Context) {
	runtimeRegistration := r.startRuntimeRetry(ctx)
	go func() {
		if runtimeRegistration != nil {
			defer r.stopRuntimeRetry(runtimeRegistration)
		}
		r.startSourceSchedule(ctx)
	}()
}

// runDownloadCycleLogging runs one download cycle and logs (without aborting the
// loop) any hard error, tagged so triggered vs ticked cycles are distinguishable
// in the logs.
func (r *Runner) runDownloadCycleLogging(ctx context.Context, triggered bool) {
	msg := "download cycle error"
	if triggered {
		msg = "triggered download cycle error"
	}
	if ctx.Err() != nil {
		// Multiple Start loops share one coalescing trigger. A canceled loop can
		// win the channel receive while a live loop is also waiting; return that
		// opportunity so cancellation cannot swallow the requested download.
		if triggered {
			r.Trigger()
		}
		return
	}
	r.requestRuntimeRetry()
	if err := r.RunDownloadCycle(ctx); err != nil {
		slog.ErrorContext(ctx, "job.Runner: "+msg, "err", err)
	}
}

// requestRuntimeRetry attaches one retry request to a download-cycle start
// without waiting for engine convergence. It resolves the current live retry
// epoch when the cadence opportunity is consumed, rather than using the epoch
// captured by whichever Start loop happened to receive it. The cap-1 channel
// collapses requests arriving while the single worker is busy into at most one
// later pass.
func (r *Runner) requestRuntimeRetry() {
	r.runtimeMu.Lock()
	defer r.runtimeMu.Unlock()
	if r.runtimeStarts == 0 {
		return
	}
	if r.runtimeStopping {
		if !r.runtimeStopped {
			r.runtimePending = true
		}
		return
	}
	if r.runtimeStopped {
		return
	}
	generation := r.ensureRuntimeRetryLocked()
	if generation == nil {
		return
	}
	if generation.ctx.Err() != nil {
		r.runtimePending = true
		return
	}
	select {
	case generation.retry <- struct{}{}:
	default:
	}
}

func (r *Runner) startRuntimeRetry(context.Context) *runtimeRetryRegistration {
	if r.runtimeReconciler == nil {
		return nil
	}
	r.runtimeMu.Lock()
	defer r.runtimeMu.Unlock()
	r.runtimeStopped = false
	r.runtimeStarts++
	epoch := r.runtimeEpoch
	if !r.runtimeStopping {
		r.ensureRuntimeRetryLocked()
	}
	return &runtimeRetryRegistration{epoch: epoch}
}

func (r *Runner) ensureRuntimeRetryLocked() *runtimeRetryGeneration {
	if r.runtimeStopped || r.runtimeStopping || r.runtimeStarts == 0 {
		return nil
	}
	if generation := r.runtimeGeneration; generation != nil {
		return generation
	}
	ctx, cancel := context.WithCancel(context.Background())
	generation := &runtimeRetryGeneration{
		ctx:    ctx,
		cancel: cancel,
		retry:  make(chan struct{}, 1),
	}
	r.runtimeGeneration = generation
	go r.runRuntimeRetryLoop(generation)
	return generation
}

func (r *Runner) stopRuntimeRetry(registration *runtimeRetryRegistration) {
	r.runtimeMu.Lock()
	defer r.runtimeMu.Unlock()
	if registration.epoch != r.runtimeEpoch {
		return
	}
	r.runtimeStarts--
	if r.runtimeStarts == 0 {
		r.runtimePending = false
		if r.runtimeGeneration != nil {
			r.runtimeGeneration.cancel()
		}
	}
}

// runRuntimeRetryLoop owns one immutable retry generation. Its private signal
// prevents a canceled epoch's buffered request from replaying after a later
// Start, while one consumer keeps passes single-flight.
func (r *Runner) runRuntimeRetryLoop(generation *runtimeRetryGeneration) {
	defer r.finishRuntimeRetryGeneration(generation)
	for {
		if generation.ctx.Err() != nil {
			return
		}
		select {
		case <-generation.ctx.Done():
			return
		case <-generation.retry:
			if generation.ctx.Err() != nil {
				return
			}
			if err := r.runRuntimeRetryPass(generation.ctx); err != nil {
				slog.WarnContext(generation.ctx, "job.Runner: pending engine runtime retry incomplete", "err", err)
			}
		}
	}
}

func (r *Runner) runRuntimeRetryPass(ctx context.Context) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("runtime retry panic recovered: %v", recovered)
		}
	}()
	return r.runtimeReconciler.ReconcilePending(ctx)
}

func (r *Runner) finishRuntimeRetryGeneration(generation *runtimeRetryGeneration) {
	r.runtimeMu.Lock()
	defer r.runtimeMu.Unlock()
	if r.runtimeGeneration != generation {
		return
	}
	r.runtimeGeneration = nil
	if r.runtimeStopping {
		runtimeStopping := r.runtimeStopDone
		r.runtimeStopping = false
		if r.runtimeStarts > 0 && !r.runtimeStopped {
			r.signalPendingRuntimeRetryLocked(r.ensureRuntimeRetryLocked())
		}
		close(runtimeStopping)
		return
	}
	if r.runtimeStarts > 0 && !r.runtimeStopped {
		r.signalPendingRuntimeRetryLocked(r.ensureRuntimeRetryLocked())
	}
}

func (r *Runner) signalPendingRuntimeRetryLocked(generation *runtimeRetryGeneration) {
	if !r.runtimePending || generation == nil || generation.ctx.Err() != nil {
		return
	}
	r.runtimePending = false
	select {
	case generation.retry <- struct{}{}:
	default:
	}
}

// ShutdownRuntimeRetry invalidates the current Start epoch, cancels and joins
// its retry generation, and drops its coalesced signal. It is safe to call
// repeatedly or concurrently with Start: a racing Start registers in the new
// epoch immediately, while at most one cadence request is retained until the
// old generation finishes. The mutex-owned stop channel avoids WaitGroup
// Add/Wait races. A timed-out caller may retry the same join later.
func (r *Runner) ShutdownRuntimeRetry(ctx context.Context) error {
	r.runtimeMu.Lock()
	if r.runtimeStopping {
		stopped := r.runtimeStopDone
		r.runtimeMu.Unlock()
		select {
		case <-stopped:
			return nil
		case <-ctx.Done():
			return fmt.Errorf("job.Runner.ShutdownRuntimeRetry: %w", ctx.Err())
		}
	}
	r.runtimeStopping = true
	r.runtimeStopped = true
	r.runtimeEpoch++
	r.runtimeStarts = 0
	r.runtimePending = false
	r.runtimeStopDone = make(chan struct{})
	stopped := r.runtimeStopDone
	if generation := r.runtimeGeneration; generation == nil {
		r.runtimeStopping = false
		close(r.runtimeStopDone)
	} else {
		generation.cancel()
	}
	r.runtimeMu.Unlock()

	select {
	case <-stopped:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("job.Runner.ShutdownRuntimeRetry: %w", ctx.Err())
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
// (svc.RefreshAll) on a TRUE PERIOD until ctx is cancelled, then Triggers a
// download cycle so newly-discovered chapters download promptly instead of
// waiting for the download ticker. Like the download loop (GAP-115), the sweep
// period is measured from the PREVIOUS SWEEP'S START, a tick landing mid-sweep is
// skipped rather than queued, and sweeps never overlap (they share one goroutine).
// Each iteration re-reads the CURRENT refresh interval from the settings overlay
// (intervals.RefreshInterval), so a runtime change to the sweep cadence takes
// effect on the next wait without a restart.
//
// After each successful sweep it runs the provider self-heal (GAP-120) then
// upgrade detection — in that order, see runRefreshSweep — triggers a download
// cycle, calls healthCount to get the current number of unhealthy sources and
// broadcasts a health.summary SSE event so UI badges stay current without a
// manual refresh. If healthCount returns an error the broadcast is skipped for
// that cycle (the error is logged). Sweep errors are logged and never stop the
// loop. Returns immediately.
func (r *Runner) StartRefresh(ctx context.Context, svc *refresh.Service, healthCount func(context.Context) (int, error)) {
	periodLoop{
		name:     "refresh",
		interval: r.intervals.RefreshInterval,
		// No trigger: the refresh sweep is driven by its period alone (a nil
		// channel never fires in the loop's select).
		run:  func(ctx context.Context, _ bool) { r.runRefreshSweep(ctx, svc, healthCount) },
		mark: r.schedule.markRefresh,
	}.start(ctx)
}

// runRefreshSweep performs one discovery sweep, heals source-identity drift,
// detects upgrades, triggers a download cycle, and broadcasts the health summary.
// Sweep / heal / count errors are logged and swallowed so the refresh loop
// survives transient failures.
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
	// Source-identity-drift self-heal (GAP-120). It sits BETWEEN the sweep and
	// upgrade detection, and both edges are load-bearing:
	//
	//   - AFTER RefreshAll, because a merge is gated on the live twin actually
	//     having a chapter feed (the empty-feed orphan guard) and the sweep is
	//     what fills that feed. Refresh is therefore the exact moment a merge
	//     declined at attach time becomes possible.
	//   - BEFORE runUpgradeDetection, because a merge REWRITES detection's two
	//     inputs: it re-points the disk-satisfied chapters onto the live provider
	//     and sets satisfied_importance == that provider's importance in one tx.
	//     Detect first and it would read the PRE-merge state — chapters still
	//     satisfied by the disk row at importance 1 while the freshly-refreshed
	//     live twin offers the same keys at its real rank — and flag the whole
	//     imported series upgrade_available on a strict `>`, so the Trigger below
	//     would re-download files the merge was about to relabel in place. Healing
	//     first leaves importance == satisfied_importance, which detection ignores.
	//
	// GAP-112's ordering survives: detection still runs ONCE per sweep and still
	// runs BEFORE the Trigger.
	r.runProviderHeal(ctx)
	// A discovery sweep is what surfaces the new provider chapters that create
	// upgrade candidates, so upgrade detection runs here — ONCE per sweep, at the
	// refresh cadence — instead of on every download cycle (GAP-112: the library-
	// wide scan blocked downloads when run every cycle). Detect BEFORE the Trigger
	// below so the download cycle it kicks off drains the freshly-flagged chapters
	// via UpgradeAll in the same beat.
	r.runUpgradeDetection(ctx)
	r.Trigger()
	if n, err := healthCount(ctx); err != nil {
		slog.ErrorContext(ctx, "refresh: health summary count failed", "err", err)
	} else {
		r.broadcastHealthSummary(n)
	}
	// Belt-and-braces periodic source-health alert (mirrors broadcastHealthSummary):
	// besides the immediate breaker-transition push, re-emit the current
	// erroring/coolingDown counts each sweep so a badge that missed a dropped SSE
	// event self-heals. No-op when no breaker snapshotter is wired.
	r.broadcastSourcesSummary(ctx)
}

// runUpgradeDetection runs one convergence-upgrade DETECTION pass: it flags every
// downloaded chapter that now has a strictly better source into upgrade_available
// (the actual re-fetch is done by UpgradeAll on the next download cycle). It is
// pure-DB and makes ZERO source calls. The batched detectUpgrades is no-N+1
// (GAP-112), so this is cheap even on a large library. Errors are logged and
// swallowed so a transient detection failure never kills the refresh loop — the
// next sweep re-detects.
func (r *Runner) runUpgradeDetection(ctx context.Context) {
	flagged, err := r.dispatcher.DetectUpgrades(ctx, r.dispatcher.MaxRetries(ctx))
	if err != nil {
		slog.ErrorContext(ctx, "job.Runner: upgrade detection error", "err", err)
		return
	}
	if flagged > 0 {
		slog.InfoContext(ctx, "job.Runner: upgrade detection flagged chapters", "flagged", flagged)
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
// the engine-host's extension catalog so available updates surface without a
// manual refresh. An interval of 0 disables the job — the goroutine idles for a
// fixed fallback period and then re-reads the setting, enabling hot-reload (setting
// a non-zero interval at runtime resumes the job without a restart). Mirrors
// StartRefresh's dynamic-timer (re-reads the interval at the top of each pass).
// Returns immediately.
func (r *Runner) StartExtensionCheck(ctx context.Context, engine sourceengine.Client) {
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
				r.runExtensionCheck(ctx, engine)
			}
		}
	}()
}

// runExtensionCheck calls RefreshExtensions (the engine host's refresh-from-repos)
// and broadcasts an extensions.checked SSE event with the count of extensions that
// have updates available. A RefreshExtensions error is logged and the broadcast is
// skipped for that cycle.
func (r *Runner) runExtensionCheck(ctx context.Context, engine sourceengine.Client) {
	exts, err := engine.RefreshExtensions(ctx)
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
