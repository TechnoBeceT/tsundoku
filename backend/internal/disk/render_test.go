package disk_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/fetcher"
)

// TestRenderChapterDeduplication verifies the in-place chapter-key update path:
// calling RenderChapter twice with the same ChapterKey must result in exactly one
// chapter entry in the sidecar, reflecting the second call's values.
func TestRenderChapterDeduplication(t *testing.T) {
	t.Parallel()

	storage := t.TempDir()
	num := 3.0
	max := 10.0

	renderWith := func(title, provider string, importance int) string {
		t.Helper()
		req := disk.RenderRequest{
			Storage: storage,
			Meta: disk.RenderMeta{
				Provider:    provider,
				Scanlator:   "",
				Language:    "en",
				SeriesTitle: "Dedup Series",
				Category:    "Manga",
				Number:      &num,
				MaxChapter:  &max,
				ChapterKey:  "3",
				ChapterName: title,
				Importance:  importance,
			},
			Pages: []fetcher.PageImage{{Data: []byte{0x00}, Ext: "jpg"}},
		}
		fn, err := disk.RenderChapter(req)
		if err != nil {
			t.Fatalf("RenderChapter(%q): %v", title, err)
		}
		return fn
	}

	renderWith("First Title", "provider-a", 2)
	renderWith("Second Title", "provider-b", 1)

	seriesDir := filepath.Join(storage, "Manga", "Dedup Series")
	sidecar, err := disk.ReadSidecar(seriesDir)
	if err != nil {
		t.Fatalf("ReadSidecar: %v", err)
	}
	if sidecar == nil {
		t.Fatal("ReadSidecar returned nil")
	}

	// Must have exactly one chapter entry — the second render replaced the first.
	if len(sidecar.Chapters) != 1 {
		t.Fatalf("sidecar.Chapters len = %d, want 1 (dedup failed)", len(sidecar.Chapters))
	}

	ch := sidecar.Chapters[0]
	assertEqual(t, "ChapterKey", "3", ch.ChapterKey)
	// The second render used "provider-b".
	assertEqual(t, "Provider after update", "provider-b", ch.Provider)
}

// TestRenderChapterSidecarCorrupt verifies that a corrupt tsundoku.json causes
// RenderChapter to return a non-nil error.
func TestRenderChapterSidecarCorrupt(t *testing.T) {
	t.Parallel()

	storage := t.TempDir()
	seriesDir := filepath.Join(storage, "Manga", "Corrupt Series")
	if err := os.MkdirAll(seriesDir, 0o750); err != nil {
		t.Fatalf("setup MkdirAll: %v", err)
	}

	// Pre-write a corrupt tsundoku.json.
	if err := os.WriteFile(filepath.Join(seriesDir, "tsundoku.json"), []byte("{invalid json"), 0o600); err != nil {
		t.Fatalf("setup WriteFile: %v", err)
	}

	num := 1.0
	req := disk.RenderRequest{
		Storage: storage,
		Meta: disk.RenderMeta{
			Provider:    "mangadex",
			Language:    "en",
			SeriesTitle: "Corrupt Series",
			Category:    "Manga",
			Number:      &num,
			MaxChapter:  &num,
			ChapterKey:  "1",
		},
		Pages: []fetcher.PageImage{{Data: []byte{0x00}, Ext: "jpg"}},
	}

	_, err := disk.RenderChapter(req)
	if err == nil {
		t.Fatal("RenderChapter with corrupt sidecar: expected error, got nil")
	}
}

