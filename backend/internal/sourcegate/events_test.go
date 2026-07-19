package sourcegate_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/sourceevents"
	"github.com/technobecet/tsundoku/internal/sourcegate"
)

// captureRecorder is a test double for sourceevents.Recorder that records every
// logged event in memory for assertion. It is safe for concurrent use.
type captureRecorder struct {
	mu     sync.Mutex
	events []sourceevents.Event
}

func (c *captureRecorder) Log(_ context.Context, event sourceevents.Event) {
	c.mu.Lock()
	c.events = append(c.events, event)
	c.mu.Unlock()
}

func (c *captureRecorder) LogBatch(_ context.Context, events []sourceevents.Event) {
	c.mu.Lock()
	c.events = append(c.events, events...)
	c.mu.Unlock()
}

// byType returns the recorded events of the given type.
func (c *captureRecorder) byType(t sourceevents.EventType) []sourceevents.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []sourceevents.Event
	for _, e := range c.events {
		if e.Type == t {
			out = append(out, e)
		}
	}
	return out
}

// TestFailingSince_SetOnStreakStartUnchangedWithinStreakClearedOnSuccess is the
// core failing_since lifecycle proof.
func TestFailingSince_SetOnStreakStartUnchangedWithinStreakClearedOnSuccess(t *testing.T) {
	db := testdb.New(t)
	svc := sourcegate.NewService(db, thresholds())
	ctx := context.Background()
	const key = "Comix"

	t0 := time.Now()
	svc.RecordFailure(ctx, key, errors.New("boom"), t0)
	first := failingSince(t, svc, ctx, key)
	if first == nil {
		t.Fatal("failing_since should be set on the first failure of a streak")
	}
	// Postgres stores timestamps at microsecond precision, so compare within a
	// tolerance rather than an exact nanosecond Equal.
	if d := first.Sub(t0); d < -time.Millisecond || d > time.Millisecond {
		t.Fatalf("failing_since = %v, want ~the streak-start time %v (delta %v)", first, t0, d)
	}

	// A later failure within the SAME streak must NOT move failing_since.
	svc.RecordFailure(ctx, key, errors.New("boom again"), t0.Add(time.Minute))
	second := failingSince(t, svc, ctx, key)
	if second == nil || !second.Equal(*first) {
		t.Fatalf("failing_since moved within a streak: was %v, now %v", first, second)
	}

	// A success clears it.
	svc.RecordSuccess(ctx, key)
	if fs := failingSince(t, svc, ctx, key); fs != nil {
		t.Fatalf("failing_since should be cleared on success, got %v", fs)
	}

	// A fresh failure after recovery starts a NEW streak at the new time.
	t1 := t0.Add(time.Hour)
	svc.RecordFailure(ctx, key, errors.New("boom"), t1)
	fs := failingSince(t, svc, ctx, key)
	if fs == nil {
		t.Fatal("failing_since should restart on a new streak")
	}
	if d := fs.Sub(t1); d < -time.Millisecond || d > time.Millisecond {
		t.Fatalf("failing_since should restart at ~%v, got %v (delta %v)", t1, fs, d)
	}
}

