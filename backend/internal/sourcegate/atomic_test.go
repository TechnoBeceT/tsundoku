// Package sourcegate_test exercises sourcegate's database-atomic breaker
// transitions under concurrent callers against ephemeral PostgreSQL.
package sourcegate_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	entsourcecircuitstate "github.com/technobecet/tsundoku/internal/ent/sourcecircuitstate"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourcegate"
)

// TestRecordFailure_ConcurrentFirstFailuresCountEveryFailureAndTripAtThreshold
// catches a query-then-create/update implementation that loses first-row
// increments or swallows unique-key collisions when many workers fail together.
func TestRecordFailure_ConcurrentFirstFailuresCountEveryFailureAndTripAtThreshold(t *testing.T) {
	db := testdb.New(t)
	const (
		key      = "Concurrent First Failure"
		failures = 24
	)
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	cooldown := 17 * time.Minute
	svc := sourcegate.NewService(db, settings.Static{
		SourcesFailureThresh: failures,
		SourcesCooldownIv:    cooldown,
	})

	runConcurrent(failures, func() {
		svc.RecordFailure(context.Background(), key, errors.New("captcha"), now)
	})

	row, err := db.SourceCircuitState.Query().
		Where(entsourcecircuitstate.SourceKeyEQ(key)).
		Only(context.Background())
	if err != nil {
		t.Fatalf("load breaker row: %v", err)
	}
	if row.ConsecutiveFailures != failures {
		t.Fatalf("consecutive_failures = %d, want every one of %d concurrent failures", row.ConsecutiveFailures, failures)
	}
	if row.CooldownUntil == nil {
		t.Fatal("cooldown_until = nil, want cooldown set by the threshold-crossing failure")
	}
	if want := now.Add(cooldown); !row.CooldownUntil.Equal(want) {
		t.Fatalf("cooldown_until = %v, want exact threshold trip time %v", row.CooldownUntil, want)
	}
	if row.FailingSince == nil || !row.FailingSince.Equal(now) {
		t.Fatalf("failing_since = %v, want first failure time %v", row.FailingSince, now)
	}
}

// TestRecordFailure_ConcurrentExistingFailuresPreserveCountAndThresholdTime
// catches a read-modify-write update that lets concurrent failures overwrite one
// another after a breaker row already exists.
func TestRecordFailure_ConcurrentExistingFailuresPreserveCountAndThresholdTime(t *testing.T) {
	db := testdb.New(t)
	const (
		key        = "Concurrent Existing Failure"
		seeded     = 1
		concurrent = 24
		threshold  = seeded + concurrent
	)
	started := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	tripAt := started.Add(time.Minute)
	cooldown := 23 * time.Minute
	svc := sourcegate.NewService(db, settings.Static{
		SourcesFailureThresh: threshold,
		SourcesCooldownIv:    cooldown,
	})
	svc.RecordFailure(context.Background(), key, errors.New("first captcha"), started)

	runConcurrent(concurrent, func() {
		svc.RecordFailure(context.Background(), key, errors.New("captcha"), tripAt)
	})

	row, err := db.SourceCircuitState.Query().
		Where(entsourcecircuitstate.SourceKeyEQ(key)).
		Only(context.Background())
	if err != nil {
		t.Fatalf("load breaker row: %v", err)
	}
	if row.ConsecutiveFailures != threshold {
		t.Fatalf("consecutive_failures = %d, want %d after seeded and concurrent failures", row.ConsecutiveFailures, threshold)
	}
	if row.CooldownUntil == nil {
		t.Fatal("cooldown_until = nil, want threshold crossing to trip the breaker")
	}
	if want := tripAt.Add(cooldown); !row.CooldownUntil.Equal(want) {
		t.Fatalf("cooldown_until = %v, want exact threshold trip time %v", row.CooldownUntil, want)
	}
	if row.FailingSince == nil || !row.FailingSince.Equal(started) {
		t.Fatalf("failing_since = %v, want original streak start %v", row.FailingSince, started)
	}
}

// TestRecordFailureAndSuccess_ConcurrentCallsResolveAsOneWholeTransition proves
// concurrent outcomes remain a valid serial success/failure ordering: either
// success is last and the streak is clear, or failure is last and it starts a
// fresh one. A mixed state would expose a non-atomic transition.
func TestRecordFailureAndSuccess_ConcurrentCallsResolveAsOneWholeTransition(t *testing.T) {
	db := testdb.New(t)
	const key = "Concurrent Success Failure"
	started := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	failureAt := started.Add(time.Minute)
	svc := sourcegate.NewService(db, settings.Static{
		SourcesFailureThresh: 3,
		SourcesCooldownIv:    time.Hour,
	})

	// Begin below threshold so either serial order has a distinct, valid final
	// state and neither outcome should be cooling down.
	svc.RecordFailure(context.Background(), key, errors.New("first"), started)
	svc.RecordFailure(context.Background(), key, errors.New("second"), started)

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		svc.RecordFailure(context.Background(), key, errors.New("captcha"), failureAt)
	}()
	go func() {
		defer wg.Done()
		<-start
		svc.RecordSuccess(context.Background(), key)
	}()
	close(start)
	wg.Wait()

	row, err := db.SourceCircuitState.Query().
		Where(entsourcecircuitstate.SourceKeyEQ(key)).
		Only(context.Background())
	if err != nil {
		t.Fatalf("load breaker row: %v", err)
	}
	if row.CooldownUntil != nil {
		t.Fatalf("cooldown_until = %v, want no cooldown in either serial outcome", row.CooldownUntil)
	}
	switch row.ConsecutiveFailures {
	case 0:
		if row.FailingSince != nil || row.LastError != "" {
			t.Fatalf("success-last state = failures=%d failing_since=%v last_error=%q, want fully reset streak", row.ConsecutiveFailures, row.FailingSince, row.LastError)
		}
	case 1:
		if row.FailingSince == nil || !row.FailingSince.Equal(failureAt) {
			t.Fatalf("failure-last failing_since = %v, want %v", row.FailingSince, failureAt)
		}
		if row.LastError != "captcha" {
			t.Fatalf("failure-last last_error = %q, want captcha", row.LastError)
		}
	default:
		t.Fatalf("consecutive_failures = %d, want one whole serial outcome (0 or 1)", row.ConsecutiveFailures)
	}
}

// runConcurrent starts all calls from one barrier and waits for every result.
// The production change that must make its callers fail is a non-atomic
// database transition that drops one of their writes.
func runConcurrent(n int, fn func()) {
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			<-start
			fn()
		}()
	}
	close(start)
	wg.Wait()
}
