package series_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/series"
)

// TestProviderDTO_FeedCountAndRanges proves the stored-feed fields on ProviderDTO:
// FeedCount is the size of the provider's ProviderChapter feed (what the source
// OFFERS) and FeedRanges is that feed's gap-collapsed coverage string — both read
// from the already-eager-loaded feed rows, never from a live source call.
//
// The gapped feed (1, 2, 3, 5) is the load-bearing case: it must collapse to
// "1-3, 5", proving the shared chapterrange walk is really applied. The empty feed
// must report 0 / "" (never a bogus "0-0"). FeedCount is deliberately asserted to
// DIFFER from ChapterCount (how many chapters this provider currently SATISFIES) —
// conflating the two is the bug this feature fixes.
func TestProviderDTO_FeedCountAndRanges(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	s := db.Series.Create().SetTitle("Feed Test Series").SetSlug("feed-test-series").SaveX(ctx)

	fed := db.SeriesProvider.Create().
		SetSeriesID(s.ID).SetProvider("mangadex").SetSuwayomiID(7).SetImportance(10).SaveX(ctx)
	empty := db.SeriesProvider.Create().
		SetSeriesID(s.ID).SetProvider("weeb").SetSuwayomiID(9).SetImportance(5).SaveX(ctx)

	// The feed offers 1, 2, 3 and 5 — a gap at 4.
	for _, n := range []float64{1, 2, 3, 5} {
		num := n
		db.ProviderChapter.Create().
			SetSeriesProviderID(fed.ID).
			SetChapterKey(strconv.FormatFloat(num, 'f', -1, 64)).
			SetNumber(num).
			SaveX(ctx)
	}

	// Only ONE of those chapters is actually downloaded from this provider, so
	// ChapterCount (1) and FeedCount (4) must not agree.
	one := 1.0
	db.Chapter.Create().SetSeriesID(s.ID).SetChapterKey("1").SetNumber(one).
		SetState("downloaded").SetSatisfiedByProviderID(fed.ID).SetSatisfiedImportance(10).SaveX(ctx)

	svc := series.NewService(db, t.TempDir(), 14)
	dto, err := svc.GetSeries(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}

	byID := make(map[string]series.ProviderDTO, len(dto.Providers))
	for _, p := range dto.Providers {
		byID[p.ID] = p
	}

	got := byID[fed.ID.String()]
	if got.FeedCount != 4 {
		t.Errorf("fed provider FeedCount = %d, want 4 (the stored feed size)", got.FeedCount)
	}
	if got.FeedRanges != "1-3, 5" {
		t.Errorf("fed provider FeedRanges = %q, want %q (gap at 4 must split the run)", got.FeedRanges, "1-3, 5")
	}
	if got.ChapterCount != 1 {
		t.Errorf("fed provider ChapterCount = %d, want 1 — FeedCount (offered) and ChapterCount (satisfied) are different questions", got.ChapterCount)
	}

	none := byID[empty.ID.String()]
	if none.FeedCount != 0 {
		t.Errorf("empty-feed provider FeedCount = %d, want 0", none.FeedCount)
	}
	if none.FeedRanges != "" {
		t.Errorf("empty-feed provider FeedRanges = %q, want \"\" (no feed ⇒ no range, never \"0-0\")", none.FeedRanges)
	}
}