// TestBreakerEvents_TripOnceThenResetOnRecovery proves the REAL gated-flow
// recovery shape: breaker_trip fires exactly once on the transition into
// cooldown, a post-cooldown re-failure does NOT re-emit a trip, and a success
// AFTER the cooldown has expired emits exactly one breaker_reset.
//
// The trip failures are driven with a `now` an hour in the PAST, so the resulting
// cooldown_until lands in the past too — the exact state the gated flow produces
// by the time IsAvailable lets a call through and RecordSuccess/RecordFailure is
// reached (the gate blocks the whole live-cooldown window). Calling RecordSuccess
// with a still-future cooldown, as the old test did, is a state the gated flow
// can never present.
func TestBreakerEvents_TripOnceThenResetOnRecovery(t *testing.T) {
	db := testdb.New(t)
	rec := &captureRecorder{}
	svc := sourcegate.NewService(db, thresholds()).WithEventRecorder(rec)
	ctx := context.Background()
	past := time.Now().Add(-time.Hour) // trip at t-1h ⇒ cooldown_until (t-1h + 10m) is already expired
	const key = "Comix"

	// Threshold 3: two failures below threshold emit nothing.
	svc.RecordFailure(ctx, key, errors.New("e"), past)
	svc.RecordFailure(ctx, key, errors.New("e"), past)
	if trips := rec.byType(sourceevents.EventBreakerTrip); len(trips) != 0 {
		t.Fatalf("no trip expected below threshold, got %d", len(trips))
	}

	// The 3rd trips it (with an already-expired cooldown) — exactly one trip.
	svc.RecordFailure(ctx, key, errors.New("cloudflare challenge"), past)
	// A post-cooldown re-failure (cooldown already expired, breaker still tripped)
	// must NOT emit another trip — the audit log must not accumulate unpaired trips.
	svc.RecordFailure(ctx, key, errors.New("e"), time.Now())
	trips := rec.byType(sourceevents.EventBreakerTrip)
	if len(trips) != 1 {
		t.Fatalf("expected exactly 1 breaker_trip, got %d", len(trips))
	}
	if trips[0].SourceKey != key || trips[0].Status != sourceevents.StatusFailed {
		t.Fatalf("trip event: key=%q status=%q", trips[0].SourceKey, trips[0].Status)
	}
	if trips[0].Err == nil {
		t.Fatal("trip event should carry its cause")
	}

	// Natural recovery: a success reached AFTER the cooldown expired emits exactly
	// one breaker_reset (the defect this test guards: an "&& After(now)" predicate
	// would see the expired cooldown as not-tripped and log NOTHING here).
	svc.RecordSuccess(ctx, key)
	resets := rec.byType(sourceevents.EventBreakerReset)
	if len(resets) != 1 {
		t.Fatalf("expected exactly 1 breaker_reset on recovery, got %d", len(resets))
	}
	if resets[0].Status != sourceevents.StatusSuccess {
		t.Fatalf("reset status = %q, want success", resets[0].Status)
	}
}

// TestBreakerEvents_SuccessOnHealthySourceEmitsNothing proves a routine success
// on a source that was never tripped emits no breaker_reset (transition-only).
func TestBreakerEvents_SuccessOnHealthySourceEmitsNothing(t *testing.T) {
	db := testdb.New(t)
	rec := &captureRecorder{}
	svc := sourcegate.NewService(db, thresholds()).WithEventRecorder(rec)
	ctx := context.Background()
	const key = "Comix"

	svc.RecordSuccess(ctx, key) // first success, no prior row
	svc.RecordFailure(ctx, key, errors.New("e"), time.Now())
	svc.RecordSuccess(ctx, key) // recovered from a SUB-threshold streak (not tripped)

	if resets := rec.byType(sourceevents.EventBreakerReset); len(resets) != 0 {
		t.Fatalf("no breaker_reset expected for a never-tripped source, got %d", len(resets))
	}
}

// TestReset_EmitsBreakerReset proves the owner Reset action logs a breaker_reset.
func TestReset_EmitsBreakerReset(t *testing.T) {
	db := testdb.New(t)
	rec := &captureRecorder{}
	svc := sourcegate.NewService(db, thresholds()).WithEventRecorder(rec)
	ctx := context.Background()
	const key = "Comix"

	if err := svc.Reset(ctx, key); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	resets := rec.byType(sourceevents.EventBreakerReset)
	if len(resets) != 1 {
		t.Fatalf("expected 1 breaker_reset from Reset, got %d", len(resets))
	}
	if resets[0].SourceKey != key {
		t.Fatalf("reset key = %q, want %q", resets[0].SourceKey, key)
	}
}

// failingSince reads the current failing_since for a source via the breaker
// snapshot (nil when not currently failing).
func failingSince(t *testing.T, svc *sourcegate.Service, ctx context.Context, key string) *time.Time {
	t.Helper()
	snap, err := svc.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	state, ok := snap[key]
	if !ok {
		return nil
	}
	return state.FailingSince
}
