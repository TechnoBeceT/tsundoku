// Package download_test contains integration tests for first_downloaded_at —
// the write-once "when did this chapter become readable" timestamp.
package download_test

import (
	"context"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/fetcher/fake"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sse"
)

// requireFirstDownloadedAt fails the test unless ch.FirstDownloadedAt is set,
// and returns its value.
func requireFirstDownloadedAt(t *testing.T, ch *ent.Chapter) time.Time {
	t.Helper()
	if ch.FirstDownloadedAt == nil {
		t.Fatal("FirstDownloadedAt must be set")
	}
	return *ch.FirstDownloadedAt
}

// requireDownloadDate fails the test unless ch.DownloadDate is set, and
// returns its value.
func requireDownloadDate(t *testing.T, ch *ent.Chapter) time.Time {
	t.Helper()
	if ch.DownloadDate == nil {
		t.Fatal("DownloadDate must be set")
	}
	return *ch.DownloadDate
}

// assertFirstDownloadedAtUnchanged fails the test unless ch's
// FirstDownloadedAt is set and equal to want.
func assertFirstDownloadedAtUnchanged(t *testing.T, ch *ent.Chapter, want time.Time) {
	t.Helper()
	got := requireFirstDownloadedAt(t, ch)
	if !got.Equal(want) {
		t.Errorf("FirstDownloadedAt changed: want %v (unchanged), got %v", want, got)
	}
}

// TestUpgradeDoesNotTouchFirstDownloadedAt is THE test of the whole feature.
// download_date is a FETCH timestamp — a convergence upgrade rewrites it on an
// OLD chapter. first_downloaded_at means "a new chapter became readable" and must
// therefore survive an upgrade untouched. Without this test the field silently
// degrades back into download_date the first time someone "simplifies" the write
// path.
func TestUpgradeDoesNotTouchFirstDownloadedAt(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storageDir := mustTempDir(t)
	hub := sse.NewHub()

	s := client.Series.Create().SetTitle("FDA Series").SetSlug("fda-series").SaveX(ctx)

	// Low-importance source satisfies the chapter first.
	spLow := client.SeriesProvider.Create().
		SetSeries(s).SetProvider("prov-low").SetImportance(2).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(spLow.ID).SetChapterKey("ch-fda").
		SetURL("https://low.example.com/ch-fda").SetProviderIndex(0).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("ch-fda").SaveX(ctx)

	d := download.New(client, fake.New(), hub, download.Config{
		Storage: storageDir,
	}, settings.Static{Retries: 3, Backoff: time.Hour}, nil)

	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("initial RunOnce: %v", err)
	}

	initial := client.Chapter.GetX(ctx, ch.ID)
	if initial.State != entchapter.StateDownloaded {
		t.Fatalf("initial state: want downloaded, got %s", initial.State)
	}
	firstAt := requireFirstDownloadedAt(t, initial)
	dlAt := requireDownloadDate(t, initial)

	// Make the next timestamp distinguishable from the first.
	time.Sleep(10 * time.Millisecond)

	// Add a higher-importance provider for the same chapter key — the upgrade
	// target — and run the convergence pass.
	spHigh := client.SeriesProvider.Create().
		SetSeries(s).SetProvider("prov-high").SetImportance(5).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(spHigh.ID).SetChapterKey("ch-fda").
		SetURL("https://high.example.com/ch-fda").SetProviderIndex(0).SaveX(ctx)

	n, err := download.DetectUpgrades(ctx, client, 3)
	if err != nil {
		t.Fatalf("DetectUpgrades: %v", err)
	}
	if n != 1 {
		t.Fatalf("DetectUpgrades: want 1 flagged, got %d", n)
	}

	if err := d.Upgrade(ctx, ch.ID); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	final := client.Chapter.GetX(ctx, ch.ID)
	if final.State != entchapter.StateDownloaded {
		t.Fatalf("state after upgrade: want downloaded, got %s", final.State)
	}
	if final.SatisfiedByProviderID == nil || *final.SatisfiedByProviderID != spHigh.ID {
		t.Fatalf("satisfied_by_provider_id: want %s (the upgrade target), got %v", spHigh.ID, final.SatisfiedByProviderID)
	}

	// The load-bearing pair of assertions: FirstDownloadedAt is UNCHANGED,
	// DownloadDate HAS MOVED. The second assertion proves the test actually
	// performed an upgrade — without it a test that silently upgrades nothing
	// would still pass and would guard nothing at all.
	assertFirstDownloadedAtUnchanged(t, final, firstAt)
	newDlAt := requireDownloadDate(t, final)
	if !newDlAt.After(dlAt) {
		t.Fatalf("DownloadDate did not move after the upgrade (want strictly after %v, got %v) — this means the upgrade did not actually happen", dlAt, newDlAt)
	}
}

// TestFirstDownloadedAtSurvivesOrphanResetAndRedownload proves write-once is
// enforced by the SQL predicate, not by Go control flow: a chapter that is
// requeued by the boot-orphan sweep (downloading → wanted) and re-downloaded
// keeps its ORIGINAL arrival time — the honest answer to "when did this
// chapter become readable" is unaffected by a crash-and-retry.
func TestFirstDownloadedAtSurvivesOrphanResetAndRedownload(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storageDir := mustTempDir(t)
	hub := sse.NewHub()

	s := client.Series.Create().SetTitle("Orphan FDA Series").SetSlug("orphan-fda-series").SaveX(ctx)
	sp := client.SeriesProvider.Create().
		SetSeries(s).SetProvider("prov-orphan").SetImportance(5).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).SetChapterKey("ch-orphan").
		SetURL("https://orphan.example.com/ch-orphan").SetProviderIndex(0).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("ch-orphan").SaveX(ctx)

	d := download.New(client, fake.New(), hub, download.Config{
		Storage: storageDir,
	}, settings.Static{Retries: 3, Backoff: time.Hour}, nil)

	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("initial RunOnce: %v", err)
	}

	initial := client.Chapter.GetX(ctx, ch.ID)
	firstAt := requireFirstDownloadedAt(t, initial)

	time.Sleep(10 * time.Millisecond)

	// Simulate a crash mid-refetch: the chapter is stuck in "downloading" when
	// the process dies. This is a raw DB write (bypassing chapter.SetState's FSM
	// guard) because that is exactly what a real crash leaves behind — no live
	// process transitioned the row, the process just stopped.
	client.Chapter.UpdateOneID(ch.ID).SetState(entchapter.StateDownloading).ExecX(ctx)

	result, err := chapter.ResetOrphanedChapters(ctx, client)
	if err != nil {
		t.Fatalf("ResetOrphanedChapters: %v", err)
	}
	if result.Requeued != 1 {
		t.Fatalf("ResetOrphanedChapters: want 1 requeued, got %d", result.Requeued)
	}
	requeued := client.Chapter.GetX(ctx, ch.ID)
	if requeued.State != entchapter.StateWanted {
		t.Fatalf("state after orphan reset: want wanted, got %s", requeued.State)
	}

	// Re-download.
	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}

	final := client.Chapter.GetX(ctx, ch.ID)
	if final.State != entchapter.StateDownloaded {
		t.Fatalf("state after redownload: want downloaded, got %s", final.State)
	}
	assertFirstDownloadedAtUnchanged(t, final, firstAt)
}
