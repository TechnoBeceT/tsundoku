package retry

import (
	"context"

	"github.com/google/uuid"
)

// Pusher pushes trackBindingID's local reading progress (chapter) to its
// bound tracker's remote entry. It is the dependency-inversion seam between
// this package (Phase 4b: the durable queue + worker) and the real tracker
// push implementation (Phase 4c: internal/tracker/sync's push service,
// which applies the never-regress/max-wins rules from the sync rule kernel
// and calls the tracker.Tracker port's UpdateEntry). RunOnce depends only on
// this interface, never on a concrete tracker client.
type Pusher interface {
	// Push attempts to push chapter as trackBindingID's reading progress to
	// its bound tracker. A non-nil error means the push did not land — the
	// caller (RunOnce) leaves the pending row in place for a later retry.
	Push(ctx context.Context, trackBindingID uuid.UUID, chapter float64) error
}
