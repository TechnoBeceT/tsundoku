package library

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/series"
)

// ConsolidateTarget describes the ONE provider every selected provider is folded
// INTO by ConsolidateProviders. It is a discriminated union with exactly one arm:
//   - ExistingProviderID != nil → fold into an existing (feed-bearing) provider
//     already on the series (e.g. Ranker: fold the disk QiScans into the real one).
//   - ExistingProviderID == nil → match-to-real-source: attach/ingest the source
//     {Source,URL,Scanlator} first (reusing attachRealSource, the same path the
//     single Match uses), then fold the selected disk providers into it (e.g.
//     KaliScan: io/.me/.com → the live KaliScan). Importance is the rank the new
//     survivor is elevated to.
//
// The handler validates that exactly one arm is set; the service trusts that.
type ConsolidateTarget struct {
	// ExistingProviderID selects an existing provider as the survivor. nil ⇒ the
	// match-to-source arm (Source/URL/Scanlator/Importance below).
	ExistingProviderID *uuid.UUID
	// Source/URL/Scanlator identify the engine-host source to attach as the new
	// survivor (match-to-source arm). Importance is the rank to give it.
	Source     string
	URL        string
	Scanlator  string
	Importance int
}

// SkippedProvider records one selected provider that ConsolidateProviders could
// not fold, with a caller-safe reason (never a raw error chain — see
// safeMergeError). Per-provider fault isolation: a bad provider is reported here
// and does NOT abort the rest of the consolidation.
type SkippedProvider struct {
	ProviderID uuid.UUID `json:"providerId"`
	Reason     string    `json:"reason"`
}

// ConsolidateResult is the outcome of a ConsolidateProviders run: how many
// providers were folded into the survivor, which were skipped (fault-isolated),
// and the survivor's SeriesProvider id.
type ConsolidateResult struct {
	Merged     int               `json:"merged"`
	Skipped    []SkippedProvider `json:"skipped,omitempty"`
	SurvivorID uuid.UUID         `json:"survivorId"`
}

// ConsolidateProviders folds every provider in mergeIDs into ONE survivor (the
// target) for a single series, WITHOUT re-downloading — the per-series
// multi-provider consolidation behind POST /api/series/:id/providers/consolidate
// (QCAT-295 Part B). It is the SYNCHRONOUS core; StartConsolidateProviders
// (consolidate_async.go) runs it detached with a 202/single-flight/SSE wrapper.
//
// It reuses the SAME hardened merge primitive the single Match uses
// (mergeDiskIntoLive → relabelOverlap/commitMatch): each selected disk provider's
// overlapping CBZs are relabeled to the survivor's identity, its chapters
// re-pointed (satisfied_by/importance/filename), and the drained SeriesProvider
// row + its (empty) feed deleted — the sanctioned merge deletion; CBZs are
// relabeled, NEVER deleted (never-auto-delete honoured).
//
// SERIAL BY DESIGN (QCAT-295 Part A, Finding 4). The providers are folded ONE AT
// A TIME, never concurrently: two providers carrying a chapter of the same NUMBER
// relabel to the IDENTICAL new filename (the filename derives from the number),
// so concurrent os.Rename calls would race and one CBZ would be silently
// overwritten/lost. Folding serially means each relabel observes the prior fold's
// on-disk result — the idempotent RelabelChapterFile (OLD-gone + NEW-exists →
// skip) makes an already-moved file a no-op, and any same-number rename resolves
// deterministically to exactly one CBZ per number with no torn state.
//
// Per-provider FAULT ISOLATION: a selected provider that is missing, already a
// linked live source, or whose fold fails is recorded in result.Skipped with a
// caller-safe reason and the loop continues — one bad provider never aborts the
// rest. IDEMPOTENT on retry: an already-merged provider is simply "not in series"
// (skipped), the idempotent relabel handles half-relabeled CBZs, and
// attachRealSource is a no-op on a source already attached.
//
// SURVIVOR IMPORTANCE (owner-approved default): the survivor carries the target's
// importance (the existing provider's own, or the match spec's), and then EVERY
// remaining provider is re-densified via series.normalizeRanks (a clean, gap-free
// descending spread) — so folding N providers away never leaves importance holes.
// The re-densify is safe against a spurious re-download: the survivor satisfies
// its own re-pointed chapters, so download.DetectUpgrades' self-churn guard +
// stale-watermark heal keep it from ever flagging them (importance == effective
// satisfied importance for the satisfier itself).
//
// A non-nil error is returned only for a HARD failure (series load, target
// resolution/attach, DB re-densify) — never for a per-provider fold failure,
// which is fault-isolated into result.Skipped.
func (s *Service) ConsolidateProviders(ctx context.Context, seriesID uuid.UUID, mergeIDs []uuid.UUID, target ConsolidateTarget) (ConsolidateResult, error) {
	// WithCategory so mergeDiskIntoLive → relabelOverlap can resolve the on-disk
	// series folder <storage>/<Category>/<Title>/. The category never changes
	// across folds, so this single load is reused for the whole serial loop.
	row, err := s.db.Series.Query().
		Where(entseries.IDEQ(seriesID)).
		WithCategory().
		Only(ctx)
	if ent.IsNotFound(err) {
		return ConsolidateResult{}, ErrSeriesNotFound
	}
	if err != nil {
		return ConsolidateResult{}, fmt.Errorf("library.ConsolidateProviders: load series %s: %w", seriesID, err)
	}

	targetID, targetImportance, err := s.resolveConsolidateTarget(ctx, row, target)
	if err != nil {
		return ConsolidateResult{}, err
	}

	result := ConsolidateResult{SurvivorID: targetID}
	for _, pid := range mergeIDs {
		s.foldOneProvider(ctx, row, pid, targetID, targetImportance, &result)
	}

	// Elevate + re-densify + converge ONLY when at least one provider actually
	// folded (chapters were re-pointed onto the survivor at targetImportance). A
	// ZERO-FOLD run must not elevate the survivor: for the match-to-source arm that
	// would leave a freshly-attached HIGH-importance source outranking the existing
	// disk chapters (importance 1) with NOTHING re-pointed onto it → DetectUpgrades
	// would re-download them, breaking the no-redownload promise. Leaving the
	// attached source PARKED at importance 0 (where attachRealSource left it) keeps
	// 0 <= every watermark, so no upgrade can fire — a safe no-op consolidation.
	// Only reachable via misuse (every selected provider invalid/linked). Reported
	// via result.Merged == 0 + result.Skipped.
	if result.Merged > 0 {
		if err := s.finaliseSurvivorRanks(ctx, seriesID, targetID, targetImportance); err != nil {
			return result, err
		}
		if s.trigger != nil {
			s.trigger()
		}
	} else {
		slog.WarnContext(ctx, "library.ConsolidateProviders: no providers folded — survivor left un-elevated (no-op)",
			"series_id", seriesID, "skipped", len(result.Skipped))
	}

	return result, nil
}

