package metrics_test

import (
	"testing"

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
