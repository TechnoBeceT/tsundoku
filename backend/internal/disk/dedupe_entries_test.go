package disk_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/technobecet/tsundoku/internal/disk"
)

// TestReadSeriesDir_ReturnsEntries proves the thin reader lists the series folder.
func TestReadSeriesDir_ReturnsEntries(t *testing.T) {
	storage := t.TempDir()
	const category, title = "Manga", "Read Series"
	seriesDir := disk.SeriesDir(storage, category, title)

	writeStubCBZ(t, seriesDir, "[A] 010.cbz")
	writeStubCBZ(t, seriesDir, "[B] 011.cbz")

	entries, err := disk.ReadSeriesDir(storage, category, title)
	if err != nil {
		t.Fatalf("ReadSeriesDir: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
}

// TestReadSeriesDir_MissingDirIsEmpty: a never-rendered series has no folder, and
// that is not an error — the caller must see an empty listing (mirrors
// ListOtherChapterFiles' missing-directory contract).
func TestReadSeriesDir_MissingDirIsEmpty(t *testing.T) {
	entries, err := disk.ReadSeriesDir(t.TempDir(), "Manga", "Ghost Series")
	if err != nil {
		t.Fatalf("ReadSeriesDir on a missing dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("entries = %d, want 0", len(entries))
	}
}

// TestReadSeriesDir_UnreadableDirErrors: a genuine read failure is surfaced, never
// silently swallowed into an empty listing (which would make the library-wide
// duplicate scan under-report instead of failing honestly).
func TestReadSeriesDir_UnreadableDirErrors(t *testing.T) {
	storage := t.TempDir()
	// The "series directory" is a regular FILE, so opening it as a directory fails
	// with something other than not-exist.
	if err := os.MkdirAll(filepath.Join(storage, "Manga"), 0o750); err != nil {
		t.Fatalf("mkdir category: %v", err)
	}
	if err := os.WriteFile(disk.SeriesDir(storage, "Manga", "Not A Dir"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if _, err := disk.ReadSeriesDir(storage, "Manga", "Not A Dir"); err == nil {
		t.Error("ReadSeriesDir on a non-directory returned nil error, want a surfaced failure")
	}
}

// listCases are the (target, keeper) combinations the matcher test drives, each
// with the EXACT filenames the strict rule must return from listFixture below — a
// normal match, a padded/unpadded numeric equivalence, a decimal with two
// duplicates, a junk target (matches nothing), and a target no file carries.
//
// The expectations are written out literally rather than derived from the other
// variant: ListOtherChapterFiles IS ReadSeriesDir + ListOtherChapterFilesIn, so
// comparing the two would run the same matcher on both sides and could never fail.
var listCases = []struct {
	name   string
	number string
	keep   string
	want   []string
}{
	// Only the OTHER file of chapter 10 goes; the keeper, the "10th" junk token,
	// the "1e3" scientific-notation token, the un-numbered names, the .txt and the
	// decoy DIRECTORY named "10.cbz" are all left alone.
	{"plain match", "10", "[A] 010.cbz", []string{"[B] 010.cbz"}},
	// A padded target canonicalises to the same key as the unpadded filename.
	{"padded target", "010", "[B] 010.cbz", []string{"[A] 010.cbz"}},
	// No keeper: every .cbz of the number is listed, in directory order.
	{"decimal", "12.5", "", []string{"[C] 012.5.cbz", "[D] 012.5.cbz"}},
	// A non-clean TARGET can never be compared safely, so it matches nothing —
	// not even the file whose own token is the identical junk.
	{"junk target", "10th", "[A] 010.cbz", nil},
	{"absent number", "999", "", nil},
}

// listFixture is the series-folder contents both list tests match against: two
// copies of chapter 10, two of 12.5, and four names the STRICT rule must refuse.
var listFixture = []string{
	"[A] 010.cbz",
	"[B] 010.cbz",
	"[C] 012.5.cbz",
	"[D] 012.5.cbz",
	"Something 10th.cbz",
	"X 1e3.cbz",
	"no-number.cbz",
	"notes.txt",
}

// TestListOtherChapterFilesIn_ListsExactlyTheStrictNumberMatches pins the pure
// matcher against a FIXED expected filename list per target shape. The library-wide
// duplicate scan is built on this variant, and it feeds a file-DELETION path, so
// what it does and does not match is asserted literally — a looser number parse
// must break the test, not merely change both sides of a comparison.
//
// Non-vacuity was proven by execution, not by argument: swapping
// strictChapterKey's plain-token guard + ParseFloat for a loose
// fmt.Sscanf(token, "%f") parse failed "plain match", "padded target" and "junk
// target", each listing "Something 10th.cbz" as a chapter-10 duplicate.
func TestListOtherChapterFilesIn_ListsExactlyTheStrictNumberMatches(t *testing.T) {
	entries := seedListFixture(t)

	for _, tc := range listCases {
		t.Run(tc.name, func(t *testing.T) {
			assertNames(t, disk.ListOtherChapterFilesIn(entries, tc.number, tc.keep), tc.want)
		})
	}
}

// TestListOtherChapterFiles_ListsExactlyTheStrictNumberMatches holds the
// directory-READING variant to the same fixed expectations. It is what pins the
// composition's wiring — that it reads the right folder and passes the target and
// the keeper through untouched — against the identical evidence.
func TestListOtherChapterFiles_ListsExactlyTheStrictNumberMatches(t *testing.T) {
	storage := t.TempDir()
	seedListFixtureIn(t, storage)

	for _, tc := range listCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := disk.ListOtherChapterFiles(storage, listCategory, listTitle, tc.number, tc.keep)
			if err != nil {
				t.Fatalf("ListOtherChapterFiles: %v", err)
			}
			assertNames(t, got, tc.want)
		})
	}
}

const listCategory, listTitle = "Manga", "Strict Match Series"

// seedListFixture writes listFixture into a fresh storage root and returns the
// read directory listing the pure matcher takes.
func seedListFixture(t *testing.T) []os.DirEntry {
	t.Helper()
	storage := t.TempDir()
	seedListFixtureIn(t, storage)

	entries, err := disk.ReadSeriesDir(storage, listCategory, listTitle)
	if err != nil {
		t.Fatalf("ReadSeriesDir: %v", err)
	}
	return entries
}

// seedListFixtureIn writes listFixture plus the decoy DIRECTORY named "10.cbz"
// (an entry whose name parses as chapter 10 but which must never be listed).
func seedListFixtureIn(t *testing.T, storage string) {
	t.Helper()
	seriesDir := disk.SeriesDir(storage, listCategory, listTitle)
	for _, name := range listFixture {
		writeStubCBZ(t, seriesDir, name)
	}
	if err := os.MkdirAll(filepath.Join(seriesDir, "10.cbz"), 0o750); err != nil {
		t.Fatalf("mkdir decoy directory: %v", err)
	}
}

// assertNames compares a listing against its expected filenames, order included
// (both variants list in directory order).
func assertNames(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("names = %v, want %v", got, want)
		}
	}
}
