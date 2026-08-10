package download

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
)

// resolvedChapter is a wanted chapter paired with the live candidate sources
// resolved for it at the start of a cycle. RunOnce resolves these once, groups
// them by primary source, and hands each source's ordered list to a scheduler.
type resolvedChapter struct {
	chapterID uuid.UUID
	seriesID  uuid.UUID
	cands     []chapter.Candidate
}

// groupBySource resolves each wanted chapter's live candidates and partitions the
// chapters by their PRIMARY source — the highest-importance live candidate,
// which is cands[0] because RankedLiveCandidates is importance-DESC. Each
// source's slice is then reordered ROUND-ROBIN ACROSS SERIES (roundRobinBySeries)
// so one series' backlog can never starve another series sharing the same
// source — see roundRobinBySeries for the exact interleaving rule.
//
// A chapter with no live candidate never enters a group and never occupies a
// start slot: it is handled inline via handleNoCandidates (stays wanted when no
// source has it yet or all are on cooldown; permanently_failed when every source
// is exhausted). Per-chapter resolution errors are logged and skipped so one bad
// chapter cannot abort the whole cycle (matching the pre-scheduler behaviour where
// RunOnce discarded each goroutine's error).
//
// disabled is the owner's paused-source set (QCAT-513), read ONCE by RunOnceAt
// for the whole pass and passed in here rather than re-read per chapter — this
// loop runs over every wanted chapter in the pass, so a per-chapter read would be
// a straight N+1.
func (d *Dispatcher) groupBySource(ctx context.Context, chapters []*ent.Chapter, maxRetries int, now time.Time, disabled map[int64]bool) map[string][]resolvedChapter {
	groups := make(map[string][]resolvedChapter)
	for _, ch := range chapters {
		cands, err := chapter.RankedLiveCandidates(ctx, d.client, ch.ID, maxRetries, now, disabled)
		if err != nil {
			slog.WarnContext(ctx, "download.RunOnce: could not rank candidates — skipping chapter this cycle",
				"chapter_id", ch.ID,
				"err", err,
			)
			continue
		}
		// Exclude any candidate whose physical source is currently cooled down by
		// the source-politeness gate — a chapter whose ONLY live candidates are
		// all cooled down is handled exactly like "no live candidate" below (stays
		// wanted, never churned through downloading→failed).
		cands = d.filterGated(ctx, cands, now)
		if len(cands) == 0 {
			if err := d.handleNoCandidates(ctx, ch, maxRetries); err != nil {
				slog.WarnContext(ctx, "download.RunOnce: handleNoCandidates failed — skipping chapter this cycle",
					"chapter_id", ch.ID,
					"err", err,
				)
			}
			continue
		}
		// Key by the canonical physical-source label (name-else-id), NOT the raw
		// provider string: one physical source can be stored under two provider
		// strings (Suwayomi numeric id vs disk-reconcile name), and keying by the raw
		// string would give it two groups → two slot channels → 2x the per-source cap.
		key := canonicalSourceKey(cands[0].SeriesProvider)
		groups[key] = append(groups[key], resolvedChapter{chapterID: ch.ID, seriesID: ch.SeriesID, cands: cands})
	}
	for key, items := range groups {
		groups[key] = roundRobinBySeries(items)
	}
	return groups
}

// roundRobinBySeries reorders one source's chapter queue so that chapters
// interleave ACROSS SERIES instead of running strictly in the input's original
// (ascending chapter-number) order. Without this, a series that happens to be
// continuing at high chapter numbers (e.g. a source just added to an
// already-partway-through series, or a resumed series) sorts entirely AFTER
// every other series' lower-numbered backlog on the same source — starving it
// behind however large that backlog is.
//
// The algorithm: partition items by SeriesID, preserving each series' relative
// (already-ascending) order, then emit round-robin — series[0]'s first item,
// series[1]'s first item, …, series[0]'s second item, and so on — until every
// series is drained. Series rotation order is FIRST-APPEARANCE order in the
// input slice, i.e. whichever series has the lowest-numbered item overall goes
// first; this is deterministic and does not depend on map iteration order. Pure
// function, no ctx/DB — safe to unit-test in isolation.
func roundRobinBySeries(items []resolvedChapter) []resolvedChapter {
	if len(items) < 2 {
		return items
	}

	// bySeries preserves each series' relative order (stable partition); order
	// tracks first-appearance so rotation is deterministic.
	bySeries := make(map[uuid.UUID][]resolvedChapter, len(items))
	var order []uuid.UUID
	for _, it := range items {
		if _, seen := bySeries[it.seriesID]; !seen {
			order = append(order, it.seriesID)
		}
		bySeries[it.seriesID] = append(bySeries[it.seriesID], it)
	}
	if len(order) < 2 {
		return items // single series: already in the desired (number-ascending) order
	}

	out := make([]resolvedChapter, 0, len(items))
	for round := 0; len(out) < len(items); round++ {
		for _, sid := range order {
			queue := bySeries[sid]
			if round < len(queue) {
				out = append(out, queue[round])
			}
		}
	}
	return out
}

