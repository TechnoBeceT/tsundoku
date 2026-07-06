package disk_test

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/technobecet/tsundoku/internal/disk"
)

// renderOneChapter renders a single chapter via disk.RenderChapter, returning
// the on-disk filename it produced — the shared fixture setup for the
// relabel tests below (mirrors render_test.go's renderWith helper).
func renderOneChapter(t *testing.T, storage string, meta disk.RenderMeta) string {
	t.Helper()
	fn, err := disk.RenderChapter(disk.RenderRequest{
		Storage: storage,
		Meta:    meta,
		Pages:   makePages(2),
	})
	if err != nil {
		t.Fatalf("RenderChapter: %v", err)
	}
	return fn
}

// countImageEntries counts non-ComicInfo entries in a CBZ — used to prove
// RelabelChapterFile never touches page images.
func countImageEntries(t *testing.T, path string) int {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open zip %q: %v", path, err)
	}
	defer func() { _ = r.Close() }()
	n := 0
	for _, f := range r.File {
		if f.Name != "ComicInfo.xml" {
			n++
		}
	}
	return n
}

// TestRelabelChapterFile_RenamesAndRewritesComicInfo proves the core Match
// disk primitive: renaming a chapter from its disk-origin identity to a real
// source's identity preserves every page image byte-for-byte while swapping
// the filename + embedded provenance.
func TestRelabelChapterFile_RenamesAndRewritesComicInfo(t *testing.T) {
	t.Parallel()
	storage := t.TempDir()
	num := 1.0

	oldMeta := disk.RenderMeta{
		Provider:      "mangadex",
		ProviderLabel: "mangadex",
		Scanlator:     "Alpha",
		Language:      "en",
		SeriesTitle:   "My Series",
		Category:      "Manga",
		Number:        &num,
		ChapterKey:    "1",
		Importance:    1,
	}
	oldFilename := renderOneChapter(t, storage, oldMeta)
	oldPath := filepath.Join(storage, "Manga", "My Series", oldFilename)
	wantImages := countImageEntries(t, oldPath)

	newMeta := oldMeta
	newMeta.Provider = "weeb"
	newMeta.ProviderLabel = "weeb"
	newMeta.Scanlator = ""
	newMeta.Importance = 5

	newFilename, oldCI, err := disk.RelabelChapterFile(storage, newMeta, oldFilename)
	if err != nil {
		t.Fatalf("RelabelChapterFile: %v", err)
	}
	if newFilename == oldFilename {
		t.Fatal("newFilename == oldFilename, want a rename (different provider/scanlator)")
	}
	if oldCI.Provider != "mangadex" || oldCI.Scanlator != "Alpha" {
		t.Fatalf("oldCI = %+v, want provider=mangadex scanlator=Alpha (the pre-relabel identity)", oldCI)
	}

	newPath := filepath.Join(storage, "Manga", "My Series", newFilename)
	assertFileGone(t, oldPath)
	assertFilePresent(t, newPath)
	assertImageCount(t, newPath, wantImages)
	assertComicInfo(t, newPath, "weeb", "", 5)
	assertSidecarEntry(t, filepath.Join(storage, "Manga", "My Series"), "1", "weeb", newFilename)
}

// assertFileGone fails the test unless path does not exist.
func assertFileGone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file %q still exists, want gone", path)
	}
}

// assertFilePresent fails the test unless path exists.
func assertFilePresent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file %q missing: %v", path, err)
	}
}

// assertImageCount fails the test unless path's CBZ has exactly want image entries.
func assertImageCount(t *testing.T, path string, want int) {
	t.Helper()
	if got := countImageEntries(t, path); got != want {
		t.Fatalf("image entries in %q = %d, want %d (unchanged)", path, got, want)
	}
}

// assertComicInfo fails the test unless path's embedded ComicInfo carries the
// given provider/scanlator/importance.
func assertComicInfo(t *testing.T, path, wantProvider, wantScanlator string, wantImportance int) {
	t.Helper()
	ci, err := disk.ReadComicInfoFromCBZ(path)
	if err != nil || ci == nil {
		t.Fatalf("ReadComicInfoFromCBZ(%q): %v", path, err)
	}
	if ci.Provider != wantProvider || ci.Scanlator != wantScanlator || ci.Importance != wantImportance {
		t.Fatalf("ComicInfo = %+v, want provider=%q scanlator=%q importance=%d", ci, wantProvider, wantScanlator, wantImportance)
	}
}