// resolveConsolidateTarget resolves the survivor the selected providers fold into
// and the importance it should carry. For the existing-provider arm it loads the
// row (ErrProviderNotInSeries on a miss) and REQUIRES a non-empty feed
// (ErrTargetNoFeed — merging into a feed-less target would orphan the disk
// chapters, the same guard DedupProviders applies). For the match-to-source arm it
// attaches/ingests the source via attachRealSource (parked at importance 0, exactly
// like the single Match) and returns the match spec's importance as the target.
func (s *Service) resolveConsolidateTarget(ctx context.Context, row *ent.Series, target ConsolidateTarget) (targetID uuid.UUID, targetImportance int, err error) {
	if target.ExistingProviderID != nil {
		tp, tErr := s.db.SeriesProvider.Query().
			Where(entseriesprovider.IDEQ(*target.ExistingProviderID), entseriesprovider.SeriesID(row.ID)).
			Only(ctx)
		if ent.IsNotFound(tErr) {
			return uuid.Nil, 0, ErrProviderNotInSeries
		}
		if tErr != nil {
			return uuid.Nil, 0, fmt.Errorf("library.ConsolidateProviders: load target provider: %w", tErr)
		}
		hasFeed, fErr := s.providerHasFeed(ctx, tp.ID)
		if fErr != nil {
			return uuid.Nil, 0, fmt.Errorf("library.ConsolidateProviders: check target feed: %w", fErr)
		}
		if !hasFeed {
			return uuid.Nil, 0, ErrTargetNoFeed
		}
		return tp.ID, tp.Importance, nil
	}

	// Match-to-source arm: collapse the scanlator when the source is flagged
	// ignore-scanlator so the ingest and the post-ingest lookup agree on one
	// [Source] provider key (mirrors MatchDiskProvider/AddProvider), then attach.
	scanlator := target.Scanlator
	if sourceID, perr := parseSourceID(target.Source); perr == nil {
		scanlator = s.ingest.EffectiveScanlator(ctx, sourceID, scanlator)
	}
	newSP, aErr := s.attachRealSource(ctx, row.ID, row.Title, target.Source, target.URL, scanlator)
	if aErr != nil {
		return uuid.Nil, 0, aErr
	}
	return newSP.ID, target.Importance, nil
}

