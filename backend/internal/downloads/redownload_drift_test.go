package downloads_test

import (
	"context"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/downloads"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
)

// driftDriver wraps an Ent SQL driver and runs ONE out-of-band callback the first
// time a read completes, which is what makes the bulk re-download's select-then-
// update window deterministically observable: the sweep SELECTs its matching set
// outside the transaction, so anything the download cycle does to those rows in
// between has to be judged by the UPDATE itself.
//
// Test-only, and single-goroutine by construction (the callback fires on the
// caller's own goroutine, between the two statements), so it needs no locking.
type driftDriver struct {
	dialect.Driver
	// after runs once, immediately after the next read returns; nil disarms it.
	after func()
}

// Query delegates the read, then fires the armed drift callback exactly once.
func (d *driftDriver) Query(ctx context.Context, query string, args, v any) error {
	err := d.Driver.Query(ctx, query, args, v)
	if d.after != nil {
		fire := d.after
		d.after = nil
		fire()
	}
	return err
}

// TestRedownloadAll_LeavesAChapterThatDriftedOutOfDownloaded pins the state guard on
// the bulk UPDATE.
//
// The matching set is SELECTed outside the transaction, so between the select and
// the update the download cycle can move a row on — to upgrade_available, or (as
// here) to superseded, which is the AUTOMATIC split-part suppression that deletes
// the CBZ and clears the filename. An ID-keyed update with no state predicate then
// force-writes wanted from whatever state the row actually holds, bypassing
// chapter.CanTransition and resurrecting a part the suppression pass just parked.
// applyRetryAll's update re-filters on state for exactly this reason; this one must
// too.
func TestRedownloadAll_LeavesAChapterThatDriftedOutOfDownloaded(t *testing.T) {
	ctx := context.Background()
	_, db := testdb.NewWithSQL(t)
	drv := &driftDriver{Driver: entsql.OpenDB(dialect.Postgres, db)}
	client := ent.NewClient(ent.Driver(drv))
	// A second client on the same database stands in for the download cycle: it
	// writes through its OWN driver, so its update is invisible to the sweep's
	// already-materialised select.
	cycle := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))

	seed := seedRedownload(ctx, t, client)

	// Arm the interleave: the instant the sweep's select returns, the suppression
	// pass supersedes one of the three rows it just picked up.
	drv.after = func() {
		cycle.Chapter.UpdateOneID(seed.rewritten).
			SetState(entchapter.StateSuperseded).
			SetFilename("").
			ExecX(ctx)
	}

	requeued, err := downloads.NewService(client).RedownloadAll(ctx, comixFilter())
	if err != nil {
		t.Fatalf("RedownloadAll: %v", err)
	}

	if got := chapterByID(ctx, t, client, seed.rewritten).State; got != entchapter.StateSuperseded {
		t.Errorf("drifted chapter state = %s; want superseded — the ID-keyed update force-wrote wanted from an arbitrary state, bypassing chapter.CanTransition", got)
	}
	if requeued != 2 {
		t.Errorf("requeued = %d; want 2 — the reported count must be the rows actually affected, not the size of the stale selection", requeued)
	}
	// The two rows that did NOT drift are still re-queued: the guard narrows the
	// update, it must not break it.
	for name, id := range map[string]uuid.UUID{"c-after": seed.afterCutoff, "c-valir": seed.valirChapter} {
		if got := chapterByID(ctx, t, client, id).State; got != entchapter.StateWanted {
			t.Errorf("%s state = %s; want wanted", name, got)
		}
	}
}
