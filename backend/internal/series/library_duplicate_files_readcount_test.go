// Package series (white-box) — the read-count proof for the library-wide
// duplicate-file scan.
//
// This is the counting-driver equivalent for the FILESYSTEM: the SQL side is
// pinned by the counting Ent driver (query_count_test.go), and this pins the
// directory reads the same way. It is white-box because the seam it counts —
// duplicateFilesRollup's directory reader — is unexported; exporting it would be
// production surface added for a test. The rows are built in memory (the rollup is
// a pure function of already-loaded rows plus a listing), so no database is needed.
package series

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
)

// buildLoadedSeries builds one in-memory series row shaped exactly like
// loadAllSeriesForCleanup returns: a Category edge, one provider carrying every
// chapter, and `chapters` downloaded chapters each with its own winning filename.
func buildLoadedSeries(title string, chapters int) *ent.Series {
	row := &ent.Series{ID: uuid.New(), Title: title}
	row.Edges.Category = &ent.Category{Name: "Manga"}

	provider := &ent.SeriesProvider{ID: uuid.New(), Provider: "comix", Importance: 60}
	for i := 1; i <= chapters; i++ {
		key := fmt.Sprintf("%d", i)
		number := float64(i)
		provider.Edges.ProviderChapters = append(provider.Edges.ProviderChapters,
			&ent.ProviderChapter{ChapterKey: key, Number: &number})
		row.Edges.Chapters = append(row.Edges.Chapters, &ent.Chapter{
			ID:         uuid.New(),
			ChapterKey: key,
			Number:     &number,
			State:      entchapter.StateDownloaded,
			Filename:   key + ".cbz",
		})
	}
	row.Edges.Providers = []*ent.SeriesProvider{provider}
	return row
}

// writeDecoyListing writes one removable leftover CBZ per chapter into a directory
// that is NOT any series' real folder, and returns it. The counting reader serves
// this listing; the service's storage root stays empty, so any code path that
// re-read a real series folder per chapter would find NOTHING removable and the
// file-count assertion below would fail. That is what makes the test non-vacuous:
// it proves the per-chapter match consumed the PASSED-IN listing.
func writeDecoyListing(t *testing.T, chapters int) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "decoy")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir decoy: %v", err)
	}
	for i := 1; i <= chapters; i++ {
		name := fmt.Sprintf("[old][en] Leftover %03d.cbz", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("dup"), 0o600); err != nil {
			t.Fatalf("write decoy leftover: %v", err)
		}
	}
	return dir
}

// TestDuplicateFilesRollupReadsEachSeriesDirectoryOnce is the no-N+1 proof for the
// FILESYSTEM: the library-wide duplicate scan must read each series folder ONCE
// and reuse that listing for every one of its chapters. A read per chapter is what
// made this endpoint unusable at library scale (tens of thousands of directory
// reads across a few hundred series), so the fix is pinned as a SLOPE — growing
// the chapters-per-series 12x must not move the read count at all.
func TestDuplicateFilesRollupReadsEachSeriesDirectoryOnce(t *testing.T) {
	for _, tc := range []struct{ seriesCount, chaptersEach int }{
		{3, 1},
		{3, 12},
	} {
		t.Run(fmt.Sprintf("%dseries_%dchapters", tc.seriesCount, tc.chaptersEach), func(t *testing.T) {
			decoy := writeDecoyListing(t, tc.chaptersEach)

			rows := make([]*ent.Series, 0, tc.seriesCount)
			for i := range tc.seriesCount {
				rows = append(rows, buildLoadedSeries(fmt.Sprintf("Read Count %02d", i), tc.chaptersEach))
			}

			reads := 0
			counting := func(_, _, _ string) ([]os.DirEntry, error) {
				reads++
				return os.ReadDir(decoy)
			}

			svc := NewService(nil, t.TempDir(), 14)
			dto, err := svc.duplicateFilesRollup(rows, counting)
			if err != nil {
				t.Fatalf("duplicateFilesRollup: %v", err)
			}

			if reads != tc.seriesCount {
				t.Errorf("directory reads = %d, want %d (one per series, never one per chapter)",
					reads, tc.seriesCount)
			}
			// Each series' chapters each match exactly one leftover in the decoy
			// listing, so a non-zero total proves the plan was resolved FROM the
			// passed-in entries rather than a re-read of the empty real folder.
			if want := tc.seriesCount * tc.chaptersEach; dto.TotalFiles != want {
				t.Errorf("totalFiles = %d, want %d — the per-chapter match must use the passed-in listing",
					dto.TotalFiles, want)
			}
		})
	}
}
