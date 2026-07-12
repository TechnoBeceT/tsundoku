package series_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
	"github.com/technobecet/tsundoku/internal/series"
)

// seedFeed creates one provider on the series with the given feed numbers.
func seedFeed(ctx context.Context, t *testing.T, client *ent.Client, seriesID uuid.UUID, name string, importance int, numbers ...float64) *ent.SeriesProvider {
	t.Helper()
	sp := client.SeriesProvider.Create().
		SetSeriesID(seriesID).SetProvider(name).SetSuwayomiID(importance).SetImportance(importance).SaveX(ctx)
	for _, n := range numbers {
		num := n
		client.ProviderChapter.Create().
			SetSeriesProviderID(sp.ID).
			SetChapterKey(strconv.FormatFloat(num, 'f', -1, 64)).
			SetNumber(num).
			SaveX(ctx)
	}
	return sp
}

// providersByID indexes a detail response's providers by their SeriesProvider id.
func providersByID(dto series.SeriesDetailDTO) map[string]series.ProviderDTO {
	byID := make(map[string]series.ProviderDTO, len(dto.Providers))
	for _, p := range dto.Providers {
		byID[p.ID] = p
	}
	return byID
}

// TestProviderDTO_FractionalFeed is the evidence the owner ticks the
// ignore-fractional toggle from: a re-uploader's systematic ".1" run is listed in
// full, ascending, straight off the already-loaded feed.
func TestProviderDTO_FractionalFeed(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	s := db.Series.Create().SetTitle("Fractional Series").SetSlug("fractional-series").SaveX(ctx)
	// A mirror that republishes whole chapters as lone ".1" re-uploads.
	reuploader := seedFeed(ctx, t, db, s.ID, "comic-asura", 40, 1, 1.1, 2, 2.1, 3)

	svc := series.NewService(db, t.TempDir(), 14)
	dto, err := svc.GetSeries(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}

	got := providersByID(dto)[reuploader.ID.String()]
	if got.FractionalCount != 2 {
		t.Errorf("FractionalCount = %d, want 2 (1.1 and 2.1)", got.FractionalCount)
	}
	want := []string{"1.1", "2.1"}
	if !slices.Equal(got.FractionalChapters, want) {
		t.Errorf("FractionalChapters = %v, want %v (ascending)", got.FractionalChapters, want)
	}
	if got.IgnoreFractional {
		t.Error("IgnoreFractional = true, want false by default (the owner has not ticked it)")
	}
}

// TestProviderDTO_IgnoreFractionalRoundTrips pins that the DTO reports the stored
// flag — the toggle the FE binds to must reflect what is in the DB, not a constant.
func TestProviderDTO_IgnoreFractionalRoundTrips(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	s := db.Series.Create().SetTitle("Toggled Series").SetSlug("toggled-series").SaveX(ctx)
	sp := seedFeed(ctx, t, db, s.ID, "comic-asura", 40, 1, 1.1)
	db.SeriesProvider.UpdateOneID(sp.ID).SetIgnoreFractional(true).ExecX(ctx)

	svc := series.NewService(db, t.TempDir(), 14)
	dto, err := svc.GetSeries(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}

	got := providersByID(dto)[sp.ID.String()]
	if !got.IgnoreFractional {
		t.Error("IgnoreFractional = false, want true (the flag is set on the row)")
	}
	// The toggle suppresses nothing in the READ model: the evidence stays visible,
	// so the owner can always see what he ignored and un-tick it.
	if got.FractionalCount != 1 {
		t.Errorf("FractionalCount = %d, want 1 — an ignored source still REPORTS its fractionals", got.FractionalCount)
	}
}

