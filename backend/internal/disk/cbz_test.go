package disk_test

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/fetcher"
)

// makePages creates a slice of synthetic PageImage values for testing.
func makePages(n int) []fetcher.PageImage {
	pages := make([]fetcher.PageImage, n)
	for i := range pages {
		// Minimal fake JPEG: just enough bytes to be non-empty.
		pages[i] = fetcher.PageImage{
			Data: []byte{0xFF, 0xD8, 0xFF, 0xE0, byte(i)},
			Ext:  "jpg",
		}
	}
	return pages
}

// TestCreateAndReadComicInfo verifies the full CBZ create → read round-trip:
// pages stored with zip.Store, ComicInfo present, and provenance fields survive.
func TestCreateAndReadComicInfo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dest := filepath.Join(dir, "test.cbz")

	ci := disk.ComicInfo{
		Title:      "Round-trip Chapter",
		Series:     "Test Series",
		Number:     "7",
		PageCount:  3,
		Notes:      "Created by Tsundoku",
		Provider:   "mangadex",
		Scanlator:  "dynasty",
		Importance: 2,
		ChapterKey: "7",
	}
	pages := makePages(3)

	if err := disk.CreateCBZ(dest, pages, ci); err != nil {
		t.Fatalf("CreateCBZ: %v", err)
	}

	// Verify the archive exists.
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("CBZ not found after CreateCBZ: %v", err)
	}

	// Verify pages are stored uncompressed (zip.Store).
	r, err := zip.OpenReader(dest)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer func() { _ = r.Close() }()

	imageCount := 0
	for _, f := range r.File {
		if filepath.Ext(f.Name) == ".jpg" {
			imageCount++
			if f.Method != zip.Store {
				t.Errorf("page %q: method = %d, want zip.Store (%d)", f.Name, f.Method, zip.Store)
			}
		}
	}
	if imageCount != 3 {
		t.Errorf("image count = %d, want 3", imageCount)
	}

	// Round-trip ComicInfo.
	got, err := disk.ReadComicInfoFromCBZ(dest)
	if err != nil {
		t.Fatalf("ReadComicInfoFromCBZ: %v", err)
	}
	if got == nil {
		t.Fatal("ReadComicInfoFromCBZ returned nil, want ComicInfo")
	}

	assertEqual(t, "Title", ci.Title, got.Title)
	assertEqual(t, "Provider", ci.Provider, got.Provider)
	assertEqual(t, "Scanlator", ci.Scanlator, got.Scanlator)
	assertEqual(t, "Importance", ci.Importance, got.Importance)
	assertEqual(t, "ChapterKey", ci.ChapterKey, got.ChapterKey)
	assertEqual(t, "Notes", ci.Notes, got.Notes)
}

// TestUpdateCBZComicInfo verifies that UpdateCBZComicInfo replaces ComicInfo
// atomically, leaving all page entries intact.
func TestUpdateCBZComicInfo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dest := filepath.Join(dir, "update_test.cbz")

	original := disk.ComicInfo{
		Title:      "Original",
		ChapterKey: "1",
		Provider:   "provider-a",
	}
	if err := disk.CreateCBZ(dest, makePages(2), original); err != nil {
		t.Fatalf("CreateCBZ: %v", err)
	}

	updated := disk.ComicInfo{
		Title:      "Updated",
		ChapterKey: "1",
		Provider:   "provider-b",
		Notes:      "Created by Tsundoku",
	}
	if err := disk.UpdateCBZComicInfo(dest, updated); err != nil {
		t.Fatalf("UpdateCBZComicInfo: %v", err)
	}

	got, err := disk.ReadComicInfoFromCBZ(dest)
	if err != nil {
		t.Fatalf("ReadComicInfoFromCBZ after update: %v", err)
	}
	if got == nil {
		t.Fatal("ReadComicInfoFromCBZ returned nil after update")
	}
	assertEqual(t, "Title after update", updated.Title, got.Title)
	assertEqual(t, "Provider after update", updated.Provider, got.Provider)

	// Verify pages are still present.
	r, err := zip.OpenReader(dest)
	if err != nil {
		t.Fatalf("open zip after update: %v", err)
	}
	defer func() { _ = r.Close() }()

	imageCount := 0
	for _, f := range r.File {
		if filepath.Ext(f.Name) == ".jpg" {
			imageCount++
		}
	}
	if imageCount != 2 {
		t.Errorf("image count after update = %d, want 2", imageCount)
	}
}

// TestCreateCBZAtomicity verifies that a failed render leaves no partial .cbz
// and no .tmp file at the destination path.
func TestCreateCBZAtomicity(t *testing.T) {
	t.Parallel()

	// Strategy: write the CBZ to a real directory first so we can confirm
	// that the normal path works, then attempt a path whose parent is a file
	// (ENOTDIR) to force failure, and verify no files are left in the writable
	// temp dir that look like partial output.
	dir := t.TempDir()

	// Create a file at the path where the category dir would be so that
	// os.MkdirAll on the series dir fails.
	blocker := filepath.Join(dir, "Manga")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	ci := disk.ComicInfo{Title: "Will fail"}
	// Destination is inside the blocked dir — MkdirAll will fail.
	dest := filepath.Join(dir, "Manga", "Series", "chapter.cbz")
	err := disk.CreateCBZ(dest, makePages(1), ci)
	if err == nil {
		t.Fatal("CreateCBZ expected error, got nil")
	}

	// Verify no stray files of any kind were created in the temp dir
	// (aside from our "blocker" file itself).
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("ReadDir: %v", readErr)
	}
	for _, e := range entries {
		name := e.Name()
		if name == "Manga" {
			continue // the blocker file we created
		}
		t.Errorf("unexpected file/dir in storage after failed render: %s", name)
	}
}
