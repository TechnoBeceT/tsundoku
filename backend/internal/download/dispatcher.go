// Package download implements the M1 state-driven download dispatcher.
//
// The dispatcher loads actionable chapters (state=wanted, or state=failed with
// retry budget remaining), fetches their pages via the ChapterFetcher port,
// renders them to disk via the disk.RenderChapter renderer, and advances
// chapter state through the state machine. Per-provider concurrency is capped
// via buffered-channel semaphores so that a single provider cannot monopolise
// the worker pool. Since Slice 2 (responsiveness + fairness), RunOnce processes
// one BOUNDED batch per source per call rather than draining a source's whole
// backlog — see RunOnce's doc comment and job.Runner.RunDownloadCycle, which
// loops it to drain a cycle while staying responsive to newly-wanted chapters.
package download

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/pkg/errorclass"
	"github.com/technobecet/tsundoku/internal/sourceevents"
	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/sse"
)

// Config holds the STRUCTURAL (env-only, restart-scoped) parameters for the
// Dispatcher. The retry policy (max-retries + backoff base) and the per-source
// download concurrency are NOT here — they are runtime-tunable and read from a
// RetrySettings at use-time so a settings change takes effect on the next cycle
// without a restart.
type Config struct {
	// Storage is the root library directory (e.g. "/data/library") passed to
	// disk.RenderChapter.
	Storage string

	// StagingRoot is the on-disk page-staging root (<storage>/.tsundoku-staging)
	// each chapter's per-provider staging dir lives under (<StagingRoot>/<pcID>/).
	// It is the SAME path handed to sourceengine.NewFetcher, threaded here so the
	// dispatcher can wipe a chapter's staging dirs when it reaches the terminal
	// permanently_failed state (its partial downloads will never be resumed). Empty
	// disables that cleanup — tests using the fake fetcher stage nothing on disk.
	StagingRoot string
}

// RetrySettings supplies the runtime-tunable download policy. The Dispatcher
// reads it per cycle / per fail-handling (never captured at construction) so an
// owner's change to the max-retries, backoff base, or per-source concurrency via
// the settings API takes effect on the next download cycle. *settings.Service and
// settings.Static both satisfy it; implementations must be safe for concurrent
// use (the accessors run in the per-source scheduler + per-chapter goroutines).
type RetrySettings interface {
	// MaxRetries is the PER-SOURCE retry budget: how many times the dispatcher
	// retries a chapter FROM ONE SOURCE before that source is abandoned for it. A
	// chapter is parked in permanently_failed only when EVERY source offering it
	// has been abandoned (see chapter.AllProvidersExhausted) — this is not a global
	// per-chapter counter.
	MaxRetries(ctx context.Context) int
	// RetryBackoff is the FLAT delay before every retry, applied PER SOURCE: the
	// gap between a source's successive tries for one chapter is constant = this
	// value (no per-attempt growth — see backoffCurve). Default 30m.
	RetryBackoff(ctx context.Context) time.Duration
	// DownloadConcurrency is the PER-SOURCE download concurrency cap: how many of a
	// source's chapters the dispatcher fetches in parallel (and, equivalently, how
	// many of that source's queued chapters may be in the downloading state at
	// once). Read once per cycle for the scheduler + fetch limiter; clamped to >= 1.
	DownloadConcurrency(ctx context.Context) int
	// SuppressSplitParts reports whether fractional-part suppression is enabled
	// (DetectSupersededParts, superseded.go). Read at use-time so a settings change
	// takes effect on the next sweep.
	SuppressSplitParts(ctx context.Context) bool
}

// backoffCurve returns the FLAT retry interval: the configured base delay itself,
// applied identically before every retry attempt (Kaizoku-style "count every
// retry, terminal at max" model — owner-ratified). There is no per-attempt
// growth: the gap between a source's successive tries for one chapter is constant
// = base. A base of 0 yields 0 (immediate retry — used by tests). The retry MODEL
// gives up on a chapter after jobs.max_retries attempts (permanently_failed); the
// drain-prevention against an anti-bot ban is the circuit-breaker (sourcegate),
// NOT a growing backoff.
func backoffCurve(base time.Duration) time.Duration {
	return base
}

// DownloadEvent is the SSE payload broadcast for every download lifecycle
// transition (start, done, fail) and for live page-progress (download.progress).
// ChapterID identifies the affected chapter; State is the new/current chapter
// state; Error is set only on failure; Current/Total are set only on progress.
type DownloadEvent struct {
	// ChapterID is the UUID of the chapter that changed state.
	ChapterID uuid.UUID `json:"chapter_id"`

	// State is the new state of the chapter after the transition.
	State string `json:"state"`

	// Error is the human-readable error message. Set only on failure events.
	Error string `json:"error,omitempty"`

	// Current is the number of pages fetched so far, set only on download.progress
	// events. omitempty keeps the start/done/fail/skip payloads byte-identical.
	Current int `json:"current,omitempty"`

	// Total is the chapter's total page count, set only on download.progress
	// events. omitempty keeps the start/done/fail/skip payloads byte-identical.
	Total int `json:"total,omitempty"`
}

// Dispatcher coordinates the M1 download pipeline. Create one with New and call
// RunOnce repeatedly (job.Runner.RunDownloadCycle's drain loop does this) to
// process all currently actionable chapters in bounded per-source batches.
type Dispatcher struct {
	client *ent.Client
	f      fetcher.ChapterFetcher
	hub    *sse.Hub
	cfg    Config
	retry  RetrySettings
	gate   *sourcegate.Service
	// events is the nil-guarded source-operation audit-log recorder. When
	// attached (WithEventRecorder), each per-source download attempt logs one
	// `download` event (success or failure). Nil ⇒ no audit events (existing call
	// sites and tests are unaffected).
	events sourceevents.Recorder
}

// New creates a Dispatcher configured with the given client, fetcher, SSE hub,
// structural Config, and runtime RetrySettings. The download policy (max-retries,
// backoff base, and per-source concurrency) is read from retry at use-time, never
// captured here.
//
// gate is the source-politeness circuit-breaker + delay (internal/sourcegate),
// consulted before a chapter's candidates are dispatched and around every fetch
// (see filterGated / tryCandidate) so a source Cloudflare is blocking never gets
// hammered further. gate may be nil (no gate configured): every gate-consulting
// call site treats a nil gate as "always available, no delay" — i.e. today's
// pre-politeness behaviour — so passing nil is a safe default for callers that
// do not need the gate (tests exercising unrelated dispatcher behaviour).
func New(client *ent.Client, f fetcher.ChapterFetcher, hub *sse.Hub, cfg Config, retry RetrySettings, gate *sourcegate.Service) *Dispatcher {
	return &Dispatcher{
		client: client,
		f:      f,
		hub:    hub,
		cfg:    cfg,
		retry:  retry,
		gate:   gate,
	}
}

// WithEventRecorder attaches the source-operation audit-log recorder so each
// per-source download attempt logs a `download` event. It returns the receiver
// for chaining off New. A nil recorder logs nothing (best-effort — never affects
// the download).
func (d *Dispatcher) WithEventRecorder(r sourceevents.Recorder) *Dispatcher {
	d.events = r
	return d
}

// logDownloadEvent records one per-source download attempt outcome (best-effort,
// nil-guarded). duration is the fetch wall-clock; itemsCount is the rendered page
// count on success (nil on failure). The source identity is derived from the
// SeriesProvider exactly as the scheduler keys it (canonicalSourceKey).
//
// When a per-cycle sink rides the context (the RunOnce path) the event is
// ACCUMULATED and flushed as one LogBatch after the cycle; otherwise (the
// standalone Process path) it is written immediately as a single event.
func (d *Dispatcher) logDownloadEvent(ctx context.Context, ch *ent.Chapter, sp *ent.SeriesProvider, status sourceevents.Status, duration time.Duration, itemsCount *int, cause error) {
	if d.events == nil {
		return
	}
	ev := sourceevents.Event{
		SourceKey:  canonicalSourceKey(sp),
		SourceID:   providerSourceID(sp),
		SourceName: canonicalSourceKey(sp),
		Language:   sp.Language,
		Type:       sourceevents.EventDownload,
		Status:     status,
		Duration:   duration,
		Err:        cause,
		ItemsCount: itemsCount,
		Metadata:   downloadEventMetadata(ch),
	}
	if sink := eventSinkFrom(ctx); sink != nil {
		sink.add(ev)
		return
	}
	d.events.Log(ctx, ev)
}

