package series_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/series"
)

// TestProviderDTOMangaID proves ProviderDTO.MangaID is ALWAYS 0 regardless of
// whether the provider is linked (live) or disk-origin (P2 Suwayomi-removal:
// the url-addressed engine host has no per-manga numeric id equivalent to the
// retired SuwayomiID column — the field is retained only for FE wire
// compatibility, never read as meaningful; see ProviderDTO's doc comment).
// Linked itself is still a real, live signal — series.IsLinkedProvider, keyed
// off whether SeriesProvider.Provider parses as a numeric source id.
func TestProviderDTOMangaID(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	s := db.Series.Create().SetTitle("MangaID Test Series").SetSlug("mangaid-test-series").SaveX(ctx)

	// Unlinked disk-origin provider (Provider is a non-numeric display name).
	diskSP := db.SeriesProvider.Create().
		SetSeriesID(s.ID).
		SetProvider("disk-provider").
		SetImportance(1).
		SaveX(ctx)

	// Linked provider (Provider is a numeric source id string).
	linkedSP := db.SeriesProvider.Create().
		SetSeriesID(s.ID).
		SetProvider("42").
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

	disk := byID[diskSP.ID.String()]
	if disk.Linked {
		t.Errorf("disk-origin provider Linked = true, want false")
	}
	if disk.MangaID != 0 {
		t.Errorf("unlinked disk provider MangaID = %d, want 0", disk.MangaID)
	}

	linked := byID[linkedSP.ID.String()]
	if !linked.Linked {
		t.Errorf("linked provider Linked = false, want true")
	}
	if linked.MangaID != 0 {
		t.Errorf("linked provider MangaID = %d, want 0 (always 0 — see ProviderDTO doc comment)", linked.MangaID)
	}
}
