// Package retry_test exercises the tracker-push retry queue (Enqueue
// coalescing + RunOnce) against an ephemeral PostgreSQL instance (testdb).
// Requires Docker.
package retry_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entpendingtrackpush "github.com/technobecet/tsundoku/internal/ent/pendingtrackpush"
	"github.com/technobecet/tsundoku/internal/tracker/retry"
)

// TestEnqueue_CreatesRowOnFirstCall proves the base case: enqueuing a
// binding with no existing pending row creates one, un-attempted and
// immediately due.
func TestEnqueue_CreatesRowOnFirstCall(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	q := retry.NewQueue(client)
	bindingID := uuid.New()

	if err := q.Enqueue(ctx, bindingID, 5); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	row := mustLoadRow(ctx, t, client, bindingID)
	if row.Chapter != 5 {
		t.Fatalf("chapter = %v, want 5", row.Chapter)
	}
	if row.Attempts != 0 {
		t.Fatalf("attempts = %d, want 0", row.Attempts)
	}
	if row.NextAttemptAt != nil {
		t.Fatalf("next_attempt_at = %v, want nil (due immediately)", row.NextAttemptAt)
	}
}

// TestEnqueue_LowerChapterDoesNotSupersedeHigherPending proves the
// coalescing rule's "keep the highest" half: enqueuing 5 then 3 for the SAME
// binding leaves exactly one row at chapter 5 — a lower/stale push arriving
// after a higher one must never drag the pending value backward. A bug that
// always overwrote (no coalescing at all) would leave chapter=3 here.
func TestEnqueue_LowerChapterDoesNotSupersedeHigherPending(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	q := retry.NewQueue(client)
	bindingID := uuid.New()

	mustEnqueue(ctx, t, q, bindingID, 5)
	mustEnqueue(ctx, t, q, bindingID, 3)

	assertRowCount(ctx, t, client, bindingID, 1)
	row := mustLoadRow(ctx, t, client, bindingID)
	if row.Chapter != 5 {
		t.Fatalf("chapter = %v, want 5 (lower enqueue must not supersede)", row.Chapter)
	}
}

// TestEnqueue_HigherChapterSupersedesAndResetsRetryState proves the
// coalescing rule's "supersede + reset" half: enqueuing 5 then 8 for the
// SAME binding updates the single row to chapter 8 and resets
// attempts/last_error/next_attempt_at — a fresh higher value must get a
// full, un-penalized retry budget rather than inheriting whatever backoff
// state the superseded lower value had accumulated.
func TestEnqueue_HigherChapterSupersedesAndResetsRetryState(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	q := retry.NewQueue(client)
	bindingID := uuid.New()

	mustEnqueue(ctx, t, q, bindingID, 5)

	// Simulate the row having failed a few times already (as RunOnce would
	// leave it), so the reset-on-supersede behavior is actually exercised.
	row := mustLoadRow(ctx, t, client, bindingID)
	future := row.CreatedAt.Add(time.Hour)
	if _, err := row.Update().
		SetAttempts(2).
		SetLastError("network timeout").
		SetNextAttemptAt(future).
		Save(ctx); err != nil {
		t.Fatalf("seed failed-retry state: %v", err)
	}

	mustEnqueue(ctx, t, q, bindingID, 8)

	assertRowCount(ctx, t, client, bindingID, 1)
	updated := mustLoadRow(ctx, t, client, bindingID)
	if updated.Chapter != 8 {
		t.Fatalf("chapter = %v, want 8", updated.Chapter)
	}
	if updated.Attempts != 0 {
		t.Fatalf("attempts = %d, want 0 (reset on supersede)", updated.Attempts)
	}
	if updated.LastError != "" {
		t.Fatalf("last_error = %q, want \"\" (reset on supersede)", updated.LastError)
	}
	if updated.NextAttemptAt != nil {
		t.Fatalf("next_attempt_at = %v, want nil (reset on supersede — due immediately)", updated.NextAttemptAt)
	}
}

// TestEnqueue_UniqueConstraintEnforced proves the schema-level guarantee
// Enqueue's query-then-upsert relies on: two rows can never exist for the
// same track_binding_id. A direct Create bypassing the service (as a bug in
// some OTHER future caller might do) must fail with a constraint violation,
// not silently succeed and leave two competing pending pushes.
func TestEnqueue_UniqueConstraintEnforced(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	bindingID := uuid.New()

	if _, err := client.PendingTrackPush.Create().
		SetTrackBindingID(bindingID).
		SetChapter(1).
		Save(ctx); err != nil {
		t.Fatalf("first create: %v", err)
	}

	if _, err := client.PendingTrackPush.Create().
		SetTrackBindingID(bindingID).
		SetChapter(2).
		Save(ctx); err == nil {
		t.Fatal("second create with the same track_binding_id succeeded, want a unique-constraint error")
	}
}

// mustEnqueue is a t.Helper wrapper around Enqueue that fails the test on
// error.
func mustEnqueue(ctx context.Context, t *testing.T, q *retry.Queue, bindingID uuid.UUID, chapter float64) {
	t.Helper()
	if err := q.Enqueue(ctx, bindingID, chapter); err != nil {
		t.Fatalf("Enqueue(chapter=%v): %v", chapter, err)
	}
}

// mustLoadRow loads bindingID's single PendingTrackPush row, failing the
// test if it is missing or the lookup errors.
func mustLoadRow(ctx context.Context, t *testing.T, client *ent.Client, bindingID uuid.UUID) *ent.PendingTrackPush {
	t.Helper()
	row, err := client.PendingTrackPush.Query().
		Where(entpendingtrackpush.TrackBindingID(bindingID)).
		Only(ctx)
	if err != nil {
		t.Fatalf("load pending push for %s: %v", bindingID, err)
	}
	return row
}

// assertRowCount asserts exactly want PendingTrackPush rows exist for
// bindingID — the direct proof that coalescing never leaves duplicate rows.
func assertRowCount(ctx context.Context, t *testing.T, client *ent.Client, bindingID uuid.UUID, want int) {
	t.Helper()
	n, err := client.PendingTrackPush.Query().
		Where(entpendingtrackpush.TrackBindingID(bindingID)).
		Count(ctx)
	if err != nil {
		t.Fatalf("count pending pushes for %s: %v", bindingID, err)
	}
	if n != want {
		t.Fatalf("row count = %d, want %d", n, want)
	}
}
