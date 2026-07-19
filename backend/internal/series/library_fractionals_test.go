package series_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/series"
)

// rowByID finds a library-fractionals row for a series id (nil when absent).
func rowByID(dto series.LibraryFractionalsDTO, id uuid.UUID) *series.SeriesFractionalsDTO {
	for i := range dto.Series {
		if dto.Series[i].SeriesID == id.String() {
			return &dto.Series[i]
		}
	}
	return nil
}

// rowCounts is the numeric + toggle projection of a library-fractionals row, so a
// test can assert every count in ONE struct comparison rather than a branchy
// sequence of ifs.
type rowCounts struct {
	Frac, Removable, Total, Ignoring int
	AllIgnoring                      bool
}

// countsOf projects a row onto its counts + toggle state.
func countsOf(r *series.SeriesFractionalsDTO) rowCounts {
	return rowCounts{r.FractionalCount, r.RemovableCount, r.ProvidersTotal, r.ProvidersIgnoring, r.AllProvidersIgnoring}
}

// seedRemovableSeries builds a series whose `removable` downloaded fractionals are
// all removable (a single ignored source), plus `extraProtected` more that a
// second NON-ignored source also carries (counted, never removable). Returns the
// series id. Extracted to a top-level helper to keep the sort test simple.
func seedRemovableSeries(ctx context.Context, t *testing.T, db *ent.Client, storage, title, slug string, removable, extraProtected int) uuid.UUID {
	t.Helper()
	s := db.Series.Create().SetTitle(title).SetSlug(slug).
		SetCategoryID(catID(ctx, db, "Manga")).SaveX(ctx)
	fx := cleanupFixture{storage: storage, series: s, providers: map[string]*ent.SeriesProvider{}}

	nums := make([]float64, 0, removable+extraProtected)
	for i := 1; i <= removable; i++ {
		nums = append(nums, float64(i)+0.1)
	}
	protNums := make([]float64, 0, extraProtected)
	for i := 1; i <= extraProtected; i++ {
		protNums = append(protNums, float64(100+i)+0.1)
	}

	ig := seedFeed(ctx, t, db, s.ID, "kaliscan", 40, append(append([]float64{}, nums...), protNums...)...)
	db.SeriesProvider.UpdateOneID(ig.ID).SetIgnoreFractional(true).ExecX(ctx)
	fx.providers["kaliscan"] = db.SeriesProvider.GetX(ctx, ig.ID)
	if extraProtected > 0 {
		seedFeed(ctx, t, db, s.ID, "comix", 60, protNums...) // a live source also carries them
	}
	for _, n := range append(append([]float64{}, nums...), protNums...) {
		seedDownloadedChapter(ctx, t, db, fx, chapterKeyOf(n), n, 2, fx.providers["kaliscan"])
	}
	return s.ID
}

// TestLibraryFractionals_ListsSeriesWithBothCounts uses the real prod shape (A
// Returner's Magic): six downloaded fractionals, one of which (268.1) is protected
// by the resurrection guard because a non-ignored source (Comix) also carries it.
// So the row reports fractionalCount=6 but removableCount=5 — the "set policy,
// THEN clean" case this page exists for.
func TestLibraryFractionals_ListsSeriesWithBothCounts(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	fx := seedReturnersMagic(ctx, t, db)
	svc := series.NewService(db, fx.storage, 14)

	dto, err := svc.LibraryFractionals(ctx)
	if err != nil {
		t.Fatalf("LibraryFractionals: %v", err)
	}
	row := rowByID(dto, fx.series.ID)
	if row == nil {
		t.Fatalf("series not listed; got %+v", dto.Series)
	}

	// 6 downloaded fractionals, 5 removable (268.1 excluded — Comix, not ignored,
	// also carries it), 3 sources with 2 ignoring → toggle OFF.
	if got, want := countsOf(row), (rowCounts{6, 5, 3, 2, false}); got != want {
		t.Errorf("counts = %+v, want %+v", got, want)
	}
	if row.Category != "Manga" {
		t.Errorf("category = %q, want Manga", row.Category)
	}
	if row.Title != "A Returner's Magic Should Be Special" || row.DisplayName == "" {
		t.Errorf("title/displayName = %q/%q, want the canonical title populated", row.Title, row.DisplayName)
	}
}

