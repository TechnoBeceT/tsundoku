package disk

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/technobecet/tsundoku/internal/chapter"
)

// RemoveOtherChapterFiles removes every duplicate CBZ for a single chapter number
// in a series directory, keeping only keepFilename. It is the convergence-cleanup
// primitive: after a chapter converges onto a new winning source (or an owner
// dedup sweep), every OTHER .cbz whose parsed chapter number equals chapterNumber
// is deleted so exactly one file per chapter number remains on disk (the Komga
// one-file-per-chapter contract).
//
// Matching is by PARSED number, not string: a "10" target matches a zero-padded
// "…010.cbz" filename (the number is extracted from the filename's final token
// and compared via chapter.FormatChapterNumber, the same canonical form the
// renderer uses). Un-numbered or unparseable filenames are left untouched.
//
// It is BEST-EFFORT per file: a single os.Remove failure is logged and skipped
// (the sweep continues), and only successfully-removed files are counted. A
// missing series directory is a no-op (removed=0, nil) — a never-rendered series
// legitimately has no folder. keepFilename is NEVER removed, so the winning/only
// file is always preserved.
func RemoveOtherChapterFiles(storage, category, title, chapterNumber, keepFilename string) (removed int, err error) {
	target, err := parseNumber(chapterNumber)
	if err != nil {
		return 0, fmt.Errorf("disk.RemoveOtherChapterFiles: parse target number %q: %w", chapterNumber, err)
	}
	targetKey := chapter.FormatChapterNumber(target)

	dir := SeriesDir(storage, category, title)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("disk.RemoveOtherChapterFiles: read dir %q: %w", dir, err)
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == keepFilename || filepath.Ext(name) != ".cbz" {
			continue
		}
		num, ok := chapterNumberFromFilename(name)
		if !ok || chapter.FormatChapterNumber(num) != targetKey {
			continue
		}
		if rmErr := os.Remove(filepath.Join(dir, name)); rmErr != nil {
			// Best-effort: log and keep sweeping the rest of the directory.
			slog.Warn("disk.RemoveOtherChapterFiles: best-effort delete of duplicate CBZ failed",
				"path", filepath.Join(dir, name), "err", rmErr)
			continue
		}
		removed++
	}
	return removed, nil
}

// chapterNumberFromFilename extracts a chapter number from a CBZ filename.
// GenerateCBZFilename always renders the (zero-padded) chapter number as the
// final space-separated token before the ".cbz" extension, e.g.
// "[Provider] Series Title 012.5.cbz" → 12.5. The number string is parsed with the
// same parseNumber used elsewhere in this package (§2 DRY — no second parser).
// Returns ok=false for an un-numbered or unparseable name.
func chapterNumberFromFilename(name string) (num float64, ok bool) {
	base := strings.TrimSuffix(name, ".cbz")
	fields := strings.Fields(base)
	if len(fields) == 0 {
		return 0, false
	}
	last := fields[len(fields)-1]
	n, err := parseNumber(last)
	if err != nil {
		return 0, false
	}
	return n, true
}
