package disk_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/technobecet/tsundoku/internal/disk"
)

func TestRemoveChapterFile(t *testing.T) {
	storage := t.TempDir()
	dir := disk.SeriesDir(storage, "Manhwa", "Test Series")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	name := "[Src] Test Series 001.1.cbz"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	removed, err := disk.RemoveChapterFile(storage, "Manhwa", "Test Series", name)
	if err != nil || !removed {
		t.Fatalf("RemoveChapterFile: removed=%v err=%v, want true,nil", removed, err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(statErr) {
		t.Fatal("file still present after RemoveChapterFile")
	}

	// Absent file → no-op, no error.
	removed2, err2 := disk.RemoveChapterFile(storage, "Manhwa", "Test Series", "nope.cbz")
	if err2 != nil || removed2 {
		t.Fatalf("absent: removed=%v err=%v, want false,nil", removed2, err2)
	}

	// Empty filename → no-op, no error.
	removed3, err3 := disk.RemoveChapterFile(storage, "Manhwa", "Test Series", "")
	if err3 != nil || removed3 {
		t.Fatalf("empty: removed=%v err=%v, want false,nil", removed3, err3)
	}
}
