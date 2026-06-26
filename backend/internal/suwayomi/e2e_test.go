//go:build suwayomi

// Package suwayomi_test — real-Suwayomi end-to-end integration tests.
//
// Build tag: suwayomi. Run with:
//
//	go test -tags suwayomi -v -timeout 15m ./internal/suwayomi/...
//
// These tests require:
//   - Docker (for the ephemeral PostgreSQL container via testdb)
//   - Java 21+ (auto-detected from /usr/lib/jvm/*/bin/java)
//   - Network access (to download the Suwayomi v2.2.2100 JAR on first run)
//
// The Suwayomi instance is shared across all tests in this file via the
// testharness singleton. It is launched once per test-binary run and torn
// down via t.Cleanup at run end.
//
// # GraphQL shape validation (Task 4 items)
//
// Three items from client.go were flagged in Task 4 for live validation:
//  1. Chapter filter operator: chapters(filter:{mangaId:{equalTo:N}})
//  2. fetchChapterPages page-URL format (relative path vs. absolute URL).
//  3. LongString scalar acceptance for sourceId in fetchSourceManga.
//
// TestValidateGraphQLShapes (below) covers all three explicitly so the test
// output documents whether the assumptions held or were corrected.
package suwayomi_test

import (
	"archive/zip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/sse"
	"github.com/technobecet/tsundoku/internal/suwayomi"
	"github.com/technobecet/tsundoku/internal/suwayomi/testharness"
)

// TestMain launches the shared Suwayomi instance before any test runs and
// tears it down after m.Run() returns. This ensures the server lifetime spans
// all tests in the binary, regardless of which test calls Shared(t) first.
//
// IMPORTANT: when unit tests re-exec this binary as a helper subprocess (the
// TestHelperProcess pattern in helpers_test.go), GO_SUWAYOMI_TEST_HELPER is
// set. In that case we must skip the real Suwayomi setup so the subprocess
// does not try to start a second Suwayomi (which would fail due to port
// conflict) and instead just runs TestHelperProcess.
func TestMain(m *testing.M) {
	// Detect helper-subprocess invocations (see helpers_test.go). If this
	// binary was re-executed by helperCmd, skip the Suwayomi setup entirely.
	if os.Getenv("GO_SUWAYOMI_TEST_HELPER") != "" {
		os.Exit(m.Run())
	}

	// Find a Java 21+ JVM. If none is found, skip the whole binary gracefully.
	javaPath, err := testharness.FindJava21()
	if err != nil {
		fmt.Fprintf(os.Stderr, "suwayomi harness: skipping all tests — %v\n", err)
		os.Exit(0) // skip, not failure
	}

	if err := testharness.Setup(javaPath); err != nil {
		fmt.Fprintf(os.Stderr, "suwayomi harness: setup failed — %v\n", err)
		testharness.GlobalCleanup()
		os.Exit(1)
	}

	code := m.Run()
	testharness.GlobalCleanup()
	os.Exit(code)
}