// downloadEventSink accumulates a cycle's per-source download events under its own
// lock so the concurrent dispatch goroutines append without racing, then the whole
// batch is flushed in ONE LogBatch after the cycle (mirrors refresh's sink).
type downloadEventSink struct {
	mu     sync.Mutex
	events []sourceevents.Event
}

// add appends one event under the sink's lock. A nil sink is a no-op.
func (s *downloadEventSink) add(ev sourceevents.Event) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.events = append(s.events, ev)
	s.mu.Unlock()
}

// eventSinkCtxKey is the private context key the per-cycle download-event sink
// rides under (an unexported type so no other package can collide with it).
type eventSinkCtxKey struct{}

// withEventSink returns a context carrying sink so tryCandidate can accumulate
// download events into it without threading the sink through the generic
// per-source scheduler. A nil sink is stored as nil (eventSinkFrom returns nil).
func withEventSink(ctx context.Context, sink *downloadEventSink) context.Context {
	return context.WithValue(ctx, eventSinkCtxKey{}, sink)
}

// eventSinkFrom returns the per-cycle download-event sink carried by ctx, or nil
// when none is set (the standalone Process path, or no recorder wired).
func eventSinkFrom(ctx context.Context) *downloadEventSink {
	sink, _ := ctx.Value(eventSinkCtxKey{}).(*downloadEventSink)
	return sink
}

// newEventSink returns a fresh sink when an audit recorder is wired, else nil (so
// the cycle collects nothing and rides a nil sink).
func (d *Dispatcher) newEventSink() *downloadEventSink {
	if d.events == nil {
		return nil
	}
	return &downloadEventSink{}
}

// flushEventSink logs the cycle's collected download events in one batch
// (best-effort, nil-guarded). Called after the cycle's goroutines have joined, so
// the slice is safe to read without further locking.
func (d *Dispatcher) flushEventSink(ctx context.Context, sink *downloadEventSink) {
	if d.events == nil || sink == nil || len(sink.events) == 0 {
		return
	}
	d.events.LogBatch(ctx, sink.events)
}

// downloadEventMetadata builds the forensic context for a download event: the
// series id and title where available (never used for aggregation, only display).
func downloadEventMetadata(ch *ent.Chapter) map[string]string {
	meta := map[string]string{"series_id": ch.SeriesID.String()}
	if ch.Edges.Series != nil {
		meta["series"] = ch.Edges.Series.Title
	}
	return meta
}

// providerSourceID returns the numeric engine-host source id (as a string) for a
// SeriesProvider, or "" for a disk-reconciled row. It mirrors canonicalSourceKey's
// distinction: a live-ingested provider stores the numeric id in `provider` with
// the name in `provider_name`, whereas a disk row stores the name in `provider`
// and leaves `provider_name` empty — so an empty provider_name means "no numeric
// id" (the spec's "" for disk sources).
func providerSourceID(sp *ent.SeriesProvider) string {
	if sp.ProviderName == "" {
		return ""
	}
	return sp.Provider
}

// gateWait enforces the politeness delay for sourceKey before a fetch. A nil
// gate is a no-op.
func (d *Dispatcher) gateWait(ctx context.Context, sourceKey string) {
	if d.gate == nil {
		return
	}
	d.gate.Wait(ctx, sourceKey)
}

// gateRecordSuccess reports a successful fetch from sourceKey to the breaker
// (resets its consecutive-failure counter and clears any cooldown). A nil
// gate is a no-op.
func (d *Dispatcher) gateRecordSuccess(ctx context.Context, sourceKey string) {
	if d.gate == nil {
		return
	}
	d.gate.RecordSuccess(ctx, sourceKey)
}

// gateRecordFailure reports a failed fetch from sourceKey to the breaker
// (bumps its consecutive-failure counter, tripping it into cooldown once the
// runtime threshold is reached). A nil gate is a no-op.
func (d *Dispatcher) gateRecordFailure(ctx context.Context, sourceKey string, cause error, now time.Time) {
	if d.gate == nil {
		return
	}
	d.gate.RecordFailure(ctx, sourceKey, cause, now)
}

// filterGated removes candidates whose physical source is currently cooled
// down by the source-politeness gate, so a Cloudflare-blocked (or otherwise
// tripped) source is excluded from candidacy entirely — not merely
// deprioritised. If this empties the candidate list, the caller's existing
// no-candidate handling applies (handleNoCandidates on the download path: the
// chapter stays wanted, exactly like the pre-existing per-source
// next_attempt_at cooldown UX — this is a SEPARATE, coarser-grained "this
// source is down entirely" gate on top of that per-chapter one). A nil gate
// never filters anything. It delegates to gateFilterCandidates so the download
// AND upgrade paths share one definition of the exclusion rule (§2 DRY).
func (d *Dispatcher) filterGated(ctx context.Context, cands []chapter.Candidate, now time.Time) []chapter.Candidate {
	return gateFilterCandidates(ctx, d.gate, cands, now)
}

// gateFilterCandidates is the free-function core of the candidacy exclusion:
// it drops candidates whose physical source's circuit-breaker is tripped. A nil
// gate never filters. Shared by the download path (Dispatcher.filterGated) and
// the gate-aware upgrade detection (detectUpgrades / fetchAndRender), which is
// why it takes an explicit *sourcegate.Service rather than reading d.gate — the
// upgrade package-level DetectUpgrades has no dispatcher.
func gateFilterCandidates(ctx context.Context, gate *sourcegate.Service, cands []chapter.Candidate, now time.Time) []chapter.Candidate {
	if gate == nil {
		return cands
	}
	out := make([]chapter.Candidate, 0, len(cands))
	for _, c := range cands {
		if gate.IsAvailable(ctx, canonicalSourceKey(c.SeriesProvider), now) {
			out = append(out, c)
		}
	}
	return out
}

// shouldRecordGateFailure reports whether a fetch error should count against
// the source's circuit-breaker. TWO conditions must both hold:
//
//   - The PARENT context is still alive. A fetch error that arrives because the
//     parent context was cancelled (graceful shutdown) is NOT evidence the source
//     is down — mirroring refresh.RefreshAll's ctx-cancel skip. A per-fetch
//     deadline that fires while the parent context is still alive DOES count: that
//     slow/blocked latency is exactly the signal the breaker exists to catch.
//   - The failure is SOURCE-WIDE, not chapter-specific. A broken page / not_found /
//     no_pages / parse means THIS chapter is broken on this source, NOT that the
//     source is down — tripping the breaker on it would wrongly pause the whole
//     source (and every other series it serves). Only ban/source-down class errors
//     (rate_limit / captcha / timeout / network / server_error / unknown) count
//     toward the breaker. Shared by the download and upgrade fetch paths.
func shouldRecordGateFailure(ctx context.Context, cause error) bool {
	return ctx.Err() == nil && !isChapterSpecificFailure(cause)
}

// downloadConcurrency reads the current per-source download concurrency cap from
// the runtime settings, clamped to at least 1. A cap of 0 (e.g. an unset
// settings.Static field) would make an unbuffered scheduler channel deadlock, so
// the clamp is a correctness guard, not a nicety.
func (d *Dispatcher) downloadConcurrency(ctx context.Context) int {
	n := d.retry.DownloadConcurrency(ctx)
	if n < 1 {
		n = 1
	}
	return n
}

// MaxRetries returns the current per-source retry budget from the runtime
// settings (read at call time, so a settings change is reflected immediately).
// It lets the job runner pass the same budget into download.DetectUpgrades that
// the dispatcher uses, keeping the exhaustion rule consistent across the download
// and upgrade paths without the runner needing its own settings handle.
func (d *Dispatcher) MaxRetries(ctx context.Context) int {
	return d.retry.MaxRetries(ctx)
}

