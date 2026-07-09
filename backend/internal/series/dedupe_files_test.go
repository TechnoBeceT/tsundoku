package series_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/series"
)

// writeCBZ creates a stub .cbz under the series directory.
func writeCBZ(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir %q: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("stub"), 0o600); err != nil {
		t.Fatalf("write %q: %v", name, err)
	}
}

// listCBZ returns the sorted .cbz filenames left in dir.
func listCBZ(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %q: %v", dir, err)
	}
	var got []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".cbz" {
			got = append(got, e.Name())
		}
	}
	sort.Strings(got)
	return got
}

// TestDedupeFiles_RemovesOrphansKeepsWinners proves the owner sweep removes every
// duplicate CBZ that does not match a chapter's winning filename, keeps the
// winners, and returns the count removed.
func TestDedupeFiles_RemovesOrphansKeepsWinners(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	storage := t.TempDir()

	sr := client.Series.Create().
		SetTitle("Sweep Series").SetSlug("sweep-series").
		SetCategoryID(catID(ctx, client, "Manga")).SaveX(ctx)

	num10, num11 := 10.0, 11.0
	client.Chapter.Create().SetSeriesID(sr.ID).SetChapterKey("10").SetNumber(num10).
		SetState(entchapter.StateDownloaded).SetFilename("[X] Sweep Series 10.cbz").SaveX(ctx)
	client.Chapter.Create().SetSeriesID(sr.ID).SetChapterKey("11").SetNumber(num11).
		SetState(entchapter.StateDownloaded).SetFilename("[Y] Sweep Series 11.cbz").SaveX(ctx)

	seriesDir := filepath.Join(storage, "Manga", "Sweep Series")
	writeCBZ(t, seriesDir, "[X] Sweep Series 10.cbz")    // winner for ch10
	writeCBZ(t, seriesDir, "[old] Sweep Series 10.cbz")  // orphan duplicate of ch10
	writeCBZ(t, seriesDir, "[gone] Sweep Series 10.cbz") // 2nd orphan duplicate of ch10
	writeCBZ(t, seriesDir, "[Y] Sweep Series 11.cbz")    // winner for ch11

	svc := series.NewService(client, storage, 14)
	removed, err := svc.DedupeFiles(ctx, sr.ID)
	if err != nil {
		t.Fatalf("DedupeFiles: %v", err)
	}
	if removed != 2 {
		t.Errorf("removed = %d, want 2 (two orphan duplicates of ch10)", removed)
	}

	got := listCBZ(t, seriesDir)
	want := []string{"[X] Sweep Series 10.cbz", "[Y] Sweep Series 11.cbz"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("remaining CBZs = %v, want %v", got, want)
	}
}

// TestDedupeFiles_NoOrphansReturnsZero proves a clean library removes nothing.
func TestDedupeFiles_NoOrphansReturnsZero(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	storage := t.TempDir()

	sr := client.Series.Create().
		SetTitle("Clean Series").SetSlug("clean-series").
		SetCategoryID(catID(ctx, client, "Manga")).SaveX(ctx)
	num := 1.0
	client.Chapter.Create().SetSeriesID(sr.ID).SetChapterKey("1").SetNumber(num).
		SetState(entchapter.StateDownloaded).SetFilename("[X] Clean Series 1.cbz").SaveX(ctx)

	seriesDir := filepath.Join(storage, "Manga", "Clean Series")
	writeCBZ(t, seriesDir, "[X] Clean Series 1.cbz")

	svc := series.NewService(client, storage, 14)
	removed, err := svc.DedupeFiles(ctx, sr.ID)
	if err != nil {
		t.Fatalf("DedupeFiles: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
	if got := listCBZ(t, seriesDir); len(got) != 1 {
		t.Errorf("remaining CBZs = %v, want the single winner intact", got)
	}
}

// TestDedupeFiles_UnknownIDReturnsNotFound proves an unknown series id maps to
// ErrSeriesNotFound.
func TestDedupeFiles_UnknownIDReturnsNotFound(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()

	svc := series.NewService(client, t.TempDir(), 14)
	if _, err := svc.DedupeFiles(ctx, uuid.New()); err != series.ErrSeriesNotFound {
		t.Fatalf("DedupeFiles(unknown): want ErrSeriesNotFound, got %v", err)
	}
}
