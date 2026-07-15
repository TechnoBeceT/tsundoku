package enginetopo_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/enginetopo"
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
// DB with the right semantics: extensions total vs cached, and distinct
// NUMERIC providers as the source universe (a disk-origin display-name
// provider is excluded).
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
	// disk-origin display-name provider (suwayomi_id=0, excluded).
	seedProvider(ctx, t, client, "Solo Leveling", "123", 42)
	seedProvider(ctx, t, client, "Omniscient Reader", "456", 43)
	seedProvider(ctx, t, client, "TBATE", "789", 44)
	seedProvider(ctx, t, client, "Nano Machine", "Asura Scans", 0) // disk-origin

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
	}
	if got != want {
		t.Errorf("Status = %+v, want %+v", got, want)
	}
}
