package disk_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/technobecet/tsundoku/internal/disk"
)

// TestRenameCategoryMovesFolderAndRewritesSidecars verifies the happy path: the
// whole category folder is renamed in one move and every child series' sidecar
// Category field is rewritten to the new name.
func TestRenameCategoryMovesFolderAndRewritesSidecars(t *testing.T) {
	t.Parallel()

	storage := t.TempDir()
	// Two series under the "Manhwa" category.
	aBytes := seedSeriesDir(t, storage, "Manhwa", "Solo Leveling")
	seedSeriesDir(t, storage, "Manhwa", "Omniscient Reader")

	if err := disk.RenameCategory(storage, "Manhwa", "Korean Comics"); err != nil {
		t.Fatalf("RenameCategory: %v", err)
	}

	// Old category dir gone, new one present.
	if _, err := os.Stat(filepath.Join(storage, "Manhwa")); !os.IsNotExist(err) {
		t.Fatalf("old category dir should be gone, stat err = %v", err)
	}
	newDir := disk.SeriesDir(storage, "Korean Comics", "Solo Leveling")
	if _, err := os.Stat(newDir); err != nil {
		t.Fatalf("series should have moved with the category: %v", err)
	}

	// CBZ carried over untouched (the move does not re-encode anything).
	got, err := os.ReadFile(filepath.Join(newDir, "[Manhwa][en] Solo Leveling 001.cbz")) //nolint:gosec // test-only path from t.TempDir()
	if err != nil {
		t.Fatalf("moved CBZ missing: %v", err)
	}
	if string(got) != string(aBytes) {
		t.Errorf("moved CBZ bytes changed")
	}

	// Both child sidecars now report the new category name.
	for _, title := range []string{"Solo Leveling", "Omniscient Reader"} {
		sc, err := disk.ReadSidecar(disk.SeriesDir(storage, "Korean Comics", title))
		if err != nil {
			t.Fatalf("read sidecar for %q: %v", title, err)
		}
		if sc == nil || sc.Category != "Korean Comics" {
			t.Errorf("sidecar for %q: want Category=Korean Comics, got %+v", title, sc)
		}
	}
}

// TestRenameCategorySameNameNoOp verifies that renaming to the same name is a
// no-op returning nil.
func TestRenameCategorySameNameNoOp(t *testing.T) {
	t.Parallel()

	storage := t.TempDir()
	seedSeriesDir(t, storage, "Manhwa", "Solo Leveling")

	if err := disk.RenameCategory(storage, "Manhwa", "Manhwa"); err != nil {
		t.Fatalf("RenameCategory same name: want nil, got %v", err)
	}
	if _, err := os.Stat(disk.SeriesDir(storage, "Manhwa", "Solo Leveling")); err != nil {
		t.Fatalf("dir should be untouched: %v", err)
	}
}

// TestRenameCategoryNoFolderIsDBOnlyNoOp verifies that a category with no folder
// on disk (no series rendered) is a no-op on disk — RenameCategory returns nil
// and creates nothing, leaving the DB-only rename to the caller.
func TestRenameCategoryNoFolderIsDBOnlyNoOp(t *testing.T) {
	t.Parallel()

	storage := t.TempDir()
	if err := disk.RenameCategory(storage, "Empty Category", "Renamed"); err != nil {
		t.Fatalf("RenameCategory no folder: want nil, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(storage, "Renamed")); !os.IsNotExist(err) {
		t.Fatalf("no folder should have been created, stat err = %v", err)
	}
}

// TestRenameCategoryTargetExistsCollision verifies that renaming onto an existing
// target folder is a collision error and nothing moves.
func TestRenameCategoryTargetExistsCollision(t *testing.T) {
	t.Parallel()

	storage := t.TempDir()
	srcBytes := seedSeriesDir(t, storage, "Manhwa", "Solo Leveling")
	// A pre-existing target category folder.
	if err := os.MkdirAll(filepath.Join(storage, "Webtoons"), 0o750); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	if err := disk.RenameCategory(storage, "Manhwa", "Webtoons"); err == nil {
		t.Fatal("RenameCategory onto existing target: want error, got nil")
	}

	// Source untouched.
	got, err := os.ReadFile(filepath.Join(disk.SeriesDir(storage, "Manhwa", "Solo Leveling"), "[Manhwa][en] Solo Leveling 001.cbz")) //nolint:gosec // test-only path
	if err != nil {
		t.Fatalf("source CBZ missing after collision: %v", err)
	}
	if string(got) != string(srcBytes) {
		t.Errorf("source CBZ changed after collision")
	}
}

// TestRenameCategoryRollsBackOnSidecarFailure is the no-net-change proof: when a
// child sidecar cannot be read/rewritten mid-rename (here a malformed
// tsundoku.json), the whole category folder is rolled back to its old name so a
// non-nil error means NOTHING net moved.
func TestRenameCategoryRollsBackOnSidecarFailure(t *testing.T) {
	t.Parallel()

	storage := t.TempDir()
	srcBytes := seedSeriesDir(t, storage, "Manhwa", "Solo Leveling")

	// Corrupt one child's sidecar so the post-rename rewrite fails.
	badSidecar := filepath.Join(disk.SeriesDir(storage, "Manhwa", "Solo Leveling"), "tsundoku.json")
	if err := os.WriteFile(badSidecar, []byte("{ not valid json"), 0o600); err != nil {
		t.Fatalf("corrupt sidecar: %v", err)
	}

	if err := disk.RenameCategory(storage, "Manhwa", "Korean Comics"); err == nil {
		t.Fatal("RenameCategory with malformed child sidecar: want error, got nil")
	}

	// Folder rolled back to the OLD name; the new name must not exist.
	if _, err := os.Stat(filepath.Join(storage, "Manhwa")); err != nil {
		t.Fatalf("rollback: old category dir should exist again: %v", err)
	}
	if _, err := os.Stat(filepath.Join(storage, "Korean Comics")); !os.IsNotExist(err) {
		t.Fatalf("rollback: new category dir should not exist, stat err = %v", err)
	}
	// CBZ intact back at the source.
	got, err := os.ReadFile(filepath.Join(disk.SeriesDir(storage, "Manhwa", "Solo Leveling"), "[Manhwa][en] Solo Leveling 001.cbz")) //nolint:gosec // test-only path
	if err != nil {
		t.Fatalf("rollback: source CBZ missing: %v", err)
	}
	if string(got) != string(srcBytes) {
		t.Errorf("rollback: source CBZ changed")
	}
}
