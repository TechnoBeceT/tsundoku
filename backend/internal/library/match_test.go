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
	importsSvc := imports.NewService(fake, ingest, client, storage, 30*time.Second, nil)
	svc := library.NewService(client, ingest, importsSvc, nil, func() {}, storage, sse.NewHub())

	found, err := svc.Scan(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	groups, err := svc.MatchCandidates(ctx, found[0].Path, nil)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if len(groups) == 0 {
		t.Fatal("want at least one candidate group")
	}
}

// TestMatchCandidates_SourcesFilterReachesSearch proves the ?sources filter is
// threaded through MatchCandidates into imports.Service.Search (not dropped on
// the floor / hardcoded to nil): the fake exposes exactly one source ("weeb"),
// so restricting the search to a NON-EXISTENT source id must narrow the fan-out
// to zero sources and return no groups — whereas the same entry with the real
// source id still returns candidates. If the filter were ignored, the
// nonexistent-source case would wrongly still return groups.
func TestMatchCandidates_SourcesFilterReachesSearch(t *testing.T) {
	storage := t.TempDir()
	writeKaizokuSeries(t, storage, "Manga", "My Series", "mangadex", "Alpha", 1)
	client := testdb.New(t)
	ctx := context.Background()

	fake := newFakeClientWithSearch(t, "My Series") // one source "weeb", one candidate
	ingest := suwayomi.NewIngest(fake, client)
	importsSvc := imports.NewService(fake, ingest, client, storage, 30*time.Second, nil)
	svc := library.NewService(client, ingest, importsSvc, nil, func() {}, storage, sse.NewHub())

	found, err := svc.Scan(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	// The real source id passes the filter → candidates come back.
	withReal, err := svc.MatchCandidates(ctx, found[0].Path, []string{"weeb"})
	if err != nil {
		t.Fatalf("match (real source): %v", err)
	}
	if len(withReal) == 0 {
		t.Fatal("filtering to the real source id should still return candidates")
	}

	// A nonexistent source id is silently dropped → the fan-out queries zero
	// sources → zero groups. This only holds if sourceIDs reached Search.
	withNone, err := svc.MatchCandidates(ctx, found[0].Path, []string{"nonexistent"})
	if err != nil {
		t.Fatalf("match (nonexistent source): %v", err)
	}
	if len(withNone) != 0 {
		t.Fatalf("filtering to a nonexistent source should return 0 groups, got %d", len(withNone))
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
	importsSvc := imports.NewService(fake, ingest, client, storage, 30*time.Second, nil)
	svc := library.NewService(client, ingest, importsSvc, nil, func() {}, storage, sse.NewHub())

	if _, err := svc.MatchCandidates(ctx, "/nonexistent/path", nil); err != library.ErrEntryNotFound {
		t.Fatalf("err = %v, want ErrEntryNotFound", err)
	}
}
