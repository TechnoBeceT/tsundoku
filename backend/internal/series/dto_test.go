package series_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/series"
)

// TestProviderDTOMangaID proves that ProviderDTO exposes the per-source Suwayomi
// manga ID so the frontend can fetch coverage breakdowns on demand. A linked
// provider (suwayomi_id != 0) carries its numeric ID; an unlinked disk-origin
// provider (suwayomi_id == 0) carries 0.
func TestProviderDTOMangaID(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	s := db.Series.Create().SetTitle("MangaID Test Series").SetSlug("mangaid-test-series").SaveX(ctx)

	// Unlinked disk-origin provider (suwayomi_id = 0).
	diskSP := db.SeriesProvider.Create().
		SetSeriesID(s.ID).
		SetProvider("disk-provider").
		SetImportance(1).
		// SuwayomiID left at its zero-value default (0).
		SaveX(ctx)

	// Linked provider with suwayomi_id = 42.
	linkedSP := db.SeriesProvider.Create().
		SetSeriesID(s.ID).
		SetProvider("suwayomi-source").
		SetSuwayomiID(42).
		SetImportance(5).
		SaveX(ctx)

	svc := series.NewService(db, t.TempDir(), 14)
	dto, err := svc.GetSeries(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}

	// Build a map of providers by ID for easy lookup.
	byID := make(map[string]series.ProviderDTO, len(dto.Providers))
	for _, p := range dto.Providers {
		byID[p.ID] = p
	}

	// Verify the unlinked disk-origin provider has MangaID = 0.
	disk := byID[diskSP.ID.String()]
	if disk.MangaID != 0 {
		t.Errorf("unlinked disk provider MangaID = %d, want 0", disk.MangaID)
	}

	// Verify the linked provider has MangaID = 42.
	linked := byID[linkedSP.ID.String()]
	if linked.MangaID != 42 {
		t.Errorf("linked provider MangaID = %d, want 42", linked.MangaID)
	}
}
