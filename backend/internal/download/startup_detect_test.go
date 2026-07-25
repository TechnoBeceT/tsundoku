// Package download_test — proof for the one-time STARTUP upgrade-detection pass
// (GAP-112 follow-up). At boot chapter.ResetOrphanedChapters unflags every stranded
// upgrade_available chapter back to downloaded, and GAP-112 moved detection off the
// per-download-cycle path onto the post-refresh pass — whose loop waits a full
// refresh_interval (~2h) before its first sweep. So without an explicit startup
// detection call the upgrade queue sits EMPTY for ~2h after any restart. main wires
// runStartupUpgradeDetection to close that gap; this test pins the behaviour it
// delivers using the shared detection harness/fixtures.
//
// Requires Docker (via testcontainers) for an ephemeral PostgreSQL instance.
package download_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
)

// TestStartupUpgradeDetection_RepopulatesQueueAfterOrphanReset reproduces the boot
// sequence — prior detection flagged an upgrade, the orphan-reset unflagged it, and
// the startup detection re-flags it — proving the upgrade queue is repopulated at
// boot instead of waiting for the first (~2h-out) refresh sweep. The middle assertion
// (upgrade_available == 0 after the reset) is exactly the empty-queue state a restart
// would be stuck in WITHOUT the startup detection call; the final assertion proves the
// call restores it.
func TestStartupUpgradeDetection_RepopulatesQueueAfterOrphanReset(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	// One upgrade candidate: a chapter satisfied by a now-demoted source while a
	// strictly-higher DIFFERENT source is available (the canonical flag case, mirroring
	// detect_batch_test's Case 1).
	s := client.Series.Create().SetTitle("startup-detect").SetSlug("startup-detect").SaveX(ctx)
	spLow := seedSource(ctx, t, client, s, "sd-low", 20, "k-sd")
	seedSource(ctx, t, client, s, "sd-high", 60, "k-sd")
	seedDownloadedChapter(ctx, t, client, s, "k-sd", &spLow.ID, 60)

	upgradeAvailable := func() int {
		return client.Chapter.Query().
			Where(entchapter.StateEQ(entchapter.StateUpgradeAvailable)).
			CountX(ctx)
	}

	// A prior run flagged the candidate into the upgrade queue.
	if _, err := download.DetectUpgrades(ctx, client, 3); err != nil {
		t.Fatalf("initial DetectUpgrades: %v", err)
	}
	if got := upgradeAvailable(); got != 1 {
		t.Fatalf("pre-reset upgrade_available = %d, want 1", got)
	}

	// The boot orphan-reset unflags every upgrade_available chapter back to downloaded —
	// the upgrade queue is now EMPTY. This is the exact post-restart state: pre-GAP-112
	// the per-cycle detection re-flagged it immediately; now detection only runs after a
	// refresh sweep, a full refresh_interval (~2h) after boot.
	if _, err := chapter.ResetOrphanedChapters(ctx, client); err != nil {
		t.Fatalf("ResetOrphanedChapters: %v", err)
	}
	if got := upgradeAvailable(); got != 0 {
		t.Fatalf("post-reset upgrade_available = %d, want 0 (the queue is empty until something re-detects)", got)
	}

	// The one-time startup detection pass (runStartupUpgradeDetection in main, which
	// calls dispatcher.DetectUpgrades — the nil-gate-equivalent of this package function)
	// re-flags the still-qualifying candidate right away, so the queue is repopulated at
	// boot rather than waiting out the ~2h sweep. WITHOUT that call the queue stays at
	// the 0 asserted above.
	if _, err := download.DetectUpgrades(ctx, client, 3); err != nil {
		t.Fatalf("startup DetectUpgrades: %v", err)
	}
	if got := upgradeAvailable(); got != 1 {
		t.Fatalf("post-startup-detection upgrade_available = %d, want 1 (startup detection must repopulate the queue)", got)
	}
}
