// Package disk_test — unit tests for the ScanLibrary scanner.
// These tests use only the filesystem (t.TempDir); no DB required.
package disk_test

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/fetcher"
)

// writeBareCBZ writes a minimal CBZ archive with no ComicInfo.xml entry, so
// that ReadComicInfoFromCBZ returns nil on it. Used to exercise the "skip CBZ
// without ComicInfo" path in chapterFactFromOrphanCBZ.
func writeBareCBZ(t *testing.T, path string) error {
	t.Helper()
	f, err := os.Create(path) //nolint:gosec
	if err != nil {
		return err
	}
	w := zip.NewWriter(f)
	entry, err := w.Create("page.jpg")
	if err != nil {
		_ = w.Close()
		_ = f.Close()
		return err
	}
	if _, err := entry.Write([]byte{0x00}); err != nil {
		_ = w.Close()
		_ = f.Close()
		return err
	}
	if err := w.Close(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// TestScanLibrary_sidecar_primary verifies that ScanLibrary reads SeriesFacts
// from the tsundoku.json sidecar as the primary source and sets FileExists
// correctly for present files.
func TestScanLibrary_sidecar_primary(t *testing.T) {
	t.Parallel()

	storage := t.TempDir()
	num := 3.0
	max := 10.0

	// Render a real chapter so sidecar + CBZ land on disk.
	req := disk.RenderRequest{
		Storage: storage,
		Meta: disk.RenderMeta{
			Provider:    "mangadex",
			Scanlator:   "dynasty",
			Language:    "en",
			SeriesTitle: "Naruto",
			Category:    disk.CategoryManga,
			Number:      &num,
			MaxChapter:  &max,
			ChapterKey:  "3",
			Importance:  2,
		},
		Pages: []fetcher.PageImage{{Data: []byte{0x00}, Ext: "jpg"}},
	}
	filename, err := disk.RenderChapter(req)
	if err != nil {
		t.Fatalf("RenderChapter: %v", err)
	}

	facts, err := disk.ScanLibrary(storage)
	if err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}

	if len(facts) != 1 {
		t.Fatalf("want 1 SeriesFacts, got %d", len(facts))
	}
	sf := facts[0]
	assertEqual(t, "Title", "Naruto", sf.Title)
	assertEqual(t, "Category", disk.CategoryManga, sf.Category)

	if len(sf.Chapters) != 1 {
		t.Fatalf("want 1 ChapterFact, got %d", len(sf.Chapters))
	}
	cf := sf.Chapters[0]
	assertEqual(t, "Key", "3", cf.Key)
	assertEqual(t, "Provider", "mangadex", cf.Provider)
	assertEqual(t, "Scanlator", "dynasty", cf.Scanlator)
	assertEqual(t, "Filename", filename, cf.Filename)
	if cf.Importance != 2 {
		t.Errorf("Importance = %d, want 2", cf.Importance)
	}
	if !cf.FileExists {
		t.Error("FileExists = false, want true (CBZ is on disk)")
	}
}

// TestScanLibrary_missing_file verifies that a chapter listed in the sidecar
// but whose CBZ file is gone gets FileExists=false.
func TestScanLibrary_missing_file(t *testing.T) {
	t.Parallel()

	storage := t.TempDir()
	num := 1.0
	max := 5.0

	req := disk.RenderRequest{
		Storage: storage,
		Meta: disk.RenderMeta{
			Provider:    "mangadex",
			Language:    "en",
			SeriesTitle: "Bleach",
			Category:    disk.CategoryManga,
			Number:      &num,
			MaxChapter:  &max,
			ChapterKey:  "1",
			Importance:  1,
		},
		Pages: []fetcher.PageImage{{Data: []byte{0x00}, Ext: "jpg"}},
	}
	filename, err := disk.RenderChapter(req)
	if err != nil {
		t.Fatalf("RenderChapter: %v", err)
	}

	// Delete the CBZ — sidecar entry remains.
	seriesDir := filepath.Join(storage, "Manga", "Bleach")
	if err := os.Remove(filepath.Join(seriesDir, filename)); err != nil {
		t.Fatalf("remove cbz: %v", err)
	}

	facts, err := disk.ScanLibrary(storage)
	if err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}
	if len(facts) != 1 || len(facts[0].Chapters) != 1 {
		t.Fatalf("unexpected facts shape: %+v", facts)
	}
	if facts[0].Chapters[0].FileExists {
		t.Error("FileExists = true for deleted CBZ, want false")
	}
}

