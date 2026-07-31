package downloads_test

import (
	"context"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/downloads"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/settings"
)

// seedWithheldChapter builds a series whose single source parked one chapter as
// WITHHELD (paywall / early access) and failed another for real. The withheld feed
// row mirrors exactly what download.deferSource writes: the source's own message,
// a far-future next_attempt_at, and an UNSPENT attempts budget.
func seedWithheldChapter(ctx context.Context, t *testing.T, client *ent.Client, until time.Time) {
	t.Helper()
	s := client.Series.Create().SetTitle("Coin Gate").SetSlug("coin-gate").
		SetCategoryID(catID(ctx, client, "Manhwa")).SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).
		SetProvider("42").SetProviderName("Hive Scans").SetImportance(30).SaveX(ctx)

	locked, broken := 12.0, 13.0
	client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey("cg-12").
		SetNillableNumber(&locked).SetURL("https://hive/cg-12").SetProviderIndex(0).
		SetLastError("upstream error: Chapter locked, coins required").
		SetNextAttemptAt(until).SaveX(ctx)
	client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey("cg-13").
		SetNillableNumber(&broken).SetURL("https://hive/cg-13").SetProviderIndex(1).
		SetAttempts(2).SetLastError("connection reset by peer").
		SetNextAttemptAt(time.Now().Add(30 * time.Minute)).SaveX(ctx)

	client.Chapter.Create().SetSeries(s).SetChapterKey("cg-12").SetNillableNumber(&locked).
		SetState(entchapter.StateFailed).
		SetLastError("upstream error: Chapter locked, coins required").SaveX(ctx)
	client.Chapter.Create().SetSeries(s).SetChapterKey("cg-13").SetNillableNumber(&broken).
		SetState(entchapter.StateFailed).SetLastError("connection reset by peer").SaveX(ctx)
}

// TestListMarksEarlyAccessChapters proves the activity read model tells an
// EARLY-ACCESS wait apart from a failure (GAP-141). A source that withholds its
// newest chapters behind coins for a few days is healthy and the chapter arrives on
// its own, so the row must carry the marker + its expiry rather than sitting in the
// Failed tab indistinguishable from a broken fetch. Nothing about the chapter's
// state changes — this is a derivation over the stored per-source last_error.
func TestListMarksEarlyAccessChapters(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()

	until := time.Now().UTC().Add(60 * time.Hour).Truncate(time.Microsecond)
	seedWithheldChapter(ctx, t, client, until)

	svc := downloads.NewService(client).WithRetrySettings(settings.Static{Retries: 5})
	res, err := svc.List(ctx, downloads.ListFilter{States: failStates, Limit: 50})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	withheld, ok := itemByKey(res.Items, "cg-12")
	if !ok {
		t.Fatal("withheld chapter missing from the failures view")
	}
	assertWithheldUntil(t, withheld, until)
	// The paywall never charged the budget, so the row must NOT read as terminal,
	// and the derivation adds no state of its own.
	if withheld.Terminal {
		t.Errorf("cg-12 Terminal = true, want false (a paywall never spends attempts)")
	}
	if withheld.State != string(entchapter.StateFailed) {
		t.Errorf("cg-12 State = %q, want %q (the derivation adds no state)", withheld.State, entchapter.StateFailed)
	}

	broken, ok := itemByKey(res.Items, "cg-13")
	if !ok {
		t.Fatal("failed chapter missing from the failures view")
	}
	if broken.Locked || broken.LockedUntil != nil {
		t.Errorf("cg-13 Locked/LockedUntil = %v/%v, want false/nil (a real failure)", broken.Locked, broken.LockedUntil)
	}
}

// assertWithheldUntil asserts an activity row reads as WITHHELD until the given instant.
func assertWithheldUntil(t *testing.T, row downloads.DownloadChapterDTO, until time.Time) {
	t.Helper()
	if !row.Locked {
		t.Errorf("%s Locked = false, want true (the source is withholding it)", row.ChapterKey)
	}
	if row.LockedUntil == nil {
		t.Fatalf("%s LockedUntil = nil, want %v", row.ChapterKey, until)
	}
	if !row.LockedUntil.Equal(until) {
		t.Errorf("%s LockedUntil = %v, want %v", row.ChapterKey, *row.LockedUntil, until)
	}
}

// TestListDoesNotMarkLapsedEarlyAccess proves the marker follows the deferral, not
// just the message: once the withholding window has elapsed the engine re-checks the
// chapter on the next cycle, so the row is queued again and must stop claiming it is
// waiting on early access.
func TestListDoesNotMarkLapsedEarlyAccess(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()

	seedWithheldChapter(ctx, t, client, time.Now().UTC().Add(-time.Minute))

	svc := downloads.NewService(client).WithRetrySettings(settings.Static{Retries: 5})
	res, err := svc.List(ctx, downloads.ListFilter{States: failStates, Limit: 50})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	row, ok := itemByKey(res.Items, "cg-12")
	if !ok {
		t.Fatal("withheld chapter missing from the failures view")
	}
	if row.Locked || row.LockedUntil != nil {
		t.Errorf("Locked/LockedUntil = %v/%v, want false/nil (the window has lapsed)", row.Locked, row.LockedUntil)
	}
}

