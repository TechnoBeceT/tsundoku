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
		// Owner-retry edges (Downloads milestone) — the only edges targeting wanted.
		{"failed→wanted (owner retry)", entchapter.StateFailed, entchapter.StateWanted, true},
		{"permanently_failed→wanted (owner reset)", entchapter.StatePermanentlyFailed, entchapter.StateWanted, true},

		// Illegal edges — must return false.
		// permanently_failed now has exactly ONE outgoing edge (→wanted); every
		// other target must stay illegal.
		{"permanently_failed→downloading (still illegal)", entchapter.StatePermanentlyFailed, entchapter.StateDownloading, false},
		{"permanently_failed→failed (still illegal)", entchapter.StatePermanentlyFailed, entchapter.StateFailed, false},
		// Skip-a-state.
		{"wanted→downloaded (skip)", entchapter.StateWanted, entchapter.StateDownloaded, false},
		// Self-loop.
		{"downloading→downloading (self-loop)", entchapter.StateDownloading, entchapter.StateDownloading, false},
		// Backward edge — downloaded must NOT reach wanted (only failed /
		// permanently_failed may, via the owner-retry edges).
		{"downloaded→wanted (still illegal)", entchapter.StateDownloaded, entchapter.StateWanted, false},
		{"downloading→wanted (still illegal)", entchapter.StateDownloading, entchapter.StateWanted, false},
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