// runPerSourceQueues is THE per-source scheduler shared by the download pass
// (RunOnceAt) and the convergence-upgrade pass (UpgradeAll) — the one definition
// of "sources proceed in parallel, each source stays within its own cap" (§2 DRY).
//
// groups maps a canonicalSourceKey to that source's ORDERED queue of work items.
// Every source gets its own goroutine, so a saturated source blocks only its own
// queue and never a source with free slots (no cross-source head-of-line blocking).
// WITHIN one source, at most concurrency items run at a time and they are STARTED
// in queue order: an item begins only once one of the in-flight items of the SAME
// source finishes. Completions may still interleave — starts-in-order is the
// guarantee. The slot is held for the WHOLE item (fetch + render + persist), so at
// most concurrency of a source's chapters are in the downloading/upgrading state at
// once; the rest stay queued.
//
// The per-source cap is what preserves politeness: parallelism is added ACROSS
// sources only — a single source never runs more than `concurrency` items at once,
// exactly as before. It composes with (and does not replace) the deeper bounds:
// the per-provider fetch limiter (providerLimiter) and internal/sourcegate's
// min-request-delay + circuit-breaker still gate every actual upstream request.
//
// Cancellation: the first non-nil error from run cancels the derived context, and
// no further item is STARTED after that (in-flight ones drain). The guard is
// applied TWICE — before queueing and again as the first statement of the queued
// closure — because errgroup.Go BLOCKS on the per-source semaphore, so a closure
// queued just before the cancel would otherwise still run. A skipped item returns
// nil, so a cancellation never masquerades as a work error; the first real error is
// returned by Wait.
//
// globalSem, when non-nil, is a GLOBAL concurrency semaphore shared across EVERY
// source's goroutine: an item acquires one slot immediately before run and releases
// it immediately after, so the TOTAL number of concurrent run executions across all
// sources never exceeds the semaphore's weight — the aggregate cap the per-source
// SetLimit cannot express (with N sources the per-source caps alone permit
// concurrency×N in flight). A nil semaphore means "no global cap": behaviour is
// exactly the historical per-source-only scheduling (the standalone RunOnce path).
//
// Deadlock safety: a global slot is acquired ONLY around run and holds no per-source
// resource that another item needs in order to reach its own Release. Every item that
// acquires a global slot therefore runs to completion and releases it, so slots are
// always freed and forward progress is guaranteed — there is no circular wait between
// the global semaphore and the per-source SetLimit. Acquire is ctx-aware, so a
// cancelled context returns promptly (treated as a skip → nil) instead of parking on
// a slot that a cancelled sibling will never grant.
func runPerSourceQueues[T any](ctx context.Context, groups map[string][]T, concurrency int, run func(context.Context, T) error, globalSem *semaphore.Weighted) error {
	if concurrency < 1 {
		concurrency = 1
	}
	sources, sctx := errgroup.WithContext(ctx)
	for _, items := range groups {
		if len(items) == 0 {
			continue
		}
		sources.Go(func() error {
			return drainSourceQueue(sctx, items, concurrency, run, globalSem)
		})
	}
	return sources.Wait()
}

