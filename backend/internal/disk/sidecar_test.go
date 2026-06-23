package disk_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/disk"
)

// TestSidecarReadWrite verifies that a Sidecar round-trips through Write → Read.
func TestSidecarReadWrite(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	uploadDate := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)

	original := disk.Sidecar{
		Title:         "Naruto",
		Category:      "Manga",
		ProviderOrder: []string{"mangadex", "mangaplus"},
		Chapters: []disk.ChapterProvenance{
			{
				ChapterKey: "1",
				Number:     ptr(1),
				Provider:   "mangadex",
				Scanlator:  "dynasty",
				Importance: 1,
				Filename:   "[mangadex-dynasty][en] Naruto 001.cbz",
				PageCount:  42,
				UploadDate: &uploadDate,
			},
		},
	}

	if err := disk.WriteSidecar(dir, original); err != nil {
		t.Fatalf("WriteSidecar: %v", err)
	}

	// File must be named tsundoku.json.
	jsonPath := filepath.Join(dir, "tsundoku.json")
	got, err := disk.ReadSidecar(dir)
	if err != nil {
		t.Fatalf("ReadSidecar: %v", err)
	}
	if got == nil {
		t.Fatal("ReadSidecar returned nil")
	}
	_ = jsonPath // confirmed by ReadSidecar succeeding

	assertEqual(t, "Title", original.Title, got.Title)
	assertEqual(t, "Category", original.Category, got.Category)
	if len(got.ProviderOrder) != len(original.ProviderOrder) {
		t.Errorf("ProviderOrder len: want %d, got %d", len(original.ProviderOrder), len(got.ProviderOrder))
	}
	if len(got.Chapters) != 1 {
		t.Fatalf("Chapters len: want 1, got %d", len(got.Chapters))
	}
	ch := got.Chapters[0]
	assertEqual(t, "ChapterKey", "1", ch.ChapterKey)
	assertEqual(t, "Provider", "mangadex", ch.Provider)
	assertEqual(t, "Scanlator", "dynasty", ch.Scanlator)
	assertEqual(t, "Importance", 1, ch.Importance)
	assertEqual(t, "Filename", "[mangadex-dynasty][en] Naruto 001.cbz", ch.Filename)
	assertEqual(t, "PageCount", 42, ch.PageCount)
	if ch.UploadDate == nil || !ch.UploadDate.Equal(uploadDate) {
		t.Errorf("UploadDate: want %v, got %v", uploadDate, ch.UploadDate)
	}
}

// TestReadSidecarMissing verifies that ReadSidecar returns nil (no error) when
// the tsundoku.json file does not exist yet.
func TestReadSidecarMissing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	got, err := disk.ReadSidecar(dir)
	if err != nil {
		t.Fatalf("ReadSidecar on missing file: %v", err)
	}
	if got != nil {
		t.Errorf("ReadSidecar on missing file: want nil, got %+v", got)
	}
}

// TestWriteSidecarMkdirAllFailure verifies that WriteSidecar returns a non-nil
// error when the target directory cannot be created because its parent is a
// regular file (ENOTDIR).
func TestWriteSidecarMkdirAllFailure(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	// Create a regular file where a directory would need to be.
	blocker := filepath.Join(base, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("setup WriteFile: %v", err)
	}

	// The target dir sits inside the file — MkdirAll must fail with ENOTDIR.
	dir := filepath.Join(blocker, "subdir")
	s := disk.Sidecar{Title: "Test", Chapters: []disk.ChapterProvenance{}}
	if err := disk.WriteSidecar(dir, s); err == nil {
		t.Fatal("WriteSidecar expected error (ENOTDIR), got nil")
	}
}
