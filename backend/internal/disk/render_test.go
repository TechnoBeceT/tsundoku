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

// TestRenderChapterMangaAndCount verifies that RenderChapter writes ComicInfo with
// Manga="YesAndRightToLeft" when Meta.Manga=true, and sets Count when
// Meta.ChapterCount>0. This exercises the Manga and ChapterCount branches in
// the unexported newComicInfo helper.
func TestRenderChapterMangaAndCount(t *testing.T) {
	t.Parallel()

	storage := t.TempDir()
	num := 1.0
	req := disk.RenderRequest{
		Storage: storage,
		Meta: disk.RenderMeta{
			Provider:     "mangadex",
			Language:     "ja",
			SeriesTitle:  "Manga Series",
			Category:     "Manga",
			Number:       &num,
			MaxChapter:   &num,
			ChapterKey:   "1",
			Manga:        true,
			ChapterCount: 50,
		},
		Pages: []fetcher.PageImage{{Data: []byte{0x00}, Ext: "jpg"}},
	}

	filename, err := disk.RenderChapter(req)
	if err != nil {
		t.Fatalf("RenderChapter: %v", err)
	}

	cbzPath := filepath.Join(storage, "Manga", "Manga Series", filename)
	ci, err := disk.ReadComicInfoFromCBZ(cbzPath)
	if err != nil {
		t.Fatalf("ReadComicInfoFromCBZ: %v", err)
	}
	if ci == nil {
		t.Fatal("ReadComicInfoFromCBZ returned nil")
	}
	if ci.Manga != "YesAndRightToLeft" {
		t.Errorf("ComicInfo.Manga = %q, want %q", ci.Manga, "YesAndRightToLeft")
	}
	if ci.Count != 50 {
		t.Errorf("ComicInfo.Count = %d, want 50", ci.Count)
	}
}

// TestBuildProviderOrderDedup verifies that buildProviderOrder deduplicates
// providers, keeping the entry with higher importance. This exercises the
// "already seen" branch in the dedup loop.
func TestBuildProviderOrderDedup(t *testing.T) {
	t.Parallel()

	storage := t.TempDir()
	num := 1.0
	max := 5.0

	render := func(chKey, provider string, importance int) {
		t.Helper()
		req := disk.RenderRequest{
			Storage: storage,
			Meta: disk.RenderMeta{
				Provider:    provider,
				Language:    "en",
				SeriesTitle: "Dedup Order Series",
				Category:    "Manga",
				Number:      &num,
				MaxChapter:  &max,
				ChapterKey:  chKey,
				Importance:  importance,
			},
			Pages: []fetcher.PageImage{{Data: []byte{0x00}, Ext: "jpg"}},
		}
		if _, err := disk.RenderChapter(req); err != nil {
			t.Fatalf("RenderChapter(%q): %v", chKey, err)
		}
	}

	// Render two chapters from the same provider, plus one from another.
	// After both, buildProviderOrder must deduplicate "mangadex" and keep it once.
	render("1", "mangadex", 2)
	render("2", "mangadex", 2) // duplicate provider — hits the seen[p.provider] dedup branch
	render("3", "other-src", 1)

	seriesDir := filepath.Join(storage, "Manga", "Dedup Order Series")
	sidecar, err := disk.ReadSidecar(seriesDir)
	if err != nil {
		t.Fatalf("ReadSidecar: %v", err)
	}
	if sidecar == nil {
		t.Fatal("ReadSidecar returned nil")
	}

	// ProviderOrder must contain each provider exactly once.
	seen := make(map[string]int)
	for _, p := range sidecar.ProviderOrder {
		seen[p]++
	}
	for p, count := range seen {
		if count > 1 {
			t.Errorf("provider %q appears %d times in ProviderOrder, want 1", p, count)
		}
	}
	if len(sidecar.ProviderOrder) != 2 {
		t.Errorf("ProviderOrder len = %d, want 2 (mangadex, other-src)", len(sidecar.ProviderOrder))
	}
}

// TestBuildProviderOrderSortsByImportanceDesc is the executable guard for the
// Tsundoku importance convention: HIGHER importance number = HIGHER priority.
// Index 0 of ProviderOrder MUST be the provider with the largest importance value.
// This test will fail if anyone inverts the comparator in buildProviderOrder.
func TestBuildProviderOrderSortsByImportanceDesc(t *testing.T) {
	t.Parallel()

	storage := t.TempDir()
	num := 1.0
	max := 10.0

	render := func(chKey, provider string, importance int) {
		t.Helper()
		req := disk.RenderRequest{
			Storage: storage,
			Meta: disk.RenderMeta{
				Provider:    provider,
				Language:    "en",
				SeriesTitle: "Importance Order Series",
				Category:    "Manga",
				Number:      &num,
				MaxChapter:  &max,
				ChapterKey:  chKey,
				Importance:  importance,
			},
			Pages: []fetcher.PageImage{{Data: []byte{0x00}, Ext: "jpg"}},
		}
		if _, err := disk.RenderChapter(req); err != nil {
			t.Fatalf("RenderChapter(%q): %v", chKey, err)
		}
	}

	// "low" has importance=1; "high" has importance=5.
	// Tsundoku convention: higher importance number = higher priority.
	// Therefore ProviderOrder[0] must be "high".
	render("ch-low", "low", 1)
	render("ch-high", "high", 5)

	seriesDir := filepath.Join(storage, "Manga", "Importance Order Series")
	sidecar, err := disk.ReadSidecar(seriesDir)
	if err != nil {
		t.Fatalf("ReadSidecar: %v", err)
	}
	if sidecar == nil {
		t.Fatal("ReadSidecar returned nil")
	}
	if len(sidecar.ProviderOrder) < 2 {
		t.Fatalf("ProviderOrder len = %d, want 2", len(sidecar.ProviderOrder))
	}
	// The provider with importance=5 must be at index 0.
	if sidecar.ProviderOrder[0] != "high" {
		t.Errorf("ProviderOrder[0] = %q, want %q (higher importance number must be first)", sidecar.ProviderOrder[0], "high")
	}
	if sidecar.ProviderOrder[1] != "low" {
		t.Errorf("ProviderOrder[1] = %q, want %q", sidecar.ProviderOrder[1], "low")
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
