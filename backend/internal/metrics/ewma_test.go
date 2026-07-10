package metrics_test

import (
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/metrics"
)

// TestNextEwma covers the seeding rule (first sample) and the blended update.
func TestNextEwma(t *testing.T) {
	cases := []struct {
		name       string
		prev       int
		sampleMs   int
		wantResult int
	}{
		{"seed on zero prev", 0, 500, 500},
		{"seed on negative prev", -3, 250, 250},
		{"blend equal", 100, 100, 100},
		{"blend up alpha 0.3", 100, 200, 130},   // 0.3*200 + 0.7*100
		{"blend down alpha 0.3", 200, 100, 170}, // 0.3*100 + 0.7*200
		{"rounds to nearest", 100, 103, 101},    // 0.3*103 + 0.7*100 = 100.9 -> 101
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := metrics.NextEwma(tc.prev, tc.sampleMs); got != tc.wantResult {
				t.Errorf("NextEwma(%d, %d) = %d, want %d", tc.prev, tc.sampleMs, got, tc.wantResult)
			}
		})
	}
}

// TestIsSlow proves a never-measured (nil) source is slow, an over-threshold
// EWMA is slow, and an under-threshold one is not.
func TestIsSlow(t *testing.T) {
	const threshold = 5000
	if !metrics.IsSlow(nil, threshold) {
		t.Error("IsSlow(nil): want true (never measured is slow)")
	}
	fast := &ent.SourceMetric{EwmaLatencyMs: 1200}
	if metrics.IsSlow(fast, threshold) {
		t.Error("IsSlow(fast): want false")
	}
	slow := &ent.SourceMetric{EwmaLatencyMs: 9000}
	if !metrics.IsSlow(slow, threshold) {
		t.Error("IsSlow(slow): want true")
	}
	atThreshold := &ent.SourceMetric{EwmaLatencyMs: 5000}
	if metrics.IsSlow(atThreshold, threshold) {
		t.Error("IsSlow(at threshold): want false (strictly greater is slow)")
	}
}

// TestIsStaleWarm proves a never-measured (nil) source and a never-warmed (nil
// LastWarmedAt) source are stale, a just-warmed source is not, an over-TTL warm is
// stale, and exactly at the TTL boundary is not stale (strictly greater).
func TestIsStaleWarm(t *testing.T) {
	const ttl = 12 * time.Minute
	now := time.Now()

	if !metrics.IsStaleWarm(nil, ttl, now) {
		t.Error("IsStaleWarm(nil): want true (never measured is stale)")
	}

	neverWarmed := &ent.SourceMetric{EwmaLatencyMs: 1000} // LastWarmedAt nil
	if !metrics.IsStaleWarm(neverWarmed, ttl, now) {
		t.Error("IsStaleWarm(never warmed): want true")
	}

	justWarmed := warmedAt(now.Add(-time.Minute))
	if metrics.IsStaleWarm(justWarmed, ttl, now) {
		t.Error("IsStaleWarm(warmed 1m ago): want false")
	}

	overTTL := warmedAt(now.Add(-13 * time.Minute))
	if !metrics.IsStaleWarm(overTTL, ttl, now) {
		t.Error("IsStaleWarm(warmed 13m ago): want true (older than 12m TTL)")
	}

	atBoundary := warmedAt(now.Add(-ttl))
	if metrics.IsStaleWarm(atBoundary, ttl, now) {
		t.Error("IsStaleWarm(warmed exactly ttl ago): want false (strictly greater is stale)")
	}
}

// warmedAt returns a SourceMetric whose LastWarmedAt is the given instant.
func warmedAt(t time.Time) *ent.SourceMetric {
	return &ent.SourceMetric{LastWarmedAt: &t}
}
