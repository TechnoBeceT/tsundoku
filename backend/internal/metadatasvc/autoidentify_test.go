// Package metadatasvc_test exercises the metadata-engine orchestration
// service against an ephemeral PostgreSQL instance (testdb). Tests require
// Docker.
package metadatasvc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/metadata"
	"github.com/technobecet/tsundoku/internal/metadatasvc"
)

// TestAutoIdentify_MatchPersistsMetadataAndSidecarNeverTouchesProviders is the
// never-link-a-source proof: a series with one confidently-matching fake
// provider gets its merged metadata written to BOTH the Series DB columns
// AND the sidecar Metadata block, while its SeriesProvider count — 0 before,
// 0 after — never changes. Auto-identify must never create, modify, or
// imply a download source.
func TestAutoIdentify_MatchPersistsMetadataAndSidecarNeverTouchesProviders(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	id := seedSeries(ctx, t, db, "Solo Leveling", "solo-leveling")
	withSeriesDir(t, storage, "Solo Leveling")

	before := seriesProviderCount(ctx, t, db, id)
	if before != 0 {
		t.Fatalf("precondition: series already has %d providers, want 0", before)
	}

	coverBody := []byte("fake-cover-bytes")
	coverSrv := coverServer(t, coverBody)

	provider := &fakeProvider{
		key: "anilist", priority: 0,
		matchResult: &metadata.SearchResult{
			Provider: "anilist", RemoteID: "1", URL: "https://anilist.co/manga/1", CoverURL: coverSrv.URL + "/cover.jpg",
		},
		metas: map[string]metadata.SeriesMetadata{
			"1": {
				Title:       "Solo Leveling",
				Description: "A weak hunter grows stronger.",
				Status:      "completed",
				Genres:      []string{"Action", "Fantasy"},
				Tags:        []string{"Reincarnation"},
				Year:        2018,
			},
		},
	}
	registry := metadata.NewRegistry(provider)
	svc := metadatasvc.NewService(db, registry, storage)

	if err := svc.AutoIdentify(ctx, id); err != nil {
		t.Fatalf("AutoIdentify: %v", err)
	}

	row := db.Series.GetX(ctx, id)
	assertAutoIdentifyDBColumns(t, row)
	assertAutoIdentifyCoverCache(t, storage, row, coverBody)
	assertAutoIdentifySidecar(t, storage, row.Title)

	after := seriesProviderCount(ctx, t, db, id)
	if after != before {
		t.Fatalf("SeriesProvider count changed from %d to %d — AutoIdentify must never link a source", before, after)
	}
}

// assertAutoIdentifyDBColumns is a standalone helper (not a closure) so its
// own branches count toward ITS complexity budget, not the calling test's —
// mirrors internal/metadata/registry_test.go's assertIdentifyOrder pattern.
func assertAutoIdentifyDBColumns(t *testing.T, row *ent.Series) {
	t.Helper()
	if row.Description != "A weak hunter grows stronger." {
		t.Errorf("Series.Description = %q, want the matched description", row.Description)
	}
	if row.Status != "completed" {
		t.Errorf("Series.Status = %q, want %q", row.Status, "completed")
	}
	if len(row.Genres) != 2 || row.Genres[0] != "Action" || row.Genres[1] != "Fantasy" {
		t.Errorf("Series.Genres = %v, want [Action Fantasy]", row.Genres)
	}
	if row.Year != 2018 {
		t.Errorf("Series.Year = %d, want 2018", row.Year)
	}
	assertAutoIdentifyMetadataSource(t, row)
}

func assertAutoIdentifyMetadataSource(t *testing.T, row *ent.Series) {
	t.Helper()
	if row.MetadataSource == nil || row.MetadataSource.Ref != "anilist" || row.MetadataSource.RemoteID != "1" {
		t.Fatalf("Series.MetadataSource = %+v, want Kind/Ref/RemoteID from anilist/1", row.MetadataSource)
	}
	if row.MetadataSource.RemoteURL != "https://anilist.co/manga/1" {
		t.Errorf("Series.MetadataSource.RemoteURL = %q, want the match's URL", row.MetadataSource.RemoteURL)
	}
}

