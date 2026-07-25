// Package refresh_test — proofs for the per-series SCOPED sweep (GAP-113).
// RefreshSeries reuses RefreshAll's sweep body restricted to one series: it is
// upsert-only (never deletes), honors the same monitored/completed gate, and
// discovers new chapters only for the one series it is asked about.
//
// Tests require Docker (via testcontainers) for an ephemeral PostgreSQL instance.
package refresh_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	enginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
)

// TestRefreshSeries_DiscoversOnlyThatSeries proves the scope: a sweep of ONE
// series discovers its new chapters and leaves a second monitored series
// (whose source also has new chapters) completely untouched.
func TestRefreshSeries_DiscoversOnlyThatSeries(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	const (
		srcA, urlA = 11, "/manga/a"
		srcB, urlB = 22, "/manga/b"
	)
	fc := enginefake.New(
		enginefake.WithChapters(srcA, urlA, []sourceengine.Chapter{{Number: num(1), URL: "a1"}}),
		enginefake.WithChapters(srcB, urlB, []sourceengine.Chapter{{Number: num(1), URL: "b1"}}),
	)
	sA, spA := seedMonitoredSeries(t, ctx, db, "series-a", srcA, urlA)
	_, spB := seedMonitoredSeries(t, ctx, db, "series-b", srcB, urlB)

	res, err := newSvc(t, db, fc).RefreshSeries(ctx, sA.ID)
	if err != nil {
		t.Fatalf("RefreshSeries: %v", err)
	}
	if res.SeriesRefreshed != 1 || res.ProvidersRefreshed != 1 || res.NewChapters != 1 {
		t.Fatalf("scoped sweep = %+v, want series=1 providers=1 new=1", res)
	}

	// series-a discovered its chapter; series-b was never fetched.
	if n := db.ProviderChapter.Query().Where(entproviderchapter.SeriesProviderID(spA.ID)).CountX(ctx); n != 1 {
		t.Errorf("series-a provider chapters = %d, want 1", n)
	}
	if n := db.ProviderChapter.Query().Where(entproviderchapter.SeriesProviderID(spB.ID)).CountX(ctx); n != 0 {
		t.Errorf("series-b provider chapters = %d, want 0 (scoped refresh must not touch it)", n)
	}
}

// TestRefreshSeries_IsUpsertOnly proves the never-auto-delete invariant (Rule 2):
// when a source's listing SHRINKS between sweeps, the vanished chapter's rows are
// KEPT, not deleted. A downloaded chapter and its provider feed row both survive a
// scoped refresh whose source no longer lists that chapter.
func TestRefreshSeries_IsUpsertOnly(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	const src, url = 33, "/manga/c"
	// First: source lists chapters 1 and 2.
	fc := enginefake.New(enginefake.WithChapters(src, url, []sourceengine.Chapter{
		{Number: num(1), URL: "c1"},
		{Number: num(2), URL: "c2"},
	}))
	s, sp := seedMonitoredSeries(t, ctx, db, "series-c", src, url)
	if _, err := newSvc(t, db, fc).RefreshSeries(ctx, s.ID); err != nil {
		t.Fatalf("RefreshSeries (initial): %v", err)
	}
	if n := db.ProviderChapter.Query().Where(entproviderchapter.SeriesProviderID(sp.ID)).CountX(ctx); n != 2 {
		t.Fatalf("after initial sweep provider chapters = %d, want 2", n)
	}
	chapterCount := db.Chapter.Query().CountX(ctx)

	// Now the source listing SHRINKS to only chapter 1 (chapter 2 vanished upstream).
	fc = enginefake.New(enginefake.WithChapters(src, url, []sourceengine.Chapter{
		{Number: num(1), URL: "c1"},
	}))
	if _, err := newSvc(t, db, fc).RefreshSeries(ctx, s.ID); err != nil {
		t.Fatalf("RefreshSeries (shrunk): %v", err)
	}

	// Upsert-only: neither the vanished ProviderChapter feed row nor the Chapter row
	// was deleted (Rule 2).
	if n := db.ProviderChapter.Query().Where(entproviderchapter.SeriesProviderID(sp.ID)).CountX(ctx); n != 2 {
		t.Errorf("provider chapters after shrink = %d, want 2 (never-auto-delete)", n)
	}
	if n := db.Chapter.Query().CountX(ctx); n != chapterCount {
		t.Errorf("chapter rows after shrink = %d, want %d (never-auto-delete)", n, chapterCount)
	}
}

// TestRefreshSeries_SkipsGatedSeries proves the monitored/completed gate matches
// RefreshAll: a completed or unmonitored series is a no-op (no fetch, zero result),
// so its source is never even queried.
func TestRefreshSeries_SkipsGatedSeries(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	const src, url = 44, "/manga/d"
	fc := enginefake.New(enginefake.WithChapters(src, url, []sourceengine.Chapter{{Number: num(1), URL: "d1"}}))
	s, sp := seedMonitoredSeries(t, ctx, db, "series-d", src, url)

	// Completed → skipped.
	db.Series.UpdateOneID(s.ID).SetCompleted(true).ExecX(ctx)
	res, err := newSvc(t, db, fc).RefreshSeries(ctx, s.ID)
	if err != nil {
		t.Fatalf("RefreshSeries (completed): %v", err)
	}
	if res.SeriesRefreshed != 0 || res.ProvidersRefreshed != 0 || res.NewChapters != 0 {
		t.Errorf("completed series sweep = %+v, want all zero (gated)", res)
	}

	// Unmonitored → skipped.
	db.Series.UpdateOneID(s.ID).SetCompleted(false).SetMonitored(false).ExecX(ctx)
	res, err = newSvc(t, db, fc).RefreshSeries(ctx, s.ID)
	if err != nil {
		t.Fatalf("RefreshSeries (unmonitored): %v", err)
	}
	if res.SeriesRefreshed != 0 {
		t.Errorf("unmonitored series sweep = %+v, want zero (gated)", res)
	}

	// Nothing was fetched in either gated call.
	if n := db.ProviderChapter.Query().Where(entproviderchapter.SeriesProviderID(sp.ID)).CountX(ctx); n != 0 {
		t.Errorf("gated series provider chapters = %d, want 0 (no fetch)", n)
	}
}
