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
		// Legal edges — every one must return true.
		{"wanted→downloading", entchapter.StateWanted, entchapter.StateDownloading, true},
		{"downloading→downloaded", entchapter.StateDownloading, entchapter.StateDownloaded, true},
		{"downloading→failed", entchapter.StateDownloading, entchapter.StateFailed, true},
		{"downloaded→upgrade_available", entchapter.StateDownloaded, entchapter.StateUpgradeAvailable, true},
		{"upgrade_available→upgrading", entchapter.StateUpgradeAvailable, entchapter.StateUpgrading, true},
		{"upgrade_available→downloaded (boot orphan-recovery)", entchapter.StateUpgradeAvailable, entchapter.StateDownloaded, true},
		{"upgrading→downloaded", entchapter.StateUpgrading, entchapter.StateDownloaded, true},
		{"failed→downloading", entchapter.StateFailed, entchapter.StateDownloading, true},
		{"failed→permanently_failed", entchapter.StateFailed, entchapter.StatePermanentlyFailed, true},
		// Owner-retry edges (Downloads milestone) — among the edges targeting wanted.
		{"failed→wanted (owner retry)", entchapter.StateFailed, entchapter.StateWanted, true},
		{"permanently_failed→wanted (owner reset)", entchapter.StatePermanentlyFailed, entchapter.StateWanted, true},
		// Owner re-download edge (QCAT-343): a DOWNLOADED chapter goes back to
		// wanted so the engine re-fetches it over the existing CBZ. Distinct in
		// kind from retry — retry gives a chapter with NO file another go.
		{"downloaded→wanted (owner re-download)", entchapter.StateDownloaded, entchapter.StateWanted, true},
		// Terminal-exhaustion edges (multi-source engine) — permanent failure can be
		// observed mid-cycle (from downloading, last live source just exhausted) or
		// on entry (from wanted, all sources already exhausted).
		{"downloading→permanently_failed", entchapter.StateDownloading, entchapter.StatePermanentlyFailed, true},
		{"wanted→permanently_failed", entchapter.StateWanted, entchapter.StatePermanentlyFailed, true},

		// Illegal edges — must return false.
		// permanently_failed now has exactly ONE outgoing edge (→wanted); every
		// other target must stay illegal.
		{"permanently_failed→downloading (still illegal)", entchapter.StatePermanentlyFailed, entchapter.StateDownloading, false},
		{"permanently_failed→failed (still illegal)", entchapter.StatePermanentlyFailed, entchapter.StateFailed, false},
		// Skip-a-state.
		{"wanted→downloaded (skip)", entchapter.StateWanted, entchapter.StateDownloaded, false},
		// Self-loop.
		{"downloading→downloading (self-loop)", entchapter.StateDownloading, entchapter.StateDownloading, false},
		// Backward edge — an IN-FLIGHT chapter must still not reach wanted; only the
		// three owner-initiated edges (failed / permanently_failed / downloaded) may.
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

func TestCanTransition_Superseded(t *testing.T) {
	cases := []struct {
		from, to entchapter.State
		want     bool
	}{
		{entchapter.StateWanted, entchapter.StateSuperseded, true},
		{entchapter.StateDownloaded, entchapter.StateSuperseded, true},
		{entchapter.StateSuperseded, entchapter.StateWanted, true},
		{entchapter.StateSuperseded, entchapter.StateDownloading, false},
		{entchapter.StateSuperseded, entchapter.StateSuperseded, false},
		{entchapter.StateFailed, entchapter.StateSuperseded, false}, // failed is NOT a supersede source edge
	}
	for _, c := range cases {
		if got := chapter.CanTransition(c.from, c.to); got != c.want {
			t.Errorf("CanTransition(%s→%s) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}