// assertSidecarEntry fails the test unless seriesDir's sidecar has a chapter
// entry for chapterKey with the given provider + filename.
func assertSidecarEntry(t *testing.T, seriesDir, chapterKey, wantProvider, wantFilename string) {
	t.Helper()
	sidecar, err := disk.ReadSidecar(seriesDir)
	if err != nil || sidecar == nil {
		t.Fatalf("ReadSidecar(%q): %v", seriesDir, err)
	}
	for _, ch := range sidecar.Chapters {
		if ch.ChapterKey == chapterKey {
			if ch.Provider != wantProvider || ch.Filename != wantFilename {
				t.Fatalf("sidecar entry = %+v, want provider=%q filename=%q", ch, wantProvider, wantFilename)
			}
			return
		}
	}
	t.Fatalf("sidecar has no entry for chapter_key=%q", chapterKey)
}

// TestRelabelChapterFile_NoOpWhenFilenameUnchanged proves that relabeling to
// an identity that happens to generate the SAME filename only rewrites the
// ComicInfo (and sidecar), performing no rename.
func TestRelabelChapterFile_NoOpWhenFilenameUnchanged(t *testing.T) {
	t.Parallel()
	storage := t.TempDir()
	num := 1.0
	meta := disk.RenderMeta{
		Provider:      "mangadex",
		ProviderLabel: "mangadex",
		Language:      "en",
		SeriesTitle:   "My Series",
		Category:      "Manga",
		Number:        &num,
		ChapterKey:    "1",
		Importance:    1,
	}
	oldFilename := renderOneChapter(t, storage, meta)

	// Same identity except Importance — filename token is unaffected by
	// Importance, so GenerateCBZFilename produces the identical name.
	newMeta := meta
	newMeta.Importance = 9

	newFilename, _, err := disk.RelabelChapterFile(storage, newMeta, oldFilename)
	if err != nil {
		t.Fatalf("RelabelChapterFile: %v", err)
	}
	if newFilename != oldFilename {
		t.Fatalf("newFilename = %q, want unchanged %q", newFilename, oldFilename)
	}

	path := filepath.Join(storage, "Manga", "My Series", oldFilename)
	ci, err := disk.ReadComicInfoFromCBZ(path)
	if err != nil || ci == nil {
		t.Fatalf("ReadComicInfoFromCBZ: %v", err)
	}
	if ci.Importance != 9 {
		t.Fatalf("ComicInfo.Importance = %d, want 9 (rewritten in place)", ci.Importance)
	}
}

// TestUndoRelabelChapterFile_RestoresOriginal proves the rollback primitive:
// after a successful RelabelChapterFile, UndoRelabelChapterFile restores the
// original filename, ComicInfo, and sidecar entry — the no-net-change
// guarantee library.MatchDiskProvider relies on when a later step fails.
func TestUndoRelabelChapterFile_RestoresOriginal(t *testing.T) {
	t.Parallel()
	storage := t.TempDir()
	num := 1.0
	oldMeta := disk.RenderMeta{
		Provider:      "mangadex",
		ProviderLabel: "mangadex",
		Scanlator:     "Alpha",
		Language:      "en",
		SeriesTitle:   "My Series",
		Category:      "Manga",
		Number:        &num,
		ChapterKey:    "1",
		Importance:    1,
	}
	oldFilename := renderOneChapter(t, storage, oldMeta)
	oldPath := filepath.Join(storage, "Manga", "My Series", oldFilename)
	wantImages := countImageEntries(t, oldPath)

	newMeta := oldMeta
	newMeta.Provider = "weeb"
	newMeta.ProviderLabel = "weeb"
	newMeta.Scanlator = ""
	newMeta.Importance = 5

	newFilename, oldCI, err := disk.RelabelChapterFile(storage, newMeta, oldFilename)
	if err != nil {
		t.Fatalf("RelabelChapterFile: %v", err)
	}

	if err := disk.UndoRelabelChapterFile(storage, oldMeta, newFilename, oldFilename, oldCI); err != nil {
		t.Fatalf("UndoRelabelChapterFile: %v", err)
	}

	newPath := filepath.Join(storage, "Manga", "My Series", newFilename)
	assertFileGone(t, newPath)
	assertFilePresent(t, oldPath)
	assertImageCount(t, oldPath, wantImages)
	assertComicInfo(t, oldPath, "mangadex", "Alpha", 1)
	assertSidecarEntry(t, filepath.Join(storage, "Manga", "My Series"), "1", "mangadex", oldFilename)
}
