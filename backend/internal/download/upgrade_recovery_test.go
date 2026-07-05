// Package download_test — upgrade retry/recovery semantics. The key invariant:
// UPGRADE failures must NEVER permanently give up on improving a chapter to a
// better source (unlike DOWNLOAD failures, which stick toward per-source
// exhaustion). A preferred source temporarily down during upgrade attempts only
// cools down and always recovers as an upgrade target.
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
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sse"
)

// seedDownloadedLowWithHighSource seeds a chapter already DOWNLOADED from a
// low-importance source (satisfied_importance=5) that also has a higher source
// ("high", importance 10) offering the same key with the given per-source state
// (attempts + optional cooldown). This isolates the upgrade path from the download
// fall-through so a test controls the high source's retry state exactly.
func seedDownloadedLowWithHighSource(ctx context.Context, t *testing.T, client *ent.Client, highAttempts int, highNext *time.Time) *ent.Chapter {
	t.Helper()
	s := client.Series.Create().SetTitle("Recover Upg").SetSlug("recover-upg").SaveX(ctx)
	spLow := client.SeriesProvider.Create().SetSeries(s).SetProvider("low").SetImportance(5).SaveX(ctx)
	spHigh := client.SeriesProvider.Create().SetSeries(s).SetProvider("high").SetImportance(10).SaveX(ctx)
	client.ProviderChapter.Create().SetSeriesProviderID(spLow.ID).SetChapterKey("c1").SetURL("https://low/c1").SetProviderIndex(0).SaveX(ctx)
	highPC := client.ProviderChapter.Create().SetSeriesProviderID(spHigh.ID).SetChapterKey("c1").SetURL("https://high/c1").SetProviderIndex(0).SetAttempts(highAttempts)
	if highNext != nil {
		highPC = highPC.SetNextAttemptAt(*highNext)
	}
	highPC.SaveX(ctx)
	return client.Chapter.Create().SetSeries(s).SetChapterKey("c1").
		SetState(entchapter.StateDownloaded).
		SetSatisfiedByProviderID(spLow.ID).SetSatisfiedImportance(5).
		SetFilename("[low] Recover Upg 001.cbz").SetPageCount(1).SetDownloadDate(time.Now()).
		SaveX(ctx)
}

// assertFailingUpgradePreservesWorkingCopy runs one upgrade cycle against a
// still-down higher source: it must be flagged (DetectUpgrades == 1), the Upgrade
// call must return nil (a failed upgrade is a handled outcome), and the chapter
// must remain downloaded at its original low importance (working copy preserved).
func assertFailingUpgradePreservesWorkingCopy(ctx context.Context, t *testing.T, client *ent.Client, d *download.Dispatcher, chID uuid.UUID, cycle int) {
	t.Helper()
	n, err := download.DetectUpgrades(ctx, client, 3)
	if err != nil {
		t.Fatalf("cycle %d DetectUpgrades: %v", cycle, err)
	}
	if n != 1 {
		t.Fatalf("cycle %d: want the down higher source still flagged as an upgrade target, got %d", cycle, n)
	}
	if err := d.Upgrade(ctx, chID); err != nil {
		t.Fatalf("cycle %d Upgrade: %v", cycle, err)
	}
	cur := client.Chapter.GetX(ctx, chID)
	if cur.State != entchapter.StateDownloaded || cur.SatisfiedImportance == nil || *cur.SatisfiedImportance != 5 {
		t.Fatalf("cycle %d: working copy not preserved (state=%s imp=%v)", cycle, cur.State, cur.SatisfiedImportance)
	}
}

