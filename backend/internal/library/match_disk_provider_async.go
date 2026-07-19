package library

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/disk"
)

// matchTimeout bounds the detached background match/merge a StartMatchDiskProvider
// request kicks off. A single-provider match is bounded by ONE disk provider's
// CBZ count: it re-fetches the target source's feed (a slow Cloudflare source can
// take tens of seconds) then rewrites every overlapping CBZ zip over NFS. The
// worst case observed on prod was ~7 min for a 238-chapter provider, so 15m gives
// comfortable headroom while still guaranteeing the background goroutine + its
// context can never leak. A var (not a const) so a test can shrink it.
var matchTimeout = 15 * time.Minute

// matchBlock, when non-nil, makes the match goroutine WAIT before running the
// real merge — a test-only seam (mirrors scanjob.go's scanBlock) so the
// single-flight-guard test can keep the first merge in flight deterministically
// while it fires a second start. Nil in production: no wait.
var matchBlock chan struct{}

// StartMatchDiskProvider runs MatchDiskProvider in the background and returns
// immediately, so the operation is DISCONNECT-PROOF (GAP-096): the whole merge
// used to run synchronously on the HTTP request context, and a client/proxy/user
// disconnect during the multi-minute op cancelled it mid-flight — stranding a
// persisted duplicate provider AND half-relabeled CBZs, then hard-500ing on
// retry. Detaching from the request context (context.WithoutCancel + a hard
// timeout, mirroring sources.Warmup / DedupAllProviders) means a disconnect can
// no longer cancel the merge, so it always runs to completion.
//
// Returns false WITHOUT starting a new merge if a Match OR a Consolidation is
// already in flight for the SAME SERIES (the shared per-series single-flight guard
// — see Service.mergeRunning): a double-click / eager retry, or a Match racing a
// Consolidation that would rewrite importances mid-relabel, must never launch a
// second concurrent merge over the same series' CBZs. Different series run
// concurrently.
//
// On completion (success OR failure) it broadcasts a provider.merged SSE event
// carrying the series id (and the error text on failure), so the frontend — which
// received a 202 and closed the dialog — refetches exactly that series' detail
// once the background merge lands. The trigger()/convergence side effects live in
// MatchDiskProvider itself and are unchanged.
func (s *Service) StartMatchDiskProvider(ctx context.Context, seriesID, diskProviderID uuid.UUID, source, url, scanlator string, importance int) bool {
	if !s.acquireMerge(seriesID) {
		return false
	}

	// Detach from the request context (which ends the moment the handler returns
	// 202) but keep a hard timeout so the goroutine + context can never leak.
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), matchTimeout)

	// Snapshot the test-only block seam in the caller's goroutine (fully sequenced
	// with the caller, so a test can restore it right after StartMatchDiskProvider
	// returns without racing the goroutine's read — mirrors scanjob.go).
	block := matchBlock

	go func() {
		defer cancel()
		defer s.releaseMerge(seriesID)

		if block != nil {
			select {
			case <-block:
			case <-runCtx.Done():
			}
		}

		if _, err := s.MatchDiskProvider(runCtx, seriesID, diskProviderID, source, url, scanlator, importance); err != nil {
			// Log the RAW error server-side, but broadcast only a caller-safe message
			// (safeMergeError). The SSE side-channel bypasses the central error
			// middleware (middleware/error.go), which genericises unmapped errors so
			// raw driver text / DSNs / stack traces never reach the client — so we
			// mirror that hygiene here explicitly.
			slog.WarnContext(runCtx, "library: background match/merge failed", "series_id", seriesID, "provider_id", diskProviderID, "err", err)
			s.broadcastMerge(MergeEvent{SeriesID: seriesID.String(), Error: safeMergeError(err)})
			return
		}
		s.broadcastMerge(MergeEvent{SeriesID: seriesID.String()})
	}()

	return true
}

// safeMergeError maps a background-merge error to a caller-safe message for the
// provider.merged SSE event, mirroring handler.mapServiceError's clean sentinel
// messages. A KNOWN sentinel yields its fixed, hygienic text (never the wrapped
// %w chain, which for ErrSourceUpstream/ErrSourceUnavailable carries the raw
// upstream cause); any UNMAPPED error yields the generic "match failed" — the
// raw detail is logged server-side by the caller, never sent to the client
// (matches middleware/error.go's "Never expose raw error text" rule).
func safeMergeError(err error) string {
	switch {
	case errors.Is(err, ErrSeriesNotFound):
		return "series not found"
	case errors.Is(err, ErrProviderNotInSeries):
		return "provider does not belong to series"
	case errors.Is(err, ErrNotADiskProvider):
		return "provider is not an unlinked disk-origin provider"
	case errors.Is(err, ErrProviderAlreadyPresent):
		return "provider already attached to series"
	case errors.Is(err, ErrSourceNotFound):
		return "source not found"
	case errors.Is(err, ErrSourceUnavailable):
		return "source temporarily unavailable, retry shortly"
	case errors.Is(err, ErrSourceUpstream):
		return "source fetch failed"
	case errors.Is(err, disk.ErrRelabelCollision):
		return "filename collision with another chapter's file"
	default:
		return "match failed"
	}
}
