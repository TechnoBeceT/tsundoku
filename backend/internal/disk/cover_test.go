package disk_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/fetcher"
)

// coverReq builds a SaveCover request for the standard test series.
func coverReq(storage string, data []byte, ext, sourceURL string) disk.CoverRequest {
	return disk.CoverRequest{
		Storage:   storage,
		Category:  "Manga",
		Title:     "Alpha Saga",
		Data:      data,
		Ext:       ext,
		SourceURL: sourceURL,
		Provider:  "mangadex",
	}
}

// TestSaveCover_WritesFileAndSidecar proves the bytes land in the series dir
// under the resolved extension and the sidecar records the provenance block
// (source_url is the cache key).
func TestSaveCover_WritesFileAndSidecar(t *testing.T) {
	storage := t.TempDir()
	want := []byte{0x89, 0x50, 0x4E, 0x47}

	filename, err := disk.SaveCover(coverReq(storage, want, "png", "/api/v1/manga/1/thumbnail"))
	if err != nil {
		t.Fatalf("SaveCover: %v", err)
	}
	if filename != "cover.png" {
		t.Fatalf("SaveCover: filename = %q, want cover.png", filename)
	}

	seriesDir := disk.SeriesDir(storage, "Manga", "Alpha Saga")
	got, err := os.ReadFile(filepath.Join(seriesDir, filename)) //nolint:gosec // test path
	if err != nil {
		t.Fatalf("read cover file: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("cover bytes mismatch")
	}

	sc, err := disk.ReadSidecar(seriesDir)
	if err != nil || sc == nil {
		t.Fatalf("ReadSidecar: %v (sidecar %v)", err, sc)
	}
	if sc.Cover == nil {
		t.Fatalf("sidecar Cover block is nil")
	}
	if sc.Cover.File != "cover.png" || sc.Cover.SourceURL != "/api/v1/manga/1/thumbnail" || sc.Cover.Provider != "mangadex" {
		t.Errorf("sidecar Cover = %+v, want file/source_url/provider populated", *sc.Cover)
	}
	if sc.Title != "Alpha Saga" || sc.Category != "Manga" {
		t.Errorf("sidecar series fields = %q/%q, want Alpha Saga/Manga", sc.Title, sc.Category)
	}

	// The sidecar JSON uses snake_case, like every other sidecar field.
	raw, err := os.ReadFile(filepath.Join(seriesDir, "tsundoku.json")) //nolint:gosec // test path
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	var probe struct {
		Cover map[string]any `json:"cover"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("unmarshal sidecar: %v", err)
	}
	if _, ok := probe.Cover["source_url"]; !ok {
		t.Errorf("sidecar cover block missing source_url key: %v", probe.Cover)
	}
}

// TestSaveCover_ReplacesPreviousCover proves a metadata-source switch overwrites
// the stored cover — including when the new image has a different extension (the
// stale file must not linger next to the new one).
func TestSaveCover_ReplacesPreviousCover(t *testing.T) {
	storage := t.TempDir()
	if _, err := disk.SaveCover(coverReq(storage, []byte("old"), "jpg", "/old")); err != nil {
		t.Fatalf("SaveCover(old): %v", err)
	}
	if _, err := disk.SaveCover(coverReq(storage, []byte("new"), "png", "/new")); err != nil {
		t.Fatalf("SaveCover(new): %v", err)
	}

	seriesDir := disk.SeriesDir(storage, "Manga", "Alpha Saga")
	if _, err := os.Stat(filepath.Join(seriesDir, "cover.jpg")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale cover.jpg still present (err = %v)", err)
	}

	data, ext, prov, err := disk.ReadCover(storage, "Manga", "Alpha Saga")
	if err != nil {
		t.Fatalf("ReadCover: %v", err)
	}
	if string(data) != "new" || ext != "png" || prov.SourceURL != "/new" {
		t.Errorf("ReadCover = %q/%q/%q, want new/png//new", data, ext, prov.SourceURL)
	}
}

// TestSaveCover_ExtensionResolution proves the stored extension follows the
// image type Suwayomi reports, and that an empty/odd value degrades to jpg
// rather than producing a dotfile or an extensionless cover.
func TestSaveCover_ExtensionResolution(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"jpg", "cover.jpg"},
		{"JPEG", "cover.jpeg"},
		{".png", "cover.png"},
		{"webp", "cover.webp"},
		{"", "cover.jpg"},
		{"../evil", "cover.jpg"},
	}
	for _, tc := range cases {
		storage := t.TempDir()
		got, err := disk.SaveCover(coverReq(storage, []byte("x"), tc.in, "/u"))
		if err != nil {
			t.Fatalf("SaveCover(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("SaveCover(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestReadCover_AbsentIsSentinel proves a series with no cover (no sidecar at
// all) reports the no-local-cover sentinel rather than an I/O error.
func TestReadCover_AbsentIsSentinel(t *testing.T) {
	_, _, _, err := disk.ReadCover(t.TempDir(), "Manga", "Alpha Saga")
	if !errors.Is(err, disk.ErrNoLocalCover) {
		t.Fatalf("ReadCover: err = %v, want ErrNoLocalCover", err)
	}
}

// TestReadCover_CorruptSidecarIsAbsent proves an unparseable tsundoku.json is
// treated as "no local cover" (re-fetch), never a thrown error.
func TestReadCover_CorruptSidecarIsAbsent(t *testing.T) {
	storage := t.TempDir()
	seriesDir := disk.SeriesDir(storage, "Manga", "Alpha Saga")
	if err := os.MkdirAll(seriesDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(seriesDir, "tsundoku.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write corrupt sidecar: %v", err)
	}

	_, _, _, err := disk.ReadCover(storage, "Manga", "Alpha Saga")
	if !errors.Is(err, disk.ErrNoLocalCover) {
		t.Fatalf("ReadCover(corrupt sidecar): err = %v, want ErrNoLocalCover", err)
	}
}

// TestReadCover_MissingCoverBlock proves a sidecar without a cover block (every
// pre-feature series) reports no local cover.
func TestReadCover_MissingCoverBlock(t *testing.T) {
	storage := t.TempDir()
	seriesDir := disk.SeriesDir(storage, "Manga", "Alpha Saga")
	if err := disk.WriteSidecar(seriesDir, disk.Sidecar{Title: "Alpha Saga", Category: "Manga"}); err != nil {
		t.Fatalf("WriteSidecar: %v", err)
	}

	_, _, _, err := disk.ReadCover(storage, "Manga", "Alpha Saga")
	if !errors.Is(err, disk.ErrNoLocalCover) {
		t.Fatalf("ReadCover(no cover block): err = %v, want ErrNoLocalCover", err)
	}
}

// TestReadCover_MissingFileIsAbsent proves a sidecar that points at a cover file
// which no longer exists on disk reports no local cover (re-fetch), not an error.
func TestReadCover_MissingFileIsAbsent(t *testing.T) {
	storage := t.TempDir()
	if _, err := disk.SaveCover(coverReq(storage, []byte("x"), "jpg", "/u")); err != nil {
		t.Fatalf("SaveCover: %v", err)
	}
	seriesDir := disk.SeriesDir(storage, "Manga", "Alpha Saga")
	if err := os.Remove(filepath.Join(seriesDir, "cover.jpg")); err != nil {
		t.Fatalf("remove cover: %v", err)
	}

	_, _, _, err := disk.ReadCover(storage, "Manga", "Alpha Saga")
	if !errors.Is(err, disk.ErrNoLocalCover) {
		t.Fatalf("ReadCover(missing file): err = %v, want ErrNoLocalCover", err)
	}
}

// TestSaveCover_ConcurrentWithRenderKeepsSidecarIntact is the load-bearing
// concurrency proof: SaveCover and RenderChapter both read-modify-write the same
// tsundoku.json, so they MUST share the per-series-dir lock. Without it the
// last writer wins and one of the two blocks (chapter provenance or cover) is
// silently lost — and the fixed ".tmp" path collides.
func TestSaveCover_ConcurrentWithRenderKeepsSidecarIntact(t *testing.T) {
	storage := t.TempDir()
	const n = 8

	var wg sync.WaitGroup
	for i := range n {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if _, err := disk.SaveCover(coverReq(storage, []byte("cover"), "jpg", "/u")); err != nil {
				t.Errorf("SaveCover: %v", err)
			}
		}()
		go func(i int) {
			defer wg.Done()
			num := float64(i + 1)
			_, err := disk.RenderChapter(disk.RenderRequest{
				Storage: storage,
				Meta: disk.RenderMeta{
					Provider:    "mangadex",
					Language:    "en",
					SeriesTitle: "Alpha Saga",
					Category:    disk.CategoryManga,
					Number:      &num,
					ChapterKey:  strconv.Itoa(i + 1),
					Importance:  10,
				},
				Pages: []fetcher.PageImage{{Data: []byte{0x00}, Ext: "jpg"}},
			})
			if err != nil {
				t.Errorf("RenderChapter: %v", err)
			}
		}(i)
	}
	wg.Wait()

	seriesDir := disk.SeriesDir(storage, "Manga", "Alpha Saga")
	sc, err := disk.ReadSidecar(seriesDir)
	if err != nil || sc == nil {
		t.Fatalf("ReadSidecar: %v (sidecar %v)", err, sc)
	}
	if sc.Cover == nil || sc.Cover.File != "cover.jpg" {
		t.Errorf("cover block lost under concurrency: %+v", sc.Cover)
	}
	if len(sc.Chapters) != n {
		t.Errorf("chapters = %d, want %d (a provenance entry was lost)", len(sc.Chapters), n)
	}
}
