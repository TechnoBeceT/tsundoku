package sourceengine_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourceengine/fake"
)

// TestFetcher_Resume_StoredLinksAndPartialStaging is the core byte-cache proof
// (GAP-099): a retry of a chapter whose page links are STORED (carried on the
// FetchRef) and whose first pages are already STAGED on disk makes ZERO Client.Pages
// calls and re-fetches ONLY the missing pages — the whole point of the ban fix, so a
// retry never re-hits the source's page-resolution step and never re-downloads a page
// it already has.
//
// It drives two real Fetch attempts against ONE staging root + ONE ProviderChapter id
// (so both attempts share the same on-disk staging dir). Attempt 1 serves pages 0-1
// but not 2 (a missing image → broken → the chapter fails, staging pages 0-1);
// attempt 2 serves all three (page 2 is now available). Assertions are on the SECOND
// attempt's counting client: 0 Pages calls (links reused) and exactly 1 Image call
// (only the missing page 2).
func TestFetcher_Resume_StoredLinksAndPartialStaging(t *testing.T) {
	stagingRoot := t.TempDir()
	pcID := uuid.New()
	jpg := validJPEG(t)

	links := []fetcher.PageLink{
		{URL: "/ch/1/page/0", ImageURL: "https://x/p0"},
		{URL: "/ch/1/page/1", ImageURL: "https://x/p1"},
		{URL: "/ch/1/page/2", ImageURL: "https://x/p2"},
	}
	ref := fetcher.FetchRef{
		Provider:          "7",
		URL:               "/ch/1",
		ProviderChapterID: pcID,
		PageLinks:         links, // STORED links ⇒ Pages must never be called
	}

	// Attempt 1: only pages 0 and 1 are serveable. Page 2's image is unconfigured, so
	// the fake returns an empty body → the validating fetcher rejects it (broken page)
	// and the whole attempt fails — but pages 0 and 1 are now staged on disk.
	client1 := fake.New(
		fake.WithImage(7, "/ch/1/page/0", jpg, "image/jpeg"),
		fake.WithImage(7, "/ch/1/page/1", jpg, "image/jpeg"),
	)
	f1 := sourceengine.NewFetcher(client1, stagingRoot)
	if _, err := f1.Fetch(context.Background(), ref); err == nil {
		t.Fatal("attempt 1: want an error (page 2 unavailable), got nil")
	}
	if n := client1.CallCount("Pages"); n != 0 {
		t.Errorf("attempt 1: Pages called %d times, want 0 (stored links must be re-used)", n)
	}
	// Pages 0 and 1 were downloaded; page 2 was attempted and failed.
	if n := client1.CallCount("Image"); n != 3 {
		t.Errorf("attempt 1: Image called %d times, want 3 (pages 0,1 staged; page 2 attempted+failed)", n)
	}
	assertStagedIndexes(t, filepath.Join(stagingRoot, pcID.String()), []int{0, 1})

	// Attempt 2: page 2 is now serveable. The retry must re-use the two staged pages
	// (no Image call for them) and re-fetch ONLY page 2 — and still never call Pages.
	client2 := fake.New(
		fake.WithImage(7, "/ch/1/page/0", jpg, "image/jpeg"),
		fake.WithImage(7, "/ch/1/page/1", jpg, "image/jpeg"),
		fake.WithImage(7, "/ch/1/page/2", jpg, "image/jpeg"),
	)
	f2 := sourceengine.NewFetcher(client2, stagingRoot)
	got, err := f2.Fetch(context.Background(), ref)
	if err != nil {
		t.Fatalf("attempt 2: %v", err)
	}
	if n := client2.CallCount("Pages"); n != 0 {
		t.Errorf("attempt 2: Pages called %d times, want 0 (stored links, zero re-resolution)", n)
	}
	if n := client2.CallCount("Image"); n != 1 {
		t.Errorf("attempt 2: Image called %d times, want 1 (ONLY the missing page 2 re-fetched)", n)
	}
	if got.PageCount != 3 || len(got.Pages) != 3 {
		t.Fatalf("attempt 2: PageCount/len(Pages) = %d/%d, want 3/3", got.PageCount, len(got.Pages))
	}
}

// assertStagedIndexes asserts the staging dir contains exactly one page file per
// wanted index (named "<index>.<ext>") and no others.
func assertStagedIndexes(t *testing.T, dir string, want []int) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read staging dir %s: %v", dir, err)
	}
	if len(entries) != len(want) {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("staging dir has %d files %v, want %d", len(entries), names, len(want))
	}
}
