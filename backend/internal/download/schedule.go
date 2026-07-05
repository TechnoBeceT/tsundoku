package download

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
)

// resolvedChapter is a wanted chapter paired with the live candidate sources
// resolved for it at the start of a cycle. RunOnce resolves these once, groups
// them by primary source, and hands each source's ordered list to a scheduler.
type resolvedChapter struct {
	chapterID uuid.UUID
	cands     []chapter.Candidate
}

// groupBySource resolves each wanted chapter's live candidates and partitions the
// chapters by their PRIMARY source — the highest-importance live candidate,
// which is cands[0] because RankedLiveCandidates is importance-DESC. The returned
// slices preserve the ascending chapter-number order of the input (WantedChapters
// orders by number), so each source's queue is already ordered for the scheduler.
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
		if len(cands) == 0 {
			if err := d.handleNoCandidates(ctx, ch, maxRetries); err != nil {
				slog.WarnContext(ctx, "download.RunOnce: handleNoCandidates failed — skipping chapter this cycle",
					"chapter_id", ch.ID,
					"err", err,
				)
			}
			continue
		}
		key := cands[0].SeriesProvider.Provider
		groups[key] = append(groups[key], resolvedChapter{chapterID: ch.ID, cands: cands})
	}
	return groups
}

// runSourceQueue dispatches one source's chapters in ascending number order, up
// to concurrency at a time, blocking on a buffered "start slot" channel before it
// begins each chapter. Because a single goroutine hands out the slots in order, a
// chapter starts only once one of the currently-downloading chapters finishes and
// only in queue (number) order — so downloads START low-number-first even though
// their completions may interleave. Each source calls this on its own goroutine,
// so a saturated source blocks only its own queue, never another source with free
// slots (no cross-source head-of-line blocking).
//
// The start slot is held for the WHOLE chapter (fetch + render + persist), so at
// most concurrency of a source's chapters are in the downloading state at once —
// the rest stay wanted (UI "Queued"). It is released when the chapter reaches a
// terminal state; because processing always returns, no slot is ever leaked and
// the send never blocks forever (no deadlock).
func (d *Dispatcher) runSourceQueue(ctx context.Context, items []resolvedChapter, concurrency, maxRetries int, now time.Time, limiter *providerLimiter) {
	slots := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for _, it := range items {
		slots <- struct{}{} // ordered, blocking: waits for a free slot before starting the next-in-number chapter
		wg.Add(1)
		go func(it resolvedChapter) {
			defer wg.Done()
			defer func() { <-slots }()
			d.downloadResolved(ctx, it, maxRetries, now, limiter)
		}(it)
	}
	wg.Wait()
}

// downloadResolved loads the full chapter (with its series + category edges for
// rendering) and runs its candidate loop. It is invoked only after the caller has
// acquired the source's start slot, so the wanted→downloading transition inside
// runCandidates is correctly gated behind slot acquisition. A per-chapter error is
// logged and swallowed so it cannot strand the source queue.
func (d *Dispatcher) downloadResolved(ctx context.Context, it resolvedChapter, maxRetries int, now time.Time, limiter *providerLimiter) {
	ch, err := d.client.Chapter.Query().
		Where(entchapter.IDEQ(it.chapterID)).
		WithSeries(func(sq *ent.SeriesQuery) { sq.WithCategory() }).
		Only(ctx)
	if err != nil {
		slog.WarnContext(ctx, "download.RunOnce: could not load chapter for download — skipping",
			"chapter_id", it.chapterID,
			"err", err,
		)
		return
	}
	if err := d.runCandidates(ctx, ch, it.chapterID, it.cands, maxRetries, now, limiter); err != nil {
		slog.WarnContext(ctx, "download.RunOnce: chapter download did not complete cleanly",
			"chapter_id", it.chapterID,
			"err", err,
		)
	}
}
