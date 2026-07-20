package downloads_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/downloads"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
)

// seedActiveSources builds a fixture that exercises every ActiveSourceCounts
// attribution rule at once:
//
// Series "Alpha" has two sources — Comix (importance 5) and Asura Scans (10) —
// both carrying every key:
//   - a-down  downloading, no satisfier              → top candidate = Asura Scans
//   - a-up    upgrading,   satisfied by Comix        → upgrade target = Asura Scans
//   - a-done  downloaded,  satisfied by Comix        → NOT active (excluded)
//   - a-wait  wanted,      no satisfier              → NOT active (excluded)
//
// Series "Beta" has ONLY Comix, carrying its key:
//   - b-down  downloading, no satisfier              → top (and only) candidate = Comix
//
// Series "Gamma" has Comix (satisfier) + Asura, but Asura's feed is GAPPED:
//   - g-up    upgrading,   satisfied by Comix, only Comix carries it → NO target → not counted
//
// Expected counts: {"Asura Scans": 2, "Comix": 1}.
func seedActiveSources(ctx context.Context, t *testing.T, client *ent.Client) {
	t.Helper()
	cat := catID(ctx, client, "Manga")

	alpha := client.Series.Create().SetTitle("Alpha").SetSlug("alpha").SetCategoryID(cat).SaveX(ctx)
	aComix := client.SeriesProvider.Create().SetSeries(alpha).SetProvider("a-comix").SetProviderName("Comix").SetImportance(5).SaveX(ctx)
	aAsura := client.SeriesProvider.Create().SetSeries(alpha).SetProvider("a-asura").SetProviderName("Asura Scans").SetImportance(10).SaveX(ctx)
	for i, key := range []string{"a-down", "a-up", "a-done", "a-wait"} {
		client.ProviderChapter.Create().SetSeriesProviderID(aComix.ID).SetChapterKey(key).SetURL("https://comix/" + key).SetProviderIndex(i).SaveX(ctx)
		client.ProviderChapter.Create().SetSeriesProviderID(aAsura.ID).SetChapterKey(key).SetURL("https://asura/" + key).SetProviderIndex(i).SaveX(ctx)
	}
	client.Chapter.Create().SetSeries(alpha).SetChapterKey("a-down").SetState(entchapter.StateDownloading).SaveX(ctx)
	client.Chapter.Create().SetSeries(alpha).SetChapterKey("a-up").SetState(entchapter.StateUpgrading).
		SetSatisfiedByProviderID(aComix.ID).SetSatisfiedImportance(aComix.Importance).SetFilename("[Comix] Alpha a-up.cbz").SaveX(ctx)
	client.Chapter.Create().SetSeries(alpha).SetChapterKey("a-done").SetState(entchapter.StateDownloaded).
		SetSatisfiedByProviderID(aComix.ID).SetSatisfiedImportance(aComix.Importance).SetFilename("[Comix] Alpha a-done.cbz").SaveX(ctx)
	client.Chapter.Create().SetSeries(alpha).SetChapterKey("a-wait").SetState(entchapter.StateWanted).SaveX(ctx)

	beta := client.Series.Create().SetTitle("Beta").SetSlug("beta").SetCategoryID(cat).SaveX(ctx)
	bComix := client.SeriesProvider.Create().SetSeries(beta).SetProvider("b-comix").SetProviderName("Comix").SetImportance(5).SaveX(ctx)
	client.ProviderChapter.Create().SetSeriesProviderID(bComix.ID).SetChapterKey("b-down").SetURL("https://comix/b-down").SetProviderIndex(0).SaveX(ctx)
	client.Chapter.Create().SetSeries(beta).SetChapterKey("b-down").SetState(entchapter.StateDownloading).SaveX(ctx)

	gamma := client.Series.Create().SetTitle("Gamma").SetSlug("gamma").SetCategoryID(cat).SaveX(ctx)
	gComix := client.SeriesProvider.Create().SetSeries(gamma).SetProvider("g-comix").SetProviderName("Comix").SetImportance(5).SaveX(ctx)
	client.SeriesProvider.Create().SetSeries(gamma).SetProvider("g-asura").SetProviderName("Asura Scans").SetImportance(10).SaveX(ctx)
	// Only Comix carries g-up → the higher source cannot be the upgrade target.
	client.ProviderChapter.Create().SetSeriesProviderID(gComix.ID).SetChapterKey("g-up").SetURL("https://comix/g-up").SetProviderIndex(0).SaveX(ctx)
	client.Chapter.Create().SetSeries(gamma).SetChapterKey("g-up").SetState(entchapter.StateUpgrading).
		SetSatisfiedByProviderID(gComix.ID).SetSatisfiedImportance(gComix.Importance).SetFilename("[Comix] Gamma g-up.cbz").SaveX(ctx)
}

