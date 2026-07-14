package retry_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/tracker/retry"
)

const testMaxAttempts = 3

// pushCall records one Push invocation for assertion.
type pushCall struct {
	trackBindingID uuid.UUID
	chapter        float64
}

// fakePusher is a retry.Pusher test double: fn (keyed by track binding id)
// decides success/failure per call; every call is recorded so a test can
// assert exactly which bindings were pushed and how many times.
type fakePusher struct {
	mu    sync.Mutex
	fn    func(id uuid.UUID, chapter float64) error
	calls []pushCall
}

func (f *fakePusher) Push(_ context.Context, id uuid.UUID, chapter float64) error {
	f.mu.Lock()
	f.calls = append(f.calls, pushCall{trackBindingID: id, chapter: chapter})
	f.mu.Unlock()
	if f.fn != nil {
		return f.fn(id, chapter)
	}
	return nil
}

func (f *fakePusher) callCount(id uuid.UUID) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c.trackBindingID == id {
			n++
		}
	}
	return n
}

var errPushFailed = errors.New("fake push: upstream rejected")

// TestRunOnce_SuccessDeletesRow proves the success path: a due row whose
// push succeeds is removed from the queue entirely — nothing left to retry.
func TestRunOnce_SuccessDeletesRow(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	q := retry.NewQueue(client)
	bindingID := uuid.New()
	mustEnqueue(ctx, t, q, bindingID, 12)

	pusher := &fakePusher{}
	processed, err := q.RunOnce(ctx, pusher, time.Now(), testMaxAttempts)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1", processed)
	}

	assertRowCount(ctx, t, client, bindingID, 0)
}

// TestRunOnce_FailureKeepsRowAndSetsBackoff is the NEVER-LOSE-PROGRESS proof
// (mission §hard rule): a failed push must NOT delete the pending row — the
// chapter number stays intact for a later retry. This test fails under a
// "delete-on-failure" bug (any code path that removes the row regardless of
// push outcome).
func TestRunOnce_FailureKeepsRowAndSetsBackoff(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	q := retry.NewQueue(client)
	bindingID := uuid.New()
	mustEnqueue(ctx, t, q, bindingID, 42)

	pusher := &fakePusher{fn: func(uuid.UUID, float64) error { return errPushFailed }}
	now := time.Now()
	processed, err := q.RunOnce(ctx, pusher, now, testMaxAttempts)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1", processed)
	}

	row := mustLoadRow(ctx, t, client, bindingID)
	if row.Chapter != 42 {
		t.Fatalf("chapter = %v, want 42 (progress must survive a failed push)", row.Chapter)
	}
	if row.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", row.Attempts)
	}
	if row.LastError == "" {
		t.Fatal("last_error is empty, want the push failure reason recorded")
	}
	if row.NextAttemptAt == nil || !row.NextAttemptAt.After(now) {
		t.Fatalf("next_attempt_at = %v, want a backoff timestamp after %v", row.NextAttemptAt, now)
	}
}

// TestRunOnce_DueFilterSkipsFutureBackoff proves the due-gate: a row whose
// next_attempt_at is still in the future is not pushed at all this pass.
func TestRunOnce_DueFilterSkipsFutureBackoff(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	q := retry.NewQueue(client)
	bindingID := uuid.New()
	mustEnqueue(ctx, t, q, bindingID, 7)

	row := mustLoadRow(ctx, t, client, bindingID)
	future := time.Now().Add(time.Hour)
	if _, err := row.Update().SetNextAttemptAt(future).Save(ctx); err != nil {
		t.Fatalf("seed future backoff: %v", err)
	}

	pusher := &fakePusher{}
	processed, err := q.RunOnce(ctx, pusher, time.Now(), testMaxAttempts)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processed != 0 {
		t.Fatalf("processed = %d, want 0 (row not yet due)", processed)
	}
	if pusher.callCount(bindingID) != 0 {
		t.Fatalf("push was called %d times, want 0", pusher.callCount(bindingID))
	}
	assertRowCount(ctx, t, client, bindingID, 1)
}

