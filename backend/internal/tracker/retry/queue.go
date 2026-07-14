// Package retry is the DURABLE, COALESCING retry queue for tracker-progress
// pushes, backed by the PendingTrackPush table (spec/trackers-sync-phase4
// §3). It is Phase 4b: the queue + worker only — the actual "push a chapter
// number to a tracker" call is Phase 4c's job, expressed here purely as the
// Pusher interface (dependency inversion), so this package has zero
// knowledge of any concrete tracker.
//
// # Coalescing
//
// A push failure enqueues a row keyed by track_binding_id (UNIQUE). A later
// Enqueue for the SAME binding never creates a second row: it keeps the
// HIGHEST pending chapter (a newer, higher push supersedes an older,
// lower one — the reader/refresh trigger that fires the newer push has
// already superseded the older one's local state), and resets the row's
// attempts/next_attempt_at/last_error so the fresh value gets a full,
// un-penalized retry budget.
//
// # Bounded retries
//
// RunOnce is one bounded pass: it loads every DUE row (next_attempt_at nil
// or <= now, AND attempts still under the cap) and pushes each via the
// injected Pusher. Success deletes the row (nothing to retry — progress is
// safely on the tracker). Failure increments attempts, records last_error,
// and sets an exponential backoff next_attempt_at. Once attempts reaches
// maxAttempts the row is EXCLUDED from all future due-passes (by the
// attempts<maxAttempts filter) but is NEVER deleted — it stays as a
// tracking-health signal the owner can see (and, in a later phase, clear by
// re-triggering a push). This is the never-lose-progress guarantee: a
// failed push always leaves its row (and therefore the pending chapter
// number) intact; nothing is ever silently dropped.
package retry

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entpendingtrackpush "github.com/technobecet/tsundoku/internal/ent/pendingtrackpush"
)

// Queue is the tracker-push retry queue service. It is ENT-TOUCHING (unlike
// internal/tracker/sync, the pure rule kernel) — it owns the durable
// PendingTrackPush table.
type Queue struct {
	client *ent.Client
}

// NewQueue builds a Queue over the given Ent client.
func NewQueue(client *ent.Client) *Queue {
	return &Queue{client: client}
}

// Enqueue records that trackBindingID needs a push of chapter, coalescing
// with any existing pending row for the same binding:
//
//   - No existing row: creates one with the given chapter (attempts=0,
//     next_attempt_at=nil ⇒ due immediately on the next RunOnce pass).
//   - An existing row with a LOWER OR EQUAL chapter: superseded — chapter,
//     attempts (→0), last_error (→""), and next_attempt_at (→nil) are all
//     reset, so the new higher value gets a fresh, un-penalized retry
//     budget rather than inheriting a failing lower value's backoff state.
//   - An existing row with a STRICTLY HIGHER chapter already pending: a
//     genuine no-op — the pending row already covers (and exceeds) what
//     this call is asking for, so nothing is written.
//
// Query-then-upsert against the UNIQUE(track_binding_id) index — the same
// find-or-create pattern used elsewhere in this codebase (e.g.
// category.FindOrCreate, disk.findOrCreateSeriesProvider); a race between
// two concurrent Enqueue calls for the same binding would surface as a
// unique-constraint error from Create, which is acceptable here (pushes are
// not expected to race in practice — the reader/refresh/manual-sync
// triggers are serialized per series) and simply propagates.
func (q *Queue) Enqueue(ctx context.Context, trackBindingID uuid.UUID, chapter float64) error {
	existing, err := q.client.PendingTrackPush.Query().
		Where(entpendingtrackpush.TrackBindingID(trackBindingID)).
		Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return fmt.Errorf("retry.Enqueue: query pending push (track_binding=%s): %w", trackBindingID, err)
	}

	if existing == nil {
		if _, cErr := q.client.PendingTrackPush.Create().
			SetTrackBindingID(trackBindingID).
			SetChapter(chapter).
			Save(ctx); cErr != nil {
			return fmt.Errorf("retry.Enqueue: create pending push (track_binding=%s): %w", trackBindingID, cErr)
		}
		return nil
	}

	if chapter <= existing.Chapter {
		// The already-pending value covers this request; never regress the
		// coalesced chapter and never reset a valid in-progress retry budget
		// for a value that isn't actually newer.
		return nil
	}

	if _, uErr := existing.Update().
		SetChapter(chapter).
		SetAttempts(0).
		SetLastError("").
		ClearNextAttemptAt().
		Save(ctx); uErr != nil {
		return fmt.Errorf("retry.Enqueue: update pending push (track_binding=%s): %w", trackBindingID, uErr)
	}
	return nil
}
