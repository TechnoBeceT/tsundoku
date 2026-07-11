// Package downloads_test — the upgrade-TARGET surface of the activity list: an
// upgrading chapter must name the source it is converging TO (the UI renders
// "Comix → Asura Scans"), and resolving it must not cost a single extra query.
package downloads_test

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/downloads"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
)

// upgradeStates is the state filter the assertions below page over: every state an
// activity row can be in, so one List call surfaces both the upgrading rows (which
// must carry a target) and the non-upgrading ones (which must not).
var upgradeStates = []entchapter.State{
	entchapter.StateWanted,
	entchapter.StateDownloading,
	entchapter.StateUpgradeAvailable,
	entchapter.StateUpgrading,
	entchapter.StateDownloaded,
	entchapter.StateFailed,
}

// seedUpgradeSeries builds ONE series with two sources — "Comix" (importance 5, the
// current satisfier) and "Asura Scans" (importance 10, the upgrade target) — and
// four chapters that cover every target-resolution case:
//
//   - u-1 upgrade_available, satisfied by Comix, in BOTH feeds  → target Asura Scans
//   - u-2 upgrading,         satisfied by Comix, in BOTH feeds  → target Asura Scans
//   - u-3 downloaded,        satisfied by Comix, in BOTH feeds  → NO target (not upgrading)
//   - u-4 upgrade_available, satisfied by Comix, only in Comix's feed (a GAPPED
//     target feed — Asura does not carry the chapter)           → NO target
//   - u-5 wanted,            no satisfier, in BOTH feeds        → NO target (not upgrading)
func seedUpgradeSeries(ctx context.Context, t *testing.T, client *ent.Client) {
	t.Helper()
	s := client.Series.Create().SetTitle("Convergence Wave").SetSlug("convergence-wave").
		SetCategoryID(catID(ctx, client, "Manga")).SaveX(ctx)
	low := client.SeriesProvider.Create().SetSeries(s).
		SetProvider("7537715367149829912").SetProviderName("Comix").SetImportance(5).SaveX(ctx)
	high := client.SeriesProvider.Create().SetSeries(s).
		SetProvider("991122").SetProviderName("Asura Scans").SetImportance(10).SaveX(ctx)

	keys := []string{"u-1", "u-2", "u-3", "u-4", "u-5"}
	for i, key := range keys {
		client.ProviderChapter.Create().SetSeriesProviderID(low.ID).SetChapterKey(key).
			SetURL("https://comix/" + key).SetProviderIndex(i).SaveX(ctx)
		if key != "u-4" { // u-4: the high source does NOT carry this chapter (gapped feed)
			client.ProviderChapter.Create().SetSeriesProviderID(high.ID).SetChapterKey(key).
				SetURL("https://asura/" + key).SetProviderIndex(i).SaveX(ctx)
		}
	}

	satisfied := map[string]entchapter.State{
		"u-1": entchapter.StateUpgradeAvailable,
		"u-2": entchapter.StateUpgrading,
		"u-3": entchapter.StateDownloaded,
		"u-4": entchapter.StateUpgradeAvailable,
	}
	for key, state := range satisfied {
		client.Chapter.Create().SetSeries(s).SetChapterKey(key).SetState(state).
			SetSatisfiedByProviderID(low.ID).SetSatisfiedImportance(low.Importance).
			SetFilename("[Comix] Convergence Wave " + key + ".cbz").SaveX(ctx)
	}
	// u-5: never downloaded — no satisfier, so no upgrade is in flight for it.
	client.Chapter.Create().SetSeries(s).SetChapterKey("u-5").SetState(entchapter.StateWanted).SaveX(ctx)
}

// TestListUpgradeTarget proves the row can render "current → target": an
// upgrade_available / upgrading chapter names the highest-importance DIFFERENT
// source whose feed carries its key, while every other state — and an upgrading
// chapter whose target feed lacks the key — leaves the field empty (no crash, no
// guess).
func TestListUpgradeTarget(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	seedUpgradeSeries(ctx, t, client)

	res, err := downloads.NewService(client).List(ctx, downloads.ListFilter{States: upgradeStates, Limit: 50})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	want := map[string]string{
		"u-1": "Asura Scans", // flagged for upgrade → names the target
		"u-2": "Asura Scans", // mid-upgrade → names the target
		"u-3": "",            // downloaded → not upgrading
		"u-4": "",            // flagged, but the higher source has no such chapter
		"u-5": "",            // wanted → not upgrading
	}
	for key, wantTarget := range want {
		item, ok := itemByKey(res.Items, key)
		if !ok {
			t.Fatalf("chapter %s missing from the list", key)
		}
		if item.UpgradeTarget != wantTarget {
			t.Errorf("chapter %s upgradeTarget = %q, want %q", key, item.UpgradeTarget, wantTarget)
		}
		// The CURRENT source must still be reported — the row shows both sides.
		if item.ProviderName != "Comix" && key != "u-5" {
			t.Errorf("chapter %s providerName = %q, want the current source %q", key, item.ProviderName, "Comix")
		}
	}
}