// DownloadConcurrency returns the current per-source download concurrency cap
// (clamped to at least 1), read at call time. It lets callers outside this
// package — e.g. job.Runner.upgradeAll, which parallelizes convergence
// upgrades — size their own concurrency pool to the same bound the dispatcher
// itself uses, without duplicating the settings read or the clamp.
func (d *Dispatcher) DownloadConcurrency(ctx context.Context) int {
	return d.downloadConcurrency(ctx)
}

// wantedScanLimit bounds how many wanted/failed chapters RunOnce loads per pass.
// One pass never exceeds this many chapters resolved+grouped, keeping the
// per-pass query cheap regardless of library size; the drain loop
// (job.Runner.RunDownloadCycle) simply calls RunOnce again for the rest.
const wantedScanLimit = 1000

// batchPerSource returns the maximum number of a single source's chapters that
// ONE RunOnce pass will dispatch (transition to downloading), given the current
// per-source download concurrency. It is deliberately larger than concurrency
// (2x) so a pass keeps a source's slots continuously fed — as soon as one
// chapter finishes another from the same batch is ready to take its slot —
// instead of a pass that dispatches exactly `concurrency` items and then idles
// while the last one finishes. The trade-off (owner-picked 2026-07-05): a bigger
// multiplier raises per-pass throughput but delays how soon a NEWLY wanted
// chapter (e.g. from a mid-cycle adopt) gets scanned into a future pass, since
// the current pass must finish its whole batch first. 2x balances the two.
func batchPerSource(concurrency int) int {
	return 2 * concurrency
}

// RunOnce runs ONE BOUNDED PASS over the actionable chapters (state wanted or
// failed): it loads up to wantedScanLimit of them, groups them by primary
// source with a round-robin-across-series order (see groupBySource /
// roundRobinBySeries), then dispatches — per source, in parallel — only the
// first batchPerSource(concurrency) chapters of each source's queue via the
// existing ordered scheduler (runSourceQueue), up to DownloadConcurrency
// in-flight at a time. It waits for that bounded batch to finish before
// returning `dispatched` = the number of chapters that made FORWARD PROGRESS
// this pass, i.e. whose atomic wanted/failed→downloading claim SUCCEEDED (each
// counted via a shared atomic counter incremented in runSourceQueue). Per-chapter
// outcomes (success, source failure, permanent failure) are recorded in the DB and
// broadcast via SSE, not propagated to the caller — only a hard infrastructure
// failure loading the work list is returned.
//
// Why forward progress and not the SELECTED count: the drain loop
// (job.Runner.RunDownloadCycle) terminates on dispatched==0. Counting chapters
// merely SELECTED into a group (which is computed before the goroutines run) would
// hot-spin the drain loop under a writes-fail/reads-succeed DB fault (e.g.
// disk-full or a read-only replica): the claim write fails, the chapter stays
// wanted/failed and is re-selected every pass, so a selected-count would never
// reach 0. Counting only successful claims makes dispatched==0 mean "no chapter
// left the wanted/failed set this pass" → the loop stops and the runner retries on
// its next interval, degrading gracefully (matching pre-Slice-2 one-pass-per-cycle
// behaviour). Under a healthy DB every selected chapter claims successfully, so
// dispatched == selected and behaviour is unchanged.
//
// Being bounded (rather than draining every source's whole queue) is what lets
// job.Runner.RunDownloadCycle's drain loop re-scan WantedChapters between
// passes, so a chapter that becomes wanted mid-cycle (e.g. a fresh adopt) is
// picked up within one pass instead of waiting out the entire prior backlog.
//
// The download policy is read ONCE here so every chapter in this pass sees a
// consistent snapshot; a settings change therefore applies from the next pass:
//
//   - maxRetries + now — the per-source retry budget + cooldown horizon.
//   - concurrency — the per-source start cap (scheduler) AND the per-provider
//     fetch cap (limiter), and the batch-size input.
//
// A chapter stays in the wanted state (UI "Queued") until the scheduler acquires
// a start slot for it — only then does it transition to downloading — so at any
// moment only up to DownloadConcurrency of a source's chapters are downloading and
// the rest remain queued, draining in round-robin-across-series order (see
// schedule.go).
//
// A single RunOnce call is one cycle in itself: it allocates a FRESH per-source
// budget, so a source may dispatch up to batchPerSource(concurrency) of its
// chapters in this one pass (the standalone entry point used by tests and the
// Process path). The cross-pass per-CYCLE cap is applied by the drain loop, which
// calls RunOnceAt with a shared budget map — see RunOnceAt.
func (d *Dispatcher) RunOnce(ctx context.Context) (dispatched int, err error) {
	return d.RunOnceAt(ctx, time.Now(), make(map[string]int))
}

// RunOnceAt runs one bounded dispatch pass anchored to the given now, honouring
// (and updating) the shared per-source per-CYCLE dispatch budget in consumed.
//
// consumed maps a canonicalSourceKey to how many of that physical source's
// chapters have ALREADY been dispatched earlier in THIS cycle (across prior
// passes of the same drain loop). A source's batch this pass is capped to the
// REMAINDER of its per-cycle budget batchPerSource(concurrency) — so however many
// passes the drain loop runs, one physical source is fetched at most
// batchPerSource(concurrency) times per cycle, and this same budget is shared with
// the upgrade pass (see UpgradeAll). Without this the drain loop, which re-calls
// RunOnceAt until a pass dispatches 0, would let one source's large backlog be
// dispatched far beyond the per-pass batch and re-ban an anti-bot source. RunOnceAt
// mutates consumed in place (adding this pass's per-source dispatch counts), so the
// caller passes the SAME map to every pass of a cycle then hands it to UpgradeAll.
//
// The job runner's drain loop (job.Runner.drainDownloads) snapshots now ONCE per
// download cycle and passes the SAME value to every pass in that cycle, so a
// source's per-source backoff (next_attempt_at) cannot elapse mid-cycle and
// collapse its retry spacing — a slow-timeout source gets one attempt per
// cycle, not one per pass. All candidacy + backoff decisions in this pass use the
// now passed here.
func (d *Dispatcher) RunOnceAt(ctx context.Context, now time.Time, consumed map[string]int) (dispatched int, err error) {
	maxRetries := d.retry.MaxRetries(ctx)
	concurrency := d.downloadConcurrency(ctx)

	chapters, err := chapter.WantedChapters(ctx, d.client, wantedScanLimit)
	if err != nil {
		return 0, fmt.Errorf("download.Dispatcher.RunOnceAt: load chapters: %w", err)
	}
	if len(chapters) == 0 {
		return 0, nil
	}

	// Resolve each chapter's live candidates and partition by primary source
	// (highest-importance live candidate), round-robin-ordered across series
	// within each source. No-candidate chapters are handled here and never
	// occupy a start slot. The limiter is shared across the whole cycle so a
	// provider's fetch cap holds even for fall-through candidates.
	groups := d.groupBySource(ctx, chapters, maxRetries, now)
	limiter := newProviderLimiter(concurrency)
	budget := batchPerSource(concurrency)

	// Cap each source to the REMAINDER of its per-cycle budget (batchPerSource
	// already consumed earlier this cycle), and record what this pass selects so
	// the next pass — and the upgrade pass — see the running total. A source at
	// budget contributes an empty batch (no goroutine, no dispatch), which is what
	// lets the drain loop terminate once every source is at budget or has no live
	// candidate.
	batched := make(map[string][]resolvedChapter, len(groups))
	for key, items := range groups {
		remaining := budget - consumed[key]
		if remaining <= 0 {
			continue
		}
		take := items[:min(len(items), remaining)]
		batched[key] = take
		consumed[key] += len(take)
	}

	// Per-cycle audit-event sink: each per-source download outcome is accumulated
	// (thread-safe) and flushed as ONE LogBatch after the cycle's goroutines join,
	// instead of N synchronous single-row inserts on the dispatch goroutines
	// (spec Write-volume: one LogBatch per cycle per-source outcome — mirrors the
	// refresh sweep's sink). The sink rides the context so tryCandidate reaches it
	// without threading it through the generic scheduler; nil recorder ⇒ no sink.
	sink := d.newEventSink()
	ctx = withEventSink(ctx, sink)

	// progressed counts chapters whose wanted/failed→downloading claim SUCCEEDED.
	// It is shared across every per-source and per-chapter goroutine (the scheduler
	// increments it on a claim), so it must be atomic; read once with .Load() after
	// all goroutines have joined.
	var progressed atomic.Int64
	d.runDownloadQueues(ctx, batched, concurrency, maxRetries, now, limiter, &progressed)
	d.flushEventSink(ctx, sink)
	return int(progressed.Load()), nil
}

