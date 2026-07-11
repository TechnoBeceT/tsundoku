package download

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

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
func (d *Dispatcher) groupBySource(ctx context.Context, chapters []*ent.Chapter, maxRetries int, now time.Time) map[string][]resolvedChapter {
	groups := make(map[string][]resolvedChapter)
	for _, ch := range chapters {
		cands, err := chapter.RankedLiveCandidates(ctx, d.client, ch.ID, maxRetries, now)
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
func runPerSourceQueues[T any](ctx context.Context, groups map[string][]T, concurrency int, run func(context.Context, T) error) error {
	if concurrency < 1 {
		concurrency = 1
	}
	sources, sctx := errgroup.WithContext(ctx)
	for _, items := range groups {
		if len(items) == 0 {
			continue
		}
		sources.Go(func() error {
			queue, qctx := errgroup.WithContext(sctx)
			queue.SetLimit(concurrency) // the per-source cap: ordered, blocking hand-out of start slots
			for _, it := range items {
				if qctx.Err() != nil {
					break
				}
				queue.Go(func() error {
					// The Go call above blocks on the semaphore, so this closure may have
					// been queued before the cancel landed — re-check before doing work.
					if qctx.Err() != nil {
						return nil
					}
					return run(qctx, it)
				})
			}
			return queue.Wait()
		})
	}
	return sources.Wait()
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
func (d *Dispatcher) runDownloadQueues(ctx context.Context, groups map[string][]resolvedChapter, concurrency, maxRetries int, now time.Time, limiter *providerLimiter, progressed *atomic.Int64) {
	_ = runPerSourceQueues(ctx, groups, concurrency,
		func(ctx context.Context, it resolvedChapter) error {
			if d.downloadResolved(ctx, it, maxRetries, now, limiter) {
				progressed.Add(1)
			}
			return nil
		})
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