// TestRenderChapterWritesCBZAndSidecar verifies that RenderChapter:
//  1. Creates the CBZ at the categorized layout path.
//  2. Creates/updates tsundoku.json with the chapter's provenance.
//  3. Returns the on-disk filename.
func TestRenderChapterWritesCBZAndSidecar(t *testing.T) {
	t.Parallel()

	storage := t.TempDir()
	uploadDate := time.Date(2024, 5, 10, 0, 0, 0, 0, time.UTC)
	num := 5.0
	maxCh := 120.0

	req := disk.RenderRequest{
		Storage: storage,
		Meta: disk.RenderMeta{
			Provider:    "mangadex",
			Scanlator:   "dynasty",
			Language:    "en",
			SeriesTitle: "Naruto",
			Category:    "Manga",
			Number:      &num,
			ChapterName: "",
			MaxChapter:  &maxCh,
			Importance:  1,
			ChapterKey:  "5",
			UploadDate:  &uploadDate,
			URL:         "https://mangadex.org/chapter/abc",
		},
		Pages: []fetcher.PageImage{
			{Data: []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x01}, Ext: "jpg"},
			{Data: []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x02}, Ext: "jpg"},
		},
	}

	filename, err := disk.RenderChapter(req)
	if err != nil {
		t.Fatalf("RenderChapter: %v", err)
	}

	// Filename must be non-empty.
	if filename == "" {
		t.Fatal("RenderChapter returned empty filename")
	}

	// CBZ must exist at <storage>/Manga/Naruto/<filename>.
	seriesDir := filepath.Join(storage, "Manga", "Naruto")
	cbzPath := filepath.Join(seriesDir, filename)
	if _, err := os.Stat(cbzPath); err != nil {
		t.Fatalf("CBZ not found at %q: %v", cbzPath, err)
	}

	// tsundoku.json must exist in series dir.
	sidecar, err := disk.ReadSidecar(seriesDir)
	if err != nil {
		t.Fatalf("ReadSidecar: %v", err)
	}
	if sidecar == nil {
		t.Fatal("ReadSidecar returned nil after RenderChapter")
	}

	// Sidecar must contain the chapter's provenance.
	if len(sidecar.Chapters) != 1 {
		t.Fatalf("sidecar.Chapters len = %d, want 1", len(sidecar.Chapters))
	}
	ch := sidecar.Chapters[0]
	assertEqual(t, "sidecar ChapterKey", "5", ch.ChapterKey)
	assertEqual(t, "sidecar Provider", "mangadex", ch.Provider)
	assertEqual(t, "sidecar Filename", filename, ch.Filename)

	// ComicInfo inside CBZ must carry provenance.
	ci, err := disk.ReadComicInfoFromCBZ(cbzPath)
	if err != nil {
		t.Fatalf("ReadComicInfoFromCBZ: %v", err)
	}
	if ci == nil {
		t.Fatal("ReadComicInfoFromCBZ returned nil")
	}
	assertEqual(t, "ci Provider", "mangadex", ci.Provider)
	assertEqual(t, "ci ChapterKey", "5", ci.ChapterKey)
}

// TestRenderChapterAtomicity verifies that a failed render leaves no partial .cbz.
func TestRenderChapterAtomicity(t *testing.T) {
	t.Parallel()

	// Create a storage root where the category dir cannot be created
	// by making a file at that path.
	storage := t.TempDir()
	blocker := filepath.Join(storage, "Manga")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	num := 1.0
	req := disk.RenderRequest{
		Storage: storage,
		Meta: disk.RenderMeta{
			Provider:    "mangadex",
			Scanlator:   "",
			Language:    "en",
			SeriesTitle: "Naruto",
			Category:    "Manga",
			Number:      &num,
			ChapterName: "",
			MaxChapter:  &num,
			ChapterKey:  "1",
		},
		Pages: []fetcher.PageImage{
			{Data: []byte{0x00}, Ext: "jpg"},
		},
	}

	_, err := disk.RenderChapter(req)
	if err == nil {
		t.Fatal("RenderChapter: expected error, got nil")
	}

	// No .cbz files anywhere in storage.
	found := false
	_ = filepath.Walk(storage, func(p string, info os.FileInfo, _ error) error {
		if !info.IsDir() && filepath.Ext(p) == ".cbz" {
			found = true
		}
		return nil
	})
	if found {
		t.Error("found orphaned .cbz after failed render")
	}
}

// TestRenderChapterUpdatesSidecar verifies that a second RenderChapter call for
// the same series appends to (rather than replaces) the sidecar.
func TestRenderChapterUpdatesSidecar(t *testing.T) {
	t.Parallel()

	storage := t.TempDir()
	num1, num2 := 1.0, 2.0
	max := 10.0

	render := func(num float64, key string) {
		t.Helper()
		req := disk.RenderRequest{
			Storage: storage,
			Meta: disk.RenderMeta{
				Provider:    "mangadex",
				Scanlator:   "",
				Language:    "en",
				SeriesTitle: "Berserk",
				Category:    "Manga",
				Number:      &num,
				MaxChapter:  &max,
				ChapterKey:  key,
				Importance:  1,
			},
			Pages: []fetcher.PageImage{{Data: []byte{0x00}, Ext: "jpg"}},
		}
		if _, err := disk.RenderChapter(req); err != nil {
			t.Fatalf("RenderChapter ch%s: %v", key, err)
		}
	}

	render(num1, "1")
	render(num2, "2")

	seriesDir := filepath.Join(storage, "Manga", "Berserk")
	sidecar, err := disk.ReadSidecar(seriesDir)
	if err != nil {
		t.Fatalf("ReadSidecar: %v", err)
	}
	if len(sidecar.Chapters) != 2 {
		t.Errorf("sidecar.Chapters len = %d, want 2", len(sidecar.Chapters))
	}
}