// Process runs the full multi-source download pipeline for a single chapter by
// id, resolving the download policy itself and using a fresh per-provider limiter.
// RunOnce drives the ordered batch path (per-source scheduler + shared limiter);
// Process is the standalone single-chapter entry point, so it needs no scheduler.
func (d *Dispatcher) Process(ctx context.Context, chapterID uuid.UUID) error {
	maxRetries := d.retry.MaxRetries(ctx)
	now := time.Now()
	limiter := newProviderLimiter(d.downloadConcurrency(ctx))
	return d.processChapter(ctx, chapterID, maxRetries, now, limiter)
}

// processChapter executes the multi-source download pipeline for one chapter.
//
// It asks chapter.RankedLiveCandidates for the sources it may try right now
// (have the chapter, retry budget left, past their cooldown), ranked best-first.
//
//   - No live candidate → handleNoCandidates decides: leave the chapter wanted
//     (no source has it yet, or sources exist but are all on cooldown) OR mark it
//     permanently_failed (every source is exhausted).
//   - Otherwise → transition to downloading and try each candidate IN IMPORTANCE
//     ORDER, falling through to the next source the instant one fails (so reading
//     is never blocked on a broken preferred source). The first success wins:
//     render + persist + downloaded. Each failing source has its per-source retry
//     state bumped (attempts++, last_error, next_attempt_at). If every candidate
//     fails this cycle, finalizeAfterAllFailed marks the chapter failed (some
//     source may still recover) or permanently_failed (all sources now exhausted).
func (d *Dispatcher) processChapter(ctx context.Context, chapterID uuid.UUID, maxRetries int, now time.Time, limiter *providerLimiter) error {
	ch, err := d.client.Chapter.Query().
		Where(entchapter.IDEQ(chapterID)).
		WithSeries(func(sq *ent.SeriesQuery) { sq.WithCategory() }).
		Only(ctx)
	if err != nil {
		return fmt.Errorf("download.Dispatcher.processChapter: load chapter %s: %w", chapterID, err)
	}

	cands, err := chapter.RankedLiveCandidates(ctx, d.client, chapterID, maxRetries, now)
	if err != nil {
		return fmt.Errorf("download.Dispatcher.processChapter: rank candidates for chapter %s: %w", chapterID, err)
	}
	cands = d.filterGated(ctx, cands, now)
	if len(cands) == 0 {
		return d.handleNoCandidates(ctx, ch, maxRetries)
	}

	// Process is the single-chapter entry point; the forward-progress claim flag is
	// only meaningful for RunOnce's drain-loop accounting, so it is discarded here.
	_, err = d.runCandidates(ctx, ch, chapterID, cands, maxRetries, now, limiter)
	return err
}

// runCandidates transitions a chapter with at least one live candidate from
// wanted/failed → downloading (announcing download.start), then tries each
// candidate in importance order with immediate fall-through — the first success
// wins. If every candidate fails this cycle, finalizeAfterAllFailed decides failed
// vs permanently_failed from the freshly-bumped per-source state.
//
// It returns claimed=true the instant the atomic wanted/failed→downloading
// transition SUCCEEDS (before the fetch loop), and claimed=false only when that
// transition itself errors. This is the FORWARD-PROGRESS signal the drain loop
// relies on: a successful claim moves the chapter out of the wanted/failed set
// (so it is not re-selected next pass), whereas a failed claim means the chapter
// made no progress and — critically — must not be counted as dispatched, or a
// write-failing DB (reads succeed, the claim write fails) would keep re-selecting
// it forever and hot-spin drainDownloads.
//
// The caller MUST already hold the source's start slot (RunOnce's per-source
// scheduler acquires it; Process is single-chapter so contention cannot arise):
// this is what keeps the wanted→downloading transition gated behind slot
// acquisition, so a queued chapter stays wanted until it truly starts. ch must be
// loaded WithSeries(WithCategory()) for the render step.
func (d *Dispatcher) runCandidates(ctx context.Context, ch *ent.Chapter, chapterID uuid.UUID, cands []chapter.Candidate, maxRetries int, now time.Time, limiter *providerLimiter) (claimed bool, err error) {
	// Transition wanted / failed → downloading and announce the attempt. If this
	// write fails, the chapter is still wanted/failed — report claimed=false so the
	// caller does NOT count it as progress.
	if setErr := chapter.SetState(ctx, d.client, chapterID, entchapter.StateDownloading); setErr != nil {
		return false, fmt.Errorf("download.Dispatcher.runCandidates: transition to downloading for chapter %s: %w", chapterID, setErr)
	}
	d.broadcast("download.start", DownloadEvent{
		ChapterID: chapterID,
		State:     string(entchapter.StateDownloading),
	})

	var lastErr error
	for _, cand := range cands {
		done, cause := d.tryCandidate(ctx, ch, chapterID, cand, limiter, now)
		if done {
			return true, nil
		}
		lastErr = cause
	}

	// Every live source failed this cycle. Decide failed vs permanently_failed
	// from the CURRENT per-source state (the loop just bumped attempts). The claim
	// already succeeded, so this pass made progress regardless of the outcome.
	return true, d.finalizeAfterAllFailed(ctx, ch, maxRetries, lastErr)
}