// seedManyActive creates one series (low + high source) with n upgrading chapters,
// all satisfied by the low source — so EVERY row needs an upgrade-target
// resolution over the batch-loaded feeds (the worst case for an N+1).
func seedManyActive(ctx context.Context, t *testing.T, client *ent.Client, n int) {
	t.Helper()
	s := client.Series.Create().SetTitle("Wave").SetSlug("wave").SetCategoryID(catID(ctx, client, "Manga")).SaveX(ctx)
	low := client.SeriesProvider.Create().SetSeries(s).SetProvider("w-low").SetProviderName("Comix").SetImportance(5).SaveX(ctx)
	high := client.SeriesProvider.Create().SetSeries(s).SetProvider("w-high").SetProviderName("Asura Scans").SetImportance(10).SaveX(ctx)
	for i := range n {
		key := "w-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		client.ProviderChapter.Create().SetSeriesProviderID(low.ID).SetChapterKey(key).SetURL("https://comix/" + key).SetProviderIndex(i).SaveX(ctx)
		client.ProviderChapter.Create().SetSeriesProviderID(high.ID).SetChapterKey(key).SetURL("https://asura/" + key).SetProviderIndex(i).SaveX(ctx)
		client.Chapter.Create().SetSeries(s).SetChapterKey(key).SetState(entchapter.StateUpgrading).
			SetSatisfiedByProviderID(low.ID).SetSatisfiedImportance(low.Importance).SetFilename("[Comix] Wave " + key + ".cbz").SaveX(ctx)
	}
}

// TestActiveSourceCounts_AttributesToFetchingSource proves ActiveSourceCounts
// attributes each downloading/upgrading chapter to the source ACTUALLY fetching it
// (downloading → top live candidate, upgrading → upgrade target), excludes every
// non-active state, and skips an upgrading chapter with no valid higher target.
func TestActiveSourceCounts_AttributesToFetchingSource(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	seedActiveSources(ctx, t, client)

	counts, err := downloads.NewService(client).ActiveSourceCounts(ctx)
	if err != nil {
		t.Fatalf("ActiveSourceCounts: %v", err)
	}

	want := map[string]int{"Asura Scans": 2, "Comix": 1}
	if len(counts) != len(want) {
		t.Fatalf("counts = %v, want %v", counts, want)
	}
	for key, n := range want {
		if counts[key] != n {
			t.Errorf("counts[%q] = %d, want %d (full map %v)", key, counts[key], n, counts)
		}
	}
}

// TestActiveSourceCounts_EmptyWhenNothingActive proves a library with no
// downloading/upgrading chapters yields an empty (non-nil) map — a valid zero
// state, not an error.
func TestActiveSourceCounts_EmptyWhenNothingActive(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	counts, err := downloads.NewService(client).ActiveSourceCounts(ctx)
	if err != nil {
		t.Fatalf("ActiveSourceCounts: %v", err)
	}
	if len(counts) != 0 {
		t.Fatalf("counts = %v, want empty", counts)
	}
}

// TestActiveSourceCounts_QueryCountIsSetSizeIndependent is the NO-N+1 proof: the
// query count must not grow with the number of active chapters/series/sources.
// It counts the SQL reads for a small active set, then seeds MORE active chapters
// into the same DB and counts again — the counts must be IDENTICAL and small.
func TestActiveSourceCounts_QueryCountIsSetSizeIndependent(t *testing.T) {
	ctx := context.Background()
	seedClient, db := testdb.NewWithSQL(t)
	seedActiveSources(ctx, t, seedClient)

	client, drv := newCountingClient(db)
	svc := downloads.NewService(client)

	countQueries := func() int64 {
		drv.queries.Store(0)
		if _, err := svc.ActiveSourceCounts(ctx); err != nil {
			t.Fatalf("ActiveSourceCounts: %v", err)
		}
		return drv.queries.Load()
	}

	small := countQueries()

	// Add a second, larger set of ACTIVE (upgrading) chapters, each needing a
	// target resolution — the worst case for an N+1.
	seedManyActive(ctx, t, seedClient, 30)
	large := countQueries()

	if small != large {
		t.Errorf("N+1: %d queries for the small active set but %d for the larger one — the query count must not scale with the active-set size", small, large)
	}
	// Bounded shape: one chapters query + one providers batch (+ its feed eager
	// load). A generous ceiling still fails an N+1.
	const maxQueries = 4
	if large > maxQueries {
		t.Errorf("ActiveSourceCounts issued %d queries, want <= %d (bounded, set-size independent)", large, maxQueries)
	}
	t.Logf("queries: small=%d large=%d", small, large)
}