// TestLibraryFractionals_ExcludesSeriesWithNoFractionals: a series whose only
// downloaded chapters are WHOLE numbers is never listed. Envelope is a non-nil []
// so the JSON never renders null.
func TestLibraryFractionals_ExcludesSeriesWithNoFractionals(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	s := db.Series.Create().SetTitle("Wholes Only").SetSlug("wholes-only").
		SetCategoryID(catID(ctx, db, "Manga")).SaveX(ctx)
	fx := cleanupFixture{storage: t.TempDir(), series: s, providers: map[string]*ent.SeriesProvider{}}
	fx.providers["comix"] = seedFeed(ctx, t, db, s.ID, "comix", 60, 1, 2, 3)
	seedDownloadedChapter(ctx, t, db, fx, "1", 1, 90, fx.providers["comix"])
	seedDownloadedChapter(ctx, t, db, fx, "2", 2, 92, fx.providers["comix"])

	svc := series.NewService(db, fx.storage, 14)
	dto, err := svc.LibraryFractionals(ctx)
	if err != nil {
		t.Fatalf("LibraryFractionals: %v", err)
	}
	if dto.Series == nil {
		t.Fatal("Series must be a non-nil slice so the JSON renders [] not null")
	}
	if rowByID(dto, s.ID) != nil {
		t.Errorf("a series with no downloaded fractionals was listed; got %+v", dto.Series)
	}
}

// TestLibraryFractionals_SortsMostActionableFirst pins the ordering: removableCount
// desc, then fractionalCount desc, then title. Three series make every tiebreak
// level bite (B and A tie on removable=5; B wins on the higher fractionalCount).
func TestLibraryFractionals_SortsMostActionableFirst(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	a := seedRemovableSeries(ctx, t, db, storage, "Aaa", "aaa", 5, 0) // rem 5, frac 5
	b := seedRemovableSeries(ctx, t, db, storage, "Bbb", "bbb", 5, 2) // rem 5, frac 7
	c := seedRemovableSeries(ctx, t, db, storage, "Ccc", "ccc", 2, 0) // rem 2, frac 2

	svc := series.NewService(db, storage, 14)
	dto, err := svc.LibraryFractionals(ctx)
	if err != nil {
		t.Fatalf("LibraryFractionals: %v", err)
	}
	if len(dto.Series) != 3 {
		t.Fatalf("want 3 rows, got %d (%+v)", len(dto.Series), dto.Series)
	}
	wantOrder := []uuid.UUID{b, a, c}
	for i, want := range wantOrder {
		if dto.Series[i].SeriesID != want.String() {
			t.Errorf("row %d = %q (rem=%d frac=%d), want series %s", i, dto.Series[i].Title,
				dto.Series[i].RemovableCount, dto.Series[i].FractionalCount, want)
		}
	}
}

// assertAllSourcesIgnore fails if any of the series' sources is not flagged.
func assertAllSourcesIgnore(ctx context.Context, t *testing.T, db *ent.Client, providers map[string]*ent.SeriesProvider) {
	t.Helper()
	for name, sp := range providers {
		if !db.SeriesProvider.GetX(ctx, sp.ID).IgnoreFractional {
			t.Errorf("source %q was not flagged ignore_fractional", name)
		}
	}
}

