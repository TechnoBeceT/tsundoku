package fetcher_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/fetcher"
)

// TestProgressFrom_NoSink verifies that ProgressFrom on a context with no sink
// returns a usable no-op (never nil), so callers can invoke it unconditionally
// without a nil check.
func TestProgressFrom_NoSink(t *testing.T) {
	t.Parallel()

	sink := fetcher.ProgressFrom(context.Background())
	if sink == nil {
		t.Fatal("ProgressFrom on a bare context must return a non-nil no-op, got nil")
	}
	// Invoking the no-op must not panic.
	sink(1, 2)
}

// TestProgressFrom_RoundTrip verifies that a sink set via WithProgress is resolved
// by ProgressFrom and receives the exact (current, total) it is invoked with.
func TestProgressFrom_RoundTrip(t *testing.T) {
	t.Parallel()

	type call struct{ current, total int }
	var seen []call

	ctx := fetcher.WithProgress(context.Background(), func(current, total int) {
		seen = append(seen, call{current, total})
	})

	sink := fetcher.ProgressFrom(ctx)
	if sink == nil {
		t.Fatal("ProgressFrom must return the sink set via WithProgress, got nil")
	}
	sink(3, 10)

	if len(seen) != 1 {
		t.Fatalf("sink call count: got %d, want 1", len(seen))
	}
	if seen[0] != (call{3, 10}) {
		t.Errorf("sink saw %+v, want {current:3 total:10}", seen[0])
	}
}