// TestScanLibrary_orphan_cbz verifies that a CBZ with no sidecar entry (orphan)
// is picked up via its ComicInfo.xml provenance.
func TestScanLibrary_orphan_cbz(t *testing.T) {
	t.Parallel()

	storage := t.TempDir()
	// Create a series dir with a CBZ + ComicInfo but NO tsundoku.json.
	seriesDir := filepath.Join(storage, "Manga", "Orphan Series")
	if err := os.MkdirAll(seriesDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	ci := disk.ComicInfo{
		Series:     "Orphan Series",
		Number:     "7",
		Provider:   "comick",
		Scanlator:  "scans",
		Importance: 1,
		ChapterKey: "7",
		PageCount:  2,
	}
	cbzPath := filepath.Join(seriesDir, "orphan.cbz")
	pages := []fetcher.PageImage{
		{Data: []byte{0x00}, Ext: "jpg"},
		{Data: []byte{0x01}, Ext: "jpg"},
	}
	if err := disk.CreateCBZ(cbzPath, pages, ci); err != nil {
		t.Fatalf("CreateCBZ: %v", err)
	}

	// Stamp a KNOWN past mtime on the orphan CBZ so we can assert the
	// ORPHAN path (chapterFactFromOrphanCBZ / orphanChapterFacts) actually
	// populates ChapterFact.ModTime from it — the sidecar path is exercised
	// by other tests and shares no code with this one.
	wantModTime := time.Date(2026, 1, 14, 10, 0, 0, 0, time.UTC)
	if err := os.Chtimes(cbzPath, wantModTime, wantModTime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	facts, err := disk.ScanLibrary(storage)
	if err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("want 1 SeriesFacts for orphan series, got %d", len(facts))
	}
	sf := facts[0]
	if len(sf.Chapters) != 1 {
		t.Fatalf("want 1 ChapterFact from orphan CBZ, got %d", len(sf.Chapters))
	}
	cf := sf.Chapters[0]
	assertEqual(t, "orphan Key", "7", cf.Key)
	assertEqual(t, "orphan Provider", "comick", cf.Provider)
	if !cf.FileExists {
		t.Error("orphan CBZ FileExists = false, want true")
	}
	// The load-bearing assertion this test previously lacked: the orphan
	// path must populate ModTime, or seedFirstDownloadedAtFromMtime silently
	// no-ops on every disk-imported (Kaizoku) chapter.
	if !cf.ModTime.Truncate(time.Second).Equal(wantModTime.Truncate(time.Second)) {
		t.Errorf("orphan ModTime = %v, want %v", cf.ModTime, wantModTime)
	}
}

// TestScanLibrary_empty_storage verifies that ScanLibrary returns no facts
// and no error when the storage directory is empty.
func TestScanLibrary_empty_storage(t *testing.T) {
	t.Parallel()

	storage := t.TempDir()
	facts, err := disk.ScanLibrary(storage)
	if err != nil {
		t.Fatalf("ScanLibrary on empty dir: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("want 0 SeriesFacts for empty storage, got %d", len(facts))
	}
}

// TestScanLibrary_multiple_categories verifies that ScanLibrary picks up series
// from multiple category subdirectories (Manga, Manhwa, etc.).
func TestScanLibrary_multiple_categories(t *testing.T) {
	t.Parallel()

	storage := t.TempDir()
	num1, num2 := 1.0, 1.0
	max := 1.0

	for _, tc := range []struct {
		title    string
		category string
		num      *float64
		key      string
	}{
		{"Series A", disk.CategoryManga, &num1, "1"},
		{"Series B", disk.CategoryManhwa, &num2, "1"},
	} {
		req := disk.RenderRequest{
			Storage: storage,
			Meta: disk.RenderMeta{
				Provider:    "mangadex",
				Language:    "en",
				SeriesTitle: tc.title,
				Category:    tc.category,
				Number:      tc.num,
				MaxChapter:  &max,
				ChapterKey:  tc.key,
				Importance:  1,
			},
			Pages: []fetcher.PageImage{{Data: []byte{0x00}, Ext: "jpg"}},
		}
		if _, err := disk.RenderChapter(req); err != nil {
			t.Fatalf("RenderChapter %q: %v", tc.title, err)
		}
	}

	facts, err := disk.ScanLibrary(storage)
	if err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}
	if len(facts) != 2 {
		t.Errorf("want 2 SeriesFacts, got %d", len(facts))
	}
}

// TestScanLibrary_orphan_cbz_no_comicinfo verifies that a CBZ with no ComicInfo.xml
// is silently skipped when no sidecar exists.
func TestScanLibrary_orphan_cbz_no_comicinfo(t *testing.T) {
	t.Parallel()

	storage := t.TempDir()
	seriesDir := filepath.Join(storage, "Manga", "No ComicInfo")
	if err := os.MkdirAll(seriesDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Write a CBZ with only an image, no ComicInfo.xml.
	cbzPath := filepath.Join(seriesDir, "bare.cbz")
	pages := []fetcher.PageImage{{Data: []byte{0x00}, Ext: "jpg"}}
	// Use a zero-valued ComicInfo (Provider="" ChapterKey="") — ReadComicInfoFromCBZ
	// returns a non-nil ci; the key will be recomputed from the empty title.
	// To get a truly nil ComicInfo we need a CBZ with NO ComicInfo.xml entry.
	_ = pages // will use disk.CreateCBZ helper below

	// We need to create a valid ZIP with NO ComicInfo.xml entry.
	// Write it manually.
	if err := writeBareCBZ(t, cbzPath); err != nil {
		t.Fatalf("writeBareCBZ: %v", err)
	}

	facts, err := disk.ScanLibrary(storage)
	if err != nil {
		t.Fatalf("ScanLibrary with no-ComicInfo CBZ: %v", err)
	}
	// The series dir exists but has no usable chapters → either nil facts or 0 chapters.
	// Either outcome is acceptable as long as no error is returned.
	for _, sf := range facts {
		for _, cf := range sf.Chapters {
			if cf.Filename == "bare.cbz" {
				t.Errorf("bare CBZ without ComicInfo should have been skipped, got fact: %+v", cf)
			}
		}
	}
}

// TestScanLibrary_number_from_sidecar verifies that Number from the sidecar
// is correctly propagated to ChapterFact.Number.
func TestScanLibrary_number_from_sidecar(t *testing.T) {
	t.Parallel()

	storage := t.TempDir()
	num := 12.5
	max := 100.0

	req := disk.RenderRequest{
		Storage: storage,
		Meta: disk.RenderMeta{
			Provider:    "mangadex",
			Language:    "en",
			SeriesTitle: "Number Test",
			Category:    disk.CategoryManga,
			Number:      &num,
			MaxChapter:  &max,
			ChapterKey:  "12.5",
			Importance:  1,
		},
		Pages: []fetcher.PageImage{{Data: []byte{0x00}, Ext: "jpg"}},
	}
	if _, err := disk.RenderChapter(req); err != nil {
		t.Fatalf("RenderChapter: %v", err)
	}

	facts, err := disk.ScanLibrary(storage)
	if err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}
	if len(facts) != 1 || len(facts[0].Chapters) != 1 {
		t.Fatalf("unexpected shape: %+v", facts)
	}
	cf := facts[0].Chapters[0]
	if cf.Number == nil || *cf.Number != 12.5 {
		t.Errorf("Number = %v, want 12.5", cf.Number)
	}
}
