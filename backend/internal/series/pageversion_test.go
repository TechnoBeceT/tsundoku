package series_test

import (
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/series"
)

// TestPageVersion_StableForUnchangedChapter proves the reader's page-bytes
// cache buster is deterministic: calling it twice with the exact same
// filename + download_date yields the exact same version, which is what lets
// the browser (and the prefetcher) recognise "still the same CBZ" across
// requests.
func TestPageVersion_StableForUnchangedChapter(t *testing.T) {
	dl := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

	got1 := series.PageVersion("[Comix] Alpha Saga 001.cbz", &dl)
	got2 := series.PageVersion("[Comix] Alpha Saga 001.cbz", &dl)

	if got1 == "" {
		t.Fatal("PageVersion: want non-empty version for a downloaded chapter, got empty")
	}
	if got1 != got2 {
		t.Fatalf("PageVersion: not stable — got %q then %q for identical inputs", got1, got2)
	}
}

// TestPageVersion_ChangesOnDownloadDate proves a Library-Convergence upgrade
// (which re-renders the CBZ and re-stamps download_date via
// download/upgrade.go's SetDownloadDate, keeping the SAME filename in the
// common case) earns a NEW version — the whole point being that the old
// version's cached bytes are never served for the replaced file.
func TestPageVersion_ChangesOnDownloadDate(t *testing.T) {
	before := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	after := before.Add(time.Hour)

	v1 := series.PageVersion("[Comix] Alpha Saga 001.cbz", &before)
	v2 := series.PageVersion("[Comix] Alpha Saga 001.cbz", &after)

	if v1 == v2 {
		t.Fatalf("PageVersion: want a different version after download_date changes, got the same %q for both", v1)
	}
}

// TestPageVersion_ChangesOnFilename proves the other half of CBZ identity: a
// re-render that lands under a different filename (e.g. a source/scanlator
// change on convergence) also earns a new version, even with the same
// download_date.
func TestPageVersion_ChangesOnFilename(t *testing.T) {
	dl := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

	v1 := series.PageVersion("[Comix] Alpha Saga 001.cbz", &dl)
	v2 := series.PageVersion("[AsuraScans] Alpha Saga 001.cbz", &dl)

	if v1 == v2 {
		t.Fatalf("PageVersion: want a different version for a different filename, got the same %q for both", v1)
	}
}

// TestPageVersion_EmptyCases proves a chapter with nothing to version — no
// filename (never downloaded) or no recorded download_date — returns "" and
// NOT a hash of the empty/zero inputs (which would be a non-empty string that
// could be mistaken for a real version).
func TestPageVersion_EmptyCases(t *testing.T) {
	dl := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

	tests := map[string]struct {
		filename     string
		downloadDate *time.Time
	}{
		"no filename":      {filename: "", downloadDate: &dl},
		"no download date": {filename: "[Comix] Alpha Saga 001.cbz", downloadDate: nil},
		"neither":          {filename: "", downloadDate: nil},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := series.PageVersion(tc.filename, tc.downloadDate); got != "" {
				t.Fatalf("PageVersion(%q, %v): want \"\", got %q", tc.filename, tc.downloadDate, got)
			}
		})
	}
}
