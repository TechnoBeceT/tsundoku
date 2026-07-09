package disk_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/technobecet/tsundoku/internal/disk"
)

// writeStubCBZ creates an empty .cbz file inside the series directory.
func writeStubCBZ(t *testing.T, seriesDir, name string) {
	t.Helper()
	if err := os.MkdirAll(seriesDir, 0o750); err != nil {
		t.Fatalf("mkdir %q: %v", seriesDir, err)
	}
	if err := os.WriteFile(filepath.Join(seriesDir, name), []byte("stub"), 0o600); err != nil {
		t.Fatalf("write %q: %v", name, err)
	}
}

// remainingCBZs returns the sorted list of .cbz filenames left in the dir.
func remainingCBZs(t *testing.T, seriesDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(seriesDir)
	if err != nil {
		t.Fatalf("read dir %q: %v", seriesDir, err)
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

// TestRemoveOtherChapterFiles_RemovesDuplicatesKeepsWinner proves the core
// convergence-cleanup behaviour: every OTHER .cbz whose parsed chapter number
// matches the target is removed, the kept file survives, and a different
// chapter's file is untouched.
func TestRemoveOtherChapterFiles_RemovesDuplicatesKeepsWinner(t *testing.T) {
	storage := t.TempDir()
	const category, title = "Manga", "My Series"
	seriesDir := disk.SeriesDir(storage, category, title)

	writeStubCBZ(t, seriesDir, "[A] 010.cbz")
	writeStubCBZ(t, seriesDir, "[B-x] 010.cbz")
	writeStubCBZ(t, seriesDir, "[C] 011.cbz")

	removed, err := disk.RemoveOtherChapterFiles(storage, category, title, "010", "[B-x] 010.cbz")
	if err != nil {
		t.Fatalf("RemoveOtherChapterFiles: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}

	got := remainingCBZs(t, seriesDir)
	want := []string{"[B-x] 010.cbz", "[C] 011.cbz"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("remaining CBZs = %v, want %v", got, want)
	}
}

// TestRemoveOtherChapterFiles_KeepOnlyFileRemovesNothing proves that when the
// keepFilename is the ONLY file for that chapter number, nothing is removed —
// the winning/only file is never deleted.
func TestRemoveOtherChapterFiles_KeepOnlyFileRemovesNothing(t *testing.T) {
	storage := t.TempDir()
	const category, title = "Manga", "Solo Series"
	seriesDir := disk.SeriesDir(storage, category, title)

	writeStubCBZ(t, seriesDir, "[B-x] 010.cbz")
	writeStubCBZ(t, seriesDir, "[C] 011.cbz")

	removed, err := disk.RemoveOtherChapterFiles(storage, category, title, "010", "[B-x] 010.cbz")
	if err != nil {
		t.Fatalf("RemoveOtherChapterFiles: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0 (only file for the chapter is the keeper)", removed)
	}
	if got := remainingCBZs(t, seriesDir); len(got) != 2 {
		t.Errorf("remaining CBZs = %v, want both files intact", got)
	}
}

// TestRemoveOtherChapterFiles_MissingDir is a no-op with no error: a series
// that was never rendered to disk has no folder.
func TestRemoveOtherChapterFiles_MissingDir(t *testing.T) {
	storage := t.TempDir()
	removed, err := disk.RemoveOtherChapterFiles(storage, "Manga", "Ghost Series", "010", "[B] 010.cbz")
	if err != nil {
		t.Fatalf("RemoveOtherChapterFiles on missing dir: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
}

// TestRemoveOtherChapterFiles_NumericEquivalence proves the match is by PARSED
// number, not string: a "10" target matches a "010"-padded filename, and a
// decimal "12.5" matches its padded form while a non-matching number is kept.
func TestRemoveOtherChapterFiles_NumericEquivalence(t *testing.T) {
	storage := t.TempDir()
	const category, title = "Manga", "Numeric Series"
	seriesDir := disk.SeriesDir(storage, category, title)

	writeStubCBZ(t, seriesDir, "[A] 010.cbz")   // number 10
	writeStubCBZ(t, seriesDir, "[B] 012.5.cbz") // number 12.5
	writeStubCBZ(t, seriesDir, "[C] 013.cbz")   // number 13 (keeper for a different call)

	// Target "10" (unpadded) must match "[A] 010.cbz" (padded).
	removed, err := disk.RemoveOtherChapterFiles(storage, category, title, "10", "[Z] 010.cbz")
	if err != nil {
		t.Fatalf("RemoveOtherChapterFiles: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1 (010 matches unpadded target 10)", removed)
	}
	got := remainingCBZs(t, seriesDir)
	if len(got) != 2 || got[0] != "[B] 012.5.cbz" || got[1] != "[C] 013.cbz" {
		t.Errorf("remaining = %v, want the 12.5 and 13 files", got)
	}
}
