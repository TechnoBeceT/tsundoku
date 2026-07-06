package disk_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/technobecet/tsundoku/internal/disk"
)

// TestRemoveSeriesDirDeletesFolder proves the series folder (and its files) is gone.
func TestRemoveSeriesDirDeletesFolder(t *testing.T) {
	storage := t.TempDir()
	dir := disk.SeriesDir(storage, "Manhwa", "Solo Leveling")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ch1.cbz"), []byte("x"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	removed, err := disk.RemoveSeriesDir(storage, "Manhwa", "Solo Leveling")
	if err != nil {
		t.Fatalf("RemoveSeriesDir: %v", err)
	}
	if !removed {
		t.Errorf("removed = false, want true (a real folder was deleted)")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("series dir still exists after removal: stat err = %v", err)
	}
}

// TestRemoveSeriesDirMissingIsNoOp proves removing an absent folder returns
// removed=false, err=nil — never-downloaded / zero-provider series legitimately
// have no folder, so callers must not treat this as a failure.
func TestRemoveSeriesDirMissingIsNoOp(t *testing.T) {
	storage := t.TempDir()
	removed, err := disk.RemoveSeriesDir(storage, "Other", "Never Downloaded")
	if err != nil {
		t.Fatalf("RemoveSeriesDir on missing folder = %v, want nil", err)
	}
	if removed {
		t.Errorf("removed = true, want false (folder never existed)")
	}
}

// TestRemoveSeriesDirNotADirectory proves a path collision (a file sitting where
// the series directory should be) surfaces as an error, not a silent no-op.
func TestRemoveSeriesDirNotADirectory(t *testing.T) {
	storage := t.TempDir()
	dir := disk.SeriesDir(storage, "Other", "Blocked")
	if err := os.MkdirAll(filepath.Dir(dir), 0o750); err != nil {
		t.Fatalf("seed parent dir: %v", err)
	}
	if err := os.WriteFile(dir, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed blocking file: %v", err)
	}

	removed, err := disk.RemoveSeriesDir(storage, "Other", "Blocked")
	if err == nil {
		t.Fatal("RemoveSeriesDir over a non-directory = nil, want an error")
	}
	if removed {
		t.Errorf("removed = true, want false on error")
	}
}
