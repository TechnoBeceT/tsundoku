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

// TestPageVersion_ImportedChapterVersionDiffersOnceDownloaded pins the SAFETY
// property behind the nil-download_date carve-out.
//
// disk.Reconcile (the Kaizoku/disk-import path — most of this library) creates
// chapters with a filename but NO download_date, so PageVersion hashes a
// "no-date" sentinel to still produce a real, cacheable version. The danger in
// that carve-out is subtle: if the sentinel ever collided with a real dated
// version, an imported chapter that is LATER upgraded (its bytes replaced, and
// a download_date stamped for the first time) would keep serving the OLD cached
// pages for a full day.
//
// So: the same filename, nil date vs a real date, MUST hash differently. This
// holds by construction today — but it is the one assertion that would catch a
// future "simplification" of the sentinel, which is why it is pinned here.
func TestPageVersion_ImportedChapterVersionDiffersOnceDownloaded(t *testing.T) {
	const filename = "[Asura Scans] Imported Saga 001.cbz"
	dl := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

	imported := series.PageVersion(filename, nil) // as disk.Reconcile leaves it
	upgraded := series.PageVersion(filename, &dl) // after a download/upgrade re-render

	if imported == "" {
		t.Fatal("PageVersion: an imported chapter (filename, no download_date) must still be versioned — an empty version means no ?v=, no cache, and no ETag")
	}
	if imported == upgraded {
		t.Fatalf("PageVersion: nil-date and dated versions collided (%q) — an upgraded import would serve stale cached pages for a day", imported)
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

// TestPageVersion_EmptyCases proves the ONLY thing that has nothing to
// version is a genuinely empty filename (never downloaded) — NOT a nil
// download_date, which is exactly what `disk.Reconcile` leaves on every
// disk-imported/Kaizoku-migrated chapter (see the FIX 1 tests below). Empty
// input returns "" and NOT a hash of the empty/zero inputs (which would be a
// non-empty string that could be mistaken for a real version).
func TestPageVersion_EmptyCases(t *testing.T) {
	dl := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

	tests := map[string]struct {
		filename     string
		downloadDate *time.Time
	}{
		"no filename":                   {filename: "", downloadDate: &dl},
		"no filename, no download date": {filename: "", downloadDate: nil},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := series.PageVersion(tc.filename, tc.downloadDate); got != "" {
				t.Fatalf("PageVersion(%q, %v): want \"\", got %q", tc.filename, tc.downloadDate, got)
			}
		})
	}
}

// TestPageVersion_NonEmptyWithoutDownloadDate is the FIX 1 regression proof:
// disk.Reconcile (adoptChapter/updateChapter — the path every disk-imported /
// Kaizoku-migrated chapter comes through) sets `filename` but NEVER
// `download_date`. Before the fix, PageVersion returned "" for exactly this
// shape, which meant most of the owner's ~1000-series imported library never
// earned a cacheable, ETag'd page response — a REGRESSION versus the flat
// max-age=300 this feature replaced. A filename with no download_date must
// yield a non-empty, stable version.
func TestPageVersion_NonEmptyWithoutDownloadDate(t *testing.T) {
	got1 := series.PageVersion("[Comix] Alpha Saga 001.cbz", nil)
	got2 := series.PageVersion("[Comix] Alpha Saga 001.cbz", nil)

	if got1 == "" {
		t.Fatal("PageVersion: want a non-empty version for a filename with no download_date (the disk.Reconcile shape), got empty")
	}
	if got1 != got2 {
		t.Fatalf("PageVersion: not stable — got %q then %q for identical nil-download_date inputs", got1, got2)
	}
}

// TestPageVersion_NoDownloadDateStillDiscriminatesByFilename proves the
// nil-download_date version isn't a single constant sentinel shared by every
// chapter — a different filename (still no download_date) must still earn a
// different version, so two distinct reconciled chapters never collide.
func TestPageVersion_NoDownloadDateStillDiscriminatesByFilename(t *testing.T) {
	v1 := series.PageVersion("[Comix] Alpha Saga 001.cbz", nil)
	v2 := series.PageVersion("[AsuraScans] Alpha Saga 001.cbz", nil)

	if v1 == v2 {
		t.Fatalf("PageVersion: want a different version for a different filename even with no download_date, got the same %q for both", v1)
	}
}
