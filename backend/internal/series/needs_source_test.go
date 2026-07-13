package series_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/series"
)

// TestNeedsSource proves the "Needs source" signal (handover 2026-07-13#15) is
// COVER-INDEPENDENT and computed purely from whether a series has a live
// download source (SuwayomiID != 0), across the three cases the definition
// covers, and that ListSeries + GetSeries agree (no N+1: both already
// eager-load providers for display resolution, so NeedsSource costs nothing
// extra).
func TestNeedsSource(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	// Case 1: only a disk-origin provider (SuwayomiID == 0) -> needs source.
	diskOnly := db.Series.Create().SetTitle("Disk Only").SetSlug("disk-only").SaveX(ctx)
	db.SeriesProvider.Create().
		SetSeriesID(diskOnly.ID).
		SetProvider("mangadex").
		SetImportance(1).
		// SuwayomiID left at its zero-value default (0) — the disk-origin marker.
		SaveX(ctx)

	// Case 2: only a live provider (SuwayomiID != 0) -> does not need a source.
	liveOnly := db.Series.Create().SetTitle("Live Only").SetSlug("live-only").SaveX(ctx)
	db.SeriesProvider.Create().
		SetSeriesID(liveOnly.ID).
		SetProvider("weeb").
		SetSuwayomiID(42).
		SetImportance(5).
		SaveX(ctx)

	// Case 3: both a disk-origin AND a live provider -> does not need a source
	// (one live source is enough, regardless of how many disk-origin rows exist).
	both := db.Series.Create().SetTitle("Both Sources").SetSlug("both-sources").SaveX(ctx)
	db.SeriesProvider.Create().
		SetSeriesID(both.ID).
		SetProvider("mangadex").
		SetImportance(1).
		SaveX(ctx)
	db.SeriesProvider.Create().
		SetSeriesID(both.ID).
		SetProvider("weeb").
		SetSuwayomiID(99).
		SetImportance(5).
		SaveX(ctx)

	svc := series.NewService(db, t.TempDir(), 14)

	rows, err := svc.ListSeries(ctx, series.ListFilter{})
	if err != nil {
		t.Fatalf("ListSeries: %v", err)
	}
	byID := make(map[string]series.SeriesSummaryDTO, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}

	cases := []struct {
		name string
		id   uuid.UUID
		want bool
	}{
		{"disk-origin only", diskOnly.ID, true},
		{"live only", liveOnly.ID, false},
		{"disk-origin + live", both.ID, false},
	}
	for _, tc := range cases {
		got, ok := byID[tc.id.String()]
		if !ok {
			t.Fatalf("%s: series %s missing from ListSeries", tc.name, tc.id)
		}
		if got.NeedsSource != tc.want {
			t.Errorf("ListSeries %s: NeedsSource = %v, want %v", tc.name, got.NeedsSource, tc.want)
		}
	}

	// GetSeries must agree with ListSeries for every case (same underlying rule).
	for _, tc := range cases {
		detail, err := svc.GetSeries(ctx, tc.id)
		if err != nil {
			t.Fatalf("%s: GetSeries: %v", tc.name, err)
		}
		if detail.NeedsSource != tc.want {
			t.Errorf("GetSeries %s: NeedsSource = %v, want %v", tc.name, detail.NeedsSource, tc.want)
		}
	}
}