// foldOneProvider folds a single selected provider (pid) into the survivor,
// recording success (result.Merged++) or a fault-isolated skip (result.Skipped).
// It RELOADS the survivor row each call: a prior fold committed its importance to
// targetImportance, so a fresh read is what lets mergeDiskIntoLive park it back to
// 0 for THIS fold's relabel window (the no-redownload invariant — see
// attachRealSource / mergeDiskIntoLive). A missing provider, a linked live source,
// or a fold error is skipped, never fatal.
func (s *Service) foldOneProvider(ctx context.Context, row *ent.Series, pid, targetID uuid.UUID, targetImportance int, result *ConsolidateResult) {
	if pid == targetID {
		result.Skipped = append(result.Skipped, SkippedProvider{ProviderID: pid, Reason: "cannot merge the target into itself"})
		return
	}

	targetSP, err := s.db.SeriesProvider.Query().
		Where(entseriesprovider.IDEQ(targetID), entseriesprovider.SeriesID(row.ID)).
		Only(ctx)
	if err != nil {
		// The survivor vanished mid-loop (concurrent delete) — treat this provider
		// as skipped rather than aborting; the next iteration will skip too.
		result.Skipped = append(result.Skipped, SkippedProvider{ProviderID: pid, Reason: "target provider unavailable"})
		return
	}

	diskSP, err := s.db.SeriesProvider.Query().
		Where(entseriesprovider.IDEQ(pid), entseriesprovider.SeriesID(row.ID)).
		Only(ctx)
	if ent.IsNotFound(err) {
		result.Skipped = append(result.Skipped, SkippedProvider{ProviderID: pid, Reason: "provider does not belong to series"})
		return
	}
	if err != nil {
		slog.WarnContext(ctx, "library.ConsolidateProviders: load provider failed, skipping", "provider_id", pid, "err", err)
		result.Skipped = append(result.Skipped, SkippedProvider{ProviderID: pid, Reason: "could not load provider"})
		return
	}
	if series.IsLinkedProvider(diskSP) {
		result.Skipped = append(result.Skipped, SkippedProvider{ProviderID: pid, Reason: "provider is not an unlinked disk-origin provider"})
		return
	}

	if _, err := s.mergeDiskIntoLive(ctx, row, diskSP, targetSP, targetImportance); err != nil {
		// Fault isolation: log the RAW error server-side, record only a caller-safe
		// reason (safeMergeError mirrors the async SSE hygiene), and continue.
		slog.WarnContext(ctx, "library.ConsolidateProviders: fold failed, skipping provider", "provider_id", pid, "err", err)
		result.Skipped = append(result.Skipped, SkippedProvider{ProviderID: pid, Reason: safeMergeError(err)})
		return
	}
	result.Merged++
}

// finaliseSurvivorRanks pins the survivor at targetImportance (covering the
// match-to-source arm with zero successful folds, where attachRealSource left the
// new source PARKED at 0) and then re-densifies EVERY remaining provider onto a
// clean, gap-free importance spread via series.ReorderProviders (which runs
// series.normalizeRanks in one all-or-nothing tx). Re-densifying is safe against a
// re-download: the survivor satisfies its own re-pointed chapters, so
// DetectUpgrades' self-churn guard never flags them regardless of the new rank.
func (s *Service) finaliseSurvivorRanks(ctx context.Context, seriesID, survivorID uuid.UUID, targetImportance int) error {
	if err := s.db.SeriesProvider.UpdateOneID(survivorID).SetImportance(targetImportance).Exec(ctx); err != nil {
		return fmt.Errorf("library.ConsolidateProviders: pin survivor importance: %w", err)
	}

	providers, err := s.db.SeriesProvider.Query().
		Where(entseriesprovider.SeriesID(seriesID)).
		All(ctx)
	if err != nil {
		return fmt.Errorf("library.ConsolidateProviders: load providers for re-densify: %w", err)
	}
	ranks := make([]series.ProviderRank, len(providers))
	for i, p := range providers {
		ranks[i] = series.ProviderRank{SeriesProviderID: p.ID, Importance: p.Importance}
	}
	if err := s.series.ReorderProviders(ctx, seriesID, ranks); err != nil {
		return fmt.Errorf("library.ConsolidateProviders: re-densify importances: %w", err)
	}
	return nil
}
