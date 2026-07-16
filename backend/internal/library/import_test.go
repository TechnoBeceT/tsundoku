package library_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/ent/importentry"
	"github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/ingest"
	"github.com/technobecet/tsundoku/internal/library"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sse"
)

// TestImport_RegistersDiskChaptersDownloaded proves the happy-path disk-only
// import: a staged 3-chapter Kaizoku series is registered fully downloaded
// (no re-download), the staged entry flips to "imported", and re-importing
// the same path is idempotent (no duplicate Series row).
func TestImport_RegistersDiskChaptersDownloaded(t *testing.T) {
	storage := t.TempDir()
	writeKaizokuSeries(t, storage, "Manga", "My Series", "mangadex", "Alpha", 3)
	client := testdb.New(t)
	ctx := context.Background()
	seriesSvc := series.NewService(client, storage, 14)
	svc := library.NewService(client, nil, nil, seriesSvc, func() {}, storage, sse.NewHub())

	found, err := svc.Scan(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	dto, err := svc.Import(ctx, found[0].Path, nil) // disk-only import
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	assertDownloadedCounts(t, dto, 3, 0)
	assertEntryStatus(t, client, ctx, found[0].Path, "imported")

	// re-import is idempotent (no duplicate series)
	if _, err := svc.Import(ctx, found[0].Path, nil); err != nil {
		t.Fatal(err)
	}
	assertSeriesCount(t, client, ctx, 1)
}

// assertDownloadedCounts checks the ChapterCounts rollup on a SeriesDetailDTO.
func assertDownloadedCounts(t *testing.T, dto series.SeriesDetailDTO, wantDownloaded, wantWanted int) {
	t.Helper()
	if dto.ChapterCounts.Downloaded != wantDownloaded || dto.ChapterCounts.Wanted != wantWanted {
		t.Fatalf("counts d=%d w=%d, want %d/%d", dto.ChapterCounts.Downloaded, dto.ChapterCounts.Wanted, wantDownloaded, wantWanted)
	}
}

// assertEntryStatus checks the persisted ImportEntry.status for path.
func assertEntryStatus(t *testing.T, client *ent.Client, ctx context.Context, path, want string) {
	t.Helper()
	e := client.ImportEntry.Query().Where(importentry.Path(path)).OnlyX(ctx)
	if e.Status != want {
		t.Fatalf("status=%q, want %q", e.Status, want)
	}
}

// assertSeriesCount checks the total number of persisted Series rows.
func assertSeriesCount(t *testing.T, client *ent.Client, ctx context.Context, want int) {
	t.Helper()
	if n := client.Series.Query().CountX(ctx); n != want {
		t.Fatalf("series count = %d, want %d", n, want)
	}
}

// TestImport_UnknownPathReturnsErrEntryNotFound proves the sentinel-error
// path when the caller passes a path that was never staged by Scan.
func TestImport_UnknownPathReturnsErrEntryNotFound(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()
	seriesSvc := series.NewService(client, storage, 14)
	svc := library.NewService(client, nil, nil, seriesSvc, func() {}, storage, sse.NewHub())

	if _, err := svc.Import(ctx, "/nonexistent/path", nil); err != library.ErrEntryNotFound {
		t.Fatalf("err = %v, want ErrEntryNotFound", err)
	}
}

// TestImportWithMatchList proves Import's matches-LIST attach (Slice P): a
// staged disk-only series, once imported, can have a list of Suwayomi
// sources attached in the SAME call via AddProviders — each landing at a
// NON-NEGATIVE importance below the disk provider's. With the disk provider at
// importance 1 there is no room below, so belowExistingImportances renumbers the
// disk provider up (1 → 20) and the attached source lands at 10 (below it) with
// its own scanlator — while a nil matches list stays import-only exactly as
// before (only the disk provider present, no attach).
func TestImportWithMatchList(t *testing.T) {
	storage := t.TempDir()
	writeKaizokuSeries(t, storage, "Manga", "My Series", "mangadex", "Alpha", 2)
	client := testdb.New(t)
	ctx := context.Background()

	fake := newFakeClientWithFeed(t) // returns 2 chapters keyed "1","2" for any url
	ingestSvc := ingest.NewIngest(fake, client)
	seriesSvc := series.NewService(client, storage, 14)
	svc := library.NewService(client, ingestSvc, nil, seriesSvc, func() {}, storage, sse.NewHub())

	found, err := svc.Scan(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	matches := []library.ProviderRef{{Source: "1", URL: "/manga/7", Scanlator: "Reset"}}
	dto, err := svc.Import(ctx, found[0].Path, matches)
	if err != nil {
		t.Fatalf("import with matches: %v", err)
	}
	if len(dto.Providers) != 2 {
		t.Fatalf("providers = %d, want 2 (disk + weeb/Reset)", len(dto.Providers))
	}

	ser := client.Series.Query().OnlyX(ctx)
	weeb, err := client.SeriesProvider.Query().
		Where(seriesprovider.SeriesID(ser.ID), seriesprovider.Provider("1"), seriesprovider.Scanlator("Reset")).
		Only(ctx)
	if err != nil {
		t.Fatalf("query weeb/Reset: %v", err)
	}
	if weeb.Importance != 10 {
		t.Fatalf("weeb/Reset importance = %d, want 10 (non-negative, below the renumbered disk provider)", weeb.Importance)
	}

	// The disk provider was renumbered up so the attach could stay non-negative
	// below it — it must still outrank the newly attached source.
	disk := client.SeriesProvider.Query().
		Where(seriesprovider.SeriesID(ser.ID), seriesprovider.Provider("mangadex")).
		OnlyX(ctx)
	if disk.Importance != 20 {
		t.Fatalf("disk provider importance = %d, want 20 (renumbered above the attached source)", disk.Importance)
	}

	// A nil matches list stays import-only: only the disk provider present.
	storage2 := t.TempDir()
	writeKaizokuSeries(t, storage2, "Manga", "Other Series", "mangadex", "Beta", 2)
	client2 := testdb.New(t)
	seriesSvc2 := series.NewService(client2, storage2, 14)
	svc2 := library.NewService(client2, nil, nil, seriesSvc2, func() {}, storage2, sse.NewHub())

	found2, err := svc2.Scan(ctx)
	if err != nil {
		t.Fatalf("scan (import-only): %v", err)
	}
	dto2, err := svc2.Import(ctx, found2[0].Path, nil)
	if err != nil {
		t.Fatalf("import-only: %v", err)
	}
	if len(dto2.Providers) != 1 {
		t.Fatalf("providers = %d, want 1 (disk only, no attach)", len(dto2.Providers))
	}
}
