package disk_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/technobecet/tsundoku/internal/disk"
)

// seedSeriesDir creates a series directory at <storage>/<category>/<title> with
// one CBZ file and a tsundoku.json sidecar (written via the real WriteSidecar so
// the on-disk shape is genuine). It returns the CBZ bytes for later integrity checks.
func seedSeriesDir(t *testing.T, storage, category, title string) []byte {
	t.Helper()

	dir := disk.SeriesDir(storage, category, title)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("seedSeriesDir MkdirAll: %v", err)
	}

	cbzBytes := []byte("fake-cbz-archive-contents")
	cbzPath := filepath.Join(dir, "["+category+"][en] "+title+" 001.cbz")
	if err := os.WriteFile(cbzPath, cbzBytes, 0o600); err != nil {
		t.Fatalf("seedSeriesDir WriteFile: %v", err)
	}

	sidecar := disk.Sidecar{
		Title:    title,
		Category: category,
		Chapters: []disk.ChapterProvenance{{
			ChapterKey: "1",
			Number:     ptr(1),
			Provider:   "mangadex",
			Importance: 1,
			Filename:   filepath.Base(cbzPath),
			PageCount:  10,
		}},
	}
	if err := disk.WriteSidecar(dir, sidecar); err != nil {
		t.Fatalf("seedSeriesDir WriteSidecar: %v", err)
	}

	return cbzBytes
}

// TestMoveSeriesCategorySameCategoryNoOp verifies that moving a series to its
// current category is a true no-op: it returns nil and leaves the dir untouched.
func TestMoveSeriesCategorySameCategoryNoOp(t *testing.T) {
	t.Parallel()

	storage := t.TempDir()
	cbzBytes := seedSeriesDir(t, storage, disk.CategoryOther, "Solo Leveling")

	if err := disk.MoveSeriesCategory(storage, disk.CategoryOther, disk.CategoryOther, "Solo Leveling"); err != nil {
		t.Fatalf("MoveSeriesCategory same-category: want nil, got %v", err)
	}

	// The original dir + CBZ are untouched.
	dir := disk.SeriesDir(storage, disk.CategoryOther, "Solo Leveling")
	got, err := os.ReadFile(filepath.Join(dir, "[Other][en] Solo Leveling 001.cbz")) //nolint:gosec // test-only, path is from t.TempDir()
	if err != nil {
		t.Fatalf("read CBZ after no-op: %v", err)
	}
	if string(got) != string(cbzBytes) {
		t.Errorf("CBZ bytes changed after no-op: want %q, got %q", cbzBytes, got)
	}
}

// TestMoveSeriesCategoryHappyMove verifies a successful recategorize: the series
// folder is relocated to the new category, the CBZ bytes survive, the sidecar's
// category is updated, and the old directory is gone.
func TestMoveSeriesCategoryHappyMove(t *testing.T) {
	t.Parallel()

	storage := t.TempDir()
	cbzBytes := seedSeriesDir(t, storage, disk.CategoryOther, "Solo Leveling")

	if err := disk.MoveSeriesCategory(storage, disk.CategoryOther, disk.CategoryManhwa, "Solo Leveling"); err != nil {
		t.Fatalf("MoveSeriesCategory happy move: %v", err)
	}

	// Old dir is gone.
	oldDir := disk.SeriesDir(storage, disk.CategoryOther, "Solo Leveling")
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Errorf("old dir still exists: stat err = %v", err)
	}

	// New dir holds the CBZ with intact bytes.
	newDir := disk.SeriesDir(storage, disk.CategoryManhwa, "Solo Leveling")
	got, err := os.ReadFile(filepath.Join(newDir, "[Other][en] Solo Leveling 001.cbz")) //nolint:gosec // test-only, path is from t.TempDir()
	if err != nil {
		t.Fatalf("read CBZ at new dir: %v", err)
	}
	if string(got) != string(cbzBytes) {
		t.Errorf("CBZ bytes changed after move: want %q, got %q", cbzBytes, got)
	}

	// Sidecar category is now Manhwa.
	sidecar, err := disk.ReadSidecar(newDir)
	if err != nil {
		t.Fatalf("ReadSidecar at new dir: %v", err)
	}
	if sidecar == nil {
		t.Fatal("ReadSidecar at new dir returned nil")
	}
	assertEqual(t, "sidecar.Category", disk.CategoryManhwa, sidecar.Category)
}

// TestMoveSeriesCategoryTargetCollision verifies that an existing target dir
// blocks the move: an error is returned and the source is NOT moved.
func TestMoveSeriesCategoryTargetCollision(t *testing.T) {
	t.Parallel()

	storage := t.TempDir()
	srcBytes := seedSeriesDir(t, storage, disk.CategoryOther, "Solo Leveling")
	// Pre-create a colliding target dir.
	if err := os.MkdirAll(disk.SeriesDir(storage, disk.CategoryManhwa, "Solo Leveling"), 0o750); err != nil {
		t.Fatalf("setup target dir: %v", err)
	}

	if err := disk.MoveSeriesCategory(storage, disk.CategoryOther, disk.CategoryManhwa, "Solo Leveling"); err == nil {
		t.Fatal("MoveSeriesCategory target-collision: want error, got nil")
	}

	// Source must be untouched.
	srcDir := disk.SeriesDir(storage, disk.CategoryOther, "Solo Leveling")
	got, err := os.ReadFile(filepath.Join(srcDir, "[Other][en] Solo Leveling 001.cbz")) //nolint:gosec // test-only, path is from t.TempDir()
	if err != nil {
		t.Fatalf("source CBZ missing after collision: %v", err)
	}
	if string(got) != string(srcBytes) {
		t.Errorf("source CBZ bytes changed after collision: want %q, got %q", srcBytes, got)
	}
}

// TestMoveSeriesCategoryMissingSource verifies that a missing source dir is a
// clear error and nothing is created at the target.
func TestMoveSeriesCategoryMissingSource(t *testing.T) {
	t.Parallel()

	storage := t.TempDir()

	if err := disk.MoveSeriesCategory(storage, disk.CategoryOther, disk.CategoryManhwa, "Ghost Series"); err == nil {
		t.Fatal("MoveSeriesCategory missing-source: want error, got nil")
	}

	// Nothing must have been created at the target.
	targetDir := disk.SeriesDir(storage, disk.CategoryManhwa, "Ghost Series")
	if _, err := os.Stat(targetDir); !os.IsNotExist(err) {
		t.Errorf("target dir created despite missing source: stat err = %v", err)
	}
}
