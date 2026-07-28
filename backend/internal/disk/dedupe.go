package disk

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
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
// Matching is by STRICT full-token number, not string: a "10" target matches a
// zero-padded "…010.cbz" filename (both canonicalise to the same
// chapter.FormatChapterNumber key). CRUCIALLY, this is a DELETION path over an
// owner's imported library whose CBZs carry arbitrary original filenames, so the
// number match is STRICT (strictChapterKey): a filename's final token counts as
// "chapter N" ONLY when it is a plain unambiguous number (digits + at most one
// decimal point). A token with trailing junk — "10th", "5-extra", "1e3" — is NOT
// treated as a chapter number and is left ALONE, so a stray file can never be
// mis-parsed into a real chapter number and false-deleted. Un-numbered or
// non-strict filenames are always skipped.
//
// It is BEST-EFFORT per file: a single os.Remove failure is logged and skipped
// (the sweep continues), and only successfully-removed files are counted. A
// missing series directory is a no-op (removed=0, nil) — a never-rendered series
// legitimately has no folder. A target number that is not itself a clean number
// deletes nothing. keepFilename is NEVER removed, so the winning/only file is
// always preserved.
func RemoveOtherChapterFiles(storage, category, title, chapterNumber, keepFilename string) (removed int, err error) {
	names, err := ListOtherChapterFiles(storage, category, title, chapterNumber, keepFilename)
	if err != nil {
		return 0, err
	}

	dir := SeriesDir(storage, category, title)
	for _, name := range names {
		path := filepath.Join(dir, name)
		if rmErr := os.Remove(path); rmErr != nil {
			// Best-effort: log and keep sweeping the rest of the directory.
			slog.Warn("disk.RemoveOtherChapterFiles: best-effort delete of duplicate CBZ failed",
				"path", path, "err", rmErr)
			continue
		}
		removed++
	}
	return removed, nil
}

// ListOtherChapterFiles is the read-only enumerator behind RemoveOtherChapterFiles:
// it returns the CBZ filenames in the series directory that RemoveOtherChapterFiles
// WOULD delete for chapterNumber (every .cbz sharing that STRICT chapter number
// except keepFilename), WITHOUT touching the disk. It is the shared source of truth
// the DedupeFiles preview lists and the executor deletes, so the dry-run list can
// never drift from what the sweep removes.
//
// Matching is identical to RemoveOtherChapterFiles (see its doc): STRICT full-token
// number (strictChapterKey) so an imported library's arbitrary filenames can never
// be mis-parsed into a real chapter number. A non-clean target number, or a missing
// series directory, yields no names (nil, nil) — never an error; only a genuine
// directory-read failure is returned. (Precisely: the directory is read BEFORE the
// target number is judged, so a non-clean number over an UNREADABLE directory
// surfaces that read failure rather than returning nil. Both inputs are broken
// there, and an unreadable library folder is worth reporting either way.)
//
// It is the READ-then-MATCH composition of ReadSeriesDir + ListOtherChapterFilesIn,
// so one directory read serves one chapter number. A caller that sweeps MANY
// chapter numbers in the same series (the library-wide duplicate scan) must call
// those two directly instead: read the folder ONCE and reuse the listing, or it
// pays a directory read per chapter.
func ListOtherChapterFiles(storage, category, title, chapterNumber, keepFilename string) ([]string, error) {
	entries, err := ReadSeriesDir(storage, category, title)
	if err != nil {
		return nil, err
	}
	return ListOtherChapterFilesIn(entries, chapterNumber, keepFilename), nil
}

// ReadSeriesDir lists a series' library folder — the thin, single directory read
// behind every duplicate-file enumeration. A series that was never rendered has no
// folder, so a not-exist error is an EMPTY listing (nil, nil), not a failure; any
// other read failure is returned so a library-wide sweep fails honestly instead of
// silently under-reporting.
func ReadSeriesDir(storage, category, title string) ([]os.DirEntry, error) {
	dir := SeriesDir(storage, category, title)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("disk.ReadSeriesDir: read dir %q: %w", dir, err)
	}
	return entries, nil
}

