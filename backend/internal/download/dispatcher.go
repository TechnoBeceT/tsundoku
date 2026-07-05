// Package download implements the M1 state-driven download dispatcher.
//
// The dispatcher loads all actionable chapters (state=wanted, or state=failed
// with retry budget remaining), fetches their pages via the ChapterFetcher
// port, renders them to disk via the disk.RenderChapter renderer, and advances
// chapter state through the state machine. Per-provider concurrency is capped
// via buffered-channel semaphores so that a single provider cannot monopolise
// the worker pool.
package download

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
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
	// RetryBackoff is the BASE delay fed to backoffCurve, applied PER SOURCE. The
	// delay before a source's retry attempt n is base×2^n (capped at 1h): the first
	// retry (n=1) = 2×base, n=2 = 4×base, and so on.
	RetryBackoff(ctx context.Context) time.Duration
	// DownloadConcurrency is the PER-SOURCE download concurrency cap: how many of a
	// source's chapters the dispatcher fetches in parallel (and, equivalently, how
	// many of that source's queued chapters may be in the downloading state at
	// once). Read once per cycle for the scheduler + fetch limiter; clamped to >= 1.
	DownloadConcurrency(ctx context.Context) int
}

// backoffCurve returns the delay for the given attempt: base×2^attempt, capped at
// 1 hour. So attempt 0 yields base, the first retry (attempt=1) yields 2×base,
// attempt=2 yields 4×base, and so on. A base of 0 yields 0 (immediate retry — used
// by tests). It is the single backoff curve, parameterised by the runtime base
// instead of a hardcoded constant.
//
// Overflow analysis: shift is capped at 12 so base×2^12 stays well within int64
// (even base=1h ⇒ 1h×4096 ≈ 1.5e16 ns << int64 max ≈9.2e18 ns); the hour ceiling
// then clamps the result.
func backoffCurve(base time.Duration, attempt int) time.Duration {
	shift := attempt
	if shift > 12 {
		shift = 12
	}
	d := base * (1 << uint(shift)) //nolint:gosec // shift is capped at 12; base×2^12 << int64 max.
	if d > time.Hour {
		d = time.Hour
	}
	return d
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
// RunOnce to process all currently actionable chapters.
type Dispatcher struct {
	client *ent.Client
	f      fetcher.ChapterFetcher
	hub    *sse.Hub
	cfg    Config
	retry  RetrySettings
}

// New creates a Dispatcher configured with the given client, fetcher, SSE hub,
// structural Config, and runtime RetrySettings. The download policy (max-retries,
// backoff base, and per-source concurrency) is read from retry at use-time, never
// captured here.
func New(client *ent.Client, f fetcher.ChapterFetcher, hub *sse.Hub, cfg Config, retry RetrySettings) *Dispatcher {
	return &Dispatcher{
		client: client,
		f:      f,
		hub:    hub,
		cfg:    cfg,
		retry:  retry,
	}
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

// RunOnce loads every actionable chapter (state wanted or failed) and processes
// them per source: each source's chapters are dispatched in ascending chapter
// number, up to DownloadConcurrency at a time, with the sources running in
// parallel. It waits for all chapters to finish before returning. Per-chapter
// outcomes (success, source failure, permanent failure) are recorded in the DB
// and broadcast via SSE, not propagated to the caller — only a hard
// infrastructure failure loading the work list is returned. Callers can run this
// method in a ticker loop.
//
// The download policy is read ONCE here so every chapter in the cycle sees a
// consistent snapshot; a settings change therefore applies from the next cycle:
//
//   - maxRetries + now — the per-source retry budget + cooldown horizon.
//   - concurrency — the per-source start cap (scheduler) AND the per-provider
//     fetch cap (limiter).
//
// A chapter stays in the wanted state (UI "Queued") until the scheduler acquires
// a start slot for it — only then does it transition to downloading — so at any
// moment only up to DownloadConcurrency of a source's chapters are downloading and
// the rest remain queued, draining in ascending order (see schedule.go).
func (d *Dispatcher) RunOnce(ctx context.Context) error {
	maxRetries := d.retry.MaxRetries(ctx)
	now := time.Now()
	concurrency := d.downloadConcurrency(ctx)

	chapters, err := chapter.WantedChapters(ctx, d.client, 1000)
	if err != nil {
		return fmt.Errorf("download.Dispatcher.RunOnce: load chapters: %w", err)
	}
	if len(chapters) == 0 {
		return nil
	}

	// Resolve each chapter's live candidates and partition by primary source
	// (highest-importance live candidate). No-candidate chapters are handled here
	// and never occupy a start slot. The limiter is shared across the whole cycle
	// so a provider's fetch cap holds even for fall-through candidates.
	groups := d.groupBySource(ctx, chapters, maxRetries, now)
	limiter := newProviderLimiter(concurrency)

	var wg sync.WaitGroup
	for _, items := range groups {
		wg.Add(1)
		go func(items []resolvedChapter) {
			defer wg.Done()
			d.runSourceQueue(ctx, items, concurrency, maxRetries, now, limiter)
		}(items)
	}
	wg.Wait()
	return nil
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
	if len(cands) == 0 {
		return d.handleNoCandidates(ctx, ch, maxRetries)
	}

	return d.runCandidates(ctx, ch, chapterID, cands, maxRetries, now, limiter)
}

// runCandidates transitions a chapter with at least one live candidate from
// wanted/failed → downloading (announcing download.start), then tries each
// candidate in importance order with immediate fall-through — the first success
// wins. If every candidate fails this cycle, finalizeAfterAllFailed decides failed
// vs permanently_failed from the freshly-bumped per-source state.
//
// The caller MUST already hold the source's start slot (RunOnce's per-source
// scheduler acquires it; Process is single-chapter so contention cannot arise):
// this is what keeps the wanted→downloading transition gated behind slot
// acquisition, so a queued chapter stays wanted until it truly starts. ch must be
// loaded WithSeries(WithCategory()) for the render step.
func (d *Dispatcher) runCandidates(ctx context.Context, ch *ent.Chapter, chapterID uuid.UUID, cands []chapter.Candidate, maxRetries int, now time.Time, limiter *providerLimiter) error {
	// Transition wanted / failed → downloading and announce the attempt.
	if err := chapter.SetState(ctx, d.client, chapterID, entchapter.StateDownloading); err != nil {
		return fmt.Errorf("download.Dispatcher.runCandidates: transition to downloading for chapter %s: %w", chapterID, err)
	}
	d.broadcast("download.start", DownloadEvent{
		ChapterID: chapterID,
		State:     string(entchapter.StateDownloading),
	})

	var lastErr error
	for _, cand := range cands {
		done, cause := d.tryCandidate(ctx, ch, chapterID, cand, limiter, now)
		if done {
			return nil
		}
		lastErr = cause
	}

	// Every live source failed this cycle. Decide failed vs permanently_failed
	// from the CURRENT per-source state (the loop just bumped attempts).
	return d.finalizeAfterAllFailed(ctx, chapterID, maxRetries, lastErr)
}

// tryCandidate attempts a single source for a chapter: it fetches under the
// source's per-provider concurrency slot, and on success renders + persists +
// transitions the chapter to downloaded (returning done=true). On any failure —
// fetch, render, or persist — it bumps that source's per-source retry state and
// returns done=false with the cause, so the caller falls through to the next
// source. The concurrency slot is held only for the network fetch; rendering is
// local disk work and does not contend for the provider's API.
func (d *Dispatcher) tryCandidate(ctx context.Context, ch *ent.Chapter, chapterID uuid.UUID, cand chapter.Candidate, limiter *providerLimiter, now time.Time) (done bool, cause error) {
	// Carry a per-chapter progress sink so the suwayomi fetcher can report live
	// per-page progress; the sink throttles + broadcasts download.progress.
	pctx := fetcher.WithProgress(ctx, d.progressSink(chapterID, string(entchapter.StateDownloading)))
	release := limiter.acquire(canonicalSourceKey(cand.SeriesProvider))
	pages, fetchErr := d.f.Fetch(pctx, buildFetchRef(cand.ProviderChapter, cand.SeriesProvider))
	release()
	if fetchErr != nil {
		d.bumpSourceFailure(ctx, cand.ProviderChapter, fetchErr, now)
		return false, fetchErr
	}

	if err := d.finishDownload(ctx, ch, chapterID, cand, pages); err != nil {
		// A render/persist failure is not the source's fault, but bumping it (and
		// falling through) keeps the chapter from stranding in downloading and lets
		// another source deliver it. On retry RenderChapter safely upserts the CBZ.
		d.bumpSourceFailure(ctx, cand.ProviderChapter, err, now)
		return false, err
	}
	return true, nil
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

// bumpSourceFailure records one failed attempt against a single source's
// ProviderChapter row: attempts++, last_error, and next_attempt_at = now +
// backoffCurve(base, newAttempts). The backoff base is read at use-time so a
// settings change applies immediately. Per-source retry state lives ONLY on the
// ProviderChapter row (the Chapter-identity invariant keeps per-source state off
// the Chapter). A DB write failure is logged, not propagated — the cycle's other
// work must not be aborted by one source's bookkeeping error.
func (d *Dispatcher) bumpSourceFailure(ctx context.Context, pc *ent.ProviderChapter, cause error, now time.Time) {
	newAttempts := pc.Attempts + 1
	nextAttempt := now.Add(backoffCurve(d.retry.RetryBackoff(ctx), newAttempts))
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

// cooldownSource records an UPGRADE fetch failure against a source's
// ProviderChapter row WITHOUT spending its retry budget: it sets last_error and a
// backoff cooldown (next_attempt_at = now + backoffCurve(base, 1)) but leaves
// attempts UNCHANGED. This is the deliberate asymmetry with bumpSourceFailure: a
// download failure sticks (attempts increments toward exhaustion, so the owner's
// "give up on a chapter this source can't provide" is honoured), whereas an
// upgrade failure only defers the next try — the engine must NEVER permanently
// give up on IMPROVING a chapter to a better source, so a preferred source that is
// temporarily down during upgrade attempts recovers as an upgrade target once it
// is back and past its cooldown. A DB write failure is logged, not propagated.
func (d *Dispatcher) cooldownSource(ctx context.Context, pc *ent.ProviderChapter, cause error, now time.Time) {
	nextAttempt := now.Add(backoffCurve(d.retry.RetryBackoff(ctx), 1))
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
func (d *Dispatcher) finalizeAfterAllFailed(ctx context.Context, chapterID uuid.UUID, maxRetries int, cause error) error {
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
	d.broadcast("download.fail", DownloadEvent{
		ChapterID: ch.ID,
		State:     string(entchapter.StatePermanentlyFailed),
		Error:     msg,
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
		Provider:         sp.Provider,
		Scanlator:        sp.Scanlator,
		Language:         sp.Language,
		URL:              pc.URL,
		SuwayomiID:       pc.SuwayomiChapterID,
		SeriesProviderID: sp.ID,
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
