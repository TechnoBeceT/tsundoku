package series_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/series"
)

// TestProviderDTO_LinkedAndChapterCount proves the Match backend's ProviderDTO
// fields: Linked is false for a disk-origin provider (Provider is a display
// NAME, not numeric — an unlinked/unknown group the owner can Match to a real
// source) and true for a real ingested provider (Provider is a numeric source
// id string — see series.IsLinkedProvider); ChapterCount reports how many of
// the series' chapters this provider currently satisfies
// (Chapter.satisfied_by_provider_id), computed with no extra query. HasFeed
// (the FE↔BE dedup-parity fix) is true only for the provider carrying a
// non-empty ProviderChapter feed — the disk-origin provider here has chapters
// SATISFIED (ChapterCount=2) but NO ProviderChapter feed row, proving HasFeed
// is a distinct signal from ChapterCount (the legacy-drift substate the FE
// dedup gate now checks instead of the satisfied-count proxy).
func TestProviderDTO_LinkedAndChapterCount(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	s := db.Series.Create().SetTitle("Linked Test Series").SetSlug("linked-test-series").SaveX(ctx)

	diskSP := db.SeriesProvider.Create().
		SetSeriesID(s.ID).
		SetProvider("mangadex").
		SetScanlator("Alpha").
		SetImportance(1).
		// Provider is a non-numeric display name — the disk-origin marker
		// under the new identity model (see series.IsLinkedProvider).
		SaveX(ctx)

	realSP := db.SeriesProvider.Create().
		SetSeriesID(s.ID).
		// Provider is a numeric source id string — the live-provider marker.
		SetProvider("42").
		SetImportance(5).
		SaveX(ctx)

	one, two, three := 1.0, 2.0, 3.0
	db.Chapter.Create().SetSeriesID(s.ID).SetChapterKey("1").SetNumber(one).
		SetState("downloaded").SetSatisfiedByProviderID(diskSP.ID).SetSatisfiedImportance(1).SaveX(ctx)
	db.Chapter.Create().SetSeriesID(s.ID).SetChapterKey("2").SetNumber(two).
		SetState("downloaded").SetSatisfiedByProviderID(diskSP.ID).SetSatisfiedImportance(1).SaveX(ctx)
	db.Chapter.Create().SetSeriesID(s.ID).SetChapterKey("3").SetNumber(three).
		SetState("downloaded").SetSatisfiedByProviderID(realSP.ID).SetSatisfiedImportance(5).SaveX(ctx)

	// realSP carries a ProviderChapter feed; diskSP (disk-origin, no live feed)
	// does not — this is the HasFeed signal under test.
	db.ProviderChapter.Create().SetSeriesProviderID(realSP.ID).SetChapterKey("3").SetNumber(three).SaveX(ctx)

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
		t.Errorf("disk-origin provider Linked = true, want false (provider=%q is not numeric)", diskSP.Provider)
	}
	if disk.ChapterCount != 2 {
		t.Errorf("disk-origin provider ChapterCount = %d, want 2", disk.ChapterCount)
	}
	if disk.HasFeed {
		t.Errorf("disk-origin provider HasFeed = true, want false (no ProviderChapter feed)")
	}

	real := byID[realSP.ID.String()]
	if !real.Linked {
		t.Errorf("real provider Linked = false, want true (provider=%q is numeric)", realSP.Provider)
	}
	if real.ChapterCount != 1 {
		t.Errorf("real provider ChapterCount = %d, want 1", real.ChapterCount)
	}
	if !real.HasFeed {
		t.Errorf("real provider HasFeed = false, want true (has a ProviderChapter feed row)")
	}
}

// TestProviderSourceID proves the numeric-id parse the cover-fetch chain
// relies on: a live provider's Provider ("42") resolves to (42, true); a
// disk-origin provider's Provider (a display name) resolves to (0, false);
// surrounding whitespace is trimmed before parsing, mirroring IsLinkedProvider.
func TestProviderSourceID(t *testing.T) {
	live := &ent.SeriesProvider{Provider: "42"}
	if id, ok := series.ProviderSourceID(live); !ok || id != 42 {
		t.Errorf("ProviderSourceID(%q) = (%d, %v), want (42, true)", live.Provider, id, ok)
	}

	padded := &ent.SeriesProvider{Provider: "  7  "}
	if id, ok := series.ProviderSourceID(padded); !ok || id != 7 {
		t.Errorf("ProviderSourceID(%q) = (%d, %v), want (7, true)", padded.Provider, id, ok)
	}

	diskOrigin := &ent.SeriesProvider{Provider: "Asura Scans"}
	if id, ok := series.ProviderSourceID(diskOrigin); ok || id != 0 {
		t.Errorf("ProviderSourceID(%q) = (%d, %v), want (0, false)", diskOrigin.Provider, id, ok)
	}
}
