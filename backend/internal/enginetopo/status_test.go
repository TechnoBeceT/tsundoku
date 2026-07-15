package enginetopo_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/enginetopo"
	"github.com/technobecet/tsundoku/internal/ent"
)

// TestTopologyStatus_EmptyDBIsZero proves a fresh install (no harvested topology,
// no library) is a valid zero Status, never an error.
func TestTopologyStatus_EmptyDBIsZero(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	got, err := enginetopo.TopologyStatus(ctx, client)
	if err != nil {
		t.Fatalf("TopologyStatus: %v", err)
	}
	if (got != enginetopo.Status{}) {
		t.Errorf("empty DB Status = %+v, want zero", got)
	}
}

// TestTopologyStatus_CountsFromDB proves every count is read straight from the
// DB with the right semantics: extensions total vs cached, distinct NUMERIC
// providers as the source universe (a disk-origin display-name provider is
// excluded), distinct sources-with-prefs, and url filled vs. still-fillable
// (a disk-origin row with suwayomi_id=0 and no url is NOT counted as remaining,
// mirroring BackfillProviderURLs's candidate set).
func TestTopologyStatus_CountsFromDB(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	// Repos: 2.
	client.HarvestedRepo.Create().SetURL("https://repo.one/index.min.json").SaveX(ctx)
	client.HarvestedRepo.Create().SetURL("https://repo.two/index.min.json").SaveX(ctx)

	// Extensions: 3 total, 2 cached.
	client.HarvestedExtension.Create().SetPkgName("ext.a").SetApkCached(true).SaveX(ctx)
	client.HarvestedExtension.Create().SetPkgName("ext.b").SetApkCached(true).SaveX(ctx)
	client.HarvestedExtension.Create().SetPkgName("ext.c").SetApkCached(false).SaveX(ctx)

	// SourcePreferences: source 123 has 2 keys, source 456 has 1 → 2 distinct
	// sources with prefs.
	client.SourcePreference.Create().SetSourceID(123).SetKey("lang").SetValue("en").SaveX(ctx)
	client.SourcePreference.Create().SetSourceID(123).SetKey("quality").SetValue("high").SaveX(ctx)
	client.SourcePreference.Create().SetSourceID(456).SetKey("lang").SetValue("ko").SaveX(ctx)

	// SeriesProviders: three numeric (live) sources {123,456,789} + one
	// disk-origin display-name provider (suwayomi_id=0). Two live rows are
	// url-filled, one live row (456) is empty-but-fillable, the disk row is
	// empty-and-unfillable.
	setURL(ctx, t, client, seedProvider(ctx, t, client, "Solo Leveling", "123", 42), "https://a.test/manga")
	seedProvider(ctx, t, client, "Omniscient Reader", "456", 43) // url="" fillable
	setURL(ctx, t, client, seedProvider(ctx, t, client, "TBATE", "789", 44), "https://c.test/manga")
	seedProvider(ctx, t, client, "Nano Machine", "Asura Scans", 0) // disk-origin, url=""

	got, err := enginetopo.TopologyStatus(ctx, client)
	if err != nil {
		t.Fatalf("TopologyStatus: %v", err)
	}

	want := enginetopo.Status{
		Repos:                2,
		ExtensionsTotal:      3,
		ExtensionsCached:     2,
		SourcesTotal:         3, // 123, 456, 789 — "Asura Scans" excluded
		SourcesPrefsCaptured: 2, // 123, 456
		URLsFilled:           2, // 123, 789
		URLsRemaining:        1, // 456 (empty + suwayomi_id!=0); disk row excluded
	}
	if got != want {
		t.Errorf("Status = %+v, want %+v", got, want)
	}
}

// setURL fills a SeriesProvider's url column and returns the row, so a fixture
// can create a provider (seedProvider leaves url="") and then mark it resolved.
func setURL(ctx context.Context, t *testing.T, client *ent.Client, sp *ent.SeriesProvider, url string) *ent.SeriesProvider {
	t.Helper()
	return client.SeriesProvider.UpdateOne(sp).SetURL(url).SaveX(ctx)
}
