package series_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/series"
)

// TestNeedsSource proves the "Needs source" signal (handover 2026-07-13#15,
// QCAT-295 Part C) is COVER-INDEPENDENT and true when the series has ≥1
// DANGLING (disk-origin, unlinked — series.IsLinkedProvider false) provider,
// EVEN WHEN another live source is already attached (the partially-consolidated
// case — the core Part C fix). It covers every case the definition spans and
// asserts ListSeries + GetSeries agree (no N+1: both already eager-load
// providers for display resolution, so NeedsSource costs nothing extra).
func TestNeedsSource(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	// Case 1: only a disk-origin provider (non-numeric Provider) -> needs source.
	diskOnly := db.Series.Create().SetTitle("Disk Only").SetSlug("disk-only").SaveX(ctx)
	db.SeriesProvider.Create().
		SetSeriesID(diskOnly.ID).
		SetProvider("mangadex").
		SetImportance(1).
		// Provider is a non-numeric display name — the disk-origin marker.
		SaveX(ctx)

	// Case 2: only a live provider (numeric Provider) -> does not need a source.
	liveOnly := db.Series.Create().SetTitle("Live Only").SetSlug("live-only").SaveX(ctx)
	db.SeriesProvider.Create().
		SetSeriesID(liveOnly.ID).
		SetProvider("42").
		SetImportance(5).
		SaveX(ctx)

	// Case 3: both a disk-origin AND a live provider -> STILL needs a source.
	// This is the core QCAT-295 Part C fix: a partially-consolidated series (one
	// domain matched, one still dangling) must remain findable so the owner can
	// finish folding the dangling provider into the live one.
	both := db.Series.Create().SetTitle("Both Sources").SetSlug("both-sources").SaveX(ctx)
	db.SeriesProvider.Create().
		SetSeriesID(both.ID).
		SetProvider("mangadex").
		SetImportance(1).
		SaveX(ctx)
	db.SeriesProvider.Create().
		SetSeriesID(both.ID).
		SetProvider("99").
		SetImportance(5).
		SaveX(ctx)

	// Case 4: no providers at all -> needs a source (nothing to fetch from).
	empty := db.Series.Create().SetTitle("No Providers").SetSlug("no-providers").SaveX(ctx)

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
		{"disk-origin + live", both.ID, true},
		{"no providers", empty.ID, true},
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
