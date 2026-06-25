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

	if err := disk.RemoveSeriesDir(storage, "Manhwa", "Solo Leveling"); err != nil {
		t.Fatalf("RemoveSeriesDir: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("series dir still exists after removal: stat err = %v", err)
	}
}

// TestRemoveSeriesDirMissingIsNoOp proves removing an absent folder returns nil.
func TestRemoveSeriesDirMissingIsNoOp(t *testing.T) {
	storage := t.TempDir()
	if err := disk.RemoveSeriesDir(storage, "Other", "Never Downloaded"); err != nil {
		t.Fatalf("RemoveSeriesDir on missing folder = %v, want nil", err)
	}
}
