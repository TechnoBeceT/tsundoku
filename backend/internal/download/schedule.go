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

// resolvedChapter is a wanted/failed chapter paired with both its PostgreSQL row
// generation and the live candidate sources resolved for it at the start of a
// pass. Admission requires that exact state/generation and revalidates every
// candidate's current retry/cooldown plus breaker state before its engine call.
// A queued item can therefore never claim a later failed generation with this
// old slice or fall through to a source that became ineligible meanwhile.
type resolvedChapter struct {
	chapterID      uuid.UUID
	seriesID       uuid.UUID
	selectedState  entchapter.State
	workGeneration string
	cands          []chapter.Candidate
	sourceID       int64
}

// groupBySource resolves the selected batch's live candidates in one bulk load and
// partitions the chapters by their PRIMARY source — the highest-importance live
// candidate, which is cands[0] because RankedLiveCandidates is importance-DESC. Each
// source's slice is then reordered ROUND-ROBIN ACROSS SERIES (roundRobinBySeries)
// so one series' backlog can never starve another series sharing the same
// source — see roundRobinBySeries for the exact interleaving rule.
//
// A chapter with no live candidate never enters a group and never occupies a
// start slot: it is handled inline via handleNoCandidates (stays wanted when no
// source has it yet or all are on cooldown; permanently_failed when every source
// is exhausted). A bulk resolution error is logged and skips this pass, matching
// the old path where the same database failure made every per-chapter resolution
// fail and be skipped.
//
// disabled is the owner's paused-source set (QCAT-513), read ONCE by RunOnceAt
// for the whole pass and passed in here rather than re-read per chapter — this
// loop runs over every wanted chapter in the pass, so a per-chapter read would be
// a straight N+1.
func (d *Dispatcher) groupBySource(ctx context.Context, selections []chapter.Selection, maxRetries int, now time.Time, disabled map[int64]bool) map[string][]resolvedChapter {
	groups := make(map[string][]resolvedChapter)
	chapters := make([]*ent.Chapter, len(selections))
	for i, selection := range selections {
		chapters[i] = selection.Chapter
	}
	candsByChapter, err := chapter.RankedLiveCandidatesForMany(ctx, d.client, chapters, maxRetries, now, disabled)
	if err != nil {
		slog.WarnContext(ctx, "download.RunOnce: could not rank candidates — skipping selected batch this cycle",
			"err", err,
		)
		return groups
	}

	for _, selection := range selections {
		ch := selection.Chapter
		cands := candsByChapter[ch.ID]
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
		groups[key] = append(groups[key], resolvedChapter{
			chapterID:      ch.ID,
			seriesID:       ch.SeriesID,
			selectedState:  ch.State,
			workGeneration: selection.Generation,
			cands:          cands,
			sourceID:       linkedSourceID(cands[0].SeriesProvider),
		})
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
// The global fetch cap is intentionally NOT held here: one scheduled item can
// wait in the source-politeness gate before it makes an engine call. The actual
// fetch boundary acquires the global semaphore after that wait, so a delayed
// source cannot consume global capacity needed by a healthy source.
func runPerSourceQueues[T any](ctx context.Context, groups map[string][]T, concurrencyFor func(string) int, run func(context.Context, string, T) error) error {
	sources, sctx := errgroup.WithContext(ctx)
	for sourceKey, items := range groups {
		if len(items) == 0 {
			continue
		}
		concurrency := clampConcurrency(concurrencyFor(sourceKey))
		sources.Go(func() error {
			return drainSourceQueue(sctx, items, concurrency, func(ctx context.Context, item T) error {
				return run(ctx, sourceKey, item)
			})
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
) error {
	queue, qctx := errgroup.WithContext(ctx)
	queue.SetLimit(concurrency) // the per-source cap: ordered, blocking hand-out of start slots
	for _, it := range items {
		if qctx.Err() != nil {
			break
		}
		queue.Go(func() error {
			return runQueuedItem(qctx, it, run)
		})
	}
	return queue.Wait()
}

// runQueuedItem executes one already-slotted item. Split out of
// drainSourceQueue's queue.Go closure so the scheduler's three nesting levels
// each read on their own. Global admission is intentionally lower, at the
// actual engine-fetch boundary after source politeness has completed.
func runQueuedItem[T any](ctx context.Context, it T, run func(context.Context, T) error) error {
	// The queue.Go call blocks on the per-source semaphore, so this may have been
	// queued before the cancel landed — re-check before doing work.
	if ctx.Err() != nil {
		return nil
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
// globalSem is the cycle-shared GLOBAL cap (nil ⇒ per-source only), passed to
// downloadResolved so only an actual engine call acquires it after the source
// politeness wait.
func (d *Dispatcher) runDownloadQueues(ctx context.Context, groups map[string][]resolvedChapter, policy sourceConcurrencyPolicy, maxRetries int, now time.Time, limiter *providerLimiter, progressed *atomic.Int64, globalSem *semaphore.Weighted) {
	_ = runPerSourceQueues(ctx, groups, func(key string) int {
		return policy.For(groups[key][0].sourceID)
	},
		func(ctx context.Context, _ string, it resolvedChapter) error {
			if d.downloadResolved(ctx, it, maxRetries, now, limiter, globalSem) {
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
// It returns claimed=true only when the exact selected generation successfully
// transitioned wanted/failed→downloading (forward progress); false if the chapter
// could not be loaded, the claim write failed, or a newer chapter/candidate/breaker
// state invalidated the queued selection. runSourceQueue counts only claimed work.
func (d *Dispatcher) downloadResolved(ctx context.Context, it resolvedChapter, maxRetries int, now time.Time, limiter *providerLimiter, globalSem *semaphore.Weighted) (claimed bool) {
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
	claimed, err = d.runCandidates(ctx, ch, it.chapterID, it.selectedState, it.workGeneration, it.cands, maxRetries, now, limiter, globalSem)
	if err != nil {
		slog.WarnContext(ctx, "download.RunOnce: chapter download did not complete cleanly",
			"chapter_id", it.chapterID,
			"err", err,
		)
	}
	return claimed
}
