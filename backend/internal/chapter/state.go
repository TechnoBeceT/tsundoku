package chapter

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
)

// legalTransitions encodes the pinned chapter state machine as an adjacency map.
// Each key is a source state; its value is the set of legal target states.
// Any edge not present is illegal, including same-state self-loops (X→X).
//
// Legal edges (M1 controller contract):
//
//	wanted             → downloading
//	downloading        → downloaded
//	downloading        → failed
//	downloaded         → upgrade_available
//	upgrade_available  → upgrading
//	upgrading          → downloaded      (success or failure; working copy retained)
//	failed             → downloading
//	failed             → permanently_failed
//	failed             → wanted          (owner-retry-only — see below)
//	permanently_failed → wanted          (owner-retry-only — the one terminal escape)
//
// Owner-retry edges (Downloads milestone): failed→wanted and
// permanently_failed→wanted are the only edges that target wanted, and they are
// reachable ONLY through the owner-initiated retry action (downloads.RetryChapter
// / RetryAll). The automatic download dispatcher NEVER targets wanted, so the
// auto-pipeline's terminal semantics are unchanged: in normal operation a chapter
// only reaches wanted on first discovery (ingest). permanently_failed is no longer
// strictly terminal — it has exactly one sanctioned owner escape hatch, mirroring
// the never-auto-delete model (a state reset is an owner action, never automatic).
var legalTransitions = map[entchapter.State]map[entchapter.State]struct{}{
	entchapter.StateWanted: {
		entchapter.StateDownloading: {},
	},
	entchapter.StateDownloading: {
		entchapter.StateDownloaded: {},
		entchapter.StateFailed:     {},
	},
	entchapter.StateDownloaded: {
		entchapter.StateUpgradeAvailable: {},
	},
	entchapter.StateUpgradeAvailable: {
		entchapter.StateUpgrading: {},
	},
	entchapter.StateUpgrading: {
		entchapter.StateDownloaded: {},
	},
	entchapter.StateFailed: {
		entchapter.StateDownloading:       {},
		entchapter.StatePermanentlyFailed: {},
		entchapter.StateWanted:            {}, // owner retry (clears failure fields)
	},
	entchapter.StatePermanentlyFailed: {
		entchapter.StateWanted: {}, // owner reset — the single terminal escape
	},
}

// CanTransition reports whether a state transition from → to is permitted by the
// chapter state machine. Every edge absent from legalTransitions is illegal,
// including same-state self-loops (X→X) and any transition out of
// permanently_failed.
func CanTransition(from, to entchapter.State) bool {
	targets, ok := legalTransitions[from]
	if !ok {
		return false
	}
	_, allowed := targets[to]
	return allowed
}

// SetState loads the current state of the Chapter identified by chapterID, checks
// whether the transition to the target state is permitted by CanTransition, and
// applies the update if so. If the transition is not permitted, it returns a
// descriptive error and performs no mutation.
func SetState(ctx context.Context, client *ent.Client, chapterID uuid.UUID, to entchapter.State) error {
	ch, err := client.Chapter.Get(ctx, chapterID)
	if err != nil {
		return fmt.Errorf("chapter.SetState: load chapter %s: %w", chapterID, err)
	}

	if !CanTransition(ch.State, to) {
		return fmt.Errorf("chapter.SetState: illegal transition %s → %s", ch.State, to)
	}

	// Defensive path: Exec error is only reachable if the DB connection is lost
	// between loading the chapter and persisting the new state — not reachable
	// under normal operation.
	if err := client.Chapter.UpdateOneID(chapterID).SetState(to).Exec(ctx); err != nil {
		return fmt.Errorf("chapter.SetState: persist state %s for chapter %s: %w", to, chapterID, err)
	}

	return nil
}
