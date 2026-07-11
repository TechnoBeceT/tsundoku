package download_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
)

// watermarkOf renders a chapter's satisfied_importance for a failure message
// ("<nil>" when unset) — %v on the raw *int would print a pointer address.
func watermarkOf(ch *ent.Chapter) string {
	if ch.SatisfiedImportance == nil {
		return "<nil>"
	}
	return strconv.Itoa(*ch.SatisfiedImportance)
}

// seedSource creates a SeriesProvider at the given importance which offers
// chapterKey, so it is a live upgrade candidate for that chapter.
func seedSource(ctx context.Context, t *testing.T, client *ent.Client, s *ent.Series, name string, importance int, chapterKey string) *ent.SeriesProvider {
	t.Helper()
	sp := client.SeriesProvider.Create().
		SetSeries(s).SetProvider(name).SetImportance(importance).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).SetChapterKey(chapterKey).
		SetURL("https://" + name + ".example.com/" + chapterKey).
		SetProviderIndex(0).SaveX(ctx)
	return sp
}

// seedDownloadedChapter creates a chapter already in state=downloaded with the
// given provenance. satisfiedBy may be nil to model the post-RemoveProvider case
// (satisfied_by cleared, watermark deliberately kept frozen).
func seedDownloadedChapter(ctx context.Context, t *testing.T, client *ent.Client, s *ent.Series, key string, satisfiedBy *uuid.UUID, watermark int) *ent.Chapter {
	t.Helper()
	create := client.Chapter.Create().
		SetSeries(s).
		SetChapterKey(key).
		SetState(entchapter.StateDownloaded).
		SetSatisfiedImportance(watermark).
		SetFilename(key + ".cbz")
	if satisfiedBy != nil {
		create = create.SetSatisfiedByProviderID(*satisfiedBy)
	}
	return create.SaveX(ctx)
}

// TestDetectUpgrades_DemotedSatisfierUnblocksUpgrade is THE regression: the owner
// DEMOTED the source that satisfied a chapter (importance 60 → 20) and promoted a
// different source to 60. satisfied_importance is a frozen snapshot of the OLD
// (60) value, so comparing the new best candidate against it (60 <= 60) refused
// the upgrade forever. The truth while satisfied_by is set is the satisfying
// source's CURRENT importance (20) — the chapter MUST be flagged upgrade_available
// and the stale watermark MUST be healed to 20.
func TestDetectUpgrades_DemotedSatisfierUnblocksUpgrade(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Demoted Series").SetSlug("demoted-series").SaveX(ctx)

	// The source that satisfied the chapter, since demoted 60 → 20.
	spOld := seedSource(ctx, t, client, s, "prov-demoted", 20, "ch-demote")
	// A DIFFERENT source, promoted to the importance the old one used to hold.
	seedSource(ctx, t, client, s, "prov-promoted", 60, "ch-demote")

	ch := seedDownloadedChapter(ctx, t, client, s, "ch-demote", &spOld.ID, 60)

	n, err := download.DetectUpgrades(ctx, client, 3)
	if err != nil {
		t.Fatalf("DetectUpgrades: %v", err)
	}
	if n != 1 {
		t.Errorf("DetectUpgrades: want 1 flagged (satisfying source demoted below a different source), got %d", n)
	}

	after := client.Chapter.GetX(ctx, ch.ID)
	if after.State != entchapter.StateUpgradeAvailable {
		t.Errorf("state: want upgrade_available, got %s", after.State)
	}
	if after.SatisfiedImportance == nil || *after.SatisfiedImportance != 20 {
		t.Errorf("satisfied_importance: want 20 (healed to the satisfying source's CURRENT importance), got %v", watermarkOf(after))
	}
}

// TestDetectUpgrades_HealsWatermarkWithoutBetterSource pins the healing half of
// the fix in isolation: a demoted satisfier with NO better source available must
// still have its stale watermark healed to the source's current importance (and
// must not be flagged — there is nothing to upgrade to).
func TestDetectUpgrades_HealsWatermarkWithoutBetterSource(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Heal Series").SetSlug("heal-series").SaveX(ctx)
	spOld := seedSource(ctx, t, client, s, "prov-only", 20, "ch-heal")
	ch := seedDownloadedChapter(ctx, t, client, s, "ch-heal", &spOld.ID, 60)

	n, err := download.DetectUpgrades(ctx, client, 3)
	if err != nil {
		t.Fatalf("DetectUpgrades: %v", err)
	}
	if n != 0 {
		t.Errorf("DetectUpgrades: want 0 flagged (no better source), got %d", n)
	}

	after := client.Chapter.GetX(ctx, ch.ID)
	if after.State != entchapter.StateDownloaded {
		t.Errorf("state: want downloaded, got %s", after.State)
	}
	if after.SatisfiedImportance == nil || *after.SatisfiedImportance != 20 {
		t.Errorf("satisfied_importance: want 20 (healed), got %v", watermarkOf(after))
	}
}

