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
// Legal edges (controller contract):
//
//	wanted             → downloading
//	wanted             → permanently_failed  (every source already exhausted — see below)
//	downloading        → downloaded
//	downloading        → failed
//	downloading        → permanently_failed  (last live source exhausted this cycle — see below)
//	downloaded         → upgrade_available
//	upgrade_available  → upgrading
//	upgrade_available  → downloaded      (boot orphan-recovery only — see below)
//	upgrading          → downloaded      (success or failure; working copy retained)
//	failed             → downloading
//	failed             → permanently_failed
//	failed             → wanted          (owner-retry-only — see below)
//	permanently_failed → wanted          (owner-retry-only — the one terminal escape)
//
// Terminal-exhaustion edges (multi-source download engine): permanently_failed is
// now reached ONLY when EVERY source offering a chapter has spent its per-source
// retry budget (see chapter.AllProvidersExhausted) — never from a single
// per-chapter counter. That exhaustion can be observed either mid-cycle from
// downloading (the last live source just failed its final attempt) or on entry
// from wanted/failed (all sources were already exhausted before this cycle), so
// downloading→permanently_failed and wanted→permanently_failed are both legal.
// failed→permanently_failed pre-existed.
//
// Owner-retry edges (Downloads milestone): failed→wanted and
// permanently_failed→wanted are the only edges that target wanted, and they are
// reachable ONLY through the owner-initiated retry action (downloads.RetryChapter
// / RetryAll, which also resets the per-source ProviderChapter retry state). The
// automatic download dispatcher NEVER targets wanted, so the auto-pipeline's
// terminal semantics are unchanged: in normal operation a chapter only reaches
// wanted on first discovery (ingest). permanently_failed is no longer strictly
// terminal — it has exactly one sanctioned owner escape hatch, mirroring the
// never-auto-delete model (a state reset is an owner action, never automatic).
//
// Ignored edges (fractional suppression): wanted→ignored and failed→ignored park
// an UNDOWNLOADED fractional chapter whose EVERY carrier is a source the owner
// flagged ignore_fractional. Left wanted, such a chapter can never download (the
// dispatcher drops all its sources from candidacy) yet clutters the queue and the
// chapter list forever; ignored is the terminal hidden resting state for it. This
// is NOT a deletion — the Chapter row and every ProviderChapter feed row are kept
// (never-auto-delete), so ignored→wanted reverses it the instant a non-ignoring
// carrier reappears (the owner un-ticks the toggle, or adds a source). The
// resurrection guard is EVERY-carrier-ignored: a fractional a non-ignored source
// also carries stays wanted and downloads normally, so it never lands in ignored.
//
// Boot orphan-recovery edge (upgrade_available→downloaded): a chapter is left in
// upgrade_available only between DetectUpgrades (which flags it) and UpgradeAll
// (which drives it back to downloaded). If a source that was the upgrade target
// is down, or the process restarts mid-cycle, a chapter can strand in
// upgrade_available. chapter.ResetOrphanedChapters resets it → downloaded at boot
// (the pre-upgrade CBZ is intact on disk, so nothing is lost); DetectUpgrades
// re-flags it next cycle if a strictly-better source still exists. Like the other
// two orphan-recovery edges (downloading→wanted, upgrading→downloaded), this is a
// SANCTIONED, startup-only bulk bypass — no live in-cycle transition uses it.
var legalTransitions = map[entchapter.State]map[entchapter.State]struct{}{
	entchapter.StateWanted: {
		entchapter.StateDownloading:       {},
		entchapter.StatePermanentlyFailed: {}, // all sources already exhausted on entry
		entchapter.StateSuperseded:        {}, // whole N downloaded + >=2 parts (fractional-part suppression)
		entchapter.StateIgnored:           {}, // fractional whose every carrier ignores fractionals (see below)
	},
	entchapter.StateDownloading: {
		entchapter.StateDownloaded:        {},
		entchapter.StateFailed:            {},
		entchapter.StatePermanentlyFailed: {}, // last live source exhausted this cycle
	},
	entchapter.StateDownloaded: {
		entchapter.StateUpgradeAvailable: {},
		entchapter.StateSuperseded:       {}, // an already-downloaded part superseded by its whole
	},
	entchapter.StateUpgradeAvailable: {
		entchapter.StateUpgrading:  {},
		entchapter.StateDownloaded: {}, // boot orphan-recovery: un-flag a stranded upgrade (see below)
	},
	entchapter.StateUpgrading: {
		entchapter.StateDownloaded: {},
	},
	entchapter.StateFailed: {
		entchapter.StateDownloading:       {},
		entchapter.StatePermanentlyFailed: {},
		entchapter.StateWanted:            {}, // owner retry (clears failure fields)
		entchapter.StateIgnored:           {}, // fractional whose every carrier ignores fractionals (see below)
	},
	entchapter.StatePermanentlyFailed: {
		entchapter.StateWanted: {}, // owner reset — the single terminal escape
	},
	entchapter.StateSuperseded: {
		entchapter.StateWanted: {}, // reversal: whole N gone, or setting disabled — the single escape
	},
	entchapter.StateIgnored: {
		entchapter.StateWanted: {}, // reversal: a now-un-ignored carrier appeared — the single escape
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
