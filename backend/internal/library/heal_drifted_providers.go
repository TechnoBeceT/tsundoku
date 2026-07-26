package library

import (
	"context"
	"errors"
	"log/slog"
)

// HealDriftedProviders folds every already-drifted (disk-origin row, live twin)
// provider pair in the library back into ONE row, unattended. It is the recurring
// self-heal for source-identity drift (GAP-120): the same merge core the owner's
// DedupProviders uses, targeted at only the series that actually carry an unlinked
// disk-origin provider (driftedSeriesIDs) and run after every discovery sweep.
//
// # Why it has to exist
//
// Merge-at-attach (linkAttachedProvider) declines to fold a disk row into a live
// twin whose ProviderChapter feed is still EMPTY — merging into an unfetched
// source would relabel nothing and then drain the disk row, orphaning the
// downloaded chapters' provenance. That decline is correct, but it leaves the two
// rows coexisting, and NOTHING used to retry it. The discovery sweep skips a
// disk-origin row every pass (its provider is not a numeric source id), so that
// row's per-chapter URLs freeze at whatever the source served on import day. When
// the source later changes its URL scheme, every frozen chapter becomes
// permanently un-fetchable. Refresh is precisely what populates the live twin's
// feed, so re-attempting the merge straight after a sweep is the moment a
// previously-declined fold becomes possible.
//
// # This is an AUTOMATIC Rule 2 mutation path — owner-ratified 2026-07-26
//
// Wiring this into the sweep created a NEW unattended mutation path, and the
// repo's never-auto-delete rule enumerates those explicitly. It RENAMES CBZ files
// (disk.RelabelChapterFile) and DELETES the drained disk `SeriesProvider` row
// (deleteDrainedDiskProvider) with no owner action. It is safe by construction:
//
//   - NO CBZ is ever deleted. Files are relabeled to the live source's identity
//     and kept; only a DB row goes away. This is the load-bearing claim.
//   - The relabel is 2-phase and the DB phase is all-or-nothing, so a failed heal
//     never half-migrates a series: either every overlapping chapter is re-pointed
//     and relabeled, or none is (mergeDiskIntoLive → commitMatch's single tx).
//   - The rollback that unwinds an already-done rename is BEST-EFFORT, so "a
//     failed heal leaves the series byte-for-byte unchanged" holds in the common
//     case but is NOT a guarantee — see rollbackRelabels / restoreImportance for
//     the two residual states and why both are safe and self-correcting.
//   - It is idempotent. With no drifted pair it merges nothing and changes nothing.
//   - It makes ZERO live source calls, so it can neither trip an anti-ban breaker
//     nor consume a retry budget.
//   - The empty-feed orphan guard is honoured identically to the owner path (the
//     shared providerHasFeed gate, via pickTwin) — a pair whose live twin has no
//     feed is SKIPPED, never merged.
//
// # It never races an owner merge — it yields
//
// Before GAP-120 the per-series merge single-flight latch (acquireMerge) only had
// to arbitrate owner-vs-owner: two Match clicks, or a Match against a
// Consolidation. A background timer is a THIRD participant, and it targets exactly
// the drifted series the owner is most likely to be matching by hand. Two
// concurrent mergeDiskIntoLive calls on one series corrupt it: relabelMoveIntoPlace
// is idempotent, so the loser does not fail fast on the already-moved file — it
// proceeds, and only its commitMatch fails (the drained disk row is already gone),
// at which point its rollback renames every CBZ BACK to the old name while the
// winner's committed rows name the new one. Every file then sits where the DB is
// not looking.
//
// So this pass takes the SAME latch, per series, and SKIPS a series whose latch is
// already held rather than blocking or queueing (dedupOneSeries — the one latched
// entry to the dedup core, shared with both owner paths). An owner action always
// wins: it never waits behind an unattended pass, and the skipped series is
// re-examined by the very next sweep at no extra cost.
//
// # Importance: a merge adopts the HIGHER of the two ranks
//
// dedupDriftedPairs folds at max(liveSP.Importance, diskSP.Importance) so a disk
// row the owner deliberately re-ranked keeps that rank through the fold. When the
// live twin was ranked LOWER than the disk row, the fold therefore RAISES the
// twin's importance for the whole series, and chapters that some OTHER provider
// satisfies at a rank now below it become genuine upgrade candidates — so the same
// sweep's detection pass can flag them and the next download cycle will re-fetch
// them. That is the owner's own ranking being honoured, not a heal side effect,
// and it is identical to what the owner-triggered dedup has always done; but on
// this path it happens unattended, so it is stated here explicitly.
//
// # Cost, and why an unmatchable row cannot thrash
//
// A permanently-unmatchable disk row — one whose name will never equal a live
// source's display name (providerNameMatches is exact, case-insensitive equality
// after trimming) — is re-examined every sweep by design, because the owner may
// install or attach the matching source at any time and the fold must then fire.
// That re-examination is bounded and silent: findDriftedPair walks the series'
// providers in memory and pickTwin only touches the database (the feed-existence
// check) for a twin that ALREADY matched on name + scanlator. So an unmatchable
// row costs one provider-list read per sweep, performs no disk IO, writes nothing,
// and — because this pass logs per series only when a merge actually happened —
// emits no log line at all. Nothing accumulates, so there is nothing to thrash.
// Loosening the name match to catch those rows is deliberately out of scope: the
// strict rule is ratified (a stricter language guard was already rejected because
// disk rows carry language="").
//
// Errors are per-series contained: one failing series is logged and skipped, never
// aborting the pass. Returns the aggregate merged / skipped counts.
func (s *Service) HealDriftedProviders(ctx context.Context) (merged, skipped int, err error) {
	ids, err := s.driftedSeriesIDs(ctx)
	if err != nil {
		return 0, 0, err
	}
	if len(ids) == 0 {
		// The common case on a healthy library: one query, no work.
		return 0, 0, nil
	}

	for _, id := range ids {
		if ctx.Err() != nil {
			return merged, skipped, ctx.Err()
		}
		m, sk, ok, derr := s.dedupOneSeries(ctx, id)
		if !ok {
			// An owner merge holds this series' latch. Yield to it — the next
			// sweep re-examines the series for free.
			slog.DebugContext(ctx, "library.HealDriftedProviders: series skipped — an owner merge is already in flight",
				"series_id", id)
			continue
		}
		if errors.Is(derr, ErrSeriesNotFound) {
			// Deleted mid-pass — benign, skip.
			continue
		}
		if derr != nil {
			slog.WarnContext(ctx, "library.HealDriftedProviders: series heal failed, skipping",
				"series_id", id, "err", derr)
			continue
		}
		merged += m
		skipped += sk
		if m > 0 {
			slog.InfoContext(ctx, "library.HealDriftedProviders: folded drifted provider(s) into their live source",
				"series_id", id, "merged", m)
			// This is an UNATTENDED mutation of shared state (a provider row is
			// gone and CBZs were renamed), so it MUST push — a user with the
			// series open would otherwise keep showing a provider that no longer
			// exists until they reload. Reuses the SAME provider.merged event the
			// two owner merge paths emit, so the existing frontend listener
			// (refetch this series' detail) covers it unchanged.
			s.broadcastMerge(MergeEvent{SeriesID: id.String(), Merged: m, Skipped: sk})
		} else if sk > 0 {
			// The live twin still has no chapter feed. Expected and self-healing
			// (the next sweep that populates it merges), so DEBUG — logging this
			// at INFO would repeat every sweep for a source that stays broken.
			slog.DebugContext(ctx, "library.HealDriftedProviders: pair skipped — live twin has no chapter feed yet",
				"series_id", id, "skipped", sk)
		}
	}
	return merged, skipped, nil
}
