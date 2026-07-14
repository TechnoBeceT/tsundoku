package sync_test

import (
	"testing"

	"github.com/technobecet/tsundoku/internal/tracker/sync"
)

// TestConverge pins the max-wins conflict rule: both sides always settle on
// the HIGHER of local/remote, never the lower and never an average. Each
// case fails under a "min wins" or "average" mutation.
func TestConverge(t *testing.T) {
	tests := []struct {
		name          string
		local, remote float64
		want          float64
	}{
		{"local higher wins", 60, 50, 60},
		{"remote higher wins", 50, 60, 60},
		{"equal stays equal", 42, 42, 42},
		{"zero both stays zero", 0, 0, 0},
		{"local higher with fractional values", 12.5, 12, 12.5},
		{"remote higher with fractional values", 12, 12.5, 12.5},
		{"the owner's own worked example: local 50 vs remote 60 converges to 60", 50, 60, 60},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sync.Converge(tt.local, tt.remote)
			if got != tt.want {
				t.Fatalf("Converge(%v, %v) = %v, want %v", tt.local, tt.remote, got, tt.want)
			}
		})
	}
}

// TestConverge_NeverBelowEitherSide is the adversarial proof: converged
// must never be lower than BOTH inputs — that would mean progress was lost
// on both sides, the exact failure max-wins exists to prevent.
func TestConverge_NeverBelowEitherSide(t *testing.T) {
	pairs := [][2]float64{{10, 20}, {20, 10}, {0, 5}, {5, 0}, {7.5, 7.5}}
	for _, p := range pairs {
		got := sync.Converge(p[0], p[1])
		if got < p[0] || got < p[1] {
			t.Fatalf("Converge(%v, %v) = %v, which is below one of the inputs", p[0], p[1], got)
		}
	}
}
