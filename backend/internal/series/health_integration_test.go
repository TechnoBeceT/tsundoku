package series_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/series"
)

// seedHealthFixture builds: a healthy single-source series, and a 2-source
// series whose source B is stale (behind + old). Returns the stale series id.
func seedHealthFixture(t *testing.T, ctx context.Context, db *ent.Client) (staleSeriesID, healthySeriesID string) {
	t.Helper()
	old := time.Now().UTC().AddDate(0, 0, -40)
	recent := time.Now().UTC().AddDate(0, 0, -1)

	// Healthy single-source series.
	h := db.Series.Create().SetTitle("Healthy").SetSlug("healthy").SaveX(ctx)
	hp := db.SeriesProvider.Create().SetSeriesID(h.ID).SetProvider("a").SetImportance(10).SaveX(ctx)
	db.Chapter.Create().SetSeriesID(h.ID).SetChapterKey("h1").SetNumber(1).SetState("downloaded").SaveX(ctx)
	db.ProviderChapter.Create().SetSeriesProviderID(hp.ID).SetChapterKey("h1").SetNumber(1).SetProviderUploadDate(recent).SaveX(ctx)

	// 2-source series: A current, B stale (only has ch1, old).
	s := db.Series.Create().SetTitle("Stale Series").SetSlug("stale-series").SaveX(ctx)
	a := db.SeriesProvider.Create().SetSeriesID(s.ID).SetProvider("a").SetImportance(20).SaveX(ctx)
	b := db.SeriesProvider.Create().SetSeriesID(s.ID).SetProvider("b").SetImportance(10).SaveX(ctx)
	for _, k := range []struct {
		key string
		n   float64
	}{{"s1", 1}, {"s2", 2}, {"s3", 3}} {
		db.Chapter.Create().SetSeriesID(s.ID).SetChapterKey(k.key).SetNumber(k.n).SetState("downloaded").SaveX(ctx)
		db.ProviderChapter.Create().SetSeriesProviderID(a.ID).SetChapterKey(k.key).SetNumber(k.n).SetProviderUploadDate(recent).SaveX(ctx)
	}
	db.ProviderChapter.Create().SetSeriesProviderID(b.ID).SetChapterKey("s1").SetNumber(1).SetProviderUploadDate(old).SaveX(ctx)

	return s.ID.String(), h.ID.String()
}

func TestLibraryHealthReturnsOnlySickSeries(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	// The fixture also seeds a healthy series; the len==1 + id==staleID checks
	// below already prove it is absent from the scan, so its id is unused here.
	staleID, _ := seedHealthFixture(t, ctx, db)

	svc := series.NewService(db, t.TempDir(), 14)
	res, err := svc.LibraryHealth(ctx)
	if err != nil {
		t.Fatalf("LibraryHealth: %v", err)
	}
	if len(res.Series) != 1 {
		t.Fatalf("LibraryHealth returned %d series, want 1 (only the sick one)", len(res.Series))
	}
	got := res.Series[0]
	if got.ID != staleID {
		t.Fatalf("sick series id = %s, want %s", got.ID, staleID)
	}
	// Only the stale source is listed.
	if len(got.Sources) != 1 || got.Sources[0].Health != series.HealthStale {
		t.Fatalf("sources = %+v, want exactly one stale source", got.Sources)
	}
}

func TestUnhealthyCount(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	seedHealthFixture(t, ctx, db)
	svc := series.NewService(db, t.TempDir(), 14)
	n, err := svc.UnhealthyCount(ctx)
	if err != nil {
		t.Fatalf("UnhealthyCount: %v", err)
	}
	if n != 1 {
		t.Fatalf("UnhealthyCount = %d, want 1", n)
	}
}

// TestLibraryHealthExcludesCompleted proves a completed series — even one whose
// numbers would otherwise be stale — never appears in the library scan and never
// counts as unhealthy. Non-vacuous: leave it un-completed and it IS reported.
func TestLibraryHealthExcludesCompleted(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	staleID, _ := seedHealthFixture(t, ctx, db)
	db.Series.UpdateOneID(uuid.MustParse(staleID)).SetCompleted(true).ExecX(ctx)

	svc := series.NewService(db, t.TempDir(), 14)

	res, err := svc.LibraryHealth(ctx)
	if err != nil {
		t.Fatalf("LibraryHealth: %v", err)
	}
	if len(res.Series) != 0 {
		t.Fatalf("LibraryHealth returned %d series, want 0 (completed excluded)", len(res.Series))
	}

	n, err := svc.UnhealthyCount(ctx)
	if err != nil {
		t.Fatalf("UnhealthyCount: %v", err)
	}
	if n != 0 {
		t.Fatalf("UnhealthyCount = %d, want 0 (completed excluded)", n)
	}
}

// TestGetSeriesCompletedProvidersReportOK proves the detail endpoint shows a
// completed series' providers as ok even when the numbers read stale.
func TestGetSeriesCompletedProvidersReportOK(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	staleID, _ := seedHealthFixture(t, ctx, db)
	db.Series.UpdateOneID(uuid.MustParse(staleID)).SetCompleted(true).ExecX(ctx)

	svc := series.NewService(db, t.TempDir(), 14)
	got, err := svc.GetSeries(ctx, uuid.MustParse(staleID))
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if !got.Completed {
		t.Fatal("detail Completed = false, want true")
	}
	for _, p := range got.Providers {
		if p.Health != series.HealthOK {
			t.Errorf("provider %s Health = %q, want ok (series completed)", p.Provider, p.Health)
		}
	}
}

func TestGetSeriesEnrichesProviderHealth(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	staleID, _ := seedHealthFixture(t, ctx, db)
	svc := series.NewService(db, t.TempDir(), 14)

	id := uuid.MustParse(staleID)
	detail, err := svc.GetSeries(ctx, id)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	var sawStale, sawOK bool
	for _, p := range detail.Providers {
		switch p.Health {
		case series.HealthStale:
			sawStale = true
		case series.HealthOK:
			sawOK = true
		}
	}
	if !sawStale || !sawOK {
		t.Fatalf("expected one stale + one ok provider, got %+v", detail.Providers)
	}
}
