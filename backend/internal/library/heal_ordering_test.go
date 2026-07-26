package library_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/fetcher/fake"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sse"
)

// detectMaxRetries is the retry budget handed to DetectUpgrades. Detection uses
// it only to exclude candidates that have exhausted their attempts; none of these
// fixtures has any attempts recorded, so the value is immaterial — it just has to
// be the production shape (a positive budget).
const detectMaxRetries = 3

// newDetector builds the real upgrade DETECTOR over the same database. Detection
// is pure DB and makes zero source calls, so the fake fetcher is never invoked;
// it exists only to satisfy the Dispatcher's port.
func newDetector(client *ent.Client, storage string) *download.Dispatcher {
	return download.New(
		client,
		fake.New(),
		sse.NewHub(),
		download.Config{Storage: storage},
		settings.Static{Retries: detectMaxRetries, Backoff: time.Hour},
		nil,
	)
}

// detectFlagged runs one real whole-library detection pass and returns how many
// chapters it moved into upgrade_available — i.e. how many the Trigger that
// follows in runRefreshSweep would RE-FETCH.
func detectFlagged(t *testing.T, client *ent.Client, ctx context.Context, storage string) int {
	t.Helper()
	flagged, err := newDetector(client, storage).DetectUpgrades(ctx, detectMaxRetries)
	if err != nil {
		t.Fatalf("DetectUpgrades: %v", err)
	}
	return flagged
}

// flaggedState counts the series' chapters actually sitting in upgrade_available,
// so a detection return value is cross-checked against persisted state.
func flaggedState(t *testing.T, client *ent.Client, ctx context.Context, seriesID uuid.UUID) int {
	t.Helper()
	return client.Chapter.Query().
		Where(entchapter.SeriesID(seriesID), entchapter.StateEQ(entchapter.StateUpgradeAvailable)).
		CountX(ctx)
}

// runHeal runs one self-heal pass and returns its merged/skipped counts.
func runHeal(t *testing.T, client *ent.Client, ctx context.Context, storage string) (merged, skipped int) {
	t.Helper()
	triggered := 0
	merged, skipped, err := healSvc(client, storage, &triggered).HealDriftedProviders(ctx)
	if err != nil {
		t.Fatalf("HealDriftedProviders: %v", err)
	}
	return merged, skipped
}

// TestProviderHeal_MustRunBeforeUpgradeDetection is the ORDERING proof for
// job.Runner.runRefreshSweep, which runs the self-heal BETWEEN the discovery sweep
// and upgrade detection. That edge was shipped documented but untested.
//
// The hazard is concrete. A disk-imported series' chapters are satisfied at the
// disk provider's watermark (importance 1). Once refresh populates the live twin's
// feed, that twin offers the very same chapter keys at its real rank (5 here), so
// detection's strict `5 > 1` flags EVERY chapter of the series upgrade_available
// and the Trigger right after it re-downloads a library the owner already has on
// disk. The heal closes it by folding the pair first: the merge re-points those
// chapters onto the live provider and sets satisfied_importance to that provider's
// importance in one transaction, leaving importance == satisfied_importance, which
// a strict `>` cannot fire on.
//
// The three sub-cases pin the whole shape — the hazard is real (A), the order
// defeats it (B), and B's zero comes from the MERGE rather than some incidental
// property of the fixture (C).
//
// HONESTY NOTE: the ordering does NOT make the hazard unreachable. The heal's
// errors are logged and swallowed (job.Runner.runProviderHeal), and a pair the
// empty-feed orphan guard declines is not merged either — in both cases the sweep
// runs detection immediately afterwards and flags the series exactly as sub-case A
// shows. That is pre-existing behaviour, not a regression, and the guard is
// deliberately kept; but the ordering rationale must not be read as "the mass
// re-download can no longer happen".
func TestProviderHeal_MustRunBeforeUpgradeDetection(t *testing.T) {
	t.Run("detect before heal flags the whole imported series", assertDetectFirstFlagsEverything)
	t.Run("heal before detect flags nothing", assertHealFirstFlagsNothing)
	t.Run("an unmergeable pair is flagged either way", assertUnmergeablePairIsFlaggedAnyway)
}

// assertDetectFirstFlagsEverything is sub-case A: the hazard itself. Detection run
// before the heal sees the PRE-merge state and flags both downloaded chapters —
// the mass re-download the ordering exists to stop. Without this the other
// sub-cases would prove nothing.
func assertDetectFirstFlagsEverything(t *testing.T) {
	ctx := context.Background()
	storage := t.TempDir()
	client := testdb.New(t)

	ser := importedDiskSeries(t, client, storage, "My Series", "mangadex", "Alpha")
	attachLinkedTwin(t, client, ctx, ser.ID, "mangadex", "Alpha", 5, true)

	if flagged := detectFlagged(t, client, ctx, storage); flagged != 2 {
		t.Fatalf("detect-first flagged %d chapters, want 2 — the hazard must be real for this test to mean anything", flagged)
	}
	if state := flaggedState(t, client, ctx, ser.ID); state != 2 {
		t.Fatalf("upgrade_available rows = %d, want 2", state)
	}
}

// assertHealFirstFlagsNothing is sub-case B: the fix. Heal first, then run the
// SAME detector over the SAME fixture — nothing is flagged, so the Trigger that
// follows in the sweep has no re-download to do.
func assertHealFirstFlagsNothing(t *testing.T) {
	ctx := context.Background()
	storage := t.TempDir()
	client := testdb.New(t)

	ser := importedDiskSeries(t, client, storage, "My Series", "mangadex", "Alpha")
	attachLinkedTwin(t, client, ctx, ser.ID, "mangadex", "Alpha", 5, true)

	if merged, _ := runHeal(t, client, ctx, storage); merged != 1 {
		t.Fatalf("merged = %d, want 1 — with no fold there is no ordering to test", merged)
	}
	if flagged := detectFlagged(t, client, ctx, storage); flagged != 0 {
		t.Fatalf("heal-first flagged %d chapters, want 0 — healing before detection is what prevents the re-download", flagged)
	}
	if state := flaggedState(t, client, ctx, ser.ID); state != 0 {
		t.Fatalf("upgrade_available rows = %d, want 0", state)
	}
}

// assertUnmergeablePairIsFlaggedAnyway is sub-case C: the control. When the pair
// CANNOT fold (the disk name can never equal the live display name under the
// ratified strict match) the ordering changes nothing and both chapters are
// flagged regardless — which is also the honest picture of the cases the ordering
// still does not cover.
func assertUnmergeablePairIsFlaggedAnyway(t *testing.T) {
	ctx := context.Background()
	storage := t.TempDir()
	client := testdb.New(t)

	ser := importedDiskSeries(t, client, storage, "Kali Series", "KaliScan.me", "")
	attachLinkedTwin(t, client, ctx, ser.ID, "KaliScan", "", 5, true)

	merged, skipped := runHeal(t, client, ctx, storage)
	if merged != 0 || skipped != 0 {
		t.Fatalf("merged/skipped = %d/%d, want 0/0 — %q can never match %q", merged, skipped, "KaliScan.me", "KaliScan")
	}
	if flagged := detectFlagged(t, client, ctx, storage); flagged != 2 {
		t.Fatalf("flagged %d chapters after a declined heal, want 2 — the ordering cannot help a pair that never folds", flagged)
	}
}