// TestShape1_LongString_SourceID validates Task-4 shape assumption 1:
// sourceId is typed as LongString! in the Suwayomi GraphQL schema.
// A schema-type error on Search would mean the assumption is wrong.
func TestShape1_LongString_SourceID(t *testing.T) {
	inst := testharness.Shared(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	results, err := inst.Client().Search(ctx, testharness.LocalSourceID, testharness.FixtureMangaTitle)
	if err != nil {
		t.Fatalf("Search with sourceId=%q: %v\n(check: is LongString! the correct scalar for sourceId?)", testharness.LocalSourceID, err)
	}
	t.Logf("CONFIRMED: LongString! scalar accepted for sourceId; got %d results", len(results))
}

// TestShape2_ChapterFilter_EqualTo validates Task-4 shape assumption 2:
// chapters are queried with filter:{mangaId:{equalTo:N}}.
//
// CORRECTION discovered by Task-7 live validation: chapters are not cached by
// Suwayomi until fetchChapters is called. We must call FetchChapters first,
// then verify that the chapters(filter:{mangaId:{equalTo:N}}) query returns the
// same data. Both the fetchChapters mutation and the chapters query are validated.
func TestShape2_ChapterFilter_EqualTo(t *testing.T) {
	inst := testharness.Shared(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := inst.Client()
	results, err := client.Search(ctx, testharness.LocalSourceID, testharness.FixtureMangaTitle)
	if err != nil {
		t.Skipf("Search failed (skipping shape2): %v", err)
	}
	if len(results) == 0 {
		t.Skip("no search results (local source may not have indexed; skipping shape2)")
	}

	mangaID := results[0].ID

	// FetchChapters (mutation) populates Suwayomi's cache from the source.
	fetched, err := client.FetchChapters(ctx, mangaID)
	if err != nil {
		t.Fatalf("FetchChapters(mangaId=%d): %v", mangaID, err)
	}
	t.Logf("CONFIRMED: fetchChapters mutation returned %d chapters for mangaId=%d", len(fetched), mangaID)

	// MangaChapters (query) reads from the now-populated cache.
	cached, err := client.MangaChapters(ctx, mangaID)
	if err != nil {
		t.Fatalf("MangaChapters(mangaId=%d): %v\n(check: is equalTo the correct filter operator?)", mangaID, err)
	}
	t.Logf("CONFIRMED: chapters(filter:{mangaId:{equalTo:%d}}) returned %d chapters", mangaID, len(cached))
}

// TestShape3_ChapterPages_URLFormat validates Task-4 shape assumption 3:
// fetchChapterPages returns relative path URLs (e.g. /api/v1/manga/N/chapter/M/page/K).
func TestShape3_ChapterPages_URLFormat(t *testing.T) {
	inst := testharness.Shared(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := inst.Client()

	results, err := client.Search(ctx, testharness.LocalSourceID, testharness.FixtureMangaTitle)
	if err != nil || len(results) == 0 {
		t.Skipf("Search failed or empty (skipping shape3): err=%v results=%d", err, len(results))
	}

	// FetchChapters populates the chapter cache before querying pages.
	chapters, err := client.FetchChapters(ctx, results[0].ID)
	if err != nil || len(chapters) == 0 {
		t.Skipf("FetchChapters failed or empty (skipping shape3): err=%v len=%d", err, len(chapters))
	}

	pages, err := client.ChapterPages(ctx, chapters[0].ID)
	if err != nil {
		t.Fatalf("ChapterPages(chapterID=%d): %v", chapters[0].ID, err)
	}
	if len(pages) == 0 {
		t.Fatalf("fetchChapterPages returned zero pages for chapter %d", chapters[0].ID)
	}

	first := pages[0]
	logPageURLFormat(t, first)
}

// logPageURLFormat logs whether the page URL is absolute or relative.
func logPageURLFormat(t *testing.T, pageURL string) {
	t.Helper()
	switch {
	case strings.HasPrefix(pageURL, "http://") || strings.HasPrefix(pageURL, "https://"):
		t.Logf("CORRECTION: page URLs are ABSOLUTE (not relative as assumed in Task 4); PageBytes uses them directly: %q", pageURL)
	case strings.HasPrefix(pageURL, "/"):
		t.Logf("CONFIRMED: page URLs are RELATIVE paths (as assumed in Task 4); PageBytes prepends BaseURL: %q", pageURL)
	default:
		t.Logf("NOTE: unexpected page URL format: %q", pageURL)
	}
}

// TestShape4_BrowseEnumType validates the discover-browse shape assumption:
// the GraphQL enum TYPE NAME `FetchSourceMangaType` is correct.
//
// Why this needs a real Suwayomi: the existing searchMutation hardcodes
// `type: SEARCH` as an inline literal, so the type name was never declared as a
// typed variable before. Browse introduces `$type: FetchSourceMangaType!` (see
// browseMutation in client.go) — the FIRST time that enum type name crosses the
// wire. The httptest fakes in client_test.go echo back canned JSON and so cannot
// catch a wrong type name or a server-side schema rejection. Only a real
// Suwayomi parses the GraphQL document and validates the declared variable type
// against its schema.
//
// What this confirms: calling Browse(...BrowsePopular...) against the real
// fixture returns NO error. A wrong `FetchSourceMangaType` (or a wrong enum
// VALUE like POPULAR) would surface as a GraphQL schema/validation error from
// doGraphQL — exactly the failure mode this test exists to catch. A zero-manga
// result is acceptable: the point is that the enum/type name is accepted and
// hasNextPage parses (BrowseResult decodes cleanly). We additionally exercise
// BrowseLatest (LATEST reuses the same FetchSourceMangaType), but tolerate a
// source-capability error there — the Local source may not support a "latest"
// listing, and that is NOT a schema/type-name rejection.
func TestShape4_BrowseEnumType(t *testing.T) {
	inst := testharness.Shared(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := inst.Client()

	// POPULAR is the load-bearing assertion: it must be accepted as a value of
	// FetchSourceMangaType. Any error here means the enum type name or value is
	// wrong and must fail the test.
	popular, err := client.Browse(ctx, testharness.LocalSourceID, suwayomi.BrowsePopular, 1)
	if err != nil {
		t.Fatalf("Browse(sourceId=%q, type=POPULAR, page=1): %v\n(check: is FetchSourceMangaType! the correct enum type name, and POPULAR a valid value?)", testharness.LocalSourceID, err)
	}
	t.Logf("CONFIRMED: FetchSourceMangaType! accepted with value POPULAR; got %d mangas, hasNextPage=%v", len(popular.Mangas), popular.HasNextPage)

	// LATEST reuses the same FetchSourceMangaType. The Local source may not
	// support a latest listing — tolerate a source-capability error, but still
	// log the outcome so the test record shows whether LATEST round-tripped.
	latest, err := client.Browse(ctx, testharness.LocalSourceID, suwayomi.BrowseLatest, 1)
	if err != nil {
		t.Logf("NOTE: Browse(type=LATEST) returned an error — tolerated as a possible source-capability limitation (NOT a type-name rejection): %v", err)
	} else {
		t.Logf("CONFIRMED: FetchSourceMangaType! accepted with value LATEST; got %d mangas, hasNextPage=%v", len(latest.Mangas), latest.HasNextPage)
	}
}

// TestE2E_AddSeriesDispatchDownload is the Milestone 2 end-to-end proof:
//
//	real Suwayomi (local source) →
//	Ingest.Search finds fixture manga →
//	Ingest.AddSeries populates rows (state=wanted) →
//	download.Dispatcher with real suwayomi.Fetcher →
//	RunOnce →
//	each chapter reaches state=downloaded with a Komga-valid CBZ on disk.
//
// It also validates faithful provenance (provider, page_count) on the chapter rows.
func TestE2E_AddSeriesDispatchDownload(t *testing.T) {
	inst := testharness.Shared(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	client := inst.Client()

	// ── Ephemeral database ────────────────────────────────────────────────────
	db := testdb.New(t)

	// ── Temp storage dir for CBZ output ──────────────────────────────────────
	storageDir := t.TempDir()

	// ── Step 1: Search the local source for the fixture manga ─────────────────
	t.Log("Step 1: searching local source for fixture manga...")
	var mangaID int
	var mangaTitle string

	// Retry search up to 30 s because the Local source may need time to index
	// on first launch.
	if err := retryUntil(ctx, 30*time.Second, func() error {
		results, err := client.Search(ctx, testharness.LocalSourceID, testharness.FixtureMangaTitle)
		if err != nil {
			return err
		}
		for _, m := range results {
			if strings.Contains(m.Title, testharness.FixtureMangaTitle) {
				mangaID = m.ID
				mangaTitle = m.Title
				return nil
			}
		}
		return fmt.Errorf("fixture manga %q not found in search results (got %d)", testharness.FixtureMangaTitle, len(results))
	}); err != nil {
		t.Fatalf("Step 1 — search: %v", err)
	}
	t.Logf("Step 1: found fixture manga ID=%d title=%q", mangaID, mangaTitle)

	// ── Step 2: AddSeries — populate DB rows ──────────────────────────────────
	t.Log("Step 2: AddSeries — ingesting chapters into DB...")
	ingest := suwayomi.NewIngest(client, db)
	result, err := ingest.AddSeries(ctx, "local", mangaID, mangaTitle)
	if err != nil {
		t.Fatalf("Step 2 — AddSeries: %v", err)
	}
	t.Logf("Step 2: ingest result: new_chapters=%d new_provider_chapters=%d", result.NewChapters, result.NewProviderChapters)

	if result.NewChapters != testharness.FixtureChapterCount {
		t.Errorf("Step 2: expected %d new chapters, got %d", testharness.FixtureChapterCount, result.NewChapters)
	}

	// ── Step 3: Verify all chapters are in state=wanted ───────────────────────
	t.Log("Step 3: verifying chapters are in state=wanted...")
	wanted, err := chapter.WantedChapters(ctx, db, 100, 3)
	if err != nil {
		t.Fatalf("Step 3 — WantedChapters: %v", err)
	}
	if len(wanted) != testharness.FixtureChapterCount {
		t.Fatalf("Step 3: expected %d wanted chapters, got %d", testharness.FixtureChapterCount, len(wanted))
	}
	t.Logf("Step 3: %d chapters in state=wanted — OK", len(wanted))

	// ── Step 4: Run the dispatcher with the real Suwayomi fetcher ─────────────
	t.Log("Step 4: running dispatcher with real suwayomi.Fetcher...")
	hub := sse.NewHub()
	fetcher := suwayomi.NewFetcher(client)
	dispatcher := download.New(db, fetcher, hub, download.Config{
		Storage:                storageDir,
		PerProviderConcurrency: 1,
		MaxRetries:             1,
	})

	if err := dispatcher.RunOnce(ctx); err != nil {
		t.Fatalf("Step 4 — RunOnce: %v", err)
	}
	t.Log("Step 4: RunOnce completed")

	// ── Step 5: Assert downloaded chapters + Komga-valid CBZs ─────────────────
	t.Log("Step 5: asserting downloaded chapters and CBZ validity...")
	// mangaTitle is the title returned by Search (= testharness.FixtureMangaTitle).
	// It must appear in ComicInfo.Series for Komga series grouping.
	assertDownloadedCBZs(t, storageDir, testharness.FixtureChapterCount, mangaTitle)

	// ── Step 6: Verify provenance in the DB (state=downloaded, page_count) ────
	t.Log("Step 6: verifying DB provenance...")
	assertProvenance(t, ctx, db, testharness.FixtureChapterCount)
}

// assertDownloadedCBZs walks storageDir, counts .cbz files, and validates each
// one is Komga-valid (ComicInfo.xml present + at least one image page +
// ComicInfo.Series equals wantSeriesTitle so Komga can group the series).
func assertDownloadedCBZs(t *testing.T, storageDir string, expectedCount int, wantSeriesTitle string) {
	t.Helper()

	var cbzFiles []string
	if err := filepath.Walk(storageDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.EqualFold(filepath.Ext(path), ".cbz") {
			cbzFiles = append(cbzFiles, path)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk storageDir: %v", err)
	}

	if len(cbzFiles) != expectedCount {
		t.Errorf("expected %d CBZ files, found %d in %s", expectedCount, len(cbzFiles), storageDir)
		for _, f := range cbzFiles {
			t.Logf("  found: %s", f)
		}
		if len(cbzFiles) == 0 {
			return
		}
	}

	for _, cbzPath := range cbzFiles {
		cbzPath := cbzPath // loop var capture
		t.Run(filepath.Base(cbzPath), func(t *testing.T) {
			assertKomgaValidCBZ(t, cbzPath, wantSeriesTitle)
		})
	}
}

// assertKomgaValidCBZ opens a CBZ and checks:
//  1. ComicInfo.xml is present and parseable by disk.ReadComicInfoFromCBZ.
//  2. At least one image page is present (png/jpg/webp/gif/avif).
//  3. ComicInfo.Series equals wantSeriesTitle (non-empty; required for Komga
//     series grouping — an empty <Series> causes every chapter to appear as
//     an ungrouped one-off in the Komga library).
func assertKomgaValidCBZ(t *testing.T, cbzPath string, wantSeriesTitle string) {
	t.Helper()

	// G304: path is from a test-controlled temp directory.
	r, err := zip.OpenReader(cbzPath) //nolint:gosec
	if err != nil {
		t.Fatalf("open CBZ: %v", err)
	}
	defer func() { _ = r.Close() }()

	hasComicInfo := false
	pageCount := 0
	for _, f := range r.File {
		name := strings.ToLower(f.Name)
		if name == "comicinfo.xml" {
			hasComicInfo = true
			continue
		}
		ext := filepath.Ext(name)
		switch ext {
		case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".avif", ".bin":
			pageCount++
		}
	}

	if !hasComicInfo {
		t.Errorf("missing ComicInfo.xml (not Komga-valid)")
	}
	if pageCount == 0 {
		t.Errorf("no image pages found (empty chapter)")
	}

	// Use disk.ReadComicInfoFromCBZ for a full parse round-trip.
	ci, err := disk.ReadComicInfoFromCBZ(cbzPath)
	if err != nil {
		t.Errorf("parse ComicInfo.xml: %v", err)
	}
	if ci != nil {
		t.Logf("ComicInfo: series=%q chapter=%q pages=%d", ci.Series, ci.Number, ci.PageCount)
		// ComicInfo.Series must be non-empty and match the expected title so that
		// Komga can group chapters under the correct series.
		if ci.Series == "" {
			t.Errorf("ComicInfo.Series is empty — Komga cannot group this chapter into a series")
		} else if ci.Series != wantSeriesTitle {
			t.Errorf("ComicInfo.Series: got %q, want %q", ci.Series, wantSeriesTitle)
		}
	}

	t.Logf("CBZ valid: ComicInfo=%v pages=%d", hasComicInfo, pageCount)
}

// assertProvenance queries the DB for downloaded chapters and verifies they
// have state=downloaded and a non-zero page_count.
func assertProvenance(t *testing.T, ctx context.Context, db *ent.Client, expectedCount int) {
	t.Helper()

	downloaded, err := db.Chapter.Query().
		Where(entchapter.StateEQ(entchapter.StateDownloaded)).
		All(ctx)
	if err != nil {
		t.Fatalf("query downloaded chapters: %v", err)
	}

	if len(downloaded) != expectedCount {
		t.Errorf("expected %d downloaded chapters, got %d", expectedCount, len(downloaded))
	}

	for _, ch := range downloaded {
		pageCount := 0
		if ch.PageCount != nil {
			pageCount = *ch.PageCount
		}
		if pageCount <= 0 {
			t.Errorf("chapter %s has page_count=%d (expected > 0)", ch.ID, pageCount)
		}
		if ch.Filename == "" {
			t.Errorf("chapter %s has empty filename", ch.ID)
		}
		t.Logf("chapter %s: state=%s pages=%d filename=%s", ch.ID, ch.State, pageCount, ch.Filename)
	}
}

// retryUntil calls fn every 500 ms until it returns nil or timeout elapses.
// The last non-nil error is returned on timeout.
func retryUntil(ctx context.Context, timeout time.Duration, fn func() error) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("timeout after %s: %w", timeout, lastErr)
}
