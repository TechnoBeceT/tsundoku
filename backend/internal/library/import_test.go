package library_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/ent/importentry"
	"github.com/technobecet/tsundoku/internal/library"
	"github.com/technobecet/tsundoku/internal/series"
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
	svc := library.NewService(client, nil, nil, seriesSvc, func() {}, storage)

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
	svc := library.NewService(client, nil, nil, seriesSvc, func() {}, storage)

	if _, err := svc.Import(ctx, "/nonexistent/path", nil); err != library.ErrEntryNotFound {
		t.Fatalf("err = %v, want ErrEntryNotFound", err)
	}
}