// ListOtherChapterFilesIn is the PURE matcher behind ListOtherChapterFiles: given
// an ALREADY-READ series-directory listing, it returns the CBZ filenames that
// share chapterNumber's STRICT chapter number, except keepFilename. It touches no
// disk at all.
//
// It exists so a caller sweeping many chapter numbers in ONE series reads the
// folder once (ReadSeriesDir) and reuses the listing, instead of re-reading it per
// chapter. Both callers share this ONE matcher — and therefore the one
// strictChapterKey / isPlainNumberToken rule — so the per-series and library-wide
// paths can never drift on what counts as a duplicate.
//
// A non-clean target number matches nothing: it can never be safely compared, so
// listing nothing is the only safe answer (this feeds a deletion path).
func ListOtherChapterFilesIn(entries []os.DirEntry, chapterNumber, keepFilename string) []string {
	targetKey, ok := strictChapterKey(chapterNumber)
	if !ok {
		return nil
	}

	var names []string
	for _, e := range entries {
		if isRemovableDuplicate(e, keepFilename, targetKey) {
			names = append(names, e.Name())
		}
	}
	return names
}

// isRemovableDuplicate reports whether a directory entry is a CBZ (other than
// keepFilename) whose STRICT chapter number canonicalises to targetKey — i.e. a
// superseded duplicate of the same chapter that RemoveOtherChapterFiles should
// delete. Directories, non-.cbz files, the keeper, and un-numbered / non-strict
// (trailing-junk) names are never removable.
func isRemovableDuplicate(e os.DirEntry, keepFilename, targetKey string) bool {
	if e.IsDir() {
		return false
	}
	name := e.Name()
	if name == keepFilename || filepath.Ext(name) != ".cbz" {
		return false
	}
	key, ok := strictChapterNumberFromFilename(name)
	if !ok {
		return false
	}
	return key == targetKey
}

// strictChapterNumberFromFilename extracts the STRICT chapter-number key from a
// CBZ filename. GenerateCBZFilename renders the (zero-padded) chapter number as
// the final space-separated token before ".cbz" (e.g.
// "[Provider] Series Title 012.5.cbz" → "12.5"). Returns ok=false when the final
// token is not a clean, unambiguous number — this is what makes the dedup DELETE
// path safe against an imported library's arbitrary filenames (see strictChapterKey).
func strictChapterNumberFromFilename(name string) (key string, ok bool) {
	base := strings.TrimSuffix(name, ".cbz")
	fields := strings.Fields(base)
	if len(fields) == 0 {
		return "", false
	}
	return strictChapterKey(fields[len(fields)-1])
}

// strictChapterKey canonicalises a chapter-number TOKEN to its
// chapter.FormatChapterNumber key, but ONLY when the token is a plain,
// unambiguous number: one or more digits with at most a single decimal point
// (e.g. "010", "12.5"). It deliberately REJECTS anything the loose
// fmt.Sscanf("%f") parser would partial-accept — trailing junk ("10th",
// "5-extra"), scientific notation ("1e3"), signs, or hex — returning ok=false so
// the dedup path never treats such a file as a chapter number and never deletes
// it. This is intentionally STRICTER than the shared reconcile parseNumber (which
// stays loose for round-tripping ComicInfo numbers); the extra strictness lives
// here because this is an irreversible file-deletion path.
func strictChapterKey(token string) (key string, ok bool) {
	if !isPlainNumberToken(token) {
		return "", false
	}
	n, err := strconv.ParseFloat(token, 64)
	if err != nil {
		// Defensive: isPlainNumberToken already guarantees a parseable form, so a
		// ParseFloat error here is unreachable for a validated token — guard anyway.
		return "", false
	}
	return chapter.FormatChapterNumber(n), true
}

// isPlainNumberToken reports whether token is a bare decimal number: at least one
// ASCII digit and at most one '.', with no other characters. This rejects
// scientific notation ('e'/'E'), signs ('+'/'-'), hex, and any trailing/leading
// junk, so only unambiguous chapter-number tokens qualify for the dedup match.
func isPlainNumberToken(token string) bool {
	if token == "" {
		return false
	}
	seenDot, seenDigit := false, false
	for _, r := range token {
		switch {
		case r >= '0' && r <= '9':
			seenDigit = true
		case r == '.':
			if seenDot {
				return false
			}
			seenDot = true
		default:
			return false
		}
	}
	return seenDigit
}
