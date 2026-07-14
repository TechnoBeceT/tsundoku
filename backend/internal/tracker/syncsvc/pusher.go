package syncsvc

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	enttrackbinding "github.com/technobecet/tsundoku/internal/ent/trackbinding"
	"github.com/technobecet/tsundoku/internal/tracker/retry"
)

// Push implements retry.Pusher — the seam the durable retry worker
// (internal/tracker/retry.Queue.RunOnce, driven by job.Runner.
// StartTrackerRetry) calls to re-attempt a previously-failed progress push.
// It loads the binding fresh (so a since-changed remote_id/library_id or
// tracker connection is honoured) and delegates to the SAME pushOne core
// PushProgress uses — the never-regress/truncate/auto-complete rules are
// identical whether the push is fresh or a retry.
//
// A binding that no longer exists (ErrNotFound — the owner unbound the
// series between the failed push and this retry) returns nil: there is
// nothing left to push, and reporting "success" lets retry.Queue.RunOnce
// delete the now-orphaned pending row instead of retrying it forever.
//
// 🔴 Push does NOT re-enqueue on failure (see pushOne's own doc comment for
// why) and does NOT consult AutoUpdateTracker — a queued push already
// represents progress the owner's reader advanced past; the toggle governs
// whether a NEW event starts a push, not whether an already-durable pending
// one still gets delivered.
func (s *Service) Push(ctx context.Context, trackBindingID uuid.UUID, chapter float64) error {
	binding, err := s.client.TrackBinding.Query().Where(enttrackbinding.IDEQ(trackBindingID)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("syncsvc: retry push: load binding %s: %w", trackBindingID, err)
	}
	return s.pushOne(ctx, binding, chapter)
}

// compile-time assertion that Service satisfies retry.Pusher.
var _ retry.Pusher = (*Service)(nil)