// TestUpgrade_FailuresNeverExhaustSource_ThenRecovers proves the core Finding-1
// fix: a better source that fails upgrade fetches MANY more times than max_retries
// never exhausts (attempts stays put), so once it recovers the chapter still
// upgrades to it.
func TestUpgrade_FailuresNeverExhaustSource_ThenRecovers(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	ch := seedDownloadedLowWithHighSource(ctx, t, client, 0, nil)

	// The high source is down; Backoff:0 so its cooldown always elapses by the next
	// cycle and it is re-attempted each pass.
	f := &providerScopedFetcher{failProviders: map[string]bool{"high": true}}
	d := download.New(client, f, sse.NewHub(), download.Config{
		Storage: mustTempDir(t),
	}, settings.Static{Retries: 3, Backoff: 0})

	const cycles = 6 // >> max_retries (3)
	for i := 0; i < cycles; i++ {
		assertFailingUpgradePreservesWorkingCopy(ctx, t, client, d, ch.ID, i)
	}
	// The high source must NEVER have accrued attempts from upgrade failures.
	if a := pcByProvider(ctx, t, client, "high").Attempts; a != 0 {
		t.Fatalf("high attempts must stay 0 across %d failed upgrades (upgrade must not exhaust a source), got %d", cycles, a)
	}

	// High recovers → the next upgrade swaps to it.
	delete(f.failProviders, "high")
	n, err := download.DetectUpgrades(ctx, client, 3)
	if err != nil {
		t.Fatalf("DetectUpgrades (recovered): %v", err)
	}
	if n != 1 {
		t.Fatalf("want high flagged after recovery, got %d", n)
	}
	if err := d.Upgrade(ctx, ch.ID); err != nil {
		t.Fatalf("Upgrade (recovered): %v", err)
	}
	final := client.Chapter.GetX(ctx, ch.ID)
	if final.SatisfiedImportance == nil || *final.SatisfiedImportance != 10 {
		t.Fatalf("after recovery: want upgraded to high (importance 10), got %v", final.SatisfiedImportance)
	}
}

// TestDetectUpgrades_SkipsSourceOnCooldown verifies that a higher source still on
// its per-source cooldown is NOT flagged as an upgrade this cycle, and IS flagged
// once the cooldown elapses.
func TestDetectUpgrades_SkipsSourceOnCooldown(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	future := time.Now().Add(1 * time.Hour)
	ch := seedDownloadedLowWithHighSource(ctx, t, client, 0, &future) // high on cooldown

	// While the high source is on cooldown, only the (live) low source counts, whose
	// importance equals satisfied_importance → no strictly-better source → no flag.
	n, err := download.DetectUpgrades(ctx, client, 3)
	if err != nil {
		t.Fatalf("DetectUpgrades (cooldown): %v", err)
	}
	if n != 0 {
		t.Fatalf("want 0 flagged while the higher source is on cooldown, got %d", n)
	}
	if st := client.Chapter.GetX(ctx, ch.ID).State; st != entchapter.StateDownloaded {
		t.Fatalf("chapter must stay downloaded (not flagged), got %s", st)
	}

	// Clear the cooldown → the higher source is now live → it must be flagged.
	pcs, _ := client.ProviderChapter.Query().WithSeriesProvider().All(ctx)
	for _, pc := range pcs {
		if pc.Edges.SeriesProvider != nil && pc.Edges.SeriesProvider.Provider == "high" {
			client.ProviderChapter.UpdateOneID(pc.ID).ClearNextAttemptAt().ExecX(ctx)
		}
	}
	n, err = download.DetectUpgrades(ctx, client, 3)
	if err != nil {
		t.Fatalf("DetectUpgrades (cooldown cleared): %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 flagged once the higher source is past cooldown, got %d", n)
	}
}

// TestUpgrade_SuccessResetsWinningSourceRetryState proves Finding 3: a successful
// upgrade clears the winning source's accrued per-source retry state (parity with
// the download success reset).
func TestUpgrade_SuccessResetsWinningSourceRetryState(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	// The high source carries prior retry state (attempts=2) but is not exhausted.
	ch := seedDownloadedLowWithHighSource(ctx, t, client, 2, nil)

	f := &providerScopedFetcher{} // all sources succeed
	d := download.New(client, f, sse.NewHub(), download.Config{
		Storage: mustTempDir(t),
	}, settings.Static{Retries: 3, Backoff: time.Hour})

	if n, err := download.DetectUpgrades(ctx, client, 3); err != nil || n != 1 {
		t.Fatalf("DetectUpgrades: n=%d err=%v (want 1, nil)", n, err)
	}
	if err := d.Upgrade(ctx, ch.ID); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	final := client.Chapter.GetX(ctx, ch.ID)
	if final.SatisfiedImportance == nil || *final.SatisfiedImportance != 10 {
		t.Fatalf("want upgraded to high (importance 10), got %v", final.SatisfiedImportance)
	}
	high := pcByProvider(ctx, t, client, "high")
	if high.Attempts != 0 || high.LastError != "" || high.NextAttemptAt != nil {
		t.Errorf("winning source retry state must be reset on upgrade success: attempts=%d lastErr=%q next=%v",
			high.Attempts, high.LastError, high.NextAttemptAt)
	}
}