// drainSourceQueue runs ONE source's ordered item list, handing out at most
// `concurrency` start slots at a time. The slots are handed out in list order and
// blocking, which is what makes a source's queue ordered rather than a free-for-all.
// It stops enqueuing as soon as the group context is cancelled (a sibling source
// failed, or the parent went away) and returns the queue's first error.
func drainSourceQueue[T any](
	ctx context.Context,
	items []T,
	concurrency int,
	run func(context.Context, T) error,
	globalSem *semaphore.Weighted,
) error {
	queue, qctx := errgroup.WithContext(ctx)
	queue.SetLimit(concurrency) // the per-source cap: ordered, blocking hand-out of start slots
	for _, it := range items {
		if qctx.Err() != nil {
			break
		}
		queue.Go(func() error {
			return runQueuedItem(qctx, it, run, globalSem)
		})
	}
	return queue.Wait()
}

// runQueuedItem executes a single already-slotted item under the optional GLOBAL
// concurrency cap. Split out of drainSourceQueue's queue.Go closure so the
// scheduler's three nesting levels each read on their own; the semantics are
// unchanged — in particular the global slot is still held for the WHOLE run and
// released by defer on every exit path.
func runQueuedItem[T any](
	ctx context.Context,
	it T,
	run func(context.Context, T) error,
	globalSem *semaphore.Weighted,
) error {
	// The queue.Go call blocks on the per-source semaphore, so this may have been
	// queued before the cancel landed — re-check before doing work.
	if ctx.Err() != nil {
		return nil
	}
	// Global cap: hold one all-sources slot for the WHOLE run. Acquire is
	// ctx-aware, so a cancellation returns nil (a skip) rather than blocking.
	if globalSem != nil {
		if err := globalSem.Acquire(ctx, 1); err != nil {
			return nil
		}
		defer globalSem.Release(1)
	}
	return run(ctx, it)
}

// runDownloadQueues dispatches the whole pass's grouped chapters through the
// shared per-source scheduler, incrementing the RunOnce-wide forward-progress
// counter for each chapter whose wanted/failed→downloading claim SUCCEEDED — so
// RunOnce can return that count and the drain loop terminates on real progress
// rather than mere selection (see RunOnce).
//
// A download item never returns an error (per-chapter failures are recorded in the
// DB and swallowed by downloadResolved), so the scheduler's first-error
// cancellation is inert on this path: only a cancelled parent context stops it —
// hence the discarded error.
//
// globalSem is the cycle-shared GLOBAL cap (nil ⇒ per-source only), forwarded to the
// scheduler so this pass's fetches count against the same all-sources budget as every
// other pass and the upgrade pass.
func (d *Dispatcher) runDownloadQueues(ctx context.Context, groups map[string][]resolvedChapter, concurrency, maxRetries int, now time.Time, limiter *providerLimiter, progressed *atomic.Int64, globalSem *semaphore.Weighted) {
	_ = runPerSourceQueues(ctx, groups, concurrency,
		func(ctx context.Context, it resolvedChapter) error {
			if d.downloadResolved(ctx, it, maxRetries, now, limiter) {
				progressed.Add(1)
			}
			return nil
		}, globalSem)
}

// downloadResolved loads the full chapter (with its series + category edges for
// rendering) and runs its candidate loop. It is invoked only after the caller has
// acquired the source's start slot, so the wanted→downloading transition inside
// runCandidates is correctly gated behind slot acquisition. A per-chapter error is
// logged and swallowed so it cannot strand the source queue.
//
// It returns claimed=true only when the chapter successfully transitioned
// wanted/failed→downloading (forward progress); false if the chapter could not be
// loaded or the claim write itself failed. runSourceQueue counts the claimed ones.
func (d *Dispatcher) downloadResolved(ctx context.Context, it resolvedChapter, maxRetries int, now time.Time, limiter *providerLimiter) (claimed bool) {
	ch, err := d.client.Chapter.Query().
		Where(entchapter.IDEQ(it.chapterID)).
		WithSeries(func(sq *ent.SeriesQuery) { sq.WithCategory() }).
		Only(ctx)
	if err != nil {
		slog.WarnContext(ctx, "download.RunOnce: could not load chapter for download — skipping",
			"chapter_id", it.chapterID,
			"err", err,
		)
		return false
	}
	claimed, err = d.runCandidates(ctx, ch, it.chapterID, it.cands, maxRetries, now, limiter)
	if err != nil {
		slog.WarnContext(ctx, "download.RunOnce: chapter download did not complete cleanly",
			"chapter_id", it.chapterID,
			"err", err,
		)
	}
	return claimed
}