// tryCandidate attempts a single source for a chapter: it fetches under the
// source's per-provider concurrency slot, and on success renders + persists +
// transitions the chapter to downloaded (returning done=true). It returns
// done=false with the cause on failure so the caller falls through to the next
// source. The concurrency slot is held only for the network fetch; rendering is
// local disk work and does not contend for the provider's API.
//
// Failure accounting (owner-ratified: error class drives TWO separate things —
// the per-(chapter,source) counter AND the circuit-breaker — see
// isChapterSpecificFailure):
//   - A CHAPTER-SPECIFIC fetch failure (not_found / no_pages / parse / broken page /
//     no live source) charges the source (chargeFetchFailure → bumpSourceFailure:
//     attempts++) and does NOT trip the breaker — this chapter is broken on this
//     source, so this source gives up on THIS chapter at max_retries while staying
//     available for its other chapters. A chapter goes terminal (permanently_failed)
//     once EVERY source offering it has exhausted its budget.
//   - A SOURCE-WIDE/ban fetch failure (rate_limit / captcha / timeout / network /
//     server_error / unknown) only cools the source down (chargeFetchFailure →
//     cooldownSource: next_attempt_at, attempts UNCHANGED) and DOES trip the breaker
//     (gateRecordFailure). The chapter is fine, so a ban never exhausts it; the
//     breaker (filterGated) EXCLUDES the tripped source so its chapters stay wanted
//     and burn NO attempts while it is down — this is the drain-prevention.
//   - A render/persist (finishDownload) failure is a LOCAL fault (disk/NFS/DB), NOT
//     the source's — the source is NOT charged at all and the breaker NOT touched, so
//     a persistent infra fault can never drain the library. The staging dir is KEPT
//     so the retry resumes.
//   - On SUCCESS the staging dir is deleted (its bytes are now in the CBZ).
func (d *Dispatcher) tryCandidate(ctx context.Context, ch *ent.Chapter, chapterID uuid.UUID, cand chapter.Candidate, limiter *providerLimiter, now time.Time) (done bool, cause error) {
	// Carry a per-chapter progress sink so the fetcher can report live per-page
	// progress; the sink throttles + broadcasts download.progress.
	pctx := fetcher.WithProgress(ctx, d.progressSink(chapterID, string(entchapter.StateDownloading)))
	sourceKey := canonicalSourceKey(cand.SeriesProvider)
	// Politeness delay BEFORE the fetch: enforces the runtime-tunable minimum
	// gap between successive requests to this physical source (independent of
	// the per-source concurrency cap below).
	d.gateWait(pctx, sourceKey)
	release := limiter.acquire(sourceKey)
	fetchStart := time.Now()
	pages, fetchErr := d.f.Fetch(pctx, buildFetchRef(cand.ProviderChapter, cand.SeriesProvider))
	fetchDuration := time.Since(fetchStart)
	release()

	// Write-through the resolved page links the instant they are known (even on a
	// byte-fetch failure), so a retry SKIPS the source's page-resolution step.
	d.persistPageLinks(ctx, cand.ProviderChapter, pages.PageLinks)

	if fetchErr != nil {
		d.logDownloadEvent(ctx, ch, cand.SeriesProvider, sourceevents.StatusFailed, fetchDuration, nil, fetchErr)
		d.chargeFetchFailure(ctx, cand.ProviderChapter, pages.StagingDir, fetchErr, now)
		// Circuit-breaker bookkeeping is SEPARATE from the per-chapter retry
		// state above: it tracks whether this source is down ENTIRELY (see
		// filterGated), not whether it can serve this one chapter. Recorded ONLY
		// for a SOURCE-WIDE/ban-class failure (shouldRecordGateFailure gates on
		// !isChapterSpecificFailure) — a broken-chapter failure must not pause the
		// whole source — and skipped on a shutdown-induced cancellation (parent ctx
		// done) so a graceful stop never trips the breaker.
		if shouldRecordGateFailure(ctx, fetchErr) {
			d.gateRecordFailure(ctx, sourceKey, fetchErr, now)
		}
		return false, fetchErr
	}

	if err := d.finishDownload(ctx, ch, chapterID, cand, pages); err != nil {
		// A render/persist failure is a LOCAL fault, NOT the source's — do NOT charge
		// the source (no bump, no cooldown) or a persistent infra fault would drain the
		// whole library in maxRetries cycles. Falling through keeps the chapter from
		// stranding in downloading; on retry RenderChapter safely upserts the CBZ from
		// the KEPT staging dir. The gate is deliberately NOT touched either — the fetch
		// itself succeeded, so this is not evidence the source is unavailable.
		d.logDownloadEvent(ctx, ch, cand.SeriesProvider, sourceevents.StatusFailed, fetchDuration, nil, err)
		slog.WarnContext(ctx, "download.tryCandidate: render/persist failed — not charging the source (local fault)",
			"chapter_id", chapterID,
			"err", err,
		)
		return false, err
	}

	// The staged bytes are now inside the CBZ — delete the staging dir so the byte
	// cache holds bytes only for in-progress chapters.
	d.cleanupStaging(ctx, pages.StagingDir)

	pageCount := pages.PageCount
	d.logDownloadEvent(ctx, ch, cand.SeriesProvider, sourceevents.StatusSuccess, fetchDuration, &pageCount, nil)
	d.gateRecordSuccess(ctx, sourceKey)
	return true, nil
}

// chargeFetchFailure applies the per-(chapter,source) retry accounting for a FETCH
// failure, CLASSIFIED (owner-ratified — see isChapterSpecificFailure):
//   - CHAPTER-SPECIFIC (this chapter is broken on this source) → bumpSourceFailure
//     (attempts++), so this source is abandoned for THIS chapter at max_retries.
//   - SOURCE-WIDE/ban (the source is down/blocking) → cooldownSource (next_attempt_at
//     only, attempts UNCHANGED), so a ban never spends the chapter's budget and never
//     drains the queue — the breaker (recorded separately by the caller) is what
//     holds a banned source out of candidacy.
//
// It is shared by BOTH the download path (tryCandidate) and the upgrade path
// (handleUpgradeFailure), so a fetch failure is accounted identically wherever it
// happens (an upgrade is a download).
//
// A not_found is treated as a STALE RESOLUTION: the stored page links, and the
// pages already staged from them, may point at moved/renamed resources, so BOTH are
// invalidated (links cleared, staging dir wiped) and the next attempt re-resolves +
// re-downloads from scratch. stagingDir is the failed fetch's staging directory ("" if none).
func (d *Dispatcher) chargeFetchFailure(ctx context.Context, pc *ent.ProviderChapter, stagingDir string, cause error, now time.Time) {
	if isChapterSpecificFailure(cause) {
		d.bumpSourceFailure(ctx, pc, cause, now)
	} else {
		d.cooldownSource(ctx, pc, cause, now)
	}
	if errorclass.Classify(cause) == errorclass.CategoryNotFound {
		// Wipe the staging dir FIRST and clear the page links ONLY if the wipe
		// succeeded: the two must stay consistent. If the links were cleared but the
		// (index-keyed) staged files survived, the next attempt would re-resolve a
		// fresh, possibly-reordered page list and pack it against those stale files —
		// the mismatched-page corruption FIX-2 guards on the upgrade path. Keeping the
		// links on a wipe failure means the next attempt re-uses the SAME links +
		// staging (consistent, if still stale); the not_found bump exhausts it anyway.
		if err := d.removeStaging(stagingDir); err != nil {
			slog.WarnContext(ctx, "download.chargeFetchFailure: staging wipe failed on not_found — keeping page links to avoid re-resolve against stale staging",
				"provider_chapter_id", pc.ID,
				"staging_dir", stagingDir,
				"err", err,
			)
			return
		}
		d.clearPageLinks(ctx, pc)
	}
}

// persistPageLinks write-throughs the freshly-resolved page links onto the
// ProviderChapter row so a retry skips the source's page-resolution step. It is
// best-effort (a write failure is logged, never fatal) and a NO-OP when the row
// already carries links (they were re-used this attempt, not re-resolved) or none
// were resolved.
func (d *Dispatcher) persistPageLinks(ctx context.Context, pc *ent.ProviderChapter, links []fetcher.PageLink) {
	if len(links) == 0 || len(pc.PageLinks) > 0 {
		return
	}
	if err := d.client.ProviderChapter.UpdateOneID(pc.ID).SetPageLinks(links).Exec(ctx); err != nil {
		slog.WarnContext(ctx, "download.persistPageLinks: could not persist resolved page links",
			"provider_chapter_id", pc.ID,
			"err", err,
		)
		return
	}
	pc.PageLinks = links
}

// clearPageLinks wipes the stored page links so the next attempt re-resolves them
// via the source — used when a not_found image failure signals the stored links
// have gone stale. Best-effort; a no-op when there are no stored links.
func (d *Dispatcher) clearPageLinks(ctx context.Context, pc *ent.ProviderChapter) {
	if len(pc.PageLinks) == 0 {
		return
	}
	if err := d.client.ProviderChapter.UpdateOneID(pc.ID).ClearPageLinks().Exec(ctx); err != nil {
		slog.WarnContext(ctx, "download.clearPageLinks: could not clear stale page links",
			"provider_chapter_id", pc.ID,
			"err", err,
		)
		return
	}
	pc.PageLinks = nil
}

// cleanupStaging removes a chapter's page-staging directory (best-effort): on a
// completed download/upgrade its bytes are now inside the rendered CBZ, and on a
// failed upgrade its stale index-keyed pages must not be reused. A leftover dir is
// bounded (in-progress chapters + a rare crash-window leak + the startup GC
// backstop), invisible to the library scanner, and re-used or re-cleaned on any
// later re-download of the chapter.
func (d *Dispatcher) cleanupStaging(ctx context.Context, dir string) {
	if err := d.removeStaging(dir); err != nil {
		slog.WarnContext(ctx, "download.cleanupStaging: could not remove staging dir",
			"staging_dir", dir,
			"err", err,
		)
	}
}

