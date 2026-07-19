package sourcegate_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/sourcegate"
)

// TestSummaryCounts folds a hand-built breaker snapshot into the two alert
// counts, proving the erroring rule (FailingSince set) and the coolingDown rule
// (tripped + cooldown in the future) are independent and computed against now.
func TestSummaryCounts(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Minute)
	future := now.Add(time.Hour)
	expired := now.Add(-time.Hour)

	snap := map[string]sourcegate.BreakerState{
		// Erroring AND cooling down (tripped, cooldown still in the future).
		"Comix": {FailingSince: &past, CooldownUntil: &future},
		// Erroring but NOT cooling down (sub-threshold streak, no cooldown).
		"Asura": {FailingSince: &past},
		// Erroring, cooldown EXPIRED — counts as erroring, not cooling down.
		"ZScans": {FailingSince: &past, CooldownUntil: &expired},
		// Fully healthy — counts for neither.
		"Weeb": {},
	}

	erroring, coolingDown := sourcegate.SummaryCounts(snap, now)
	if erroring != 3 {
		t.Errorf("erroring = %d, want 3 (Comix, Asura, ZScans)", erroring)
	}
	if coolingDown != 1 {
		t.Errorf("coolingDown = %d, want 1 (only Comix's cooldown is in the future)", coolingDown)
	}

	// An empty snapshot yields zero of both (the resting healthy state).
	if e, c := sourcegate.SummaryCounts(map[string]sourcegate.BreakerState{}, now); e != 0 || c != 0 {
		t.Errorf("empty snapshot: erroring=%d coolingDown=%d, want 0/0", e, c)
	}
}

// TestTransitionHook_FiresOnTripRecoveryAndReset proves the hook fires exactly on
// the three sanctioned transitions — a trip (crossing the threshold), a natural
// recovery, and an owner Reset — and NOT on a sub-threshold failure.
func TestTransitionHook_FiresOnTripRecoveryAndReset(t *testing.T) {
	db := testdb.New(t)
	var fired atomic.Int64
	svc := sourcegate.NewService(db, thresholds()).
		WithTransitionHook(func() { fired.Add(1) })
	ctx := context.Background()
	past := time.Now().Add(-time.Hour) // trip so the resulting cooldown is already expired (gated-flow shape)
	const key = "Comix"

	// Threshold is 3: the first two failures are sub-threshold — no transition.
	svc.RecordFailure(ctx, key, errors.New("e"), past)
	svc.RecordFailure(ctx, key, errors.New("e"), past)
	if n := fired.Load(); n != 0 {
		t.Fatalf("no transition expected below threshold, hook fired %d times", n)
	}

	// The 3rd failure trips the breaker — one transition.
	svc.RecordFailure(ctx, key, errors.New("cloudflare"), past)
	if n := fired.Load(); n != 1 {
		t.Fatalf("expected 1 hook fire on trip, got %d", n)
	}

	// Natural recovery after the cooldown expired — one more transition.
	svc.RecordSuccess(ctx, key)
	if n := fired.Load(); n != 2 {
		t.Fatalf("expected 2 hook fires after recovery, got %d", n)
	}

	// Owner Reset is always a clear transition — one more.
	if err := svc.Reset(ctx, key); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if n := fired.Load(); n != 3 {
		t.Fatalf("expected 3 hook fires after owner reset, got %d", n)
	}
}

// TestTransitionHook_NilAndPanicNeverBreakBreaker proves the best-effort posture:
// a nil hook is a safe no-op, and a hook that PANICS is recovered so the breaker
// record still completes (RecordFailure still trips the breaker).
func TestTransitionHook_NilAndPanicNeverBreakBreaker(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	now := time.Now()

	// Nil hook (never attached): a full trip must not panic and must still trip.
	nilHook := sourcegate.NewService(db, thresholds())
	const k1 = "NilHookSource"
	nilHook.RecordFailure(ctx, k1, errors.New("e"), now)
	nilHook.RecordFailure(ctx, k1, errors.New("e"), now)
	nilHook.RecordFailure(ctx, k1, errors.New("e"), now)
	if nilHook.IsAvailable(ctx, k1, now) {
		t.Fatal("breaker should have tripped with a nil hook")
	}

	// A panicking hook must be recovered and must NOT stop the breaker tripping.
	panicHook := sourcegate.NewService(db, thresholds()).
		WithTransitionHook(func() { panic("boom") })
	const k2 = "PanicHookSource"
	panicHook.RecordFailure(ctx, k2, errors.New("e"), now)
	panicHook.RecordFailure(ctx, k2, errors.New("e"), now)
	panicHook.RecordFailure(ctx, k2, errors.New("e"), now) // trips → fires the panicking hook
	if panicHook.IsAvailable(ctx, k2, now) {
		t.Fatal("breaker should have tripped even though the transition hook panicked")
	}
}
