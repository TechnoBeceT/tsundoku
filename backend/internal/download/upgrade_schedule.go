package download

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
)

// unresolvedTargetKey is the group key for a flagged chapter that has NO live,
// non-gated upgrade target right now (its better source was exhausted, cooled
// down, breaker-tripped, or removed between DetectUpgrades and this pass). Such a
// chapter still MUST go through Upgrade: that is what resolves the stale
// upgrade_available flag back to downloaded (fetchAndRender finds no live source →
// handleUpgradeFailure restores the working copy). It fetches NOTHING, so it costs
// no source politeness budget; grouping these together keeps them off every real
// source's queue where they would otherwise consume a start slot.
const unresolvedTargetKey = ""

// UpgradeAll upgrades chapters currently in state=upgrade_available, with
// PER-SOURCE concurrency AND a per-source per-cycle VOLUME cap, returning the
// number of upgrades that completed without a hard error.
//
// # Why the per-cycle volume cap (the ban-loop fix)
//
// An upgrade fetch and a download fetch hit the SAME protected source endpoint, so
// they must draw from ONE per-source budget per cycle. downloadsConsumed carries
// how many of each physical source's chapters the DOWNLOAD drain already fetched
// this cycle (canonicalSourceKey → count, produced by job.Runner.drainDownloads);
// each upgrade-target source is then allowed at most
// max(0, batchPerSource(concurrency) - downloadsConsumed[key]) upgrades THIS cycle.
// The rest stay upgrade_available (already flagged) and are picked up next cycle, so
// the flagged count converges by at most the budget per cycle instead of the engine
// firing every eligible upgrade at once. Without this a library where hundreds of
// chapters are all upgrade_available toward one higher source (a fallback source's
// whole backlog) fetched that entire backlog in a single cycle, re-banning the
// source; the failed upgrades reverted to downloaded, DetectUpgrades re-flagged them,
// and the loop never converged. A nil/empty downloadsConsumed simply means no
// downloads ran this cycle — each source still gets the full per-cycle budget.
// The unresolvedTargetKey group (no live target) is never capped: it fetches
// nothing (it only resolves a stale flag), so it costs no source budget.
//
// # Why per-source (the throughput fix)
//
// Upgrades used to run under ONE GLOBAL concurrency limit, so a convergence wave
// aimed at several sources was serialised behind whichever source happened to fill
// the pool: five Comix upgrades could hold every slot while chapters targeting
// other sources sat idle. This mirrors the download path instead — chapters are
// grouped by their UPGRADE TARGET (the same best candidate bestUpgradeCandidate
// picks, i.e. the source the upgrade will actually fetch from) and each source's
// queue runs on its own goroutine through the SHARED per-source scheduler
// (runPerSourceQueues, §2 DRY). Different sources therefore progress in PARALLEL.
//
// # Why this cannot make any single source more aggressive (the anti-ban argument)
//
//   - Each source's queue is capped at DownloadConcurrency concurrent upgrades —
//     the SAME number that could previously target one source when it monopolised
//     the global pool. The per-source ceiling is unchanged; only the CROSS-source
//     parallelism is new.
//   - One providerLimiter is shared by the whole pass, so concurrent FETCHES against
//     one physical source (canonicalSourceKey, which collapses a source's numeric-id
//     and display-name rows into one identity) are capped at DownloadConcurrency even
//     if the grouping and the fetch-time pick disagree.
//   - internal/sourcegate still enforces the per-source minimum request delay before
//     every fetch and still excludes a source whose circuit-breaker is tripped (both
//     at grouping time via bestUpgradeCandidate and again inside fetchAndRender).
//     Rate per source is gate-bound, not pool-bound.
//
// Failure semantics are unchanged: an individual upgrade failure is handled inside
// Upgrade (working CBZ + provenance kept, chapter returns to downloaded, source
// cooled down without spending retry budget) and does NOT abort the pass. Only a
// hard infrastructure error propagates — it cancels the pass (no further upgrade is
// STARTED) and is returned along with the count of upgrades that did complete.
func (d *Dispatcher) UpgradeAll(ctx context.Context, downloadsConsumed map[string]int) (int, error) {
	chapters, err := d.client.Chapter.Query().
		Where(entchapter.StateEQ(entchapter.StateUpgradeAvailable)).
		// Stable order (chapter number ascending, nulls last, id tiebreak — the same
		// order the download path uses in chapter.WantedChapters): each target
		// source's group is built in this order, so the per-source cap deterministically
		// takes the lowest-numbered chapters first and the leftover set next cycle is
		// stable rather than Postgres-row-order-dependent.
		Order(
			entchapter.ByNumber(sql.OrderAsc(), sql.OrderNullsLast()),
			entchapter.ByID(),
		).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("download.Dispatcher.UpgradeAll: query upgrade_available chapters: %w", err)
	}
	if len(chapters) == 0 {
		return 0, nil
	}

	maxRetries := d.retry.MaxRetries(ctx)
	concurrency := d.downloadConcurrency(ctx)
	now := time.Now()

	groups := d.groupByUpgradeTarget(ctx, chapters, maxRetries, now)
	capUpgradeGroups(groups, batchPerSource(concurrency), downloadsConsumed)
	limiter := newProviderLimiter(concurrency)

	// Shared across every per-source goroutine — incremented once per upgrade that
	// returned nil, read after all goroutines have joined.
	var upgraded atomic.Int64
	err = runPerSourceQueues(ctx, groups, concurrency, func(ctx context.Context, chapterID uuid.UUID) error {
		if err := d.upgradeWith(ctx, chapterID, limiter); err != nil {
			return fmt.Errorf("download.Dispatcher.UpgradeAll: upgrade chapter %s: %w", chapterID, err)
		}
		upgraded.Add(1)
		return nil
	})
	return int(upgraded.Load()), err
}