// removeStaging removes dir (recursively), returning any error so a caller that
// must react to a failure (chargeFetchFailure's not_found branch, which only
// clears the page links when the wipe SUCCEEDS) can. A blank dir is a no-op.
func (d *Dispatcher) removeStaging(dir string) error {
	if dir == "" {
		return nil
	}
	return os.RemoveAll(dir)
}

// cleanupChapterStaging removes the page-staging dirs of EVERY provider offering
// this chapter. It is called when a chapter reaches the terminal
// permanently_failed state, whose partial downloads will never be resumed (the
// owner must manually retry, which resets the per-source budgets and re-downloads
// from scratch). Best-effort — a query/remove failure is logged, never fatal — and
// a no-op when no staging root is configured (tests using the fake fetcher stage
// nothing on disk). The startup GC (GCStagingRoot) is the backstop for the sources
// this misses (a removed provider, a deleted series, a crash before this runs).
func (d *Dispatcher) cleanupChapterStaging(ctx context.Context, ch *ent.Chapter) {
	if d.cfg.StagingRoot == "" {
		return
	}
	pcIDs, err := d.client.ProviderChapter.Query().
		Where(
			entproviderchapter.ChapterKeyEQ(ch.ChapterKey),
			entproviderchapter.HasSeriesProviderWith(entseriesprovider.SeriesIDEQ(ch.SeriesID)),
		).
		IDs(ctx)
	if err != nil {
		slog.WarnContext(ctx, "download.cleanupChapterStaging: could not list provider chapters — staging dirs left for the startup GC",
			"chapter_id", ch.ID,
			"err", err,
		)
		return
	}
	for _, id := range pcIDs {
		d.cleanupStaging(ctx, filepath.Join(d.cfg.StagingRoot, id.String()))
	}
}

// finishDownload renders the fetched pages to disk, writes the success provenance
// (satisfied-by source + importance, filename, page count, download date), clears
// the chapter's last_error, resets the winning source's per-source retry state
// (it demonstrably works), and transitions the chapter to downloaded, broadcasting
// download.done. Any error is returned so tryCandidate can fall through.
func (d *Dispatcher) finishDownload(ctx context.Context, ch *ent.Chapter, chapterID uuid.UUID, cand chapter.Candidate, pages fetcher.ChapterPages) error {
	pc := cand.ProviderChapter
	sp := cand.SeriesProvider

	maxChap := maxChapterNumber(ctx, d.client, ch.SeriesID)
	filename, err := disk.RenderChapter(disk.RenderRequest{
		Storage: d.cfg.Storage,
		Meta:    buildRenderMeta(ch, pc, sp, maxChap),
		Pages:   pages.Pages,
	})
	if err != nil {
		return fmt.Errorf("render chapter %s: %w", chapterID, err)
	}

	if err := d.client.Chapter.UpdateOneID(chapterID).
		SetSatisfiedByProviderID(sp.ID).
		SetSatisfiedImportance(sp.Importance).
		SetFilename(filename).
		SetPageCount(pages.PageCount).
		SetDownloadDate(time.Now()).
		SetLastError("").
		Exec(ctx); err != nil {
		return fmt.Errorf("persist provenance for chapter %s: %w", chapterID, err)
	}

	// first_downloaded_at is WRITE-ONCE: the PREDICATE (not Go control flow) is what
	// enforces it, so there is no read-modify-write and no race. This also survives
	// the orphan-reset boot sweep — a chapter that is reset and re-downloaded keeps
	// its ORIGINAL arrival time, which is the honest answer to "when did this
	// chapter become readable". Deliberately NOT touched by upgrade.go's
	// persistUpgradeSuccess — an upgrade re-fetches an OLD chapter, it does not make
	// a new one readable.
	if _, err := d.client.Chapter.Update().
		Where(entchapter.ID(chapterID), entchapter.FirstDownloadedAtIsNil()).
		SetFirstDownloadedAt(time.Now()).
		Save(ctx); err != nil {
		return fmt.Errorf("stamp first_downloaded_at for chapter %s: %w", chapterID, err)
	}

	// The winning source works: clear its per-source retry state so a prior
	// transient failure never nudges it toward exhaustion.
	if err := d.client.ProviderChapter.UpdateOneID(pc.ID).
		SetAttempts(0).
		SetLastError("").
		ClearNextAttemptAt().
		Exec(ctx); err != nil {
		return fmt.Errorf("reset source retry state for chapter %s: %w", chapterID, err)
	}

	if err := chapter.SetState(ctx, d.client, chapterID, entchapter.StateDownloaded); err != nil {
		// Defensive path: only reachable on DB failure between the provenance
		// update and this transition. Returned so tryCandidate falls through
		// rather than stranding the chapter in downloading.
		return fmt.Errorf("transition chapter %s to downloaded: %w", chapterID, err)
	}

	d.broadcast("download.done", DownloadEvent{
		ChapterID: chapterID,
		State:     string(entchapter.StateDownloaded),
	})
	return nil
}

// bumpSourceFailure records one CHAPTER-SPECIFIC failed attempt against a single
// source's ProviderChapter row: attempts++, last_error, and next_attempt_at = now +
// backoffCurve(base) (the flat retry interval). It is charged for a failure that is
// specific to THIS chapter on THIS source (a broken page / not_found / no_pages /
// parse / no-live-source), on BOTH the download and upgrade paths — so this source
// is abandoned for this chapter at max_retries (and, on the upgrade path, stops
// being re-flagged, ending the downloaded↔upgrade_available oscillation). The
// backoff base is read at use-time so a settings change applies immediately.
// Per-source retry state lives ONLY on the ProviderChapter row (the Chapter-identity
// invariant keeps per-source state off the Chapter). A DB write failure is logged,
// not propagated — the cycle's other work must not be aborted by one source's
// bookkeeping error.
func (d *Dispatcher) bumpSourceFailure(ctx context.Context, pc *ent.ProviderChapter, cause error, now time.Time) {
	newAttempts := pc.Attempts + 1
	nextAttempt := now.Add(backoffCurve(d.retry.RetryBackoff(ctx)))
	if err := d.client.ProviderChapter.UpdateOneID(pc.ID).
		SetAttempts(newAttempts).
		SetLastError(cause.Error()).
		SetNextAttemptAt(nextAttempt).
		Exec(ctx); err != nil {
		slog.WarnContext(ctx, "download.bumpSourceFailure: could not persist per-source retry state",
			"provider_chapter_id", pc.ID,
			"err", err,
		)
		return
	}
	// Keep the in-memory row consistent with the DB for any later read this cycle.
	pc.Attempts = newAttempts
}

// cooldownSource records a SOURCE-WIDE/ban fetch failure against a source's
// ProviderChapter row WITHOUT spending its retry budget: it sets last_error and a
// backoff cooldown (next_attempt_at = now + backoffCurve(base), the flat retry
// interval) but leaves attempts UNCHANGED. It is charged (on BOTH the download and
// upgrade paths) when the SOURCE is down/blocking (rate_limit / captcha / timeout /
// network / server_error / unknown) rather than the chapter being broken: the
// chapter is fine, so a ban must never nudge it toward exhaustion or drain the
// queue — only defer the next try until the source recovers (and, for an upgrade
// target, keep it eligible so a preferred source temporarily down recovers as the
// swap target). The circuit-breaker, recorded separately by the caller, is what
// holds the whole source out of candidacy while it is down. A DB write failure is
// logged, not propagated.
func (d *Dispatcher) cooldownSource(ctx context.Context, pc *ent.ProviderChapter, cause error, now time.Time) {
	nextAttempt := now.Add(backoffCurve(d.retry.RetryBackoff(ctx)))
	if err := d.client.ProviderChapter.UpdateOneID(pc.ID).
		SetLastError(cause.Error()).
		SetNextAttemptAt(nextAttempt).
		Exec(ctx); err != nil {
		slog.WarnContext(ctx, "download.cooldownSource: could not persist per-source cooldown",
			"provider_chapter_id", pc.ID,
			"err", err,
		)
	}
}

