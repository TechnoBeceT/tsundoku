package library_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/library"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sse"
)

// TestDedupAllProviders_AggregatesAcrossSeries proves the sweep visits every
// series, sums merged/skipped from the per-series DedupProviders, and reports
// how many series it processed. A clean series (disk-origin provider only, no
// matching linked twin) contributes 0/0; a drifted series (disk-origin
// provider + an already-attached linked twin sharing the same provider name +
// scanlator, feed present) contributes its one merge; the sweep never aborts
// on one series.
func TestDedupAllProviders_AggregatesAcrossSeries(t *testing.T) {
	ctx := context.Background()
	storage := t.TempDir()

	// Drifted series: disk-origin provider ("mangadex"/"Alpha") that will get a
	// matching linked twin attached below (the source-identity drift dedup
	// exists to clean up).
	writeKaizokuSeries(t, storage, "Manga", "Drifted Series", "mangadex", "Alpha", 2)
	// Clean series: disk-origin provider only, no matching twin — a no-op pass.
	writeKaizokuSeries(t, storage, "Manga", "Clean Series", "weebcentral", "Beta", 2)

	client := testdb.New(t)

	facts, err := disk.ScanLibrary(storage)
	if err != nil {
		t.Fatalf("disk.ScanLibrary: %v", err)
	}
	if len(facts) != 2 {
		t.Fatalf("facts = %d, want 2", len(facts))
	}
	for _, sf := range facts {
		if _, err := disk.ReconcileOne(ctx, client, sf); err != nil {
			t.Fatalf("disk.ReconcileOne(%s): %v", sf.Title, err)
		}
	}

	drifted := findSeriesByTitle(t, client, ctx, "Drifted Series")

	// Attach the linked twin: same provider name + scanlator as the disk row,
	// with a non-empty ProviderChapter feed — an already-drifted pair.
	attachDriftedTwin(t, client, ctx, drifted.ID)

	seriesSvc := series.NewService(client, storage, 14)
	svc := library.NewService(client, nil, nil, seriesSvc, func() {}, storage, sse.NewHub())

	processed, merged, skipped, err := svc.DedupAllProviders(ctx)
	if err != nil {
		t.Fatalf("DedupAllProviders: %v", err)
	}
	if processed != 2 {
		t.Errorf("processed = %d, want 2", processed)
	}
	if merged != 1 {
		t.Errorf("merged = %d, want 1 (the drifted series' one pair)", merged)
	}
	if skipped != 0 {
		t.Errorf("skipped = %d, want 0", skipped)
	}
}

// findSeriesByTitle returns the series matching title, failing the test if
// none is found.
func findSeriesByTitle(t *testing.T, client *ent.Client, ctx context.Context, title string) *ent.Series {
	t.Helper()
	for _, s := range client.Series.Query().AllX(ctx) {
		if s.Title == title {
			return s
		}
	}
	t.Fatalf("series %q not found", title)
	return nil
}

// attachDriftedTwin creates a linked SeriesProvider ("weeb"/"mangadex"/
// "Alpha") on seriesID carrying a non-empty ProviderChapter feed for keys
// "1"/"2" — the same provider name + scanlator as the disk-origin row written
// by writeKaizokuSeries, i.e. an already-drifted (disk, live) pair.
func attachDriftedTwin(t *testing.T, client *ent.Client, ctx context.Context, seriesID uuid.UUID) {
	t.Helper()
	live := client.SeriesProvider.Create().
		SetSeriesID(seriesID).
		SetProvider("weeb").
		SetProviderName("mangadex").
		SetScanlator("Alpha").
		SetSuwayomiID(99).
		SetImportance(5).
		SaveX(ctx)
	one, two := 1.0, 2.0
	client.ProviderChapter.Create().SetSeriesProviderID(live.ID).SetChapterKey("1").SetNumber(one).SaveX(ctx)
	client.ProviderChapter.Create().SetSeriesProviderID(live.ID).SetChapterKey("2").SetNumber(two).SaveX(ctx)
}