// TestDetectUpgrades_RemovedSatisfierKeepsFrozenWatermark pins the UNCHANGED
// removed-provider behaviour: series.RemoveProvider clears satisfied_by but KEEPS
// satisfied_importance precisely so a lower/equal source cannot pose as an upgrade
// for a chapter that is already satisfied at that quality. With satisfied_by NULL
// there is no current importance to read, so the frozen watermark still guards:
// an equal-importance source must NOT upgrade, a strictly higher one must.
func TestDetectUpgrades_RemovedSatisfierKeepsFrozenWatermark(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Removed Series").SetSlug("removed-series").SaveX(ctx)
	// satisfied_by is NULL (the satisfying source was removed by the owner), the
	// watermark stays frozen at 60.
	seedSource(ctx, t, client, s, "prov-equal", 60, "ch-removed")
	ch := seedDownloadedChapter(ctx, t, client, s, "ch-removed", nil, 60)

	n, err := download.DetectUpgrades(ctx, client, 3)
	if err != nil {
		t.Fatalf("DetectUpgrades (equal-importance case): %v", err)
	}
	if n != 0 {
		t.Errorf("DetectUpgrades: want 0 flagged (equal to the frozen watermark), got %d", n)
	}
	after := client.Chapter.GetX(ctx, ch.ID)
	if after.State != entchapter.StateDownloaded {
		t.Errorf("state: want downloaded, got %s", after.State)
	}
	if after.SatisfiedImportance == nil || *after.SatisfiedImportance != 60 {
		t.Errorf("satisfied_importance: want 60 (frozen watermark preserved), got %v", watermarkOf(after))
	}

	// A strictly higher source still upgrades — the frozen watermark guards, it
	// does not freeze the chapter out of all future upgrades.
	seedSource(ctx, t, client, s, "prov-higher", 70, "ch-removed")
	n, err = download.DetectUpgrades(ctx, client, 3)
	if err != nil {
		t.Fatalf("DetectUpgrades (strictly-higher case): %v", err)
	}
	if n != 1 {
		t.Errorf("DetectUpgrades: want 1 flagged (source strictly above the frozen watermark), got %d", n)
	}
	if client.Chapter.GetX(ctx, ch.ID).State != entchapter.StateUpgradeAvailable {
		t.Error("state: want upgrade_available after a strictly-higher source appeared")
	}
}

// TestDetectUpgrades_PromotedDifferentSourceStillUpgrades pins the ordinary
// promotion path (unchanged by the healing fix): the satisfying source sits at its
// recorded importance and a DIFFERENT source outranks it ⇒ flagged.
func TestDetectUpgrades_PromotedDifferentSourceStillUpgrades(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Promote Series").SetSlug("promote-series").SaveX(ctx)
	spLow := seedSource(ctx, t, client, s, "prov-low", 20, "ch-promote")
	seedSource(ctx, t, client, s, "prov-high", 60, "ch-promote")
	ch := seedDownloadedChapter(ctx, t, client, s, "ch-promote", &spLow.ID, 20)

	n, err := download.DetectUpgrades(ctx, client, 3)
	if err != nil {
		t.Fatalf("DetectUpgrades: %v", err)
	}
	if n != 1 {
		t.Errorf("DetectUpgrades: want 1 flagged, got %d", n)
	}
	if client.Chapter.GetX(ctx, ch.ID).State != entchapter.StateUpgradeAvailable {
		t.Error("state: want upgrade_available")
	}
}

// TestDetectUpgrades_NoPerChapterProviderLookup is the N+1 guard for the healing
// fix. DetectUpgrades reads the satisfying source's CURRENT importance for every
// downloaded chapter; doing that with a per-chapter query would add one query per
// chapter to a library-wide scan.
//
// It counts every Ent query issued during a scan (via a client interceptor) for
// two different chapter counts and asserts the PER-CHAPTER slope is unchanged at
// 3 — the three queries chapter.RankedLiveCandidates already issues per chapter
// (load chapter · load its provider-chapters · eager-load their series-providers).
// The satisfying source is resolved by a single batched WithSatisfiedBy eager-load
// for the WHOLE scan, so it contributes a constant, not a per-chapter, cost: a
// regression to a per-chapter lookup pushes the slope to 4 and fails this test.
//
// The fixture keeps every watermark already correct and offers no better source,
// so no heal-write and no state transition perturbs the count.
func TestDetectUpgrades_NoPerChapterProviderLookup(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	queries := 0
	client.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) {
			queries++
			return next.Query(ctx, q)
		})
	}))

	// countScan seeds a fresh series with n downloaded chapters (all watermarks
	// already correct, no better source) and returns the queries a scan issues.
	countScan := func(slug string, n int) int {
		s := client.Series.Create().SetTitle(slug).SetSlug(slug).SaveX(ctx)
		sp := client.SeriesProvider.Create().
			SetSeries(s).SetProvider("prov-" + slug).SetImportance(10).SaveX(ctx)
		for i := range n {
			key := slug + "-" + string(rune('a'+i))
			client.ProviderChapter.Create().
				SetSeriesProviderID(sp.ID).SetChapterKey(key).
				SetURL("https://x.example.com/" + key).SetProviderIndex(0).SaveX(ctx)
			seedDownloadedChapter(ctx, t, client, s, key, &sp.ID, 10)
		}
		before := queries
		if _, err := download.DetectUpgrades(ctx, client, 3); err != nil {
			t.Fatalf("DetectUpgrades: %v", err)
		}
		return queries - before
	}

	// The second scan also re-scans the first series' chapters (DetectUpgrades is
	// library-wide), so the chapter counts are 3 and 3+9=12.
	small := countScan("nplusone-small", 3)
	large := countScan("nplusone-large", 9)

	const perChapter = 3
	slope := float64(large-small) / float64(9)
	if slope > perChapter {
		t.Errorf("per-chapter query slope: want <= %d, got %.2f (small=%d queries for 3 chapters, large=%d for 12) — a per-chapter satisfied-source lookup was introduced (N+1)",
			perChapter, slope, small, large)
	}
}
