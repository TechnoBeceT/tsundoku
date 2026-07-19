package library

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// consolidatePerProviderTimeout is the per-selected-provider slice of the
// detached background consolidation's overall bound. Each fold is bounded by ONE
// disk provider's CBZ count (the same work a single Match does — rewriting every
// overlapping CBZ zip over NFS, ~7 min worst case observed on prod), so the total
// timeout SCALES with the number of providers being folded: matchTimeout × N. A
// consolidation of N providers gets exactly the headroom N single matches would,
// so the goroutine + its context can never leak yet a large multi-provider fold
// is never cut off mid-flight.
var consolidatePerProviderTimeout = matchTimeout

// consolidateBlock, when non-nil, makes the consolidation goroutine WAIT before
// running the real work — a test-only seam (mirrors matchBlock) so the
// single-flight-guard test can hold the first consolidation in flight
// deterministically while it fires a second start. Nil in production.
var consolidateBlock chan struct{}

// acquireMerge claims the per-series merge single-flight latch shared by the
// single Match and the multi-provider consolidation (Service.mergeRunning). It
// returns false when a merge of EITHER kind is already in flight for seriesID, so
// the two can never run concurrently on the same series (a consolidation's
// importance re-densify would otherwise re-arm a re-download inside a concurrent
// Match's park window). Lazily initialises the map under the lock.
func (s *Service) acquireMerge(seriesID uuid.UUID) bool {
	s.mergeMu.Lock()
	defer s.mergeMu.Unlock()
	if s.mergeRunning == nil {
		s.mergeRunning = make(map[uuid.UUID]struct{})
	}
	if _, running := s.mergeRunning[seriesID]; running {
		return false
	}
	s.mergeRunning[seriesID] = struct{}{}
	return true
}

// releaseMerge frees the per-series merge latch acquired by acquireMerge (called
// from the background goroutine's defer in both async merge paths).
func (s *Service) releaseMerge(seriesID uuid.UUID) {
	s.mergeMu.Lock()
	delete(s.mergeRunning, seriesID)
	s.mergeMu.Unlock()
}

// consolidateTimeout returns the detached-context bound for consolidating n
// providers: matchTimeout × max(1, n), so a zero/one-provider request still gets
// a full match's worth of headroom.
func consolidateTimeout(n int) time.Duration {
	if n < 1 {
		n = 1
	}
	return consolidatePerProviderTimeout * time.Duration(n)
}

// StartConsolidateProviders runs ConsolidateProviders in the background and
// returns immediately, so a per-series multi-provider consolidation is
// DISCONNECT-PROOF (QCAT-295 Part B, mirroring StartMatchDiskProvider): the merge
// relabels many CBZs over NFS and legitimately runs for MINUTES, far longer than
// any CDN/proxy request budget, and a client disconnect must not cancel it
// mid-flight (which would strand half-relabeled CBZs). Detaching from the request
// context (context.WithoutCancel + an N-scaled hard timeout) means it always runs
// to completion.
//
// Returns false WITHOUT starting a new run if a consolidation is already in flight
// for the SAME series (single-flight guard keyed by series id — a whole-series
// consolidation touches every selected provider's CBZs, so two must never race).
// Different series run concurrently.
//
// On completion (success OR failure) it broadcasts a provider.merged SSE event
// carrying the series id — plus the merged/skipped counts on success, or the
// caller-safe error text on a hard failure (safeMergeError, so no raw driver text
// leaks via the side-channel that bypasses the central error middleware) — so the
// frontend, which received a 202 and closed the dialog, refetches exactly that
// series' detail once the background consolidation lands. It reuses the SAME
// provider.merged event the single Match emits, so the existing FE handling
// (refetch + reconnect-reconcile) covers it unchanged.
func (s *Service) StartConsolidateProviders(ctx context.Context, seriesID uuid.UUID, mergeIDs []uuid.UUID, target ConsolidateTarget) bool {
	if !s.acquireMerge(seriesID) {
		return false
	}

	// Detach from the request context (which ends the moment the handler returns
	// 202) but keep a hard, N-scaled timeout so the goroutine + context can never leak.
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), consolidateTimeout(len(mergeIDs)))

	// Snapshot the test-only block seam in the caller's goroutine (fully sequenced
	// with the caller — mirrors StartMatchDiskProvider).
	block := consolidateBlock

	go func() {
		defer cancel()
		defer s.releaseMerge(seriesID)

		if block != nil {
			select {
			case <-block:
			case <-runCtx.Done():
			}
		}

		result, err := s.ConsolidateProviders(runCtx, seriesID, mergeIDs, target)
		if err != nil {
			slog.WarnContext(runCtx, "library: background consolidation failed", "series_id", seriesID, "err", err)
			s.broadcastMerge(MergeEvent{SeriesID: seriesID.String(), Error: safeMergeError(err)})
			return
		}
		s.broadcastMerge(MergeEvent{SeriesID: seriesID.String(), Merged: result.Merged, Skipped: len(result.Skipped)})
	}()

	return true
}
