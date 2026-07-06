package series_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/series"
)

// TestProviderDTO_LinkedAndChapterCount proves the Match backend's two new
// ProviderDTO fields: Linked is false for a disk-origin provider (suwayomi_id
// == 0 — an unlinked/unknown group the owner can Match to a real source) and
// true for a real ingested provider (suwayomi_id != 0); ChapterCount reports
// how many of the series' chapters this provider currently satisfies
// (Chapter.satisfied_by_provider_id), computed with no extra query.
func TestProviderDTO_LinkedAndChapterCount(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	s := db.Series.Create().SetTitle("Linked Test Series").SetSlug("linked-test-series").SaveX(ctx)

	diskSP := db.SeriesProvider.Create().
		SetSeriesID(s.ID).
		SetProvider("mangadex").
		SetScanlator("Alpha").
		SetImportance(1).
		// SuwayomiID left at its zero-value default (0) — the disk-origin marker.
		SaveX(ctx)

	realSP := db.SeriesProvider.Create().
		SetSeriesID(s.ID).
		SetProvider("weeb").
		SetSuwayomiID(42).
		SetImportance(5).
		SaveX(ctx)

	one, two, three := 1.0, 2.0, 3.0
	db.Chapter.Create().SetSeriesID(s.ID).SetChapterKey("1").SetNumber(one).
		SetState("downloaded").SetSatisfiedByProviderID(diskSP.ID).SetSatisfiedImportance(1).SaveX(ctx)
	db.Chapter.Create().SetSeriesID(s.ID).SetChapterKey("2").SetNumber(two).
		SetState("downloaded").SetSatisfiedByProviderID(diskSP.ID).SetSatisfiedImportance(1).SaveX(ctx)
	db.Chapter.Create().SetSeriesID(s.ID).SetChapterKey("3").SetNumber(three).
		SetState("downloaded").SetSatisfiedByProviderID(realSP.ID).SetSatisfiedImportance(5).SaveX(ctx)

	svc := series.NewService(db, t.TempDir(), 14)
	dto, err := svc.GetSeries(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}

	byID := make(map[string]series.ProviderDTO, len(dto.Providers))
	for _, p := range dto.Providers {
		byID[p.ID] = p
	}

	disk := byID[diskSP.ID.String()]
	if disk.Linked {
		t.Errorf("disk-origin provider Linked = true, want false (suwayomi_id=0)")
	}
	if disk.ChapterCount != 2 {
		t.Errorf("disk-origin provider ChapterCount = %d, want 2", disk.ChapterCount)
	}

	real := byID[realSP.ID.String()]
	if !real.Linked {
		t.Errorf("real provider Linked = false, want true (suwayomi_id=42)")
	}
	if real.ChapterCount != 1 {
		t.Errorf("real provider ChapterCount = %d, want 1", real.ChapterCount)
	}
}
