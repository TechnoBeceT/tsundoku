package metadatasvc_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
)

// seedSeries creates a minimal categorized series ("Manga"/title) with no
// providers and no on-disk folder — the common starting point for a
// never-link-a-source assertion (provider count must stay 0 throughout).
func seedSeries(ctx context.Context, t *testing.T, db *ent.Client, title, slug string) uuid.UUID {
	t.Helper()
	catID, err := category.IDByName(ctx, db, "Manga")
	if err != nil {
		t.Fatalf("category.IDByName: %v", err)
	}
	s := db.Series.Create().
		SetTitle(title).
		SetSlug(slug).
		SetCategoryID(catID).
		SaveX(ctx)
	return s.ID
}

// withSeriesDir creates the on-disk library folder for a "Manga"/title series
// under storage — required before disk.WriteMetadata/SaveCover will persist
// anything (neither ever creates the series directory itself).
func withSeriesDir(t *testing.T, storage, title string) {
	t.Helper()
	if err := os.MkdirAll(disk.SeriesDir(storage, "Manga", title), 0o750); err != nil {
		t.Fatalf("mkdir series dir: %v", err)
	}
}

// randomUUID returns a fresh random id that matches no seeded row — used by
// the ErrSeriesNotFound tests.
func randomUUID() uuid.UUID {
	return uuid.New()
}

// seriesProviderCount returns how many SeriesProvider rows belong to id — the
// never-link-a-source proof reads this before and after a
// metadatasvc.Service call and asserts it never changed.
func seriesProviderCount(ctx context.Context, t *testing.T, db *ent.Client, id uuid.UUID) int {
	t.Helper()
	n, err := db.SeriesProvider.Query().Where(entseriesprovider.SeriesIDEQ(id)).Count(ctx)
	if err != nil {
		t.Fatalf("count series providers: %v", err)
	}
	return n
}

// seedSeriesProviderWithCover creates a SeriesProvider row for seriesID with
// the given provider identity, display name, and cover_url — the fixture
// both CoverCandidates' source half (Feature 2) and SetCover's "source"
// branch need.
func seedSeriesProviderWithCover(ctx context.Context, t *testing.T, db *ent.Client, seriesID uuid.UUID, provider, providerName, coverURL string) uuid.UUID {
	t.Helper()
	p := db.SeriesProvider.Create().
		SetSeriesID(seriesID).
		SetProvider(provider).
		SetProviderName(providerName).
		SetCoverURL(coverURL).
		SetImportance(1).
		SaveX(ctx)
	return p.ID
}
