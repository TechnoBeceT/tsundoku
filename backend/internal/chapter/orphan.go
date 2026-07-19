package chapter

import (
	"context"
	"errors"
	"fmt"

	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
)

// ResetResult reports how many chapters ResetOrphanedChapters moved out of
// each process-owned state, so the caller can log a useful startup line.
type ResetResult struct {
	// Requeued is the number of chapters moved downloading → wanted.
	Requeued int
	// UpgradesReset is the number of chapters moved upgrading → downloaded.
	UpgradesReset int
	// UpgradesUnflagged is the number of chapters moved upgrade_available →
	// downloaded (a stranded upgrade whose target was down / never processed).
	UpgradesUnflagged int
}

// ResetOrphanedChapters re-queues chapters stranded in a process-owned or
// mid-convergence state by a crash or restart mid-cycle: downloading → wanted
// (re-download from scratch — the fetch never finished, so nothing usable is on
// disk), upgrading → downloaded (the pre-upgrade CBZ is still on disk;
// DetectUpgrades will re-flag it upgrade_available next cycle if a better source
// still exists, so nothing is lost by not restarting the upgrade immediately),
// and upgrade_available → downloaded (a chapter DetectUpgrades flagged but which
// UpgradeAll never converged — e.g. the target source was down — survives even a
// restart otherwise; the pre-upgrade CBZ is intact, and DetectUpgrades re-flags
// it next cycle when a strictly-better source is reachable again).
//
// This is a SANCTIONED, startup-only bulk bypass of the per-chapter FSM:
// downloading→wanted is not a legal SetState edge (legalTransitions forbids it by
// design, because no live process should ever hop a chapter backwards mid-cycle);
// upgrading→downloaded and upgrade_available→downloaded ARE legal edges, but this
// sweep drives them in bulk without loading each row — but a crash is not a normal
// in-cycle transition, it is the ABSENCE of one. Call this exactly once at
// boot, before any download/refresh ticker starts, so it can never race a
// live fetch or upgrade in progress. That "cannot race a live cycle" guarantee
// is a SINGLE-INSTANCE precondition: it holds within one process (this boot
// step runs before that process starts its own tickers). Two backend instances
// pointed at the same DB — e.g. an overlapping rolling restart where a new
// instance sweeps while the old one is still mid-cycle — is outside the
// single-owner homelab threat model and not guarded here. A second, later call
// within one process is safe (see the idempotence test) but is not the intended
// use — this is a startup step, not a periodic sweep.
//
// Implemented as three bulk Ent updates (not a per-row loop and not SetState)
// because this is a mass, unconditional state flip over every row in each
// stranded state — there is no per-row business rule to evaluate, only
// "was this state process-owned / mid-convergence when the process died". All
// updates run in a
// SINGLE transaction so the sweep is all-or-nothing: on any error the rollback
// leaves every row untouched, which is why an error returns a zero ResetResult
// (no partial counts to reconcile). It deliberately does not touch anything
// else: not Chapter.last_error (these chapters had no error — they were simply
// mid-fetch), not file provenance (filename / satisfied_by_provider_id /
// satisfied_importance / page_count survive on the upgrading→downloaded rows),
// and not ProviderChapter's per-source retry state (attempts / next_attempt_at
// / last_error), which lives on a different table entirely and is untouched by
// any Chapter-state change.
func ResetOrphanedChapters(ctx context.Context, client *ent.Client) (ResetResult, error) {
	var result ResetResult
	err := withTx(ctx, client, func(tx *ent.Tx) error {
		requeued, err := tx.Chapter.Update().
			Where(entchapter.StateEQ(entchapter.StateDownloading)).
			SetState(entchapter.StateWanted).
			Save(ctx)
		if err != nil {
			return fmt.Errorf("requeue downloading: %w", err)
		}

		upgradesReset, err := tx.Chapter.Update().
			Where(entchapter.StateEQ(entchapter.StateUpgrading)).
			SetState(entchapter.StateDownloaded).
			Save(ctx)
		if err != nil {
			return fmt.Errorf("reset upgrading: %w", err)
		}

		upgradesUnflagged, err := tx.Chapter.Update().
			Where(entchapter.StateEQ(entchapter.StateUpgradeAvailable)).
			SetState(entchapter.StateDownloaded).
			Save(ctx)
		if err != nil {
			return fmt.Errorf("reset upgrade_available: %w", err)
		}

		result = ResetResult{
			Requeued:          requeued,
			UpgradesReset:     upgradesReset,
			UpgradesUnflagged: upgradesUnflagged,
		}
		return nil
	})
	if err != nil {
		return ResetResult{}, fmt.Errorf("chapter.ResetOrphanedChapters: %w", err)
	}
	return result, nil
}

// withTx runs fn inside a database transaction, committing on success and
// rolling back (joining any rollback error) on failure, so the two-statement
// orphan sweep can never half-apply. Mirrors the closure-based tx helper used
// by the downloads domain's retry reset (§2 DRY — same pattern, per-package
// because the helper is unexported there).
func withTx(ctx context.Context, client *ent.Client, fn func(tx *ent.Tx) error) error {
	tx, err := client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return errors.Join(err, fmt.Errorf("rollback: %w", rbErr))
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
