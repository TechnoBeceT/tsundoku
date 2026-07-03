package library_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/library"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sse"
)

// TestImportBatch_PartialSuccess proves the core batch semantic: a batch of
// paths NEVER aborts on one bad entry. Two staged series import cleanly
// (disk-only, mirrors TestImport_RegistersDiskChaptersDownloaded); a third,
// never-staged path fails with ErrEntryNotFound's message but does not stop
// the other two from being counted as imported.
func TestImportBatch_PartialSuccess(t *testing.T) {
	storage := t.TempDir()
	writeKaizokuSeries(t, storage, "Manga", "Series One", "mangadex", "Alpha", 2)
	writeKaizokuSeries(t, storage, "Manga", "Series Two", "mangadex", "Alpha", 1)
	client := testdb.New(t)
	ctx := context.Background()
	seriesSvc := series.NewService(client, storage, 14)
	svc := library.NewService(client, nil, nil, seriesSvc, func() {}, storage, sse.NewHub())

	found, err := svc.Scan(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("found %d, want 2", len(found))
	}

	paths := []string{found[0].Path, found[1].Path, "/nonexistent/bogus"}
	result, err := svc.ImportBatch(ctx, paths)
	if err != nil {
		t.Fatalf("ImportBatch returned a top-level error: %v (partial failures must not abort the batch)", err)
	}

	if result.Imported != 2 {
		t.Fatalf("Imported = %d, want 2", result.Imported)
	}
	if len(result.Failed) != 1 {
		t.Fatalf("Failed = %+v, want exactly 1 entry", result.Failed)
	}
	if result.Failed[0].Path != "/nonexistent/bogus" {
		t.Fatalf("Failed[0].Path = %q, want /nonexistent/bogus", result.Failed[0].Path)
	}
	if result.Failed[0].Message != library.ErrEntryNotFound.Error() {
		t.Fatalf("Failed[0].Message = %q, want %q", result.Failed[0].Message, library.ErrEntryNotFound.Error())
	}

	assertEntryStatus(t, client, ctx, found[0].Path, "imported")
	assertEntryStatus(t, client, ctx, found[1].Path, "imported")
}

// TestImportBatch_AllSucceed proves the plain happy path: every path in the
// batch imports cleanly, Failed is empty (not nil-vs-empty ambiguous — see
// handler DTO), and Imported counts every entry.
func TestImportBatch_AllSucceed(t *testing.T) {
	storage := t.TempDir()
	writeKaizokuSeries(t, storage, "Manga", "Only Series", "mangadex", "Alpha", 1)
	client := testdb.New(t)
	ctx := context.Background()
	seriesSvc := series.NewService(client, storage, 14)
	svc := library.NewService(client, nil, nil, seriesSvc, func() {}, storage, sse.NewHub())

	found, err := svc.Scan(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	result, err := svc.ImportBatch(ctx, []string{found[0].Path})
	if err != nil {
		t.Fatalf("ImportBatch: %v", err)
	}
	if result.Imported != 1 {
		t.Fatalf("Imported = %d, want 1", result.Imported)
	}
	if len(result.Failed) != 0 {
		t.Fatalf("Failed = %+v, want empty", result.Failed)
	}
}