// TestSetIgnoreFractional_TogglesAndIsReversible pins the toggle both ways: it is
// a PREFERENCE, so un-ticking must restore the source's fractionals immediately.
func TestSetIgnoreFractional_TogglesAndIsReversible(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	s := db.Series.Create().SetTitle("Toggle Me").SetSlug("toggle-me").SaveX(ctx)
	sp := seedFeed(ctx, t, db, s.ID, "comic-asura", 40, 1, 1.1)
	svc := series.NewService(db, t.TempDir(), 14)

	if err := svc.SetIgnoreFractional(ctx, s.ID, sp.ID, true); err != nil {
		t.Fatalf("SetIgnoreFractional(true): %v", err)
	}
	if got := db.SeriesProvider.GetX(ctx, sp.ID).IgnoreFractional; !got {
		t.Fatal("ignore_fractional = false after setting it true")
	}

	if err := svc.SetIgnoreFractional(ctx, s.ID, sp.ID, false); err != nil {
		t.Fatalf("SetIgnoreFractional(false): %v", err)
	}
	if got := db.SeriesProvider.GetX(ctx, sp.ID).IgnoreFractional; got {
		t.Error("ignore_fractional = true after un-ticking — the toggle must be reversible")
	}
}

// TestSetIgnoreFractional_UnknownSeries: a series id that does not exist is a 404
// (ErrSeriesNotFound), not a silent no-op.
func TestSetIgnoreFractional_UnknownSeries(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	svc := series.NewService(db, t.TempDir(), 14)

	err := svc.SetIgnoreFractional(ctx, uuid.New(), uuid.New(), true)
	if !errors.Is(err, series.ErrSeriesNotFound) {
		t.Errorf("err = %v, want ErrSeriesNotFound", err)
	}
}

// TestSetIgnoreFractional_ProviderOfAnotherSeries is the ownership guard: a real
// SeriesProvider row that belongs to a DIFFERENT series must be rejected
// (ErrProviderNotInSeries → 400), never silently toggled. Without the
// series-scoped check the endpoint would let any series flip any provider's flag.
func TestSetIgnoreFractional_ProviderOfAnotherSeries(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	mine := db.Series.Create().SetTitle("Mine").SetSlug("mine").SaveX(ctx)
	theirs := db.Series.Create().SetTitle("Theirs").SetSlug("theirs").SaveX(ctx)
	foreign := seedFeed(ctx, t, db, theirs.ID, "comic-asura", 40, 1, 1.1)

	svc := series.NewService(db, t.TempDir(), 14)
	err := svc.SetIgnoreFractional(ctx, mine.ID, foreign.ID, true)
	if !errors.Is(err, series.ErrProviderNotInSeries) {
		t.Fatalf("err = %v, want ErrProviderNotInSeries", err)
	}
	if db.SeriesProvider.GetX(ctx, foreign.ID).IgnoreFractional {
		t.Error("the other series' provider was toggled — the ownership check did not hold")
	}
}

// TestSetIgnoreFractional_DeletesNothing is the NEVER-AUTO-DELETE guard. Flipping
// the flag is a preference change: every ProviderChapter row (including the
// fractional ones it suppresses) and every Chapter row must survive, so un-ticking
// restores the source instantly and no downloaded chapter is orphaned.
//
// Non-vacuous by construction: the counts are taken from a feed that actually
// CONTAINS fractionals (1.1, 2.1) plus a Chapter row for one of them, and the
// fractional rows are counted separately — a delete-on-toggle implementation
// would drop them and fail here.
func TestSetIgnoreFractional_DeletesNothing(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	s := db.Series.Create().SetTitle("Keep Everything").SetSlug("keep-everything").SaveX(ctx)
	sp := seedFeed(ctx, t, db, s.ID, "comic-asura", 40, 1, 1.1, 2, 2.1)
	num := 1.1
	db.Chapter.Create().
		SetSeriesID(s.ID).SetChapterKey("1.1").SetNumber(num).
		SetState("downloaded").SetFilename("[comic-asura] Keep Everything 001.1.cbz").
		SetSatisfiedByProviderID(sp.ID).SaveX(ctx)

	countRows := func() (pcs, fracPCs, chapters int) {
		t.Helper()
		pcs = db.ProviderChapter.Query().CountX(ctx)
		fracPCs = db.ProviderChapter.Query().
			Where(entproviderchapter.ChapterKeyIn("1.1", "2.1")).CountX(ctx)
		chapters = db.Chapter.Query().CountX(ctx)
		return
	}

	beforePC, beforeFrac, beforeCh := countRows()
	if beforeFrac != 2 || beforeCh != 1 {
		t.Fatalf("seed is vacuous: fractional feed rows = %d (want 2), chapters = %d (want 1)", beforeFrac, beforeCh)
	}

	svc := series.NewService(db, t.TempDir(), 14)
	if err := svc.SetIgnoreFractional(ctx, s.ID, sp.ID, true); err != nil {
		t.Fatalf("SetIgnoreFractional: %v", err)
	}

	afterPC, afterFrac, afterCh := countRows()
	if afterPC != beforePC || afterFrac != beforeFrac || afterCh != beforeCh {
		t.Errorf("never-auto-delete VIOLATED: provider chapters %d→%d (fractional %d→%d), chapters %d→%d",
			beforePC, afterPC, beforeFrac, afterFrac, beforeCh, afterCh)
	}
}

