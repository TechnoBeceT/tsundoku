package library_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/library"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sse"
)

// TestSkip_MarksEntrySkipped proves the happy path: skipping a staged
// pending entry flips its status to "skipped" and it then shows up under
// the "skipped" filter of ListImports.
func TestSkip_MarksEntrySkipped(t *testing.T) {
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

	if err := svc.Skip(ctx, found[0].Path); err != nil {
		t.Fatalf("skip: %v", err)
	}

	skipped, err := svc.ListImports(ctx, "skipped", 50, 0)
	if err != nil {
		t.Fatalf("list skipped: %v", err)
	}
	if len(skipped) != 1 || skipped[0].Path != found[0].Path {
		t.Fatalf("skipped list = %+v, want [%s]", skipped, found[0].Path)
	}
}

// TestSkip_UnknownPathReturnsErrEntryNotFound proves the sentinel-error path
// when the caller passes a path that was never staged by Scan.
func TestSkip_UnknownPathReturnsErrEntryNotFound(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()
	seriesSvc := series.NewService(client, storage, 14)
	svc := library.NewService(client, nil, nil, seriesSvc, func() {}, storage, sse.NewHub())

	if err := svc.Skip(ctx, "/nope"); err != library.ErrEntryNotFound {
		t.Fatalf("err = %v, want ErrEntryNotFound", err)
	}
}
