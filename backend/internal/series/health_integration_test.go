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

// fakeSourceLister is a canned series.SourceLister for the availability tests.
// It returns its fields verbatim so a test can drive the "loaded, id absent"
// success path (ok=true) and both fail-safe paths (ok=false / err).
type fakeSourceLister struct {
	loaded map[int64]struct{}
	ok     bool
	err    error
}

func (f fakeSourceLister) LoadedSourceIDs(context.Context) (map[int64]struct{}, bool, error) {
	return f.loaded, f.ok, f.err
}

// seedUnavailableFixture builds one multi-source series carrying:
//   - a LIVE provider (suwayomi_id 777, provider "777") whose source is NOT in
//     the engine's loaded set (its extension was uninstalled), and
//   - a disk-origin provider (suwayomi_id 0) that is likewise absent from the
//     loaded set but must NEVER be flagged unavailable (it was never an engine
//     source).
//
// Both carry a recent leading-edge feed so neither is stale/erroring on its own
// — the only unhealthy signal in play is the missing extension. Returns the
// series id.
func seedUnavailableFixture(t *testing.T, ctx context.Context, db *ent.Client) string {
	t.Helper()
	recent := time.Now().UTC().AddDate(0, 0, -1)

	s := db.Series.Create().SetTitle("Gone Extension").SetSlug("gone-extension").SaveX(ctx)
	live := db.SeriesProvider.Create().
		SetSeriesID(s.ID).SetProvider("777").SetSuwayomiID(777).SetImportance(20).SaveX(ctx)
	dsk := db.SeriesProvider.Create().
		SetSeriesID(s.ID).SetProvider("legacy-group").SetImportance(10).SaveX(ctx) // suwayomi_id defaults 0

	db.Chapter.Create().SetSeriesID(s.ID).SetChapterKey("c1").SetNumber(1).SetState("downloaded").SaveX(ctx)
	db.ProviderChapter.Create().SetSeriesProviderID(live.ID).SetChapterKey("c1").SetNumber(1).SetProviderUploadDate(recent).SaveX(ctx)
	db.ProviderChapter.Create().SetSeriesProviderID(dsk.ID).SetChapterKey("c1").SetNumber(1).SetProviderUploadDate(recent).SaveX(ctx)

	return s.ID.String()
}

// TestGetSeriesFlagsUnavailableSource proves the detail endpoint flags a live
// provider whose source is no longer loaded as "unavailable", while a
// disk-origin provider absent from the same loaded set stays "ok" (it was never
// an engine source). Non-vacuous: drop the SuwayomiID==0 guard and the
// disk-origin provider would flip to unavailable too.
func TestGetSeriesFlagsUnavailableSource(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	seriesID := seedUnavailableFixture(t, ctx, db)

	// Loaded set is empty ⇒ source 777 is absent (extension uninstalled).
	svc := series.NewService(db, t.TempDir(), 14).
		WithSourceLister(fakeSourceLister{loaded: map[int64]struct{}{}, ok: true})

	got, err := svc.GetSeries(ctx, uuid.MustParse(seriesID))
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	var sawUnavailable, sawOK bool
	for _, p := range got.Providers {
		switch {
		case p.Provider == "777":
			if p.Health != series.HealthUnavailable {
				t.Errorf("live provider Health = %q, want %q", p.Health, series.HealthUnavailable)
			}
			sawUnavailable = true
		default: // disk-origin provider
			if p.Health != series.HealthOK {
				t.Errorf("disk-origin provider Health = %q, want %q (never an engine source)", p.Health, series.HealthOK)
			}
			sawOK = true
		}
	}
	if !sawUnavailable || !sawOK {
		t.Fatalf("expected one unavailable live + one ok disk provider, got %+v", got.Providers)
	}
}

// TestGetSeriesFailSafeWhenListerCannotLoad proves the fail-safe contract: when
// the lister cannot determine the loaded set (ok=false OR an error), NO provider
// is flagged unavailable — a transient engine hiccup must never flip the library
// to "unavailable". Also covers the no-lister default. Non-vacuous: make
// loadedSources treat ok=false as "loaded empty" and each sub-case would report
// unavailable.
func TestGetSeriesFailSafeWhenListerCannotLoad(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	seriesID := seedUnavailableFixture(t, ctx, db)
	id := uuid.MustParse(seriesID)

	cases := []struct {
		name string
		svc  *series.Service
	}{
		{"no lister attached", series.NewService(db, t.TempDir(), 14)},
		{"lister reports ok=false", series.NewService(db, t.TempDir(), 14).
			WithSourceLister(fakeSourceLister{ok: false})},
		{"lister errors", series.NewService(db, t.TempDir(), 14).
			WithSourceLister(fakeSourceLister{err: context.DeadlineExceeded})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.svc.GetSeries(ctx, id)
			if err != nil {
				t.Fatalf("GetSeries: %v", err)
			}
			for _, p := range got.Providers {
				if p.Health == series.HealthUnavailable {
					t.Errorf("provider %s flagged unavailable, want fail-safe (no flag)", p.Provider)
				}
			}
		})
	}
}

// TestUnhealthyCountIncludesUnavailable proves an unavailable-only series (no
// stale/erroring source, just a missing extension) counts as unhealthy and is
// returned by the library scan. Non-vacuous: drop HealthUnavailable from
// sickSources' condition and both assertions fall to 0.
func TestUnhealthyCountIncludesUnavailable(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	seriesID := seedUnavailableFixture(t, ctx, db)

	svc := series.NewService(db, t.TempDir(), 14).
		WithSourceLister(fakeSourceLister{loaded: map[int64]struct{}{}, ok: true})

	n, err := svc.UnhealthyCount(ctx)
	if err != nil {
		t.Fatalf("UnhealthyCount: %v", err)
	}
	if n != 1 {
		t.Fatalf("UnhealthyCount = %d, want 1 (unavailable series counts)", n)
	}

	res, err := svc.LibraryHealth(ctx)
	if err != nil {
		t.Fatalf("LibraryHealth: %v", err)
	}
	if len(res.Series) != 1 || res.Series[0].ID != seriesID {
		t.Fatalf("LibraryHealth = %+v, want the one unavailable series %s", res.Series, seriesID)
	}
	var sawUnavailable bool
	for _, p := range res.Series[0].Sources {
		if p.Health == series.HealthUnavailable {
			sawUnavailable = true
		}
	}
	if !sawUnavailable {
		t.Fatalf("scanned series lists no unavailable source: %+v", res.Series[0].Sources)
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
