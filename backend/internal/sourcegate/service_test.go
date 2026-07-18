// Package sourcegate_test exercises the source-politeness gate (circuit-breaker
// + politeness delay) against an ephemeral PostgreSQL instance (testdb). Tests
// require Docker.
package sourcegate_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	entsourcecircuitstate "github.com/technobecet/tsundoku/internal/ent/sourcecircuitstate"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourcegate"
)

// thresholds returns a fixed settings.Static satisfying sourcegate.Thresholds,
// with a small failure threshold and cooldown so tests stay fast.
func thresholds() settings.Static {
	return settings.Static{
		SourcesFailureThresh: 3,
		SourcesCooldownIv:    10 * time.Minute,
		SourcesMinDelay:      50 * time.Millisecond,
	}
}

// TestIsAvailable_NoRowMeansAvailable proves a source with no breaker row at
// all is available (the zero-value / never-failed state).
func TestIsAvailable_NoRowMeansAvailable(t *testing.T) {
	db := testdb.New(t)
	svc := sourcegate.NewService(db, thresholds())

	if !svc.IsAvailable(context.Background(), "NeverSeenSource", time.Now()) {
		t.Error("IsAvailable should be true for a source with no breaker row")
	}
}

// TestRecordFailure_TripsAfterThreshold proves the breaker stays available
// under the threshold and trips (IsAvailable → false) once consecutive
// failures reach it.
func TestRecordFailure_TripsAfterThreshold(t *testing.T) {
	db := testdb.New(t)
	svc := sourcegate.NewService(db, thresholds())
	ctx := context.Background()
	now := time.Now()
	const key = "Comix"

	// Threshold is 3 — the first two failures must NOT trip the breaker.
	svc.RecordFailure(ctx, key, errors.New("timeout"), now)
	if !svc.IsAvailable(ctx, key, now) {
		t.Fatal("should still be available after 1 failure (threshold 3)")
	}
	svc.RecordFailure(ctx, key, errors.New("timeout"), now)
	if !svc.IsAvailable(ctx, key, now) {
		t.Fatal("should still be available after 2 failures (threshold 3)")
	}

	// The 3rd consecutive failure trips it.
	svc.RecordFailure(ctx, key, errors.New("timeout"), now)
	if svc.IsAvailable(ctx, key, now) {
		t.Fatal("should be tripped (unavailable) after 3 consecutive failures")
	}
}

// TestRecordSuccess_ResetsCounterAndClearsCooldown proves a success on a
// tripped source clears the cooldown and resets the failure counter — so a
// SUBSEQUENT failure alone does not immediately re-trip it.
func TestRecordSuccess_ResetsCounterAndClearsCooldown(t *testing.T) {
	db := testdb.New(t)
	svc := sourcegate.NewService(db, thresholds())
	ctx := context.Background()
	now := time.Now()
	const key = "Comix"

	svc.RecordFailure(ctx, key, errors.New("timeout"), now)
	svc.RecordFailure(ctx, key, errors.New("timeout"), now)
	svc.RecordFailure(ctx, key, errors.New("timeout"), now)
	if svc.IsAvailable(ctx, key, now) {
		t.Fatal("expected tripped after 3 failures")
	}

	svc.RecordSuccess(ctx, key)
	if !svc.IsAvailable(ctx, key, now) {
		t.Fatal("expected available immediately after RecordSuccess")
	}

	// Counter must have been reset to 0: a single subsequent failure should not
	// re-trip a threshold-3 breaker.
	svc.RecordFailure(ctx, key, errors.New("timeout"), now)
	if !svc.IsAvailable(ctx, key, now) {
		t.Fatal("one failure after a reset should not re-trip a threshold-3 breaker")
	}
}

// TestIsAvailable_TrueAfterCooldownElapses proves a tripped source becomes
// available again once cooldown_until is in the past, using an injected `now`
// rather than a real sleep.
func TestIsAvailable_TrueAfterCooldownElapses(t *testing.T) {
	db := testdb.New(t)
	svc := sourcegate.NewService(db, thresholds())
	ctx := context.Background()
	tripAt := time.Now()
	const key = "Comix"

	for i := 0; i < 3; i++ {
		svc.RecordFailure(ctx, key, errors.New("timeout"), tripAt)
	}
	if svc.IsAvailable(ctx, key, tripAt) {
		t.Fatal("expected tripped at trip time")
	}

	// Just before cooldown (10m) elapses: still unavailable.
	almostElapsed := tripAt.Add(9*time.Minute + 59*time.Second)
	if svc.IsAvailable(ctx, key, almostElapsed) {
		t.Fatal("expected still tripped just before cooldown elapses")
	}

	// After cooldown elapses: available again.
	afterCooldown := tripAt.Add(10*time.Minute + time.Second)
	if !svc.IsAvailable(ctx, key, afterCooldown) {
		t.Fatal("expected available after cooldown elapses")
	}
}