// finalizeAfterAllFailed transitions a chapter out of downloading once every live
// source has failed this cycle. It re-reads the current per-source state (the
// candidate loop just bumped it): if every source offering the chapter is now
// exhausted, the chapter becomes permanently_failed; otherwise it becomes failed
// (some source is still on cooldown and may recover on a later cycle). It records
// the last cause as the chapter's last_error and broadcasts download.fail with the
// resting state.
func (d *Dispatcher) finalizeAfterAllFailed(ctx context.Context, ch *ent.Chapter, maxRetries int, cause error) error {
	chapterID := ch.ID
	exhausted, err := chapter.AllProvidersExhausted(ctx, d.client, chapterID, maxRetries)
	if err != nil {
		return fmt.Errorf("download.Dispatcher.finalizeAfterAllFailed: exhaustion check for chapter %s: %w", chapterID, err)
	}

	final := entchapter.StateFailed
	if exhausted {
		final = entchapter.StatePermanentlyFailed
	}
	if setErr := chapter.SetState(ctx, d.client, chapterID, final); setErr != nil {
		// Defensive path: only reachable on DB failure between the downloading
		// transition and this one — not reachable under normal operation.
		return fmt.Errorf("download.Dispatcher.finalizeAfterAllFailed: transition chapter %s to %s: %w", chapterID, final, setErr)
	}

	msg := "all live sources failed this cycle"
	if cause != nil {
		msg = cause.Error()
	}
	if err := d.client.Chapter.UpdateOneID(chapterID).SetLastError(msg).Exec(ctx); err != nil {
		slog.WarnContext(ctx, "download.finalizeAfterAllFailed: could not persist chapter last_error",
			"chapter_id", chapterID,
			"err", err,
		)
	}

	// Terminal state → the chapter's partial staged pages are dead weight; free them.
	if exhausted {
		d.cleanupChapterStaging(ctx, ch)
	}

	d.broadcast("download.fail", DownloadEvent{
		ChapterID: chapterID,
		State:     string(final),
		Error:     msg,
	})
	return nil
}

// handleNoCandidates handles a chapter that has no LIVE source to try this cycle.
// It distinguishes three cases:
//
//   - No source offers the chapter at all → leave it wanted and emit download.skip.
//     This is near-defensive (ingest always creates a ProviderChapter alongside a
//     Chapter) but reachable via a manual DB edit; the chapter awaits a source.
//   - Every source is exhausted → mark permanently_failed (from wanted or failed)
//     and broadcast download.fail. This is the sole permanent-failure entry that
//     does not pass through the download loop (the sources were already spent
//     before this cycle began).
//   - Sources exist, none exhausted, all on cooldown → nothing to do this cycle;
//     leave the chapter as-is (no state change, no event) and let a later cycle
//     retry the survivors once their backoff elapses.
func (d *Dispatcher) handleNoCandidates(ctx context.Context, ch *ent.Chapter, maxRetries int) error {
	hasAny, err := chapter.HasAnyProviderChapter(ctx, d.client, ch.ID)
	if err != nil {
		return fmt.Errorf("download.Dispatcher.handleNoCandidates: provider check for chapter %s: %w", ch.ID, err)
	}
	if !hasAny {
		// hasAny is false over the ignore-fractional-DROPPED set, so two very
		// different situations land here: a genuinely sourceless chapter (no feed
		// carries its key), and a fractional whose EVERY carrier the owner flagged
		// ignore_fractional. Only the latter must be parked in the terminal `ignored`
		// state — a wanted fractional no live source will ever fetch, left wanted,
		// clogs the queue and the chapter list forever. IsIgnorableFractional reads
		// the RAW carrier set to tell them apart (≥1 carrier, all ignored).
		return d.handleSourcelessChapter(ctx, ch)
	}

	exhausted, err := chapter.AllProvidersExhausted(ctx, d.client, ch.ID, maxRetries)
	if err != nil {
		return fmt.Errorf("download.Dispatcher.handleNoCandidates: exhaustion check for chapter %s: %w", ch.ID, err)
	}
	if !exhausted {
		// Sources remain but are all on cooldown — wait for the next cycle.
		return nil
	}

	if err := chapter.SetState(ctx, d.client, ch.ID, entchapter.StatePermanentlyFailed); err != nil {
		return fmt.Errorf("download.Dispatcher.handleNoCandidates: transition chapter %s to permanently_failed: %w", ch.ID, err)
	}
	const msg = "all sources exhausted their retry budget"
	if err := d.client.Chapter.UpdateOneID(ch.ID).SetLastError(msg).Exec(ctx); err != nil {
		slog.WarnContext(ctx, "download.handleNoCandidates: could not persist chapter last_error",
			"chapter_id", ch.ID,
			"err", err,
		)
	}
	// Terminal state → free any staged pages left by this chapter's sources.
	d.cleanupChapterStaging(ctx, ch)
	d.broadcast("download.fail", DownloadEvent{
		ChapterID: ch.ID,
		State:     string(entchapter.StatePermanentlyFailed),
		Error:     msg,
	})
	return nil
}

// handleSourcelessChapter handles a chapter that no LIVE (non-dropped) source
// offers this cycle. It distinguishes an all-carriers-ignored FRACTIONAL — parked
// in the terminal `ignored` state so it stops clogging the queue and the chapter
// list — from a genuinely sourceless chapter, which stays wanted (download.skip)
// awaiting a source via ingest.
//
// Parking is a STATE change, not a deletion: the Chapter row and every
// ProviderChapter feed row are kept (never-auto-delete), so the reconcile in
// series.SetIgnoreFractional returns the chapter to wanted the moment a
// non-ignoring carrier reappears. Doing it here (not only in the toggle sweep) is
// what stops a wanted/failed all-ignored fractional from re-accumulating between
// sweeps — every download cycle drains it.
func (d *Dispatcher) handleSourcelessChapter(ctx context.Context, ch *ent.Chapter) error {
	ignorable, err := chapter.IsIgnorableFractional(ctx, d.client, ch)
	if err != nil {
		return fmt.Errorf("download.Dispatcher.handleSourcelessChapter: ignorable check for chapter %s: %w", ch.ID, err)
	}
	if ignorable {
		if err := chapter.SetState(ctx, d.client, ch.ID, entchapter.StateIgnored); err != nil {
			return fmt.Errorf("download.Dispatcher.handleSourcelessChapter: transition chapter %s to ignored: %w", ch.ID, err)
		}
		d.broadcast("download.skip", DownloadEvent{
			ChapterID: ch.ID,
			State:     string(entchapter.StateIgnored),
			Error:     "every source for this fractional chapter is set to ignore fractionals",
		})
		return nil
	}

	slog.WarnContext(ctx, "download.processChapter: no provider for chapter — staying wanted until ingest supplies a source",
		"chapter_id", ch.ID,
	)
	d.broadcast("download.skip", DownloadEvent{
		ChapterID: ch.ID,
		State:     string(ch.State),
		Error:     "no provider available for this chapter",
	})
	return nil
}

// providerLimiter caps how many chapters may be FETCHED concurrently from the
// same physical source. It hands out one buffered-channel semaphore per source
// KEY (canonicalSourceKey = name-else-id, so both stored representations of one
// physical source share a single semaphore), each of capacity = the per-cycle
// DownloadConcurrency, so a single busy source can never monopolise the fetch pool
// while other sources proceed in parallel. Safe for concurrent use by the
// per-chapter goroutines.
//
// It is DISTINCT from the per-source start scheduler (schedule.go): the scheduler
// orders a source's primary chapters and gates their wanted→downloading
// transition, while this limiter bounds actual upstream fetch concurrency keyed by
// the source being fetched — so it also caps fall-through secondaries and the
// upgrade path, which the scheduler (keyed by primary source) does not cover. Both
// key by the SAME canonicalSourceKey, so the state-count cap and the fetch cap
// agree on what "one source" is. A chapter never acquires two slots of THIS limiter
// at once, and the scheduler's start channel is a separate object, so no
// self-deadlock is possible.
type providerLimiter struct {
	mu   sync.Mutex
	cap  int
	sems map[string]chan struct{}
}