// groupByUpgradeTarget partitions flagged chapters by the physical source their
// upgrade will fetch FROM — the highest-importance live, non-gated candidate
// (bestUpgradeCandidate: the exact pick fetchAndRender makes), keyed by
// canonicalSourceKey so a source stored under both its numeric id and its display
// name is ONE queue with ONE cap.
//
// A chapter whose target cannot be resolved right now (no live candidate, or the
// candidate query failed) lands in the unresolvedTargetKey group: it is still
// upgraded — which cleanly un-flags it — but never occupies a real source's slot.
//
// The target is recomputed by fetchAndRender at fetch time, so a chapter can in
// principle end up fetching from a different source than it was grouped under (an
// owner re-rank or a breaker trip landing mid-pass). The shared providerLimiter and
// the source gate bound the actual fetch rate in that case, so the drift costs
// scheduling accuracy, never politeness.
func (d *Dispatcher) groupByUpgradeTarget(ctx context.Context, chapters []*ent.Chapter, maxRetries int, now time.Time) map[string][]uuid.UUID {
	groups := make(map[string][]uuid.UUID)
	for _, ch := range chapters {
		best, err := bestUpgradeCandidate(ctx, d.client, d.gate, ch, maxRetries, now)
		if err != nil {
			slog.WarnContext(ctx, "download.UpgradeAll: could not rank upgrade candidates — running the chapter unqueued so its flag resolves",
				"chapter_id", ch.ID,
				"err", err,
			)
		}
		key := unresolvedTargetKey
		if best != nil {
			key = canonicalSourceKey(best.SeriesProvider)
		}
		groups[key] = append(groups[key], ch.ID)
	}
	return groups
}

// capUpgradeGroups truncates each REAL upgrade-target source's queue to the
// remainder of its shared per-cycle fetch budget: budget minus what the download
// drain already fetched from that physical source this cycle (consumed, keyed by
// canonicalSourceKey; a nil map reads as all-zero). Chapters dropped from a group
// stay upgrade_available and are handled next cycle. The unresolvedTargetKey group
// (no live target — it fetches nothing, only clears a stale flag) is left uncapped
// so a stale flag always resolves regardless of any source's budget. Groups are
// ordered lowest-chapter-number-first (see UpgradeAll's query), so truncation keeps
// the earliest chapters and defers the rest deterministically.
func capUpgradeGroups(groups map[string][]uuid.UUID, budget int, consumed map[string]int) {
	for key, ids := range groups {
		if key == unresolvedTargetKey {
			continue
		}
		remaining := budget - consumed[key]
		if remaining <= 0 {
			delete(groups, key)
			continue
		}
		if len(ids) > remaining {
			groups[key] = ids[:remaining]
		}
	}
}