// TestRunOnce_TerminalAtMaxAttemptsIsNeverRetriedAgain proves the hard
// retry cap: once a row's attempts reaches maxAttempts it is (a) kept, not
// deleted, and (b) permanently excluded from every LATER due-pass — even
// when its next_attempt_at has elapsed and a real due row would normally be
// picked up. This fails under a "no-cap" bug that keeps retrying forever
// (the push-call count for the binding would keep growing past maxAttempts
// on subsequent immediately-due passes).
func TestRunOnce_TerminalAtMaxAttemptsIsNeverRetriedAgain(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	q := retry.NewQueue(client)
	bindingID := uuid.New()
	mustEnqueue(ctx, t, q, bindingID, 3)

	pusher := &fakePusher{fn: func(uuid.UUID, float64) error { return errPushFailed }}

	// Drive attempts up to the cap. Each pass's failure sets a backoff, so we
	// force the row due again for the NEXT pass by clearing next_attempt_at
	// directly (isolating "did the cap logic trigger" from "did the backoff
	// clock happen to elapse").
	now := time.Now()
	for i := 0; i < testMaxAttempts; i++ {
		if _, err := q.RunOnce(ctx, pusher, now, testMaxAttempts); err != nil {
			t.Fatalf("RunOnce pass %d: %v", i, err)
		}
		row := mustLoadRow(ctx, t, client, bindingID)
		if _, err := row.Update().ClearNextAttemptAt().Save(ctx); err != nil {
			t.Fatalf("clear backoff after pass %d: %v", i, err)
		}
	}

	row := mustLoadRow(ctx, t, client, bindingID)
	if row.Attempts != testMaxAttempts {
		t.Fatalf("attempts = %d, want %d (cap reached)", row.Attempts, testMaxAttempts)
	}
	callsAtCap := pusher.callCount(bindingID)
	if callsAtCap != testMaxAttempts {
		t.Fatalf("push calls = %d, want %d", callsAtCap, testMaxAttempts)
	}

	// One more pass: the row is nil next_attempt_at (structurally "due" by
	// the timing gate alone) but must be excluded by the attempts filter.
	processed, err := q.RunOnce(ctx, pusher, time.Now(), testMaxAttempts)
	if err != nil {
		t.Fatalf("RunOnce (post-cap pass): %v", err)
	}
	if processed != 0 {
		t.Fatalf("processed = %d, want 0 (terminal row must not be retried)", processed)
	}
	if pusher.callCount(bindingID) != callsAtCap {
		t.Fatalf("push was called again after the cap: %d calls, want %d", pusher.callCount(bindingID), callsAtCap)
	}

	// The row is a tracking-health signal — kept, not deleted.
	assertRowCount(ctx, t, client, bindingID, 1)
	final := mustLoadRow(ctx, t, client, bindingID)
	if final.LastError == "" {
		t.Fatal("last_error is empty on the terminal row, want the final failure reason recorded")
	}
}

// TestRunOnce_PerRowIsolation proves one row's push failure never aborts
// the rest of the batch: two due rows, one fails and one succeeds — both
// must be attempted and each must land in its own correct end state.
func TestRunOnce_PerRowIsolation(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	q := retry.NewQueue(client)
	failing := uuid.New()
	succeeding := uuid.New()
	mustEnqueue(ctx, t, q, failing, 1)
	mustEnqueue(ctx, t, q, succeeding, 2)

	pusher := &fakePusher{fn: func(id uuid.UUID, _ float64) error {
		if id == failing {
			return errPushFailed
		}
		return nil
	}}

	processed, err := q.RunOnce(ctx, pusher, time.Now(), testMaxAttempts)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processed != 2 {
		t.Fatalf("processed = %d, want 2 (both rows attempted despite one failing)", processed)
	}

	assertRowCount(ctx, t, client, failing, 1)
	assertRowCount(ctx, t, client, succeeding, 0)
}

// TestRunOnce_HardErrorOnQueryFailurePropagates proves the one legitimate
// RunOnce error path: a due-row query failure (here, a canceled context)
// surfaces as an error rather than being silently swallowed like per-row
// failures are.
func TestRunOnce_HardErrorOnQueryFailurePropagates(t *testing.T) {
	client := testdb.New(t)
	q := retry.NewQueue(client)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := q.RunOnce(canceled, &fakePusher{}, time.Now(), testMaxAttempts)
	if err == nil {
		t.Fatal("RunOnce with a canceled context returned nil error, want a load-failure error")
	}
}
