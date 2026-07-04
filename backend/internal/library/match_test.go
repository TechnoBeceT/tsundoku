package library_test

import (
	"context"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/imports"
	"github.com/technobecet/tsundoku/internal/library"
	"github.com/technobecet/tsundoku/internal/sse"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// TestMatchCandidates_ReturnsSearchGroups proves the happy path: a staged
// entry's title is used to search Suwayomi sources, and the grouped
// candidates come back to the caller (so the owner can pick one for Import).
func TestMatchCandidates_ReturnsSearchGroups(t *testing.T) {
	storage := t.TempDir()
	writeKaizokuSeries(t, storage, "Manga", "My Series", "mangadex", "Alpha", 1)
	client := testdb.New(t)
	ctx := context.Background()

	fake := newFakeClientWithSearch(t, "My Series") // Search returns 1 manga titled "My Series"
	ingest := suwayomi.NewIngest(fake, client)
	importsSvc := imports.NewService(fake, ingest, client, storage, 30*time.Second)
	svc := library.NewService(client, ingest, importsSvc, nil, func() {}, storage, sse.NewHub())

	found, err := svc.Scan(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	groups, err := svc.MatchCandidates(ctx, found[0].Path)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if len(groups) == 0 {
		t.Fatal("want at least one candidate group")
	}
}

// TestMatchCandidates_UnknownPathReturnsErrEntryNotFound proves the sentinel-
// error path when the caller passes a path that was never staged by Scan.
func TestMatchCandidates_UnknownPathReturnsErrEntryNotFound(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()

	fake := newFakeClientWithSearch(t, "My Series")
	ingest := suwayomi.NewIngest(fake, client)
	importsSvc := imports.NewService(fake, ingest, client, storage, 30*time.Second)
	svc := library.NewService(client, ingest, importsSvc, nil, func() {}, storage, sse.NewHub())

	if _, err := svc.MatchCandidates(ctx, "/nonexistent/path"); err != library.ErrEntryNotFound {
		t.Fatalf("err = %v, want ErrEntryNotFound", err)
	}
}
