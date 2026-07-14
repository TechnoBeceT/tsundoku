package syncsvc

import (
	"context"

	"github.com/google/uuid"
)

// SyncOnView is the series-detail-open trigger: opening a series' detail
// page reconciles that series with every tracker it is bound to, the same
// max-wins-both-directions converge SyncNow already performs (pull the
// remote entry, adopt whichever of {local library read-count, the binding's
// stored value, the remote's reported progress} is furthest ahead, and push
// back when local was ahead). It is a thin, intent-naming wrapper over
// SyncNow that discards the per-binding result slice — the handler layer
// only needs to know whether the reconcile as a whole succeeded, not the
// updated rows (the very next GetSeries the handler already does re-reads
// current state from the DB, so returning the bindings here would be dead
// weight).
//
// 🔴 UNGATED by auto_update_track: that toggle governs only the PASSIVE
// reading-triggered push (PushProgress, fired from the reader's mark-read
// hook — a side effect of an unrelated action the owner may not want
// broadcast automatically). Opening a series' detail page is itself a
// DELIBERATE view action — the owner is looking straight at that series —
// so it always reconciles regardless of the toggle. SyncNow (which this
// wraps) never consulted AutoUpdateTracker to begin with; this method exists
// so the detail-open call site has a narrowly-named, intent-documented
// method to depend on instead of reaching for SyncNow directly and leaving
// the "why is this ungated" reasoning implicit at the call site.
func (s *Service) SyncOnView(ctx context.Context, seriesID uuid.UUID) error {
	_, err := s.SyncNow(ctx, seriesID)
	return err
}