// newProviderLimiter builds a limiter whose per-source concurrency is capacity
// (clamped to >= 1).
func newProviderLimiter(capacity int) *providerLimiter {
	if capacity < 1 {
		capacity = 1
	}
	return &providerLimiter{cap: capacity, sems: make(map[string]chan struct{})}
}

// acquire blocks until a concurrency slot for the given source key is free, then
// returns a release func the caller must invoke (once) to free the slot.
func (l *providerLimiter) acquire(sourceKey string) (release func()) {
	l.mu.Lock()
	sem, ok := l.sems[sourceKey]
	if !ok {
		sem = make(chan struct{}, l.cap)
		l.sems[sourceKey] = sem
	}
	l.mu.Unlock()

	sem <- struct{}{}
	return func() { <-sem }
}

// broadcast emits an SSE event of the given type with data as the JSON payload.
// JSON encoding errors are silently discarded — a missing SSE event is
// preferable to crashing the dispatcher goroutine.
func (d *Dispatcher) broadcast(eventType string, data DownloadEvent) {
	// Pre-marshal to ensure the Data field is a concrete type that the SSE
	// handler can JSON-encode without reflection surprises.
	raw, err := json.Marshal(data)
	if err != nil {
		// Defensive path: DownloadEvent contains only uuid.UUID and string fields,
		// so Marshal should never fail. Document as unreachable.
		return
	}
	d.hub.Broadcast(sse.Event{
		Type: eventType,
		Data: json.RawMessage(raw),
	})
}

// buildRenderMeta constructs a disk.RenderMeta from the Chapter, its
// best ProviderChapter, the owning SeriesProvider, and the series' max chapter
// number (for zero-padding).
//
// Known limitation (matches legacy Kaizoku.GO): as the series max grows,
// previously-rendered files keep their old (narrower) padding until re-rendered.
// Acceptable for M1.
// firstNonEmpty returns the first non-empty string in vals, or "" if all empty.
// Used to resolve the filename provider label: the source's display name when
// known, else its ID.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// canonicalSourceKey returns the single per-physical-source identity used to group
// chapters for the scheduler AND to key the fetch limiter: the source's display
// name (provider_name) when known, else its raw provider id. It mirrors
// series.ProviderLabel, kept local so the low-level download engine does not import
// the higher-level series domain package (10+ deps, and a latent import-cycle risk
// if series ever needs the engine); the package already resolves the identical
// label for the CBZ filename via buildRenderMeta.
//
// WHY the label, not the raw provider string: one physical source can be stored
// under TWO provider strings — the Suwayomi ingest path stores the numeric source
// id in `provider` with the display name in `provider_name`, while the disk-reconcile
// path stores that same display name in `provider` with an empty `provider_name`.
// Keying by the raw string makes two scheduler groups AND two fetch semaphores for
// the one source, each granted the full per-source concurrency cap ⇒ 2x the cap.
// Collapsing both to the shared label bounds the downloading-state count AND the
// concurrent fetches to the cap per physical source. The rare over-merge — two
// genuinely different sources that happen to share a display name — is accepted:
// they conservatively share one cap (owner-ratified).
//
// The result is TrimSpace'd: a Kaizoku-import disk provider is parsed from
// ComicInfo Publisher / the filename bracket (disk/kaizoku.go) and can carry
// leading/trailing whitespace, so "Comix " (disk) must still collapse onto "Comix"
// (the Suwayomi provider_name). Case is deliberately NOT folded — case-insensitive
// merging is a separate over-merge decision the owner has not taken.
func canonicalSourceKey(sp *ent.SeriesProvider) string {
	return strings.TrimSpace(firstNonEmpty(sp.ProviderName, sp.Provider))
}

func buildRenderMeta(ch *ent.Chapter, pc *ent.ProviderChapter, sp *ent.SeriesProvider, maxChapter *float64) disk.RenderMeta {
	seriesTitle := ""
	if ch.Edges.Series != nil {
		seriesTitle = ch.Edges.Series.Title
	}
	return disk.RenderMeta{
		Provider:            sp.Provider,
		ProviderLabel:       canonicalSourceKey(sp),
		Scanlator:           sp.Scanlator,
		Language:            sp.Language,
		SeriesTitle:         seriesTitle,
		Category:            seriesCategoryName(ch),
		Number:              pc.Number,
		MaxChapter:          maxChapter,
		ChapterName:         pc.Name,
		ChapterKey:          pc.ChapterKey,
		UploadDate:          pc.ProviderUploadDate,
		URL:                 pc.URL,
		WebURL:              pc.WebURL,
		Importance:          sp.Importance,
		SeriesProviderTitle: sp.Title,
	}
}

// seriesCategoryName resolves the on-disk category folder name for a chapter's
// series from its eagerly-loaded category edge. It is the SINGLE definition of
// "which folder does this chapter's CBZ live under" (§2 DRY), shared by
// buildRenderMeta (where the file is written) and tryDeleteOldCBZ (where the old
// file is removed) so a render and its later cleanup can never disagree on the
// folder — the F-C bug was tryDeleteOldCBZ hardcoding "Other" while the render
// used the real category, orphaning the old CBZ for any non-Other series.
//
// Callers MUST have loaded the series with its category edge
// (WithSeries(WithCategory())) — both process and Upgrade do. The disk.CategoryOther
// fallback is a documented DEFENSIVE last resort, reachable only if the series
// edge is unloaded/absent or the series is genuinely category-less (a pre-backfill
// state that no longer occurs — every series is category-linked at create time);
// a downloaded chapter must still render somewhere valid rather than panic.
func seriesCategoryName(ch *ent.Chapter) string {
	if ch.Edges.Series == nil {
		return disk.CategoryOther
	}
	if name := category.NameOf(ch.Edges.Series); name != "" {
		return name
	}
	return disk.CategoryOther
}

// buildFetchRef constructs a fetcher.FetchRef from a ProviderChapter and its
// owning SeriesProvider. It is the single place that maps provider-row fields
// to the fetch port's input type, shared by process and Upgrade so that no
// ref-building logic is duplicated.
//
// SuwayomiID comes from the ProviderChapter row (the per-chapter Suwayomi ID),
// not from SeriesProvider.SuwayomiID (which is the manga/series-level ID used
// for MangaChapters queries). The chapter-level ID is required by
// suwayomi.Fetcher.Fetch → client.ChapterPages(ctx, ref.SuwayomiID).
func buildFetchRef(pc *ent.ProviderChapter, sp *ent.SeriesProvider) fetcher.FetchRef {
	return fetcher.FetchRef{
		Provider:          sp.Provider,
		Scanlator:         sp.Scanlator,
		Language:          sp.Language,
		URL:               pc.URL,
		SuwayomiID:        pc.SuwayomiChapterID,
		SeriesProviderID:  sp.ID,
		ProviderChapterID: pc.ID,
		PageLinks:         pc.PageLinks,
	}
}

// maxChapterNumber returns the highest chapter number across all ProviderChapters
// for the given series, used to zero-pad CBZ filenames to consistent width.
// Returns nil if no numbered chapters exist for this series.
func maxChapterNumber(ctx context.Context, client *ent.Client, seriesID uuid.UUID) *float64 {
	var result []struct {
		Max *float64 `json:"max"`
	}
	err := client.ProviderChapter.Query().
		Where(
			entproviderchapter.HasSeriesProviderWith(
				entseriesprovider.SeriesIDEQ(seriesID),
			),
			entproviderchapter.NumberNotNil(),
		).
		Aggregate(ent.Max(entproviderchapter.FieldNumber)).
		Scan(ctx, &result)
	if err != nil || len(result) == 0 || result[0].Max == nil {
		// Defensive path: on query failure or no numbered chapters, fall back to
		// unpadded filenames — non-critical for correctness.
		return nil
	}
	return result[0].Max
}
