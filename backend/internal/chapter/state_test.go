package chapter_test

import (
	"testing"

	"github.com/technobecet/tsundoku/internal/chapter"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
)

// TestCanTransition verifies the chapter state machine against all legal edges
// and a representative sample of illegal edges. This is a pure function test —
// no database required.
func TestCanTransition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		from entchapter.State
		to   entchapter.State
		want bool
	}{
		// Legal edges — all 8 must return true.
		{"wanted→downloading", entchapter.StateWanted, entchapter.StateDownloading, true},
		{"downloading→downloaded", entchapter.StateDownloading, entchapter.StateDownloaded, true},
		{"downloading→failed", entchapter.StateDownloading, entchapter.StateFailed, true},
		{"downloaded→upgrade_available", entchapter.StateDownloaded, entchapter.StateUpgradeAvailable, true},
		{"upgrade_available→upgrading", entchapter.StateUpgradeAvailable, entchapter.StateUpgrading, true},
		{"upgrading→downloaded", entchapter.StateUpgrading, entchapter.StateDownloaded, true},
		{"failed→downloading", entchapter.StateFailed, entchapter.StateDownloading, true},
		{"failed→permanently_failed", entchapter.StateFailed, entchapter.StatePermanentlyFailed, true},

		// Illegal edges — must return false.
		// permanently_failed has no outgoing edges — covers the map-miss !ok path.
		{"permanently_failed→downloading (terminal)", entchapter.StatePermanentlyFailed, entchapter.StateDownloading, false},
		// Skip-a-state.
		{"wanted→downloaded (skip)", entchapter.StateWanted, entchapter.StateDownloaded, false},
		// Self-loop.
		{"downloading→downloading (self-loop)", entchapter.StateDownloading, entchapter.StateDownloading, false},
		// Backward edge.
		{"downloaded→wanted (backward)", entchapter.StateDownloaded, entchapter.StateWanted, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := chapter.CanTransition(tc.from, tc.to)
			if got != tc.want {
				t.Errorf("CanTransition(%s, %s) = %v; want %v", tc.from, tc.to, got, tc.want)
			}
		})
	}
}