// TestSetIgnoreFractionalForSeries_SetsAllSourcesAndReconciles proves the
// whole-series toggle flags EVERY source (so all fractionals become removable and
// allProvidersIgnoring flips true) AND runs the reconcile that parks an
// undownloaded fractional — and that clearing the flag restores it.
func TestSetIgnoreFractionalForSeries_SetsAllSourcesAndReconciles(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	fx := seedReturnersMagic(ctx, t, db)

	// A wanted fractional carried ONLY by the (currently non-ignored) Comix — the
	// reconcile must park it once Comix is flagged.
	n := 300.1
	db.ProviderChapter.Create().SetSeriesProviderID(fx.providers["comix"].ID).
		SetChapterKey("300.1").SetNumber(n).SaveX(ctx)
	wanted := db.Chapter.Create().SetSeriesID(fx.series.ID).SetChapterKey("300.1").
		SetNumber(n).SetState("wanted").SaveX(ctx)

	svc := series.NewService(db, fx.storage, 14)
	if err := svc.SetIgnoreFractionalForSeries(ctx, fx.series.ID, true); err != nil {
		t.Fatalf("SetIgnoreFractionalForSeries(true): %v", err)
	}
	assertAllSourcesIgnore(ctx, t, db, fx.providers)

	// The undownloaded fractional was parked (wanted → ignored) by the reconcile.
	if got := db.Chapter.GetX(ctx, wanted.ID).State; got != entchapter.StateIgnored {
		t.Errorf("wanted fractional state = %s, want ignored (reconcile should have parked it)", got)
	}

	dto, err := svc.LibraryFractionals(ctx)
	if err != nil {
		t.Fatalf("LibraryFractionals: %v", err)
	}
	row := rowByID(dto, fx.series.ID)
	if row == nil {
		t.Fatal("series not listed after toggle")
	}
	// Every source ignores now → 268.1 removable too, so removable == fractional == 6.
	if got, want := countsOf(row), (rowCounts{6, 6, 3, 3, true}); got != want {
		t.Errorf("after toggle: counts = %+v, want %+v", got, want)
	}

	// Clearing the flag restores the parked fractional (ignored → wanted).
	if err := svc.SetIgnoreFractionalForSeries(ctx, fx.series.ID, false); err != nil {
		t.Fatalf("SetIgnoreFractionalForSeries(false): %v", err)
	}
	if got := db.Chapter.GetX(ctx, wanted.ID).State; got != entchapter.StateWanted {
		t.Errorf("after clearing: parked fractional state = %s, want wanted (restored)", got)
	}
}

// TestSetIgnoreFractionalForSeries_UnknownSeries: an unknown id is ErrSeriesNotFound.
func TestSetIgnoreFractionalForSeries_UnknownSeries(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	svc := series.NewService(db, t.TempDir(), 14)

	if err := svc.SetIgnoreFractionalForSeries(ctx, uuid.New(), true); !errors.Is(err, series.ErrSeriesNotFound) {
		t.Errorf("err = %v, want ErrSeriesNotFound", err)
	}
}

// TestLibraryFractionalsQueryCountIsSeriesCountIndependent is the NO-N+1 proof:
// LibraryFractionals resolves every row IN MEMORY from one bounded eager load, so
// the SQL read count must be identical for a 2-series and a 20-series library.
func TestLibraryFractionalsQueryCountIsSeriesCountIndependent(t *testing.T) {
	ctx := context.Background()
	seedClient, db := testdb.NewWithSQL(t)
	storage := t.TempDir()

	seedN := func(prefix string, n int) {
		for i := range n {
			seedRemovableSeries(ctx, t, seedClient, storage, prefix+chapterKeyOf(float64(i)), prefix+chapterKeyOf(float64(i)), 1, 0)
		}
	}

	client, drv := newCountingClient(db)
	count := func(want int) int64 {
		svc := series.NewService(client, storage, 14)
		drv.queries.Store(0)
		dto, err := svc.LibraryFractionals(ctx)
		if err != nil {
			t.Fatalf("LibraryFractionals: %v", err)
		}
		if len(dto.Series) != want {
			t.Fatalf("listed %d series, want %d", len(dto.Series), want)
		}
		return drv.queries.Load()
	}

	seedN("aaa", 2)
	smallQ := count(2)
	seedN("bbb", 18)
	bigQ := count(20)

	if smallQ != bigQ {
		t.Errorf("N+1: %d queries for 2 series but %d for 20 — it must be flat", smallQ, bigQ)
	}
	const maxQueries = 6
	if bigQ > maxQueries {
		t.Errorf("LibraryFractionals issued %d queries, want <= %d (one bounded load + its eager loads)", bigQ, maxQueries)
	}
	t.Logf("queries: 2 series=%d 20 series=%d", smallQ, bigQ)
}