// TestProviderDTO_NoFractionals_EmptyNotNull: a clean source reports 0 and an
// EMPTY, NON-NIL slice, so the JSON renders [] and the FE never guards a null.
func TestProviderDTO_NoFractionals_EmptyNotNull(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	s := db.Series.Create().SetTitle("Clean Series").SetSlug("clean-series").SaveX(ctx)
	clean := seedFeed(ctx, t, db, s.ID, "asura", 60, 1, 2, 3)

	svc := series.NewService(db, t.TempDir(), 14)
	dto, err := svc.GetSeries(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}

	got := providersByID(dto)[clean.ID.String()]
	if got.FractionalCount != 0 {
		t.Errorf("FractionalCount = %d, want 0 (a whole-numbered feed)", got.FractionalCount)
	}
	if got.FractionalChapters == nil {
		t.Fatal("FractionalChapters is nil — it must be an empty slice so the JSON is [] not null")
	}
	if len(got.FractionalChapters) != 0 {
		t.Errorf("FractionalChapters = %v, want []", got.FractionalChapters)
	}

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal ProviderDTO: %v", err)
	}
	if !strings.Contains(string(raw), `"fractionalChapters":[]`) {
		t.Errorf("ProviderDTO JSON = %s, want fractionalChapters marshalled as [] (never null)", raw)
	}
}

// TestProviderDTO_FractionalFeed_CostsNoExtraQueries is the query-slope guard: the
// fractional summary is computed IN MEMORY from the feed GetSeries already
// eager-loads, so its query count must not grow with the size of that feed. A
// 4-row feed and a 40-row feed (20 of them fractional) must cost the SAME reads —
// a per-chapter lookup would blow the larger one up.
func TestProviderDTO_FractionalFeed_CostsNoExtraQueries(t *testing.T) {
	ctx := context.Background()
	seedClient, db := testdb.NewWithSQL(t)

	small := seedClient.Series.Create().SetTitle("Small Feed").SetSlug("small-feed").SaveX(ctx)
	seedFeed(ctx, t, seedClient, small.ID, "small-src", 10, 1, 1.1, 2, 2.1)

	big := seedClient.Series.Create().SetTitle("Big Feed").SetSlug("big-feed").SaveX(ctx)
	nums := make([]float64, 0, 40)
	for i := 1; i <= 20; i++ {
		nums = append(nums, float64(i), float64(i)+0.1)
	}
	seedFeed(ctx, t, seedClient, big.ID, "big-src", 10, nums...)

	client, drv := newCountingClient(db)
	svc := series.NewService(client, t.TempDir(), 14)

	count := func(id uuid.UUID, wantFractional int) int64 {
		drv.queries.Store(0)
		dto, err := svc.GetSeries(ctx, id)
		if err != nil {
			t.Fatalf("GetSeries(%s): %v", id, err)
		}
		if got := dto.Providers[0].FractionalCount; got != wantFractional {
			t.Fatalf("FractionalCount = %d, want %d", got, wantFractional)
		}
		return drv.queries.Load()
	}

	smallQ, bigQ := count(small.ID, 2), count(big.ID, 20)
	if smallQ != bigQ {
		t.Errorf("N+1: GetSeries issued %d queries for a 4-row feed but %d for a 40-row feed — the fractional summary must cost no query", smallQ, bigQ)
	}
	t.Log(fmt.Sprintf("queries: feed(4)=%d feed(40)=%d", smallQ, bigQ))
}
