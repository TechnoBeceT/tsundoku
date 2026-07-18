package library_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/library"
	"github.com/technobecet/tsundoku/internal/sse"
)

// seedImportEntry creates a minimal pending ImportEntry row with an explicit
// scanned_at so pagination ordering (ByScannedAt, ascending) is deterministic
// regardless of wall-clock resolution. The title equals the path unless
// seedImportEntryTitled is used — this keeps the pagination test unchanged.
func seedImportEntry(t *testing.T, client *ent.Client, ctx context.Context, path string, scannedAt time.Time) {
	t.Helper()
	seedImportEntryTitled(t, client, ctx, path, path, scannedAt)
}

// seedImportEntryTitled is seedImportEntry with an explicit title, used by the
// search test to seed rows whose title differs from their path.
func seedImportEntryTitled(t *testing.T, client *ent.Client, ctx context.Context, path, title string, scannedAt time.Time) {
	t.Helper()
	if _, err := client.ImportEntry.Create().
		SetPath(path).SetTitle(title).SetCategory("Manga").
		SetChapterCount(1).SetStatus("pending").
		SetScannedAt(scannedAt).
		Save(ctx); err != nil {
		t.Fatalf("seed import entry %s: %v", path, err)
	}
}

// TestListImports_Paginated proves limit/offset page through the staged
// entries in scanned_at order: page 1 (limit 2, offset 0) returns the two
// oldest rows; page 2 (limit 2, offset 2) returns the remaining one.
func TestListImports_Paginated(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	svc := library.NewService(client, nil, nil, nil, func() {}, "", sse.NewHub())

	base := time.Now()
	seedImportEntry(t, client, ctx, "/a", base)
	seedImportEntry(t, client, ctx, "/b", base.Add(time.Second))
	seedImportEntry(t, client, ctx, "/c", base.Add(2*time.Second))

	page1, err := svc.ListImports(ctx, "", "", 2, 0)
	if err != nil {
		t.Fatalf("list page1: %v", err)
	}
	if len(page1) != 2 || page1[0].Path != "/a" || page1[1].Path != "/b" {
		t.Fatalf("page1 = %+v, want [/a /b]", page1)
	}

	page2, err := svc.ListImports(ctx, "", "", 2, 2)
	if err != nil {
		t.Fatalf("list page2: %v", err)
	}
	if len(page2) != 1 || page2[0].Path != "/c" {
		t.Fatalf("page2 = %+v, want [/c]", page2)
	}
}

// searchTitles lists with the given ?q + limit and returns the resulting
// titles in order — the shared assertion helper for TestListImports_Search
// (keeps each case a single comparison so the test stays under cyclop 10).
func searchTitles(t *testing.T, svc *library.Service, ctx context.Context, q string, limit int) []string {
	t.Helper()
	rows, err := svc.ListImports(ctx, "", q, limit, 0)
	if err != nil {
		t.Fatalf("list q=%q limit=%d: %v", q, limit, err)
	}
	titles := make([]string, len(rows))
	for i, r := range rows {
		titles[i] = r.Title
	}
	return titles
}

// TestListImports_Search proves the ?q filter is a case-insensitive title
// substring (TitleContainsFold / ILIKE %q%), applied by the DB across the
// whole staged set, composing with pagination.
func TestListImports_Search(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	svc := library.NewService(client, nil, nil, nil, func() {}, "", sse.NewHub())

	base := time.Now()
	seedImportEntryTitled(t, client, ctx, "/one", "Solo Leveling", base)
	seedImportEntryTitled(t, client, ctx, "/two", "The Beginning After the End", base.Add(time.Second))
	seedImportEntryTitled(t, client, ctx, "/three", "Leveling Up Alone", base.Add(2*time.Second))

	// Case-insensitive substring "level" matches "Solo Leveling" and
	// "Leveling Up Alone", not "The Beginning After the End".
	if got := searchTitles(t, svc, ctx, "level", 50); !reflect.DeepEqual(got, []string{"Solo Leveling", "Leveling Up Alone"}) {
		t.Fatalf("search 'level' = %v, want [Solo Leveling, Leveling Up Alone]", got)
	}
	// Search is still paginated: limit 1 returns only the first (oldest) match.
	if got := searchTitles(t, svc, ctx, "level", 1); !reflect.DeepEqual(got, []string{"Solo Leveling"}) {
		t.Fatalf("search 'level' limit 1 = %v, want [Solo Leveling]", got)
	}
	// Empty q is unchanged (no filter): all three rows.
	if got := searchTitles(t, svc, ctx, "", 50); len(got) != 3 {
		t.Fatalf("empty q = %v, want 3 rows", got)
	}
	// A non-matching query returns nothing.
	if got := searchTitles(t, svc, ctx, "nonexistent-xyz", 50); len(got) != 0 {
		t.Fatalf("no-match q = %v, want 0 rows", got)
	}
}