// TestPersistence_CooldownSurvivesServiceReload proves the breaker state is
// PERSISTED (not in-memory only): a second Service instance built over the
// SAME client sees the cooldown a first instance recorded — the property that
// makes the breaker survive an app restart.
func TestPersistence_CooldownSurvivesServiceReload(t *testing.T) {
	db := testdb.New(t)
	first := sourcegate.NewService(db, thresholds())
	ctx := context.Background()
	now := time.Now()
	const key = "Comix"

	for i := 0; i < 3; i++ {
		first.RecordFailure(ctx, key, errors.New("timeout"), now)
	}
	if first.IsAvailable(ctx, key, now) {
		t.Fatal("expected tripped on the first instance")
	}

	// A second Service, sharing the same underlying DB, must see the SAME
	// cooldown — proving it lives in the DB, not the Go struct.
	second := sourcegate.NewService(db, thresholds())
	if second.IsAvailable(ctx, key, now) {
		t.Fatal("a fresh Service instance over the same DB should also see the source tripped")
	}

	// Sanity: the row is really there with the fields we expect.
	row, err := db.SourceCircuitState.Query().Where(entsourcecircuitstate.SourceKeyEQ(key)).Only(ctx)
	if err != nil {
		t.Fatalf("load breaker row: %v", err)
	}
	if row.ConsecutiveFailures != 3 {
		t.Errorf("ConsecutiveFailures = %d, want 3", row.ConsecutiveFailures)
	}
	if row.CooldownUntil == nil {
		t.Error("CooldownUntil should be set")
	}
	if row.LastError == "" {
		t.Error("LastError should be recorded")
	}
}

// TestReset_ClearsTrippedBreaker proves the owner "reset source" action clears a
// tripped breaker: after Reset the source is available again and no row remains.
func TestReset_ClearsTrippedBreaker(t *testing.T) {
	db := testdb.New(t)
	svc := sourcegate.NewService(db, thresholds())
	ctx := context.Background()
	now := time.Now()
	const key = "Comix"

	for i := 0; i < 3; i++ {
		svc.RecordFailure(ctx, key, errors.New("timeout"), now)
	}
	if svc.IsAvailable(ctx, key, now) {
		t.Fatal("expected tripped after 3 failures")
	}

	if err := svc.Reset(ctx, key); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if !svc.IsAvailable(ctx, key, now) {
		t.Fatal("expected available immediately after Reset")
	}

	// The row must be gone (Reset deletes it, not just clears the cooldown).
	if _, err := db.SourceCircuitState.Query().
		Where(entsourcecircuitstate.SourceKeyEQ(key)).Only(ctx); err == nil {
		t.Fatal("expected no breaker row after Reset")
	}
}

// TestReset_NoOpOnAbsentRow proves Reset is idempotent: resetting a source with
// no breaker row returns nil (no error).
func TestReset_NoOpOnAbsentRow(t *testing.T) {
	db := testdb.New(t)
	svc := sourcegate.NewService(db, thresholds())

	if err := svc.Reset(context.Background(), "NeverSeenSource"); err != nil {
		t.Errorf("Reset on absent row should be a nil no-op, got %v", err)
	}
}

// TestReset_TouchesOnlyTargetKey proves Reset is scoped to exactly its key: a
// second, still-tripped source is unaffected.
func TestReset_TouchesOnlyTargetKey(t *testing.T) {
	db := testdb.New(t)
	svc := sourcegate.NewService(db, thresholds())
	ctx := context.Background()
	now := time.Now()

	for i := 0; i < 3; i++ {
		svc.RecordFailure(ctx, "Comix", errors.New("timeout"), now)
		svc.RecordFailure(ctx, "Asura", errors.New("timeout"), now)
	}
	if svc.IsAvailable(ctx, "Comix", now) || svc.IsAvailable(ctx, "Asura", now) {
		t.Fatal("expected both sources tripped")
	}

	if err := svc.Reset(ctx, "Comix"); err != nil {
		t.Fatalf("Reset Comix: %v", err)
	}
	if !svc.IsAvailable(ctx, "Comix", now) {
		t.Error("Comix should be available after its reset")
	}
	if svc.IsAvailable(ctx, "Asura", now) {
		t.Error("Asura must stay tripped — Reset(Comix) must not touch it")
	}
}

