// Package download_test — proof for the SERIES-SCOPED upgrade detection (GAP-113).
// DetectUpgradesForSeries reuses the SAME batched, no-N+1 detectUpgradesScoped path
// as the whole-library DetectUpgrades, restricted to one series. The equivalence
// test below pins the guarantee: for a given series, the scoped scan flags EXACTLY
// the chapters the whole-library scan would flag for that series — and touches no
// other series.
//
// Tests require Docker (via testcontainers) for an ephemeral PostgreSQL instance.
package download_test

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

// TestDetectUpgradesForSeries_FlagsSameSetAsWholeLibrary seeds a multi-series
// library that hits several detection carve-outs, then proves two things about
// DetectUpgradesForSeries(target):
//
//  1. SCOPING — it flags ONLY the target series' chapters; every OTHER series that
//     the whole-library scan would flag is left untouched.
//  2. EQUIVALENCE — the exact chapters it flags within the target series are the
//     SAME chapters the whole-library DetectUpgrades flags for that series (proving
//     the shared batched path produces byte-identical per-chapter decisions when
//     restricted by series_id).
func TestDetectUpgradesForSeries_FlagsSameSetAsWholeLibrary(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	// --- TARGET series: two chapters, one must flag (demoted satisfier + a strictly
	// higher different source) and one must NOT (only its own satisfier remains). ---
	target := client.Series.Create().SetTitle("scoped-target").SetSlug("scoped-target").SaveX(ctx)
	spDemoted := seedSource(ctx, t, client, target, "t-demoted-low", 20, "k-demote")
	seedSource(ctx, t, client, target, "t-demoted-high", 60, "k-demote")
	chFlag := seedDownloadedChapter(ctx, t, client, target, "k-demote", &spDemoted.ID, 60)

	spOnly := seedSource(ctx, t, client, target, "t-only", 20, "k-only")
	chNoFlag := seedDownloadedChapter(ctx, t, client, target, "k-only", &spOnly.ID, 20)

	// --- OTHER series that WOULD flag under a whole-library scan: they must be left
	// untouched by the scoped scan. ---
	other := client.Series.Create().SetTitle("scoped-other").SetSlug("scoped-other").SaveX(ctx)
	seedSource(ctx, t, client, other, "o-removed-hi", 70, "k-rhi")
	chOther := seedDownloadedChapter(ctx, t, client, other, "k-rhi", nil, 60) // removed satisfier, strictly higher → would flag whole-library

	d := download.New(client, fake.New(), sse.NewHub(), download.Config{Storage: t.TempDir()},
		settings.Static{Retries: 3, Backoff: time.Hour}, nil)

	// --- Scoped run over the target only. ---
	n, err := d.DetectUpgradesForSeries(ctx, target.ID, 3)
	if err != nil {
		t.Fatalf("DetectUpgradesForSeries: %v", err)
	}
	if n != 1 {
		t.Errorf("scoped scan flagged %d chapters, want 1 (only the demoted chapter)", n)
	}

	scopedFlaggedTarget := flaggedInSeries(ctx, t, client, target.ID)
	if !scopedFlaggedTarget[chFlag.ID] {
		t.Errorf("scoped scan did NOT flag the demoted chapter %s", chFlag.ID)
	}
	if scopedFlaggedTarget[chNoFlag.ID] {
		t.Errorf("scoped scan wrongly flagged the no-better chapter %s", chNoFlag.ID)
	}
	// Scoping: the other series must be untouched.
	if got := client.Chapter.GetX(ctx, chOther.ID).State; got != entchapter.StateDownloaded {
		t.Errorf("scoped scan leaked into OTHER series: chapter %s state = %s, want downloaded", chOther.ID, got)
	}

	// --- Equivalence: reset the flags the scoped run set, then run the WHOLE-LIBRARY
	// scan and compare the target series' flagged subset. They must be identical. ---
	client.Chapter.Update().
		Where(entchapter.StateEQ(entchapter.StateUpgradeAvailable)).
		SetState(entchapter.StateDownloaded).
		ExecX(ctx)

	if _, err := download.DetectUpgrades(ctx, client, 3); err != nil {
		t.Fatalf("whole-library DetectUpgrades: %v", err)
	}
	wholeFlaggedTarget := flaggedInSeries(ctx, t, client, target.ID)

	if !sameIDSet(scopedFlaggedTarget, wholeFlaggedTarget) {
		t.Errorf("scoped vs whole-library flagged set for target series differ:\n scoped=%v\n whole =%v",
			scopedFlaggedTarget, wholeFlaggedTarget)
	}
	// Sanity: the whole-library scan DID also flag the other series (so the two runs
	// really are over different scopes — the equivalence above is not vacuous).
	if got := client.Chapter.GetX(ctx, chOther.ID).State; got != entchapter.StateUpgradeAvailable {
		t.Errorf("whole-library scan should have flagged the OTHER series chapter %s, got state %s", chOther.ID, got)
	}
}

// flaggedInSeries returns the set of chapter ids currently in upgrade_available
// within one series.
func flaggedInSeries(ctx context.Context, t *testing.T, client *ent.Client, seriesID uuid.UUID) map[uuid.UUID]bool {
	t.Helper()
	out := map[uuid.UUID]bool{}
	for _, ch := range client.Chapter.Query().
		Where(entchapter.StateEQ(entchapter.StateUpgradeAvailable), entchapter.SeriesID(seriesID)).
		AllX(ctx) {
		out[ch.ID] = true
	}
	return out
}

// sameIDSet reports whether two id sets are equal.
func sameIDSet(a, b map[uuid.UUID]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for id := range a {
		if !b[id] {
			return false
		}
	}
	return true
}