func assertAutoIdentifyCoverCache(t *testing.T, storage string, row *ent.Series, wantBody []byte) {
	t.Helper()
	if row.CoverFile == "" {
		t.Fatal("Series.CoverFile is empty, want the cached cover filename")
	}
	if row.CoverSource == nil || row.CoverSource.Ref != "anilist" {
		t.Fatalf("Series.CoverSource = %+v, want Ref=anilist", row.CoverSource)
	}
	data, _, err := disk.ReadCoverFile(storage, "Manga", row.Title, row.CoverFile)
	if err != nil {
		t.Fatalf("ReadCoverFile: %v", err)
	}
	if string(data) != string(wantBody) {
		t.Errorf("cached cover bytes = %q, want %q", data, wantBody)
	}
}

func assertAutoIdentifySidecar(t *testing.T, storage, title string) {
	t.Helper()
	sidecar, err := disk.ReadSidecar(disk.SeriesDir(storage, "Manga", title))
	if err != nil {
		t.Fatalf("ReadSidecar: %v", err)
	}
	if sidecar == nil || sidecar.Metadata == nil {
		t.Fatalf("sidecar has no Metadata block, want the persisted rich fields")
	}
	if sidecar.Metadata.Description != "A weak hunter grows stronger." {
		t.Errorf("sidecar Metadata.Description = %q, want the matched description", sidecar.Metadata.Description)
	}
	assertAutoIdentifySidecarSources(t, sidecar)
}

func assertAutoIdentifySidecarSources(t *testing.T, sidecar *disk.Sidecar) {
	t.Helper()
	if sidecar.Metadata.MetadataSource == nil || sidecar.Metadata.MetadataSource.Ref != "anilist" {
		t.Fatalf("sidecar Metadata.MetadataSource = %+v, want Ref=anilist", sidecar.Metadata.MetadataSource)
	}
	if sidecar.Metadata.CoverSource == nil || sidecar.Metadata.CoverSource.Ref != "anilist" {
		t.Fatalf("sidecar Metadata.CoverSource = %+v, want Ref=anilist (mirrors the cover cache write)", sidecar.Metadata.CoverSource)
	}
	if sidecar.Cover == nil || sidecar.Cover.File == "" {
		t.Fatalf("sidecar Cover block = %+v, want the cached cover's provenance", sidecar.Cover)
	}
}

// TestAutoIdentify_NoMatchLeavesSeriesUntouchedAndReturnsNoError confirms
// "no confident match anywhere" is a designed non-error outcome: the series'
// metadata stays at its zero value and AutoIdentify returns nil, so a
// detached background caller never needs special-case handling.
func TestAutoIdentify_NoMatchLeavesSeriesUntouchedAndReturnsNoError(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	id := seedSeries(ctx, t, db, "Obscure Series", "obscure-series")

	provider := &fakeProvider{key: "anilist"} // matchResult nil ⇒ never matches
	registry := metadata.NewRegistry(provider)
	svc := metadatasvc.NewService(db, registry, storage)

	if err := svc.AutoIdentify(ctx, id); err != nil {
		t.Fatalf("AutoIdentify with no match: want nil error, got %v", err)
	}

	row := db.Series.GetX(ctx, id)
	if row.Description != "" || row.Status != "" || len(row.Genres) != 0 || row.MetadataSource != nil {
		t.Fatalf("series metadata was touched despite no provider match: %+v", row)
	}
}

// TestAutoIdentify_UnknownSeriesReturnsErrSeriesNotFound confirms the
// sentinel error for an id that matches no row.
func TestAutoIdentify_UnknownSeriesReturnsErrSeriesNotFound(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	registry := metadata.NewRegistry(&fakeProvider{key: "anilist"})
	svc := metadatasvc.NewService(db, registry, storage)

	err := svc.AutoIdentify(ctx, randomUUID())
	if !errors.Is(err, metadatasvc.ErrSeriesNotFound) {
		t.Fatalf("AutoIdentify(unknown series) error = %v, want ErrSeriesNotFound", err)
	}
}
