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

// assertDedupeIsNoOp seeds a series with a single chapter (key/number/state/
// filename) and a single on-disk CBZ named onDiskFilename, then asserts
// DedupeFiles removes nothing and leaves that file in place. Shared by the two
// single-chapter "nothing should be swept" cases — a clean library with a
// legitimate winner, and a whole-integer superseded chapter that must not be
// treated as a fractional split part.
func assertDedupeIsNoOp(t *testing.T, seriesTitle, seriesSlug, chapterKey string, number float64, state entchapter.State, chapterFilename, onDiskFilename string) {
	t.Helper()
	client := testdb.New(t)
	ctx := context.Background()
	storage := t.TempDir()

	sr := client.Series.Create().
		SetTitle(seriesTitle).SetSlug(seriesSlug).
		SetCategoryID(catID(ctx, client, "Manga")).SaveX(ctx)
	client.Chapter.Create().SetSeriesID(sr.ID).SetChapterKey(chapterKey).SetNumber(number).
		SetState(state).SetFilename(chapterFilename).SaveX(ctx)

	seriesDir := filepath.Join(storage, "Manga", seriesTitle)
	writeCBZ(t, seriesDir, onDiskFilename)

	svc := series.NewService(client, storage, 14)
	removed, err := svc.DedupeFiles(ctx, sr.ID)
	if err != nil {
		t.Fatalf("DedupeFiles: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
	if got := listCBZ(t, seriesDir); len(got) != 1 || got[0] != onDiskFilename {
		t.Errorf("remaining CBZs = %v, want [%s] untouched", got, onDiskFilename)
	}
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
	assertDedupeIsNoOp(t, "Clean Series", "clean-series", "1", 1.0,
		entchapter.StateDownloaded, "[X] Clean Series 1.cbz", "[X] Clean Series 1.cbz")
}

// TestDedupeFiles_RemovesSupersededPartOrphanKeepsWhole proves the second
// DedupeFiles pass reaches a superseded fractional-part chapter's orphaned CBZ
// (filename cleared in the DB by fractional-part suppression, but the file
// itself survived on disk because the best-effort delete at supersede time
// failed): DedupeFiles removes the orphan, keeps the downloaded whole
// chapter's own winning CBZ untouched, and never touches an unrelated chapter
// number that merely shares a leading digit (exact strict-key matching).
func TestDedupeFiles_RemovesSupersededPartOrphanKeepsWhole(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	storage := t.TempDir()

	sr := client.Series.Create().
		SetTitle("Part Sweep Series").SetSlug("part-sweep-series").
		SetCategoryID(catID(ctx, client, "Manga")).SaveX(ctx)

	numWhole, numPart, numEleven := 1.0, 1.1, 11.0
	client.Chapter.Create().SetSeriesID(sr.ID).SetChapterKey("1").SetNumber(numWhole).
		SetState(entchapter.StateDownloaded).SetFilename("[X] Part Sweep Series 001.cbz").SaveX(ctx)
	client.Chapter.Create().SetSeriesID(sr.ID).SetChapterKey("1.1").SetNumber(numPart).
		SetState(entchapter.StateSuperseded).SetFilename("").SaveX(ctx)
	// Unrelated chapter 11 — shares a leading "1" digit with both 1 and 1.1;
	// its winning file must never be touched by an exact-key sweep.
	client.Chapter.Create().SetSeriesID(sr.ID).SetChapterKey("11").SetNumber(numEleven).
		SetState(entchapter.StateDownloaded).SetFilename("[Z] Part Sweep Series 011.cbz").SaveX(ctx)

	seriesDir := filepath.Join(storage, "Manga", "Part Sweep Series")
	writeCBZ(t, seriesDir, "[X] Part Sweep Series 001.cbz")     // whole's winner — must survive
	writeCBZ(t, seriesDir, "[old] Part Sweep Series 001.1.cbz") // superseded part's orphan — must be removed
	writeCBZ(t, seriesDir, "[Z] Part Sweep Series 011.cbz")     // unrelated chapter 11 — must survive

	svc := series.NewService(client, storage, 14)
	removed, err := svc.DedupeFiles(ctx, sr.ID)
	if err != nil {
		t.Fatalf("DedupeFiles: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1 (the superseded part's orphan CBZ)", removed)
	}

	got := listCBZ(t, seriesDir)
	want := []string{"[X] Part Sweep Series 001.cbz", "[Z] Part Sweep Series 011.cbz"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("remaining CBZs = %v, want %v", got, want)
	}
}

// TestDedupeFiles_SupersededPartNoFileIsNoOp proves a superseded fractional
// part with NO file on disk (the best-effort delete at supersede time already
// succeeded, the common case) yields no error and removes nothing extra —
// the second pass is itself best-effort, mirroring the first pass.
func TestDedupeFiles_SupersededPartNoFileIsNoOp(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	storage := t.TempDir()

	sr := client.Series.Create().
		SetTitle("Clean Part Series").SetSlug("clean-part-series").
		SetCategoryID(catID(ctx, client, "Manga")).SaveX(ctx)

	numWhole, numPart := 2.0, 2.1
	client.Chapter.Create().SetSeriesID(sr.ID).SetChapterKey("2").SetNumber(numWhole).
		SetState(entchapter.StateDownloaded).SetFilename("[X] Clean Part Series 002.cbz").SaveX(ctx)
	client.Chapter.Create().SetSeriesID(sr.ID).SetChapterKey("2.1").SetNumber(numPart).
		SetState(entchapter.StateSuperseded).SetFilename("").SaveX(ctx)

	seriesDir := filepath.Join(storage, "Manga", "Clean Part Series")
	writeCBZ(t, seriesDir, "[X] Clean Part Series 002.cbz") // only the whole's winner exists

	svc := series.NewService(client, storage, 14)
	removed, err := svc.DedupeFiles(ctx, sr.ID)
	if err != nil {
		t.Fatalf("DedupeFiles: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0 (no orphan file to sweep)", removed)
	}
	if got := listCBZ(t, seriesDir); len(got) != 1 {
		t.Errorf("remaining CBZs = %v, want the single whole-chapter winner intact", got)
	}
}

// TestDedupeFiles_WholeSupersededChapterSkipped proves a superseded chapter
// whose number is a WHOLE integer (not a fractional split part — defensive:
// this state combination is never produced by fractional-part suppression, but
// DedupeFiles must not assume it) is skipped by the second pass: its file, if
// any, is left alone rather than being swept with an empty keeper.
func TestDedupeFiles_WholeSupersededChapterSkipped(t *testing.T) {
	assertDedupeIsNoOp(t, "Whole Superseded Series", "whole-superseded-series", "3", 3.0,
		entchapter.StateSuperseded, "", "[X] Whole Superseded Series 003.cbz")
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