// countingDriver wraps an Ent SQL driver and counts every read query issued
// through it (eager-loading sub-queries included — Ent runs them through the same
// driver). Test-only: it exists solely to PROVE the downloads list's query count is
// bounded, i.e. does not grow with the page size (no N+1).
type countingDriver struct {
	dialect.Driver
	queries atomic.Int64
}

// Query counts the read and delegates.
func (d *countingDriver) Query(ctx context.Context, query string, args, v any) error {
	d.queries.Add(1)
	return d.Driver.Query(ctx, query, args, v)
}

// newCountingClient builds a second Ent client over the SAME test database whose
// reads are counted.
func newCountingClient(db *sql.DB) (*ent.Client, *countingDriver) {
	drv := &countingDriver{Driver: entsql.OpenDB(dialect.Postgres, db)}
	return ent.NewClient(ent.Driver(drv)), drv
}

// seedManyUpgrades creates one series with a low + high source and n chapters, all
// flagged upgrade_available and satisfied by the low source — so EVERY row on the
// page needs an upgrade-target resolution (the worst case for an N+1).
func seedManyUpgrades(ctx context.Context, t *testing.T, client *ent.Client, n int) {
	t.Helper()
	s := client.Series.Create().SetTitle("Wave").SetSlug("wave").
		SetCategoryID(catID(ctx, client, "Manga")).SaveX(ctx)
	low := client.SeriesProvider.Create().SetSeries(s).SetProvider("low").SetProviderName("Comix").SetImportance(5).SaveX(ctx)
	high := client.SeriesProvider.Create().SetSeries(s).SetProvider("high").SetProviderName("Asura Scans").SetImportance(10).SaveX(ctx)
	for i := range n {
		key := fmt.Sprintf("w-%02d", i)
		num := float64(i)
		client.ProviderChapter.Create().SetSeriesProviderID(low.ID).SetChapterKey(key).
			SetNillableNumber(&num).SetURL("https://comix/" + key).SetProviderIndex(i).SaveX(ctx)
		client.ProviderChapter.Create().SetSeriesProviderID(high.ID).SetChapterKey(key).
			SetNillableNumber(&num).SetURL("https://asura/" + key).SetProviderIndex(i).SaveX(ctx)
		client.Chapter.Create().SetSeries(s).SetChapterKey(key).SetNillableNumber(&num).
			SetState(entchapter.StateUpgradeAvailable).
			SetSatisfiedByProviderID(low.ID).SetSatisfiedImportance(low.Importance).SaveX(ctx)
	}
}

// TestListQueryCountIsPageSizeIndependent is the NO-N+1 proof — including for the
// new upgradeTarget field, which is resolved IN MEMORY from the feeds the list
// already batch-loads.
//
// It counts the SQL reads (via countingDriver, which sees Ent's eager-loading
// sub-queries too) issued by one List call at page size 2 and again at page size
// 20, with every row on the page requiring a target resolution. The counts must be
// IDENTICAL and small: an N+1 would make the larger page cost ~18 more queries.
func TestListQueryCountIsPageSizeIndependent(t *testing.T) {
	ctx := context.Background()
	seedClient, db := testdb.NewWithSQL(t)
	seedManyUpgrades(ctx, t, seedClient, 20)

	client, drv := newCountingClient(db)
	svc := downloads.NewService(client)

	count := func(limit int) int64 {
		drv.queries.Store(0)
		res, err := svc.List(ctx, downloads.ListFilter{States: upgradeStates, Limit: limit})
		if err != nil {
			t.Fatalf("List(limit=%d): %v", limit, err)
		}
		if len(res.Items) != limit {
			t.Fatalf("List(limit=%d) returned %d items, want %d", limit, len(res.Items), limit)
		}
		for _, it := range res.Items {
			if it.UpgradeTarget != "Asura Scans" {
				t.Fatalf("chapter %s upgradeTarget = %q, want %q (every row must resolve a target)", it.ChapterKey, it.UpgradeTarget, "Asura Scans")
			}
		}
		return drv.queries.Load()
	}

	small, large := count(2), count(20)
	if small != large {
		t.Errorf("N+1: List issued %d queries for a 2-item page but %d for a 20-item page — the query count must not scale with page size", small, large)
	}
	// The bounded shape: COUNT + chapters page (+ its series/category eager loads) +
	// ONE providers load (+ its feeds). A generous ceiling still fails an N+1.
	const maxQueries = 8
	if large > maxQueries {
		t.Errorf("List issued %d queries for one page, want <= %d (bounded, page-size independent)", large, maxQueries)
	}
	t.Logf("queries: page(2)=%d page(20)=%d", small, large)
}