// TestListNeverMarksAChapterThatIsOnDisk is the INTEGRATION pin for the on-disk
// suppression on the ACTIVITY read model (GAP-141) — the counterpart of the
// series-detail pin. Both read models call series.EarlyAccessUnlessSettled, and
// both need their own assertion: with only the helper's unit test, the call can be
// deleted from either model and the suite stays green, which is precisely the
// blind spot that let the original defect ship.
//
// Shape: a convergence upgrade whose BETTER source is withheld behind coins, while
// the chapter itself is already on disk and readable.
func TestListNeverMarksAChapterThatIsOnDisk(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	until := time.Now().UTC().Add(60 * time.Hour).Truncate(time.Microsecond)

	s := client.Series.Create().SetTitle("Coin Gate").SetSlug("coin-gate-ondisk").
		SetCategoryID(catID(ctx, client, "Manhwa")).SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).
		SetProvider("42").SetProviderName("Hive Scans").SetImportance(30).SaveX(ctx)

	num := 9.0
	client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey("cg-9").
		SetNillableNumber(&num).SetURL("https://hive/cg-9").SetProviderIndex(0).
		SetLastError("upstream error: Chapter locked, coins required").
		SetNextAttemptAt(until).SaveX(ctx)
	client.Chapter.Create().SetSeries(s).SetChapterKey("cg-9").SetNillableNumber(&num).
		SetState(entchapter.StateUpgradeAvailable).
		SetFilename("[Comix][en] Coin Gate 009.cbz").SaveX(ctx)

	svc := downloads.NewService(client).WithRetrySettings(settings.Static{Retries: 5})
	res, err := svc.List(ctx, downloads.ListFilter{
		States: []entchapter.State{entchapter.StateUpgradeAvailable}, Limit: 50,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	row, ok := itemByKey(res.Items, "cg-9")
	if !ok {
		t.Fatalf("cg-9 missing from the activity list (items=%d)", len(res.Items))
	}
	if row.Locked || row.LockedUntil != nil {
		t.Errorf("locked=%v lockedUntil=%v for a chapter on disk, want false/nil — "+
			"a readable file must keep its own state badge", row.Locked, row.LockedUntil)
	}
}

// TestListNeverMarksASupersededChapter is the ACTIVITY-side pin for the
// PARKED-state half of the suppression (GAP-141) — the counterpart of the
// series-detail pin, and it needs its own assertion for the same reason: with only
// the helper's unit test, the call can be deleted from either read model and the
// suite stays green.
//
// A superseded split part has NO file (download.supersedeOnePart deletes its CBZ
// and clears Chapter.filename), so the file test alone cannot suppress it and the
// row rendered "Early access · free ~3d" INSTEAD of its Superseded state badge —
// on a chapter nothing was ever going to fetch. `?state=superseded` is an ordinary
// client request (any valid enum value is accepted), so this view is reachable.
func TestListNeverMarksASupersededChapter(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	until := time.Now().UTC().Add(60 * time.Hour).Truncate(time.Microsecond)

	s := client.Series.Create().SetTitle("Coin Gate").SetSlug("coin-gate-superseded").
		SetCategoryID(catID(ctx, client, "Manhwa")).SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).
		SetProvider("42").SetProviderName("Hive Scans").SetImportance(30).SaveX(ctx)

	num := 8.1
	client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey("cg-8-1").
		SetNillableNumber(&num).SetURL("https://hive/cg-8-1").SetProviderIndex(0).
		SetLastError("upstream error: Chapter locked, coins required").
		SetNextAttemptAt(until).SaveX(ctx)
	client.Chapter.Create().SetSeries(s).SetChapterKey("cg-8-1").SetNillableNumber(&num).
		SetState(entchapter.StateSuperseded).SaveX(ctx)

	svc := downloads.NewService(client).WithRetrySettings(settings.Static{Retries: 5})
	res, err := svc.List(ctx, downloads.ListFilter{
		States: []entchapter.State{entchapter.StateSuperseded}, Limit: 50,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	row, ok := itemByKey(res.Items, "cg-8-1")
	if !ok {
		t.Fatalf("cg-8-1 missing from the activity list (items=%d)", len(res.Items))
	}
	if row.Locked || row.LockedUntil != nil {
		t.Errorf("locked=%v lockedUntil=%v for a superseded chapter, want false/nil — "+
			"nothing will fetch a parked chapter, so it must keep its own state badge",
			row.Locked, row.LockedUntil)
	}
}
