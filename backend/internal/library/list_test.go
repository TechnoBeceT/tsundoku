package library_test

import (
	"context"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/library"
)

// seedImportEntry creates a minimal pending ImportEntry row with an explicit
// scanned_at so pagination ordering (ByScannedAt, ascending) is deterministic
// regardless of wall-clock resolution.
func seedImportEntry(t *testing.T, client *ent.Client, ctx context.Context, path string, scannedAt time.Time) {
	t.Helper()
	if _, err := client.ImportEntry.Create().
		SetPath(path).SetTitle(path).SetCategory("Manga").
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
	svc := library.NewService(client, nil, nil, nil, func() {}, "")

	base := time.Now()
	seedImportEntry(t, client, ctx, "/a", base)
	seedImportEntry(t, client, ctx, "/b", base.Add(time.Second))
	seedImportEntry(t, client, ctx, "/c", base.Add(2*time.Second))

	page1, err := svc.ListImports(ctx, "", 2, 0)
	if err != nil {
		t.Fatalf("list page1: %v", err)
	}
	if len(page1) != 2 || page1[0].Path != "/a" || page1[1].Path != "/b" {
		t.Fatalf("page1 = %+v, want [/a /b]", page1)
	}

	page2, err := svc.ListImports(ctx, "", 2, 2)
	if err != nil {
		t.Fatalf("list page2: %v", err)
	}
	if len(page2) != 1 || page2[0].Path != "/c" {
		t.Fatalf("page2 = %+v, want [/c]", page2)
	}
}