// TestSnapshot_ReturnsTrippedStateInOneRead proves Snapshot returns the current
// breaker state for every source keyed by source_key, with the cooldown/failure
// fields a read model surfaces.
func TestSnapshot_ReturnsTrippedStateInOneRead(t *testing.T) {
	db := testdb.New(t)
	svc := sourcegate.NewService(db, thresholds())
	ctx := context.Background()
	now := time.Now()

	for i := 0; i < 3; i++ {
		svc.RecordFailure(ctx, "Comix", errors.New("cf block"), now)
	}
	svc.RecordFailure(ctx, "Asura", errors.New("one blip"), now) // 1 failure: not tripped

	snap, err := svc.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap) != 2 {
		t.Fatalf("Snapshot len = %d, want 2", len(snap))
	}

	comix := snap["Comix"]
	if !comix.IsCoolingDown(now) {
		t.Error("Comix should be cooling down (tripped)")
	}
	if comix.ConsecutiveFailures != 3 {
		t.Errorf("Comix failures = %d, want 3", comix.ConsecutiveFailures)
	}
	if comix.CooldownUntil == nil || comix.LastError == "" {
		t.Error("Comix should carry a cooldown + last error")
	}

	asura := snap["Asura"]
	if asura.IsCoolingDown(now) {
		t.Error("Asura should NOT be cooling down (1 failure, threshold 3)")
	}
}

// TestSnapshot_EmptyWhenNoRows proves Snapshot returns an empty (non-nil) map
// when no source has a breaker row.
func TestSnapshot_EmptyWhenNoRows(t *testing.T) {
	db := testdb.New(t)
	svc := sourcegate.NewService(db, thresholds())

	snap, err := svc.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap) != 0 {
		t.Errorf("Snapshot len = %d, want 0", len(snap))
	}
}

// TestWait_EnforcesMinimumGap proves Wait blocks a second call for the same key
// until the configured politeness delay has elapsed since the first.
func TestWait_EnforcesMinimumGap(t *testing.T) {
	db := testdb.New(t)
	svc := sourcegate.NewService(db, thresholds()) // 50ms delay
	ctx := context.Background()
	const key = "Comix"

	start := time.Now()
	svc.Wait(ctx, key) // first call: no prior access, returns immediately
	svc.Wait(ctx, key) // second call: must wait out the remaining delay
	elapsed := time.Since(start)

	if elapsed < 50*time.Millisecond {
		t.Errorf("elapsed = %v, want >= 50ms between two Wait calls", elapsed)
	}
}

// TestWait_DelayZeroIsNoOp proves a 0 min-request-delay disables politeness
// entirely — back-to-back Wait calls never block.
func TestWait_DelayZeroIsNoOp(t *testing.T) {
	db := testdb.New(t)
	svc := sourcegate.NewService(db, settings.Static{SourcesMinDelay: 0})
	ctx := context.Background()
	const key = "Comix"

	start := time.Now()
	for i := 0; i < 5; i++ {
		svc.Wait(ctx, key)
	}
	elapsed := time.Since(start)

	if elapsed > 25*time.Millisecond {
		t.Errorf("elapsed = %v, want near-instant with delay=0", elapsed)
	}
}

// TestWait_DifferentKeysDoNotSerialize proves the politeness delay is PER
// SOURCE: two different keys never block each other.
func TestWait_DifferentKeysDoNotSerialize(t *testing.T) {
	db := testdb.New(t)
	svc := sourcegate.NewService(db, thresholds()) // 50ms delay
	ctx := context.Background()

	start := time.Now()
	svc.Wait(ctx, "SourceA")
	svc.Wait(ctx, "SourceB")
	elapsed := time.Since(start)

	if elapsed >= 50*time.Millisecond {
		t.Errorf("elapsed = %v, want well under 50ms for two DIFFERENT keys", elapsed)
	}
}

// TestIsAvailable_FailsOpenOnReadError proves a DB read failure (here: the
// Ent client already closed) makes IsAvailable return true rather than
// wedging the caller — the fail-open guarantee.
func TestIsAvailable_FailsOpenOnReadError(t *testing.T) {
	db := testdb.New(t)
	svc := sourcegate.NewService(db, thresholds())

	if err := db.Close(); err != nil {
		t.Fatalf("close client: %v", err)
	}

	if !svc.IsAvailable(context.Background(), "AnySource", time.Now()) {
		t.Error("IsAvailable should fail OPEN (true) on a read error, not wedge the caller")
	}
}

// TestRecordFailure_BestEffortOnClosedClient proves RecordFailure never panics
// or blocks when the underlying write fails — it must log and swallow.
func TestRecordFailure_BestEffortOnClosedClient(t *testing.T) {
	db := testdb.New(t)
	svc := sourcegate.NewService(db, thresholds())

	if err := db.Close(); err != nil {
		t.Fatalf("close client: %v", err)
	}

	// Must not panic.
	svc.RecordFailure(context.Background(), "AnySource", errors.New("boom"), time.Now())
	svc.RecordSuccess(context.Background(), "AnySource")
}
