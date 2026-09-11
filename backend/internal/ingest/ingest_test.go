// Package ingest_test — unit + integration tests for Ingest.
//
// Tests use the shared sourceengine/fake.Client (no network, no real engine
// host) and the testdb ephemeral-Postgres harness (Docker required). This is
// the P2 (Suwayomi-removal) port of internal/suwayomi/ingest_test.go onto the
// URL-addressed engine-host client — every case that exercised suwayomi-only
// concepts (suwayomi_chapter_id backfill, Search passthrough) is either
// dropped (no engine-host equivalent) or replaced with the URL-addressed
// equivalent; the rest is a faithful port.
package ingest_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/ingest"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	enginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
	"github.com/technobecet/tsundoku/internal/sse"
)

// --- helpers -----------------------------------------------------------------

// chapterURL is the deterministic per-chapter URL makeChapters assigns.
func chapterURL(n int) string {
	return "https://engine.test/ch/" + chapter.FormatChapterNumber(float64(n))
}

// makeChapters builds n stub sourceengine.Chapter values with sequential
// numbers 1..n so that NormalizeChapterKey produces deterministic, distinct
// keys.
func makeChapters(n int) []sourceengine.Chapter {
	chs := make([]sourceengine.Chapter, n)
	for i := range n {
		num := float64(i + 1)
		chs[i] = sourceengine.Chapter{
			Name:   "Chapter " + chapter.FormatChapterNumber(num),
			Number: num,
			URL:    chapterURL(i + 1),
		}
	}
	return chs
}

// makeChaptersWithScanlator builds n stub sourceengine.Chapter values
// (mirroring makeChapters) where each chapter's Scanlator is set to scanlator.
// Chapter numbers are sequential starting at start so that two scanlator
// groups for the same manga produce disjoint, deterministic chapter keys.
func makeChaptersWithScanlator(n int, start int, scanlator string) []sourceengine.Chapter {
	chs := make([]sourceengine.Chapter, n)
	for i := range n {
		num := float64(start + i)
		chs[i] = sourceengine.Chapter{
			Name:      "Chapter " + chapter.FormatChapterNumber(num),
			Number:    num,
			URL:       chapterURL(start + i),
			Scanlator: scanlator,
		}
	}
	return chs
}

func seedReconcileStaging(t *testing.T, root string, providerChapterID uuid.UUID) string {
	t.Helper()
	dir := filepath.Join(root, providerChapterID.String())
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("create staging dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "000001.jpg"), []byte("staged"), 0o600); err != nil {
		t.Fatalf("create staged page: %v", err)
	}
	return dir
}

func assertReplacementResultAndCache(t *testing.T, stagingDir string, result chapter.IngestResult) {
	t.Helper()
	if result.NewChapters != 1 || result.NewProviderChapters != 1 {
		t.Errorf("result = %+v, want one new chapter and provider chapter", result)
	}
	if _, err := os.Stat(stagingDir); !os.IsNotExist(err) {
		t.Errorf("changed-address staging directory stat error = %v, want not exist", err)
	}
}

func assertReplacementProviderIdentity(t *testing.T, before, gotProvider *ent.SeriesProvider) {
	t.Helper()
	if gotProvider.ID != before.ID || gotProvider.Provider != before.Provider || gotProvider.Scanlator != before.Scanlator || gotProvider.Importance != before.Importance || gotProvider.Metadata != before.Metadata || !gotProvider.CreatedAt.Equal(before.CreatedAt) {
		t.Errorf("provider identity/preserved fields changed: before=%+v after=%+v", before, gotProvider)
	}
}

func assertReplacementProviderValues(t *testing.T, gotProvider *ent.SeriesProvider) {
	t.Helper()
	if gotProvider.URL != "/new/address" || gotProvider.WebURL != "https://new.example/title" || gotProvider.AddressMode.String() != "url_search" {
		t.Errorf("provider address = %q/%q/%q, want replacement tuple", gotProvider.URL, gotProvider.WebURL, gotProvider.AddressMode)
	}
	if gotProvider.Title != "Replacement at Source" || gotProvider.CoverURL != "https://new.example/cover.jpg" || gotProvider.ProviderName != "Renamed Source" {
		t.Errorf("provider metadata = %q/%q/%q, want refreshed non-empty values", gotProvider.Title, gotProvider.CoverURL, gotProvider.ProviderName)
	}
}

func assertReplacementProviderChapter(t *testing.T, existingProviderChapter, gotProviderChapter *ent.ProviderChapter) {
	t.Helper()
	if gotProviderChapter.ID != existingProviderChapter.ID || gotProviderChapter.Attempts != 2 || gotProviderChapter.LastError != "retry later" {
		t.Errorf("matching provider chapter identity/retry state changed: %+v", gotProviderChapter)
	}
}

func assertReplacementLogicalChapter(t *testing.T, existingChapter, gotChapter *ent.Chapter) {
	t.Helper()
	if gotChapter.ID != existingChapter.ID || gotChapter.State != "downloaded" || gotChapter.Filename != "chapter-1.cbz" || gotChapter.SatisfiedImportance == nil || *gotChapter.SatisfiedImportance != 50 {
		t.Errorf("matching chapter identity/download state changed: %+v", gotChapter)
	}
}

func assertReplacementFeed(t *testing.T, ctx context.Context, client *ent.Client, seriesID, providerID uuid.UUID) {
	t.Helper()
	if count := client.ProviderChapter.Query().Where(entproviderchapter.SeriesProviderID(providerID)).CountX(ctx); count != 3 {
		t.Errorf("provider chapter count = %d, want 3 (new plus retained missing upstream)", count)
	}
	newChapter := client.Chapter.Query().Where(entchapter.SeriesID(seriesID), entchapter.ChapterKey("2")).OnlyX(ctx)
	if newChapter.State != entchapter.StateWanted {
		t.Errorf("new chapter state = %q, want wanted", newChapter.State)
	}
}

func assertRollbackRestored(
	t *testing.T,
	ctx context.Context,
	client *ent.Client,
	providerID, providerChapterID uuid.UUID,
	stagingDir string,
) {
	t.Helper()
	provider := client.SeriesProvider.GetX(ctx, providerID)
	if provider.URL != "/old/address" || provider.WebURL != "https://old.example/title" || provider.AddressMode.String() != "direct" {
		t.Errorf("provider address after rollback = %q/%q/%q, want original tuple", provider.URL, provider.WebURL, provider.AddressMode)
	}
	providerChapter := client.ProviderChapter.GetX(ctx, providerChapterID)
	if providerChapter.Name != "Chapter 1 old" || providerChapter.URL != "/old/chapter-1" || len(providerChapter.PageLinks) != 1 {
		t.Errorf("provider chapter after rollback = name %q URL %q links %+v, want original cache-bearing row", providerChapter.Name, providerChapter.URL, providerChapter.PageLinks)
	}
	if count := client.Chapter.Query().CountX(ctx); count != 1 {
		t.Errorf("chapter count after rollback = %d, want 1", count)
	}
	if _, err := os.Stat(filepath.Join(stagingDir, "000001.jpg")); err != nil {
		t.Errorf("staging cache after rollback: %v, want restored", err)
	}
}

// assertSeries checks that exactly one Series exists with the expected slug and title.
func assertSeries(t *testing.T, ctx context.Context, client *ent.Client, wantTitle, wantSlug string) {
	t.Helper()
	list := client.Series.Query().AllX(ctx)
	if len(list) != 1 {
		t.Fatalf("Series count: got %d, want 1", len(list))
	}
	if list[0].Slug != wantSlug {
		t.Errorf("Series.Slug: got %q, want %q", list[0].Slug, wantSlug)
	}
	if list[0].Title != wantTitle {
		t.Errorf("Series.Title: got %q, want %q", list[0].Title, wantTitle)
	}
}

// assertSeriesProvider checks that exactly one SeriesProvider exists with the
// expected provider (stringified sourceID), url, and title.
func assertSeriesProvider(t *testing.T, ctx context.Context, client *ent.Client, wantProvider string, wantURL, wantTitle string) *ent.SeriesProvider {
	t.Helper()
	list := client.SeriesProvider.Query().AllX(ctx)
	if len(list) != 1 {
		t.Fatalf("SeriesProvider count: got %d, want 1", len(list))
	}
	sp := list[0]
	if sp.Provider != wantProvider {
		t.Errorf("SeriesProvider.Provider: got %q, want %q", sp.Provider, wantProvider)
	}
	if sp.URL != wantURL {
		t.Errorf("SeriesProvider.URL: got %q, want %q", sp.URL, wantURL)
	}
	if sp.Title != wantTitle {
		t.Errorf("SeriesProvider.Title: got %q, want %q", sp.Title, wantTitle)
	}
	return sp
}

// assertProviderChapterURLs checks that K ProviderChapters exist for spID, each
// with a URL from wantURLs and SuwayomiChapterID left at its zero value — this
// package never writes that legacy column (see the package doc comment).
func assertProviderChapterURLs(
	t *testing.T,
	ctx context.Context,
	client *ent.Client,
	spID uuid.UUID,
	wantURLs map[string]string,
) {
	t.Helper()
	pcs := client.ProviderChapter.Query().
		Where(entproviderchapter.SeriesProviderID(spID)).
		AllX(ctx)
	if len(pcs) != len(wantURLs) {
		t.Fatalf("ProviderChapter count: got %d, want %d", len(pcs), len(wantURLs))
	}
	for _, pc := range pcs {
		wantURL, ok := wantURLs[pc.ChapterKey]
		if !ok {
			t.Errorf("ProviderChapter %q: unexpected chapter_key", pc.ChapterKey)
			continue
		}
		if pc.URL != wantURL {
			t.Errorf("ProviderChapter %q: URL got %q, want %q", pc.ChapterKey, pc.URL, wantURL)
		}
		if pc.SuwayomiChapterID != 0 {
			t.Errorf("ProviderChapter %q: SuwayomiChapterID got %d, want 0 (never written by internal/ingest)", pc.ChapterKey, pc.SuwayomiChapterID)
		}
	}
}

// buildWantURLs converts a []sourceengine.Chapter to the chapter_key → url map
// expected by assertProviderChapterURLs.
func buildWantURLs(chs []sourceengine.Chapter) map[string]string {
	m := make(map[string]string, len(chs))
	for _, ch := range chs {
		var num *float64
		if ch.Number >= 0 {
			n := ch.Number
			num = &n
		}
		m[chapter.NormalizeChapterKey(num, ch.Name)] = ch.URL
	}
	return m
}

// assertChapterCount checks that exactly n Chapter rows exist with state=wanted.
func assertChapterCount(t *testing.T, ctx context.Context, client *ent.Client, n int) {
	t.Helper()
	chs := client.Chapter.Query().AllX(ctx)
	if len(chs) != n {
		t.Fatalf("Chapter count: got %d, want %d", len(chs), n)
	}
	for _, ch := range chs {
		if ch.State != "wanted" {
			t.Errorf("Chapter %q: state got %q, want wanted", ch.ChapterKey, ch.State)
		}
	}
}

// --- tests -------------------------------------------------------------------

// TestIngest_AddSeries_Basic verifies that AddSeries for a manga with K chapters
// creates exactly one Series (slug = disk.Slugify(title)), one SeriesProvider
// (provider = stringified sourceID, url = the passed url), K ProviderChapters
// each carrying its chapter's URL, and one Chapter per key at state=wanted
// (reusing the M1 dedup invariant).
func TestIngest_AddSeries_Basic(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		sourceID   int64 = 42
		mangaURL         = "/manga/42"
		mangaTitle       = "My Test Manga"
		k                = 3
	)

	stubs := makeChapters(k)
	fc := enginefake.New(
		enginefake.WithChapters(sourceID, mangaURL, stubs),
		// MangaDetails must return the source title — the same value as the
		// adopt title here because this test does not distinguish canonical
		// from source titles.
		enginefake.WithMangaDetails(sourceID, mangaURL, sourceengine.MangaDetails{Title: mangaTitle}),
	)

	ing := ingest.NewIngest(fc, client)
	result, err := ing.AddSeries(ctx, sourceID, mangaURL, mangaTitle, "")
	if err != nil {
		t.Fatalf("AddSeries: unexpected error: %v", err)
	}

	// Exactly K new chapters and K new provider-chapters on first call.
	if result.NewChapters != k {
		t.Errorf("result.NewChapters: got %d, want %d", result.NewChapters, k)
	}
	if result.NewProviderChapters != k {
		t.Errorf("result.NewProviderChapters: got %d, want %d", result.NewProviderChapters, k)
	}

	assertSeries(t, ctx, client, mangaTitle, disk.Slugify(mangaTitle))
	sp := assertSeriesProvider(t, ctx, client, "42", mangaURL, mangaTitle)
	assertProviderChapterURLs(t, ctx, client, sp.ID, buildWantURLs(stubs))
	assertChapterCount(t, ctx, client, k)
}

// TestIngest_AddSeries_CollapsesSourceNameScanlator proves the defensive
// scanlator collapse: when the caller passes the SOURCE'S OWN display name as
// the scanlator (the untagged bucket, uncollapsed by a stale/other FE
// surface), AddSeries treats it as "" (all/untagged) so the source's untagged
// chapters are ingested — instead of being silently filtered to an empty feed.
func TestIngest_AddSeries_CollapsesSourceNameScanlator(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		sourceID   int64 = 42
		mangaURL         = "/manga/asura-extra"
		mangaTitle       = "The Novel's Extra"
		sourceName       = "Asura Scans"
		k                = 3
	)

	// Untagged chapters (Scanlator == "") — the group's OWN site tags nothing.
	stubs := makeChapters(k)
	fc := enginefake.New(
		enginefake.WithChapters(sourceID, mangaURL, stubs),
		enginefake.WithMangaDetails(sourceID, mangaURL, sourceengine.MangaDetails{Title: mangaTitle}),
		enginefake.WithSources([]sourceengine.Source{{ID: sourceID, Name: sourceName}}),
	)

	ing := ingest.NewIngest(fc, client)
	// The LEAK: pass the source's own display name as the scanlator.
	result, err := ing.AddSeries(ctx, sourceID, mangaURL, mangaTitle, sourceName)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}

	// The collapse must have kept ALL k untagged chapters, not filtered to 0.
	if result.NewProviderChapters != k {
		t.Errorf("NewProviderChapters got %d, want %d (source-name scanlator must collapse to \"\")", result.NewProviderChapters, k)
	}
	sp := client.SeriesProvider.Query().OnlyX(ctx)
	if sp.Scanlator != "" {
		t.Errorf("SeriesProvider.Scanlator got %q, want \"\" (collapsed)", sp.Scanlator)
	}
}

// TestIngest_AddSeries_KeepsDistinctScanlator proves the collapse is precise: a
// scanlator that is NOT the source's own name (e.g. the Comix aggregator
// hosting the "Asura Scans" group) is preserved, and only that scanlator's
// chapters are ingested — the collapse must never over-fire.
func TestIngest_AddSeries_KeepsDistinctScanlator(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		sourceID   int64 = 7
		mangaURL         = "/manga/novel-extra"
		mangaTitle       = "The Novel's Extra"
		sourceName       = "Comix"
		scanlator        = "Asura Scans"
	)

	// Two chapters tagged "Asura Scans" + one under another group.
	stubs := []sourceengine.Chapter{
		{Name: "Chapter 1", Number: 1, URL: "u1", Scanlator: scanlator},
		{Name: "Chapter 2", Number: 2, URL: "u2", Scanlator: scanlator},
		{Name: "Chapter 3", Number: 3, URL: "u3", Scanlator: "Other Group"},
	}
	fc := enginefake.New(
		enginefake.WithChapters(sourceID, mangaURL, stubs),
		enginefake.WithMangaDetails(sourceID, mangaURL, sourceengine.MangaDetails{Title: mangaTitle}),
		enginefake.WithSources([]sourceengine.Source{{ID: sourceID, Name: sourceName}}),
	)

	ing := ingest.NewIngest(fc, client)
	result, err := ing.AddSeries(ctx, sourceID, mangaURL, mangaTitle, scanlator)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}

	// Only the two "Asura Scans" chapters ingested — the collapse did NOT fire.
	if result.NewProviderChapters != 2 {
		t.Errorf("NewProviderChapters got %d, want 2 (distinct scanlator preserved)", result.NewProviderChapters)
	}
	sp := client.SeriesProvider.Query().OnlyX(ctx)
	if sp.Scanlator != scanlator {
		t.Errorf("SeriesProvider.Scanlator got %q, want %q (not collapsed)", sp.Scanlator, scanlator)
	}
}

// TestIngest_AddSeries_RepairsBrokenScanlatorRowInPlace proves the self-heal: a
// SeriesProvider left broken by the pre-fix leak (scanlator == source display
// name, empty feed, owner-set importance) is REPAIRED IN PLACE on the next
// AddSeries — its scanlator repointed to "", its feed repopulated, and
// crucially its importance PRESERVED — instead of being duplicated with
// importance reset to 0.
func TestIngest_AddSeries_RepairsBrokenScanlatorRowInPlace(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		sourceID   int64 = 42
		mangaURL         = "/manga/asura-extra"
		mangaTitle       = "The Novel's Extra"
		sourceName       = "Asura Scans"
		k                = 3
	)

	// Pre-create the Series + a BROKEN provider row exactly as the pre-fix bug
	// left it: scanlator = the source display name, an owner-set importance, and
	// NO ProviderChapter feed.
	cat, err := category.ResolveDefault(ctx, client)
	if err != nil {
		t.Fatalf("resolve default category: %v", err)
	}
	sr := client.Series.Create().
		SetTitle(mangaTitle).
		SetSlug(disk.Slugify(mangaTitle)).
		SetCategoryID(cat.ID).
		SaveX(ctx)
	broken := client.SeriesProvider.Create().
		SetSeriesID(sr.ID).
		SetProvider("42").
		SetProviderName(sourceName).
		SetScanlator(sourceName). // the leak
		SetImportance(5).
		SaveX(ctx)

	stubs := makeChapters(k)
	fc := enginefake.New(
		enginefake.WithChapters(sourceID, mangaURL, stubs),
		enginefake.WithMangaDetails(sourceID, mangaURL, sourceengine.MangaDetails{Title: mangaTitle}),
		enginefake.WithSources([]sourceengine.Source{{ID: sourceID, Name: sourceName}}),
	)
	ing := ingest.NewIngest(fc, client)
	if _, err := ing.AddSeries(ctx, sourceID, mangaURL, mangaTitle, sourceName); err != nil {
		t.Fatalf("AddSeries: %v", err)
	}

	// Exactly ONE provider row — the broken one, repaired, NOT a duplicate.
	sps := client.SeriesProvider.Query().AllX(ctx)
	if len(sps) != 1 {
		t.Fatalf("SeriesProvider count got %d, want 1 (repaired in place, no duplicate)", len(sps))
	}
	sp := sps[0]
	if sp.ID != broken.ID {
		t.Errorf("repaired row id changed: got %s, want %s (should reuse the broken row)", sp.ID, broken.ID)
	}
	if sp.Scanlator != "" {
		t.Errorf("Scanlator got %q, want \"\" (repaired)", sp.Scanlator)
	}
	if sp.Importance != 5 {
		t.Errorf("Importance got %d, want 5 (preserved, not reset)", sp.Importance)
	}
	// Feed repopulated on the SAME row.
	n := client.ProviderChapter.Query().Where(entproviderchapter.SeriesProviderID(sp.ID)).CountX(ctx)
	if n != k {
		t.Errorf("ProviderChapter feed got %d, want %d (repopulated in place)", n, k)
	}
}

// TestIngest_AddSeries_Idempotent verifies that calling AddSeries twice for the
// same manga produces no duplicate Series, SeriesProvider, or Chapter rows.
func TestIngest_AddSeries_Idempotent(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		sourceID   int64 = 99
		mangaURL         = "/manga/idempotent"
		mangaTitle       = "Idempotent Manga"
		k                = 2
	)

	stubs := makeChapters(k)
	fc := enginefake.New(
		enginefake.WithChapters(sourceID, mangaURL, stubs),
		enginefake.WithMangaDetails(sourceID, mangaURL, sourceengine.MangaDetails{Title: mangaTitle}),
	)
	ing := ingest.NewIngest(fc, client)

	// First call.
	if _, err := ing.AddSeries(ctx, sourceID, mangaURL, mangaTitle, ""); err != nil {
		t.Fatalf("first AddSeries: %v", err)
	}

	// Second call (idempotent re-add).
	result2, err := ing.AddSeries(ctx, sourceID, mangaURL, mangaTitle, "")
	if err != nil {
		t.Fatalf("second AddSeries: %v", err)
	}

	// M1 dedup: second call must not create new chapters.
	if result2.NewChapters != 0 {
		t.Errorf("second AddSeries: NewChapters got %d, want 0", result2.NewChapters)
	}
	if result2.NewProviderChapters != 0 {
		t.Errorf("second AddSeries: NewProviderChapters got %d, want 0", result2.NewProviderChapters)
	}

	// Still exactly one of each row type.
	assertSeries(t, ctx, client, mangaTitle, disk.Slugify(mangaTitle))
	sp := assertSeriesProvider(t, ctx, client, "99", mangaURL, mangaTitle)

	// K Chapter rows and K ProviderChapters with correct URLs.
	assertChapterCount(t, ctx, client, k)
	assertProviderChapterURLs(t, ctx, client, sp.ID, buildWantURLs(stubs))
}

// TestIngest_AddSeries_FetchChaptersError verifies that a Chapters client
// error is propagated as-is and no DB rows are created.
func TestIngest_AddSeries_FetchChaptersError(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	sentinel := errors.New("engine: manga not found")
	fc := enginefake.New(enginefake.WithError("Chapters", sentinel))
	ing := ingest.NewIngest(fc, client)

	_, err := ing.AddSeries(ctx, 7, "/manga/broken", "Broken Manga", "")
	if !errors.Is(err, sentinel) {
		t.Errorf("AddSeries: err got %v, want to wrap %v", err, sentinel)
	}
	// No Series rows should have been created (client error fires before DB touch).
	if n := len(client.Series.Query().AllX(ctx)); n != 0 {
		t.Errorf("Series count after client error: got %d, want 0", n)
	}
}

// countingChapterClient wraps enginefake.Client and counts Chapters calls per
// url, so a test can prove a given (source, manga) was fetched from the
// upstream engine host exactly N times — the same shape as
// internal/imports/cache_test.go's countingClient, reimplemented here because
// this package's tests exercise Ingest directly (no imports.Service in scope).
type countingChapterClient struct {
	*enginefake.Client
	mu    sync.Mutex
	calls map[string]int
}

func newCountingChapterClient(fc *enginefake.Client) *countingChapterClient {
	return &countingChapterClient{Client: fc, calls: map[string]int{}}
}

func (c *countingChapterClient) Chapters(ctx context.Context, sourceID int64, url string, mangaTitle string) ([]sourceengine.Chapter, error) {
	c.mu.Lock()
	c.calls[url]++
	c.mu.Unlock()
	return c.Client.Chapters(ctx, sourceID, url, mangaTitle)
}

func (c *countingChapterClient) ChaptersRef(ctx context.Context, ref sourceengine.ProviderRef, mangaTitle string) (sourceengine.ChaptersResult, error) {
	c.mu.Lock()
	c.calls[ref.URL]++
	c.mu.Unlock()
	return c.Client.ChaptersRef(ctx, ref, mangaTitle)
}

func (c *countingChapterClient) count(url string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[url]
}

// TestIngest_AddSeries_SharedCacheKeyedByTitle_NoCrossContamination is the P2
// chapter-fidelity regression proof at the Ingest layer: production shares ONE
// *ingest.ChapterCache between imports.Service's read-only discovery preview
// (mangaTitle="" — see its fetchChapters doc comment) and this package's
// fetchForAdopt (the real title). Before the cache key included mangaTitle,
// whichever call ran first "won" the entry for the whole TTL — so a preview
// run before Adopt (the normal coverage→configure→adopt wizard flow) silently
// starved the adopt-side fetch of the engine host's title-strip recognition
// step. This proves AddSeries's real-title fetch is NOT served the ""
// preview's entry: it triggers its OWN upstream Chapters call, and a THIRD
// call with the same real title is then a genuine cache hit.
func TestIngest_AddSeries_SharedCacheKeyedByTitle_NoCrossContamination(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		sourceID  int64 = 88
		mangaURL        = "/manga/shared-cache"
		realTitle       = "7th Time Loop"
	)
	stubs := []sourceengine.Chapter{{Name: "Chapter 1", Number: 1, URL: "https://engine.test/shared/1"}}
	fc := enginefake.New(
		enginefake.WithChapters(sourceID, mangaURL, stubs),
		enginefake.WithMangaDetails(sourceID, mangaURL, sourceengine.MangaDetails{Title: realTitle}),
	)
	cc := newCountingChapterClient(fc)
	cache := ingest.NewChapterCacheConst(time.Minute)

	// Simulate the discovery preview sharing the SAME cache instance production
	// wires between imports.Service and this package's Ingest.
	if _, err := cache.Get(ctx, sourceID, mangaURL, "", func() ([]sourceengine.Chapter, error) {
		return cc.Chapters(ctx, sourceID, mangaURL, "")
	}); err != nil {
		t.Fatalf("preview cache.Get: %v", err)
	}
	if got := cc.count(mangaURL); got != 1 {
		t.Fatalf("preview fetch count = %d, want 1", got)
	}

	// Adopt: AddSeries with the REAL title, sharing the same cache instance —
	// must NOT be served the ""-populated entry.
	ing := ingest.NewIngestWithGate(cc, client, cache, nil)
	if _, err := ing.AddSeries(ctx, sourceID, mangaURL, realTitle, ""); err != nil {
		t.Fatalf("AddSeries: %v", err)
	}
	if got := cc.count(mangaURL); got != 2 {
		t.Fatalf("post-adopt fetch count = %d, want 2 (real-title fetch must NOT reuse the \"\" preview's entry)", got)
	}

	// A repeat with the SAME real title is a genuine hit (no third fetch).
	if _, err := cache.Get(ctx, sourceID, mangaURL, realTitle, func() ([]sourceengine.Chapter, error) {
		return cc.Chapters(ctx, sourceID, mangaURL, realTitle)
	}); err != nil {
		t.Fatalf("repeat cache.Get: %v", err)
	}
	if got := cc.count(mangaURL); got != 2 {
		t.Fatalf("post-repeat fetch count = %d, want still 2 (same real title must hit)", got)
	}
}

// TestIngest_AddSeries_UnparsedNumberSentinel_UsesNameKey verifies that a
// chapter carrying the engine host's raw Mihon "unparsed number" sentinel
// (-1 — see hasParsedNumber's doc comment) is keyed by name (NormalizeChapterKey
// nil-number path), exactly like the old Suwayomi client's nil-Number case.
func TestIngest_AddSeries_UnparsedNumberSentinel_UsesNameKey(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		sourceID int64 = 55
		mangaURL       = "/manga/special"
	)
	stubs := []sourceengine.Chapter{
		{Name: "Special Volume", Number: -1, URL: "https://engine.test/special", UploadDate: 1710460800000},
	}
	fc := enginefake.New(
		enginefake.WithChapters(sourceID, mangaURL, stubs),
		enginefake.WithMangaDetails(sourceID, mangaURL, sourceengine.MangaDetails{Title: "Special Series"}),
	)
	ing := ingest.NewIngest(fc, client)

	result, err := ing.AddSeries(ctx, sourceID, mangaURL, "Special Series", "")
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}
	if result.NewChapters != 1 {
		t.Errorf("NewChapters: got %d, want 1", result.NewChapters)
	}

	sp := client.SeriesProvider.Query().OnlyX(ctx)
	pc := client.ProviderChapter.Query().
		Where(entproviderchapter.SeriesProviderID(sp.ID)).
		OnlyX(ctx)

	wantKey := chapter.NormalizeChapterKey(nil, "Special Volume")
	if pc.ChapterKey != wantKey {
		t.Errorf("ChapterKey: got %q, want %q (name-based, sentinel treated as unparsed)", pc.ChapterKey, wantKey)
	}
	if pc.Number != nil {
		t.Errorf("ProviderChapter.Number: got %v, want nil (sentinel -1 must not be stored as a real number)", pc.Number)
	}
}

// TestIngest_AddSeries_UnparsedNumberSentinel_NoCollision proves the sentinel
// handling doesn't collapse distinct numberless chapters onto one literal "-1"
// chapter_key: two chapters both carrying Number=-1 but different Names must
// ingest as TWO distinct Chapter rows, keyed by their (distinct) names.
func TestIngest_AddSeries_UnparsedNumberSentinel_NoCollision(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		sourceID int64 = 56
		mangaURL       = "/manga/specials"
	)
	stubs := []sourceengine.Chapter{
		{Name: "Prologue", Number: -1, URL: "https://engine.test/prologue"},
		{Name: "Extra Story", Number: -1, URL: "https://engine.test/extra"},
	}
	fc := enginefake.New(
		enginefake.WithChapters(sourceID, mangaURL, stubs),
		enginefake.WithMangaDetails(sourceID, mangaURL, sourceengine.MangaDetails{Title: "Specials Series"}),
	)
	ing := ingest.NewIngest(fc, client)

	result, err := ing.AddSeries(ctx, sourceID, mangaURL, "Specials Series", "")
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}
	if result.NewChapters != 2 {
		t.Fatalf("NewChapters: got %d, want 2 (both sentinel chapters must survive as distinct rows, no -1 collision)", result.NewChapters)
	}
	assertChapterCount(t, ctx, client, 2)
}

// TestIngest_AddSeries_TitleUpdate verifies that re-calling AddSeries with a
// changed title UPDATES Series.Title while keeping Series.Slug unchanged and
// creates no duplicate Series row.
func TestIngest_AddSeries_TitleUpdate(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		sourceID     int64 = 88
		mangaURL           = "/manga/title-update"
		initialTitle       = "some manga title"
		// updatedTitle has the same slug ("some-manga-title") after Slugify but
		// different casing, exercising the update branch of upsertSeries.
		updatedTitle = "Some Manga Title"
	)

	stubs := makeChapters(1)
	fc := enginefake.New(
		enginefake.WithChapters(sourceID, mangaURL, stubs),
		enginefake.WithMangaDetails(sourceID, mangaURL, sourceengine.MangaDetails{Title: "source title"}),
	)
	ing := ingest.NewIngest(fc, client)

	// First call: creates the Series row.
	if _, err := ing.AddSeries(ctx, sourceID, mangaURL, initialTitle, ""); err != nil {
		t.Fatalf("first AddSeries: %v", err)
	}

	initialSlug := disk.Slugify(initialTitle)
	assertSeries(t, ctx, client, initialTitle, initialSlug)

	// Second call with a changed title: Series.Title must be updated.
	if _, err := ing.AddSeries(ctx, sourceID, mangaURL, updatedTitle, ""); err != nil {
		t.Fatalf("second AddSeries (title change): %v", err)
	}

	// Still exactly one Series row.
	list := client.Series.Query().AllX(ctx)
	if len(list) != 1 {
		t.Fatalf("Series count after title update: got %d, want 1", len(list))
	}
	if list[0].Title != updatedTitle {
		t.Errorf("Series.Title after update: got %q, want %q", list[0].Title, updatedTitle)
	}
	if list[0].Slug != initialSlug {
		t.Errorf("Series.Slug after title update: got %q, want %q (slug must not change)", list[0].Slug, initialSlug)
	}
}

// TestIngest_AddSeries_SeriesProviderTitle verifies that upsertSeriesProvider
// stores the source's own title (from MangaDetails) in SeriesProvider.Title on
// both the create and the update path — NOT the canonical adopt title.
func TestIngest_AddSeries_SeriesProviderTitle(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		sourceID       int64 = 77
		mangaURL             = "/manga/dragon-reborn"
		canonicalTitle       = "Dragon Reborn"
		// sourceTitle is what the source knows the manga as — it can differ in
		// casing or localisation from the canonical adopt title.
		sourceTitle = "Dragon Reborn (Source)"
	)

	stubs := makeChapters(1)
	fc := enginefake.New(
		enginefake.WithChapters(sourceID, mangaURL, stubs),
		enginefake.WithMangaDetails(sourceID, mangaURL, sourceengine.MangaDetails{Title: sourceTitle}),
	)
	ing := ingest.NewIngest(fc, client)

	// ── Create path: source title must be stored on first AddSeries ──────────
	if _, err := ing.AddSeries(ctx, sourceID, mangaURL, canonicalTitle, ""); err != nil {
		t.Fatalf("first AddSeries: %v", err)
	}

	sp := client.SeriesProvider.Query().OnlyX(ctx)
	if sp.Title != sourceTitle {
		t.Errorf("SeriesProvider.Title after create: got %q, want %q (source title from MangaDetails)", sp.Title, sourceTitle)
	}
	seriesRow := client.Series.Query().OnlyX(ctx)
	if seriesRow.Title != canonicalTitle {
		t.Errorf("Series.Title: got %q, want %q (canonical must not be changed by source title)", seriesRow.Title, canonicalTitle)
	}

	// ── Update path: source title must be refreshed on re-add ────────────────
	updatedSourceTitle := "Dragon Reborn (Source v2)"
	fc2 := enginefake.New(
		enginefake.WithChapters(sourceID, mangaURL, stubs),
		enginefake.WithMangaDetails(sourceID, mangaURL, sourceengine.MangaDetails{Title: updatedSourceTitle}),
	)
	ing2 := ingest.NewIngest(fc2, client)
	if _, err := ing2.AddSeries(ctx, sourceID, mangaURL, canonicalTitle, ""); err != nil {
		t.Fatalf("second AddSeries: %v", err)
	}

	sp = client.SeriesProvider.Query().OnlyX(ctx)
	if sp.Title != updatedSourceTitle {
		t.Errorf("SeriesProvider.Title after update: got %q, want %q", sp.Title, updatedSourceTitle)
	}
	if n := len(client.SeriesProvider.Query().AllX(ctx)); n != 1 {
		t.Errorf("SeriesProvider count: got %d, want 1", n)
	}
}

// TestIngest_AddSeries_SeriesProviderEmptyMetaKeepsExisting is the
// self-healer proof: a re-ingest whose MangaDetails response comes back with
// a BLANK Title and ThumbnailURL (a transient engine hiccup that still
// returns 200) must NOT blank the previously-stored good Title/CoverURL —
// mirrors the pre-existing providerName != "" guard (TestIngest_
// AddSeries_ProviderNameUnresolved's create-path sibling), extended to the
// update path for title/cover.
func TestIngest_AddSeries_SeriesProviderEmptyMetaKeepsExisting(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		sourceID       int64 = 91
		mangaURL             = "/manga/empty-meta-guard"
		canonicalTitle       = "Empty Meta Guard"
		sourceTitle          = "Empty Meta Guard (Source)"
		sourceCover          = "https://engine.test/cover/91.jpg"
	)

	// ── Create path: a good title/cover is stored ─────────────────────────
	fc := enginefake.New(
		enginefake.WithChapters(sourceID, mangaURL, makeChapters(1)),
		enginefake.WithMangaDetails(sourceID, mangaURL, sourceengine.MangaDetails{
			Title:        sourceTitle,
			ThumbnailURL: sourceCover,
		}),
	)
	ing := ingest.NewIngest(fc, client)
	if _, err := ing.AddSeries(ctx, sourceID, mangaURL, canonicalTitle, ""); err != nil {
		t.Fatalf("first AddSeries: %v", err)
	}

	sp := client.SeriesProvider.Query().OnlyX(ctx)
	if sp.Title != sourceTitle {
		t.Fatalf("SeriesProvider.Title after create: got %q, want %q", sp.Title, sourceTitle)
	}
	if sp.CoverURL != sourceCover {
		t.Fatalf("SeriesProvider.CoverURL after create: got %q, want %q", sp.CoverURL, sourceCover)
	}

	// ── Update path: a blank MangaDetails response must NOT blank either field ──
	fc2 := enginefake.New(
		enginefake.WithChapters(sourceID, mangaURL, makeChapters(1)),
		enginefake.WithMangaDetails(sourceID, mangaURL, sourceengine.MangaDetails{Title: "", ThumbnailURL: ""}),
	)
	ing2 := ingest.NewIngest(fc2, client)
	if _, err := ing2.AddSeries(ctx, sourceID, mangaURL, canonicalTitle, ""); err != nil {
		t.Fatalf("second AddSeries (empty meta): %v", err)
	}

	sp = client.SeriesProvider.Query().OnlyX(ctx)
	if sp.Title != sourceTitle {
		t.Errorf("SeriesProvider.Title after empty-meta re-add: got %q, want %q (must NOT be blanked)", sp.Title, sourceTitle)
	}
	if sp.CoverURL != sourceCover {
		t.Errorf("SeriesProvider.CoverURL after empty-meta re-add: got %q, want %q (must NOT be blanked)", sp.CoverURL, sourceCover)
	}
	if n := len(client.SeriesProvider.Query().AllX(ctx)); n != 1 {
		t.Errorf("SeriesProvider count: got %d, want 1 (idempotent)", n)
	}
}

// TestIngest_AddSeries_SeriesProviderURL is the CRUX test for the
// URL-addressed migration: it proves SeriesProvider.URL is set from the url
// ARGUMENT passed into AddSeries — never derived from the MangaDetails
// response — on both the create and the update path. A response carrying a
// DIFFERENT url must be ignored; the stored value always matches the caller's
// key.
func TestIngest_AddSeries_SeriesProviderURL(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		sourceID       int64 = 88
		mangaURL             = "/manga/solo-ascension"
		canonicalTitle       = "Solo Ascension"
		// responseURL is DELIBERATELY different from mangaURL — MangaDetails
		// must never be allowed to override the caller's key.
		responseURL = "https://example-source.test/manga/88-from-response"
	)

	fc := enginefake.New(
		enginefake.WithChapters(sourceID, mangaURL, makeChapters(1)),
		enginefake.WithMangaDetails(sourceID, mangaURL, sourceengine.MangaDetails{Title: canonicalTitle, URL: responseURL}),
	)
	ing := ingest.NewIngest(fc, client)

	// ── Create path ────────────────────────────────────────────────────────
	if _, err := ing.AddSeries(ctx, sourceID, mangaURL, canonicalTitle, ""); err != nil {
		t.Fatalf("first AddSeries: %v", err)
	}

	sp := client.SeriesProvider.Query().OnlyX(ctx)
	if sp.URL != mangaURL {
		t.Errorf("SeriesProvider.URL after create: got %q, want %q (must be the caller's url, NOT the response's)", sp.URL, mangaURL)
	}

	// ── Update path: re-add must keep storing the caller's url ───────────────
	if _, err := ing.AddSeries(ctx, sourceID, mangaURL, canonicalTitle, ""); err != nil {
		t.Fatalf("second AddSeries: %v", err)
	}

	sp = client.SeriesProvider.Query().OnlyX(ctx)
	if sp.URL != mangaURL {
		t.Errorf("SeriesProvider.URL after update: got %q, want %q", sp.URL, mangaURL)
	}
}

// TestIngest_AddSeries_WebURL is the realUrl round-trip proof: the manga's
// realUrl (sourceengine.MangaDetails.RealURL) lands on SeriesProvider.WebURL,
// and each chapter's realUrl (sourceengine.Chapter.RealURL) lands on the
// corresponding ProviderChapter.WebURL — both distinct in purpose from their
// sibling URL fields, which keep storing the exact source-owned address.
func TestIngest_AddSeries_WebURL(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		sourceID       int64 = 92
		mangaURL             = "/manga/web-url-proof"
		canonicalTitle       = "Web URL Proof"
		mangaRealURL         = "https://source.example/manga/web-url-proof"
		chapterRealURL       = "https://source.example/manga/web-url-proof/ch/1"
	)

	chapters := []sourceengine.Chapter{
		{
			Name:    "Chapter 1",
			Number:  1,
			URL:     chapterURL(1),
			RealURL: chapterRealURL,
		},
	}
	fc := enginefake.New(
		enginefake.WithChapters(sourceID, mangaURL, chapters),
		enginefake.WithMangaDetails(sourceID, mangaURL, sourceengine.MangaDetails{
			Title:   canonicalTitle,
			RealURL: mangaRealURL,
		}),
	)
	ing := ingest.NewIngest(fc, client)
	if _, err := ing.AddSeries(ctx, sourceID, mangaURL, canonicalTitle, ""); err != nil {
		t.Fatalf("AddSeries: %v", err)
	}

	sp := client.SeriesProvider.Query().OnlyX(ctx)
	if sp.WebURL != mangaRealURL {
		t.Errorf("SeriesProvider.WebURL: got %q, want %q", sp.WebURL, mangaRealURL)
	}
	if sp.URL != mangaURL {
		t.Errorf("SeriesProvider.URL: got %q, want %q (addressing url must be unaffected)", sp.URL, mangaURL)
	}

	pc := client.ProviderChapter.Query().Where(entproviderchapter.SeriesProviderID(sp.ID)).OnlyX(ctx)
	if pc.WebURL != chapterRealURL {
		t.Errorf("ProviderChapter.WebURL: got %q, want %q", pc.WebURL, chapterRealURL)
	}
	if pc.URL != chapterURL(1) {
		t.Errorf("ProviderChapter.URL: got %q, want %q (addressing url must be unaffected)", pc.URL, chapterURL(1))
	}
}

// TestIngest_AddSeries_SourceLinkRendersEndToEnd is the end-to-end proof that
// ingest WRITES SeriesProvider.URL (this file) and series.GetSeries's
// sourceLinks READS it (internal/series/dto.go). Uses the real series.Service
// (not a series-package unit test) so the assertion exercises the actual
// write→read round-trip through two packages, not two isolated halves.
func TestIngest_AddSeries_SourceLinkRendersEndToEnd(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		sourceID       int64 = 89
		mangaURL             = "/manga/blossoming-blade"
		canonicalTitle       = "Return of the Blossoming Blade"
		providerName         = "Asura Scans"
		sourceURL            = "https://asura.example/manga/blossoming-blade"
	)

	fc := enginefake.New(
		enginefake.WithChapters(sourceID, sourceURL, makeChapters(1)),
		enginefake.WithMangaDetails(sourceID, sourceURL, sourceengine.MangaDetails{Title: canonicalTitle}),
		enginefake.WithSources([]sourceengine.Source{{ID: sourceID, Name: providerName}}),
	)
	ing := ingest.NewIngest(fc, client)

	if _, err := ing.AddSeries(ctx, sourceID, sourceURL, canonicalTitle, ""); err != nil {
		t.Fatalf("AddSeries: %v", err)
	}

	s := client.Series.Query().OnlyX(ctx)

	svc := series.NewService(client, t.TempDir(), 14)
	detail, err := svc.GetSeries(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}

	found := false
	for _, l := range detail.Links {
		if l.URL == sourceURL {
			found = true
			if l.Label != providerName {
				t.Errorf("source link label: got %q, want %q", l.Label, providerName)
			}
		}
	}
	if !found {
		t.Fatalf("GetSeries.Links: source link for %q not found, got %+v", sourceURL, detail.Links)
	}
	_ = mangaURL // kept only to document intent; sourceURL is the actual key used.
}

// TestIngest_AddSeries_PerSourceMetadata verifies that AddSeries stores the
// source's own title (from MangaDetails) on SeriesProvider.Title instead of
// the canonical adopt title, and stores the source thumbnail as
// SeriesProvider.CoverURL. Series.Title must remain the canonical adopt title.
func TestIngest_AddSeries_PerSourceMetadata(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		sourceID       int64 = 7
		mangaURL             = "/manga/canonical"
		canonicalTitle       = "Canonical"
		sourceTitle          = "Source-Specific Title"
		sourceCover          = "https://engine.test/manga/7/thumbnail"
	)

	fc := enginefake.New(
		enginefake.WithChapters(sourceID, mangaURL, makeChapters(1)),
		enginefake.WithMangaDetails(sourceID, mangaURL, sourceengine.MangaDetails{
			Title:        sourceTitle,
			ThumbnailURL: sourceCover,
		}),
	)

	ing := ingest.NewIngest(fc, client)
	_, err := ing.AddSeries(ctx, sourceID, mangaURL, canonicalTitle, "")
	if err != nil {
		t.Fatalf("AddSeries: unexpected error: %v", err)
	}

	seriesRow := client.Series.Query().OnlyX(ctx)
	if seriesRow.Title != canonicalTitle {
		t.Errorf("Series.Title: got %q, want %q (canonical must not be overwritten by source title)",
			seriesRow.Title, canonicalTitle)
	}

	sp := client.SeriesProvider.Query().OnlyX(ctx)
	if sp.Title != sourceTitle {
		t.Errorf("SeriesProvider.Title: got %q, want %q (must use source title from MangaDetails, NOT canonical)",
			sp.Title, sourceTitle)
	}
	if sp.CoverURL != sourceCover {
		t.Errorf("SeriesProvider.CoverURL: got %q, want %q", sp.CoverURL, sourceCover)
	}
}

// TestIngest_AddSeries_ProviderName verifies that AddSeries resolves the
// source's human-readable display name from client.Sources() and stores it in
// SeriesProvider.provider_name on BOTH the create and the update path, keyed
// by matching sourceID against Source.ID.
func TestIngest_AddSeries_ProviderName(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		sourceID   int64 = 7537715367149829912
		mangaURL         = "/manga/named-source"
		mangaTitle       = "Named Source Manga"
		sourceName       = "WebToon"
	)

	fc := enginefake.New(
		enginefake.WithChapters(sourceID, mangaURL, makeChapters(1)),
		enginefake.WithMangaDetails(sourceID, mangaURL, sourceengine.MangaDetails{Title: mangaTitle}),
		enginefake.WithSources([]sourceengine.Source{
			{ID: 999, Name: "Other Source"},
			{ID: sourceID, Name: sourceName},
		}),
	)
	ing := ingest.NewIngest(fc, client)

	// ── Create path: display name must be resolved and stored ────────────────
	if _, err := ing.AddSeries(ctx, sourceID, mangaURL, mangaTitle, ""); err != nil {
		t.Fatalf("first AddSeries: %v", err)
	}
	sp := client.SeriesProvider.Query().OnlyX(ctx)
	if sp.Provider != "7537715367149829912" {
		t.Errorf("SeriesProvider.Provider: got %q, want %q (stringified numeric id)", sp.Provider, "7537715367149829912")
	}
	if sp.ProviderName != sourceName {
		t.Errorf("SeriesProvider.ProviderName after create: got %q, want %q", sp.ProviderName, sourceName)
	}

	// ── Update path: a renamed source must refresh provider_name on re-add ────
	fc2 := enginefake.New(
		enginefake.WithChapters(sourceID, mangaURL, makeChapters(1)),
		enginefake.WithMangaDetails(sourceID, mangaURL, sourceengine.MangaDetails{Title: mangaTitle}),
		enginefake.WithSources([]sourceengine.Source{{ID: sourceID, Name: "WebToon (renamed)"}}),
	)
	ing2 := ingest.NewIngest(fc2, client)
	if _, err := ing2.AddSeries(ctx, sourceID, mangaURL, mangaTitle, ""); err != nil {
		t.Fatalf("second AddSeries: %v", err)
	}
	sp = client.SeriesProvider.Query().OnlyX(ctx)
	if sp.ProviderName != "WebToon (renamed)" {
		t.Errorf("SeriesProvider.ProviderName after update: got %q, want %q", sp.ProviderName, "WebToon (renamed)")
	}
	if n := len(client.SeriesProvider.Query().AllX(ctx)); n != 1 {
		t.Errorf("SeriesProvider count: got %d, want 1 (idempotent)", n)
	}
}

// TestIngest_AddSeries_ProviderNameUnresolved verifies the non-fatal fallback:
// when the source id is absent from client.Sources() OR Sources() errors,
// AddSeries still succeeds and stores an empty provider_name.
func TestIngest_AddSeries_ProviderNameUnresolved(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name    string
		sources []sourceengine.Source
		srcErr  error
	}{
		{name: "id absent from list", sources: []sourceengine.Source{{ID: 111, Name: "Nope"}}},
		{name: "sources error", srcErr: errors.New("engine: sources unavailable")},
		{name: "empty source list", sources: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := testdb.New(t)
			opts := []enginefake.Option{
				enginefake.WithChapters(12345, "/manga/unresolved", makeChapters(1)),
				enginefake.WithMangaDetails(12345, "/manga/unresolved", sourceengine.MangaDetails{Title: "Unresolved Manga"}),
			}
			if tc.sources != nil {
				opts = append(opts, enginefake.WithSources(tc.sources))
			}
			if tc.srcErr != nil {
				opts = append(opts, enginefake.WithError("Sources", tc.srcErr))
			}
			fc := enginefake.New(opts...)
			ing := ingest.NewIngest(fc, client)

			if _, err := ing.AddSeries(ctx, 12345, "/manga/unresolved", "Unresolved Manga", ""); err != nil {
				t.Fatalf("AddSeries must not fail on unresolved provider name: %v", err)
			}
			sp := client.SeriesProvider.Query().OnlyX(ctx)
			if sp.ProviderName != "" {
				t.Errorf("SeriesProvider.ProviderName: got %q, want \"\" (unresolved fallback)", sp.ProviderName)
			}
		})
	}
}

// --- scanlator-aware provider identity tests ---------------------------------

// TestIngest_AddSeries_ScanlatorFilter_TwoGroupsCoexist verifies that a manga
// whose upstream chapter list carries two distinct scanlators produces TWO
// independent SeriesProvider rows — one per scanlator — each holding only its
// own ProviderChapter feed.
func TestIngest_AddSeries_ScanlatorFilter_TwoGroupsCoexist(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		sourceID   int64 = 1001
		mangaURL         = "/manga/two-scanlators"
		mangaTitle       = "Two Scanlators Manga"
	)

	alphaChapters := makeChaptersWithScanlator(2, 1, "Alpha")
	betaChapters := makeChaptersWithScanlator(3, 1, "Beta")
	allChapters := append(append([]sourceengine.Chapter{}, alphaChapters...), betaChapters...)

	fc := enginefake.New(
		enginefake.WithChapters(sourceID, mangaURL, allChapters),
		enginefake.WithMangaDetails(sourceID, mangaURL, sourceengine.MangaDetails{Title: mangaTitle}),
	)
	ing := ingest.NewIngest(fc, client)

	// AddSeries(..., "Alpha") must create a SeriesProvider scoped to Alpha only.
	if _, err := ing.AddSeries(ctx, sourceID, mangaURL, mangaTitle, "Alpha"); err != nil {
		t.Fatalf("AddSeries(Alpha): %v", err)
	}
	// AddSeries(..., "Beta") must create a SECOND, independent SeriesProvider.
	if _, err := ing.AddSeries(ctx, sourceID, mangaURL, mangaTitle, "Beta"); err != nil {
		t.Fatalf("AddSeries(Beta): %v", err)
	}

	sps := client.SeriesProvider.Query().AllX(ctx)
	if len(sps) != 2 {
		t.Fatalf("SeriesProvider count: got %d, want 2", len(sps))
	}
	byScanlator := indexSeriesProvidersByScanlator(sps)

	alphaSP := requireSeriesProvider(t, byScanlator, "Alpha", "1001")
	betaSP := requireSeriesProvider(t, byScanlator, "Beta", "1001")
	if alphaSP.ID == betaSP.ID {
		t.Fatalf("Alpha and Beta SeriesProvider rows must be distinct, got the same ID %s", alphaSP.ID)
	}

	// Each row's ProviderChapter feed must hold ONLY its own scanlator's chapters.
	assertProviderChapterURLs(t, ctx, client, alphaSP.ID, buildWantURLs(alphaChapters))
	assertProviderChapterURLs(t, ctx, client, betaSP.ID, buildWantURLs(betaChapters))
}

// indexSeriesProvidersByScanlator builds a scanlator → SeriesProvider lookup,
// used by tests that assert two scanlator-scoped rows coexist for one source.
func indexSeriesProvidersByScanlator(sps []*ent.SeriesProvider) map[string]*ent.SeriesProvider {
	byScanlator := make(map[string]*ent.SeriesProvider, len(sps))
	for _, sp := range sps {
		byScanlator[sp.Scanlator] = sp
	}
	return byScanlator
}

// requireSeriesProvider fetches the SeriesProvider keyed by scanlator from the
// index built by indexSeriesProvidersByScanlator, failing the test if absent
// or if its Provider does not match wantProvider.
func requireSeriesProvider(t *testing.T, byScanlator map[string]*ent.SeriesProvider, scanlator, wantProvider string) *ent.SeriesProvider {
	t.Helper()
	sp, ok := byScanlator[scanlator]
	if !ok {
		t.Fatalf("no SeriesProvider found for scanlator %q", scanlator)
	}
	if sp.Provider != wantProvider {
		t.Errorf("SeriesProvider.Provider: got %q, want %q", sp.Provider, wantProvider)
	}
	return sp
}

// TestIngest_AddSeries_ScanlatorFilter_EmptyIngestsAll verifies the
// regression-critical default: AddSeries(..., "") ingests ALL chapters (across
// every scanlator, tagged or untagged) into a single scanlator==""
// SeriesProvider row.
func TestIngest_AddSeries_ScanlatorFilter_EmptyIngestsAll(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		sourceID   int64 = 1002
		mangaURL         = "/manga/mixed-scanlators"
		mangaTitle       = "Mixed Scanlators Manga"
	)

	alphaChapters := makeChaptersWithScanlator(2, 1, "Alpha")
	betaChapters := makeChaptersWithScanlator(2, 3, "Beta")
	untaggedChapters := makeChaptersWithScanlator(1, 5, "") // no scanlator credited
	allChapters := append(append(append([]sourceengine.Chapter{}, alphaChapters...), betaChapters...), untaggedChapters...)

	fc := enginefake.New(
		enginefake.WithChapters(sourceID, mangaURL, allChapters),
		enginefake.WithMangaDetails(sourceID, mangaURL, sourceengine.MangaDetails{Title: mangaTitle}),
	)
	ing := ingest.NewIngest(fc, client)

	if _, err := ing.AddSeries(ctx, sourceID, mangaURL, mangaTitle, ""); err != nil {
		t.Fatalf("AddSeries(\"\"): %v", err)
	}

	sp := assertSeriesProvider(t, ctx, client, "1002", mangaURL, mangaTitle)
	if sp.Scanlator != "" {
		t.Errorf("SeriesProvider.Scanlator: got %q, want \"\"", sp.Scanlator)
	}
	// All 5 chapters (2 Alpha + 2 Beta + 1 untagged) must be present on the "" row.
	assertProviderChapterURLs(t, ctx, client, sp.ID, buildWantURLs(allChapters))
}

// TestIngest_AddSeries_ScanlatorFilter_RefreshUpdatesSameRow verifies the
// idempotency/refresh requirement: calling AddSeries(..., "Alpha") twice
// updates the SAME SeriesProvider row (no duplicate) and the ProviderChapter
// feed stays Alpha-only.
func TestIngest_AddSeries_ScanlatorFilter_RefreshUpdatesSameRow(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		sourceID   int64 = 1003
		mangaURL         = "/manga/refresh-scanlator"
		mangaTitle       = "Refresh Scanlator Manga"
	)

	alphaChapters := makeChaptersWithScanlator(2, 1, "Alpha")
	betaChapters := makeChaptersWithScanlator(1, 3, "Beta")
	allChapters := append(append([]sourceengine.Chapter{}, alphaChapters...), betaChapters...)

	fc := enginefake.New(
		enginefake.WithChapters(sourceID, mangaURL, allChapters),
		enginefake.WithMangaDetails(sourceID, mangaURL, sourceengine.MangaDetails{Title: mangaTitle}),
	)
	ing := ingest.NewIngest(fc, client)

	// First call: creates the Alpha-scoped SeriesProvider.
	if _, err := ing.AddSeries(ctx, sourceID, mangaURL, mangaTitle, "Alpha"); err != nil {
		t.Fatalf("first AddSeries(Alpha): %v", err)
	}
	first := client.SeriesProvider.Query().OnlyX(ctx)

	// Second call (simulating a refresh sweep re-fetch): must update the SAME row.
	result2, err := ing.AddSeries(ctx, sourceID, mangaURL, mangaTitle, "Alpha")
	if err != nil {
		t.Fatalf("second AddSeries(Alpha): %v", err)
	}
	if result2.NewChapters != 0 {
		t.Errorf("second AddSeries(Alpha): NewChapters got %d, want 0 (idempotent)", result2.NewChapters)
	}

	sps := client.SeriesProvider.Query().AllX(ctx)
	if len(sps) != 1 {
		t.Fatalf("SeriesProvider count after refresh: got %d, want 1 (no duplicate row)", len(sps))
	}
	if sps[0].ID != first.ID {
		t.Fatalf("SeriesProvider row changed identity across refresh: got %s, want %s", sps[0].ID, first.ID)
	}
	if sps[0].Scanlator != "Alpha" {
		t.Errorf("SeriesProvider.Scanlator after refresh: got %q, want %q", sps[0].Scanlator, "Alpha")
	}

	// The feed must still be Alpha-only — Beta's chapter must never have leaked in.
	assertProviderChapterURLs(t, ctx, client, sps[0].ID, buildWantURLs(alphaChapters))
}

// TestIngest_AddSeries_MangaDetailsError verifies that a MangaDetails client
// error is propagated and no SeriesProvider row is created (the series row is
// created first, but the provider/chapter rows must not be).
func TestIngest_AddSeries_MangaDetailsError(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	sentinel := errors.New("engine: manga details unavailable")
	fc := enginefake.New(
		enginefake.WithChapters(7, "/manga/some-series", makeChapters(1)),
		enginefake.WithError("MangaDetails", sentinel),
	)
	ing := ingest.NewIngest(fc, client)

	_, err := ing.AddSeries(ctx, 7, "/manga/some-series", "Some Series", "")
	if !errors.Is(err, sentinel) {
		t.Errorf("AddSeries: err got %v, want to wrap %v", err, sentinel)
	}
	// No SeriesProvider rows should have been created.
	if n := len(client.SeriesProvider.Query().AllX(ctx)); n != 0 {
		t.Errorf("SeriesProvider count after MangaDetails error: got %d, want 0", n)
	}
}

// TestMapToFetchedChapters_ProviderIndexReversed proves the P2 mapper-audit M6
// fix: the engine host's raw chapter list is newest-first (index 0 = newest —
// see SourceCalls.chapters), and mapToFetchedChapters must assign
// ProviderIndex REVERSED (oldest=0 .. newest=N-1) to match Suwayomi's own
// sourceOrder convention (Chapter.kt's `uniqueChapters.reversed().
// forEachIndexed`). A raw 3-chapter list ["newest", "middle", "oldest"] must
// therefore map to ProviderIndex [2, 1, 0] — NOT the raw position [0, 1, 2].
func TestMapToFetchedChapters_ProviderIndexReversed(t *testing.T) {
	raw := []sourceengine.Chapter{
		{Name: "newest", Number: 3, URL: "/ch/3"},
		{Name: "middle", Number: 2, URL: "/ch/2"},
		{Name: "oldest", Number: 1, URL: "/ch/1"},
	}
	got := ingest.MapToFetchedChapters(raw, "")
	if len(got) != 3 {
		t.Fatalf("MapToFetchedChapters: got %d chapters, want 3", len(got))
	}
	wantIndex := []int{2, 1, 0} // reversed: raw idx 0 (newest) -> 2, raw idx 2 (oldest) -> 0
	for i, fc := range got {
		if fc.ProviderIndex != wantIndex[i] {
			t.Errorf("ProviderIndex[%d] (%s): got %d, want %d", i, fc.Name, fc.ProviderIndex, wantIndex[i])
		}
	}
	// The OLDEST chapter (last in the raw, newest-first list) must carry the
	// LOWEST index — the direction Suwayomi's sourceOrder uses.
	if got[2].ProviderIndex != 0 {
		t.Errorf("oldest chapter ProviderIndex: got %d, want 0", got[2].ProviderIndex)
	}
	// The NEWEST chapter (first in the raw list) must carry the HIGHEST index.
	if got[0].ProviderIndex != len(got)-1 {
		t.Errorf("newest chapter ProviderIndex: got %d, want %d", got[0].ProviderIndex, len(got)-1)
	}
}

func TestAddSeriesRefRetainsAndResolvesAddressContext(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	const sourceID int64 = 4242
	const mangaURL = "/manga/addressed"
	const webURL = "https://example.test/manga/addressed"
	engine := enginefake.New(
		enginefake.WithChaptersResult(sourceID, mangaURL, sourceengine.ChaptersResult{
			Chapters:    makeChapters(1),
			AddressMode: sourceengine.AddressModeURLSearch,
		}),
		enginefake.WithMangaDetails(sourceID, mangaURL, sourceengine.MangaDetails{
			Title:       "Addressed",
			AddressMode: sourceengine.AddressModeURLSearch,
		}),
	)

	ing := ingest.NewIngest(engine, client)
	_, err := ing.AddSeriesUngatedRef(ctx, sourceengine.ProviderRef{
		SourceID: sourceID,
		URL:      mangaURL,
		WebURL:   webURL,
	}, "Addressed", "")
	if err != nil {
		t.Fatalf("AddSeriesUngatedRef: %v", err)
	}

	provider := client.SeriesProvider.Query().OnlyX(ctx)
	if got := provider.AddressMode.String(); got != "url_search" {
		t.Fatalf("address mode = %q, want url_search", got)
	}
	if provider.URL != mangaURL {
		t.Fatalf("stored address URL = %q, want unchanged %q", provider.URL, mangaURL)
	}
	if provider.WebURL != webURL {
		t.Fatalf("stored web URL = %q, want %q", provider.WebURL, webURL)
	}
}

func TestAddSeriesRefPersistsChapterResolutionBeforeLaterDetailsFailure(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	const sourceID int64 = 4243
	const mangaURL = "/manga/existing-address"
	series := client.Series.Create().SetTitle("Existing Address").SetSlug("existing-address").SaveX(ctx)
	provider := client.SeriesProvider.Create().SetSeries(series).SetProvider("4243").SetURL(mangaURL).SaveX(ctx)
	engine := enginefake.New(
		enginefake.WithChaptersResult(sourceID, mangaURL, sourceengine.ChaptersResult{
			Chapters:    makeChapters(1),
			AddressMode: sourceengine.AddressModeDirect,
		}),
		enginefake.WithError("MangaDetails", errors.New("details unavailable")),
	)

	_, err := ingest.NewIngest(engine, client).AddSeriesUngatedRef(ctx, sourceengine.ProviderRef{
		SourceID: sourceID,
		URL:      mangaURL,
	}, "Existing Address", "")
	if err == nil {
		t.Fatal("AddSeriesUngatedRef error = nil, want later details failure")
	}

	got := client.SeriesProvider.GetX(ctx, provider.ID)
	if got.AddressMode.String() != "direct" {
		t.Fatalf("address mode after successful chapters + failed details = %q, want direct", got.AddressMode)
	}
}

func TestAddSeriesWithChaptersRefPreservesExistingProviderAddress(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	const sourceID int64 = 4244
	const title = "Address Preservation"
	series := client.Series.Create().SetTitle(title).SetSlug("address-preservation").SaveX(ctx)
	provider := client.SeriesProvider.Create().
		SetSeries(series).
		SetProvider("4244").
		SetURL("/current/address").
		SetWebURL("https://current.example/title").
		SetAddressMode("direct").
		SaveX(ctx)
	engine := enginefake.New(
		enginefake.WithMangaDetails(sourceID, "/stale/address", sourceengine.MangaDetails{
			Title:       title,
			RealURL:     "https://stale.example/title",
			AddressMode: sourceengine.AddressModeURLSearch,
		}),
	)

	_, err := ingest.NewIngest(engine, client).AddSeriesWithChaptersRef(ctx, sourceengine.ProviderRef{
		SourceID:    sourceID,
		URL:         "/stale/address",
		WebURL:      "https://stale.example/title",
		AddressMode: sourceengine.AddressModeURLSearch,
	}, title, "", sourceengine.ChaptersResult{
		Chapters:    makeChapters(1),
		AddressMode: sourceengine.AddressModeURLSearch,
	})
	if err != nil {
		t.Fatalf("AddSeriesWithChaptersRef: %v", err)
	}

	got := client.SeriesProvider.GetX(ctx, provider.ID)
	if got.URL != "/current/address" {
		t.Errorf("stored URL = %q, want preserved current address", got.URL)
	}
	if got.WebURL != "https://current.example/title" {
		t.Errorf("stored web URL = %q, want preserved current address", got.WebURL)
	}
	if got.AddressMode.String() != "direct" {
		t.Errorf("stored address mode = %q, want preserved direct", got.AddressMode)
	}
}

func TestReconcileProviderReplacePreservesIdentityAndUpsertsFeed(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	const sourceID int64 = 4245
	series := client.Series.Create().SetTitle("Replacement").SetSlug("replacement").SaveX(ctx)
	provider := client.SeriesProvider.Create().
		SetSeries(series).
		SetProvider("4245").
		SetProviderName("Source").
		SetScanlator("").
		SetURL("/old/address").
		SetWebURL("https://old.example/title").
		SetAddressMode("direct").
		SetImportance(50).
		SetMetadata(true).
		SaveX(ctx)
	existingChapter := client.Chapter.Create().
		SetSeries(series).
		SetChapterKey("1").
		SetNumber(1).
		SetState("downloaded").
		SetFilename("chapter-1.cbz").
		SetSatisfiedByProviderID(provider.ID).
		SetSatisfiedImportance(50).
		SaveX(ctx)
	existingProviderChapter := client.ProviderChapter.Create().
		SetSeriesProvider(provider).
		SetChapterKey("1").
		SetNumber(1).
		SetName("Chapter 1 old").
		SetURL("/old/chapter-1").
		SetAttempts(2).
		SetLastError("retry later").
		SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProvider(provider).
		SetChapterKey("9").
		SetNumber(9).
		SetURL("/missing-upstream/chapter-9").
		SaveX(ctx)
	stagingRoot := t.TempDir()
	stagingDir := seedReconcileStaging(t, stagingRoot, existingProviderChapter.ID)

	before := client.SeriesProvider.GetX(ctx, provider.ID)
	tx, err := client.Tx(ctx)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	ing := ingest.NewIngest(enginefake.New(), client).
		WithProviderChapterCacheInvalidator(download.NewProviderChapterCacheInvalidator(stagingRoot))
	result, err := ing.ReconcileProvider(ctx, tx, provider.ID, ingest.ProviderReconcileInput{
		Ref: sourceengine.ProviderRef{
			SourceID:    sourceID,
			URL:         "/new/address",
			WebURL:      "https://new.example/title",
			AddressMode: sourceengine.AddressModeURLSearch,
		},
		Title:        "Replacement at Source",
		CoverURL:     "https://new.example/cover.jpg",
		ProviderName: "Renamed Source",
		Chapters: []sourceengine.Chapter{
			{Name: "Chapter 1 new", Number: 1, URL: "/new/chapter-1"},
			{Name: "Chapter 2", Number: 2, URL: "/new/chapter-2"},
		},
	}, ingest.ReplaceProviderAddress)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("ReconcileProvider: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	assertReplacementResultAndCache(t, stagingDir, result)
	gotProvider := client.SeriesProvider.GetX(ctx, provider.ID)
	assertReplacementProviderIdentity(t, before, gotProvider)
	assertReplacementProviderValues(t, gotProvider)
	assertReplacementProviderChapter(t, existingProviderChapter, client.ProviderChapter.GetX(ctx, existingProviderChapter.ID))
	assertReplacementLogicalChapter(t, existingChapter, client.Chapter.GetX(ctx, existingChapter.ID))
	assertReplacementFeed(t, ctx, client, series.ID, provider.ID)
}

func TestReconcileProviderTransactionRollbackRestoresAddressAndFeed(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	series := client.Series.Create().SetTitle("Rollback").SetSlug("rollback").SaveX(ctx)
	provider := client.SeriesProvider.Create().
		SetSeries(series).
		SetProvider("4246").
		SetURL("/old/address").
		SetWebURL("https://old.example/title").
		SetAddressMode("direct").
		SaveX(ctx)
	providerChapter := client.ProviderChapter.Create().
		SetSeriesProvider(provider).
		SetChapterKey("1").
		SetNumber(1).
		SetName("Chapter 1 old").
		SetURL("/old/chapter-1").
		SetPageLinks([]fetcher.PageLink{{URL: "/old/page-1"}}).
		SaveX(ctx)
	client.Chapter.Create().SetSeries(series).SetChapterKey("1").SetNumber(1).SaveX(ctx)
	stagingRoot := t.TempDir()
	stagingDir := seedReconcileStaging(t, stagingRoot, providerChapter.ID)

	tx, err := client.Tx(ctx)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	_, err = ingest.NewIngest(enginefake.New(), client).
		WithProviderChapterCacheInvalidator(download.NewProviderChapterCacheInvalidator(stagingRoot)).
		ReconcileProvider(ctx, tx, provider.ID, ingest.ProviderReconcileInput{
			Ref: sourceengine.ProviderRef{SourceID: 4246, URL: "/new/address", WebURL: "https://new.example/title", AddressMode: sourceengine.AddressModeURLSearch},
			Chapters: []sourceengine.Chapter{
				{Name: "Chapter 1 new", Number: 1, URL: "/new/chapter-1"},
				{Name: "Chapter 2", Number: 2, URL: "/chapter-2"},
			},
		}, ingest.ReplaceProviderAddress)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("ReconcileProvider: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	assertRollbackRestored(t, ctx, client, provider.ID, providerChapter.ID, stagingDir)
}

type blockedOldAddressFetcher struct {
	entered     chan fetcher.FetchRef
	release     chan struct{}
	stagingRoot string
}

func (f *blockedOldAddressFetcher) Fetch(_ context.Context, ref fetcher.FetchRef) (fetcher.ChapterPages, error) {
	f.entered <- ref
	<-f.release
	dir := filepath.Join(f.stagingRoot, ref.ProviderChapterID.String())
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fetcher.ChapterPages{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, "000001.jpg"), []byte("old-address-page"), 0o600); err != nil {
		return fetcher.ChapterPages{}, err
	}
	return fetcher.ChapterPages{
		StagingDir: dir,
		PageLinks:  []fetcher.PageLink{{URL: "/old/page-1"}},
	}, errors.New("old-address fetch stopped")
}

func startProviderReplacement(
	ctx context.Context,
	client *ent.Client,
	stagingRoot string,
	providerID uuid.UUID,
	sourceID int64,
) chan error {
	done := make(chan error, 1)
	go func() {
		tx, err := client.Tx(ctx)
		if err == nil {
			_, err = ingest.NewIngest(enginefake.New(), client).
				WithProviderChapterCacheInvalidator(download.NewProviderChapterCacheInvalidator(stagingRoot)).
				ReconcileProvider(ctx, tx, providerID, ingest.ProviderReconcileInput{
					Ref:      sourceengine.ProviderRef{SourceID: sourceID, URL: "/new/address", AddressMode: sourceengine.AddressModeDirect},
					Chapters: []sourceengine.Chapter{{Name: "Chapter 1", Number: 1, URL: "/new/chapter-1"}},
				}, ingest.ReplaceProviderAddress)
		}
		if err == nil {
			err = tx.Commit()
		} else if tx != nil {
			_ = tx.Rollback()
		}
		done <- err
	}()
	return done
}

func assertReplacementStillBlocked(t *testing.T, done chan error) {
	t.Helper()
	select {
	case err := <-done:
		t.Errorf("replacement completed before old fetch release: %v", err)
		done <- err
	case <-time.After(150 * time.Millisecond):
	}
}

func TestReconcileProviderWaitsForActiveOldAddressFetchAndInvalidatesItsArtifacts(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	const sourceID int64 = 4248
	series := client.Series.Create().SetTitle("Blocked Fetch").SetSlug("blocked-fetch").SaveX(ctx)
	provider := client.SeriesProvider.Create().
		SetSeries(series).
		SetProvider("4248").
		SetURL("/old/address").
		SetAddressMode("direct").
		SetImportance(10).
		SaveX(ctx)
	providerChapter := client.ProviderChapter.Create().
		SetSeriesProvider(provider).
		SetChapterKey("1").
		SetNumber(1).
		SetURL("/old/chapter-1").
		SaveX(ctx)
	client.Chapter.Create().SetSeries(series).SetChapterKey("1").SetNumber(1).SaveX(ctx)

	stagingRoot := t.TempDir()
	blockedFetcher := &blockedOldAddressFetcher{
		entered:     make(chan fetcher.FetchRef, 1),
		release:     make(chan struct{}),
		stagingRoot: stagingRoot,
	}
	dispatcher := download.New(client, blockedFetcher, sse.NewHub(), download.Config{
		Storage:     t.TempDir(),
		StagingRoot: stagingRoot,
	}, settings.Static{Retries: 3, Backoff: time.Hour, DownloadConc: 1}, nil)
	downloadDone := make(chan error, 1)
	go func() {
		_, err := dispatcher.RunOnce(ctx)
		downloadDone <- err
	}()
	fetchRef := <-blockedFetcher.entered
	if fetchRef.URL != "/old/chapter-1" {
		t.Fatalf("blocked fetch URL = %q, want old chapter address", fetchRef.URL)
	}

	replacementDone := startProviderReplacement(ctx, client, stagingRoot, provider.ID, sourceID)
	assertReplacementStillBlocked(t, replacementDone)
	close(blockedFetcher.release)
	if err := <-downloadDone; err != nil {
		t.Fatalf("download cycle: %v", err)
	}
	if err := <-replacementDone; err != nil {
		t.Fatalf("replacement: %v", err)
	}
	got := client.ProviderChapter.GetX(ctx, providerChapter.ID)
	if got.URL != "/new/chapter-1" || len(got.PageLinks) != 0 {
		t.Errorf("provider chapter after replacement = URL %q links %+v, want new address with empty resolver cache", got.URL, got.PageLinks)
	}
	if _, err := os.Stat(filepath.Join(stagingRoot, providerChapter.ID.String())); !os.IsNotExist(err) {
		t.Errorf("old-address staging directory stat error = %v, want not exist", err)
	}
}

type blockingDetailsClient struct {
	*enginefake.Client
	entered chan struct{}
	release chan struct{}
}

func (c *blockingDetailsClient) MangaDetailsRef(ctx context.Context, ref sourceengine.ProviderRef) (sourceengine.MangaDetails, error) {
	close(c.entered)
	select {
	case <-ctx.Done():
		return sourceengine.MangaDetails{}, ctx.Err()
	case <-c.release:
		return c.Client.MangaDetailsRef(ctx, ref)
	}
}

func TestStaleRefreshCannotRestoreAddressAfterReplacementCommit(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	const sourceID int64 = 4247
	const title = "Concurrent Replacement"
	series := client.Series.Create().SetTitle(title).SetSlug("concurrent-replacement").SaveX(ctx)
	provider := client.SeriesProvider.Create().
		SetSeries(series).
		SetProvider("4247").
		SetURL("/old/address").
		SetWebURL("https://old.example/title").
		SetAddressMode("direct").
		SaveX(ctx)
	providerChapter := client.ProviderChapter.Create().
		SetSeriesProvider(provider).
		SetChapterKey("1").
		SetNumber(1).
		SetURL("/old/chapter-1").
		SaveX(ctx)
	client.Chapter.Create().SetSeries(series).SetChapterKey("1").SetNumber(1).SaveX(ctx)
	base := enginefake.New(enginefake.WithMangaDetails(sourceID, "/old/address", sourceengine.MangaDetails{
		Title:       title,
		RealURL:     "https://old.example/title",
		AddressMode: sourceengine.AddressModeDirect,
	}))
	engine := &blockingDetailsClient{Client: base, entered: make(chan struct{}), release: make(chan struct{})}
	ing := ingest.NewIngest(engine, client)

	refreshErr := make(chan error, 1)
	go func() {
		_, err := ing.AddSeriesWithChaptersRef(ctx, sourceengine.ProviderRef{
			SourceID: sourceID, URL: "/old/address", WebURL: "https://old.example/title", AddressMode: sourceengine.AddressModeDirect,
		}, title, "", sourceengine.ChaptersResult{
			Chapters:    []sourceengine.Chapter{{Name: "Chapter 1 stale", Number: 1, URL: "/old/chapter-1"}},
			AddressMode: sourceengine.AddressModeDirect,
		})
		refreshErr <- err
	}()
	<-engine.entered

	tx, err := client.Tx(ctx)
	if err != nil {
		t.Fatalf("begin replacement transaction: %v", err)
	}
	_, err = ing.ReconcileProvider(ctx, tx, provider.ID, ingest.ProviderReconcileInput{
		Ref:      sourceengine.ProviderRef{SourceID: sourceID, URL: "/new/address", WebURL: "https://new.example/title", AddressMode: sourceengine.AddressModeURLSearch},
		Chapters: []sourceengine.Chapter{{Name: "Chapter 1 current", Number: 1, URL: "/new/chapter-1"}},
	}, ingest.ReplaceProviderAddress)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("ReconcileProvider: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit replacement: %v", err)
	}
	close(engine.release)
	if err := <-refreshErr; err != nil {
		t.Fatalf("stale refresh: %v", err)
	}

	got := client.SeriesProvider.GetX(ctx, provider.ID)
	if got.URL != "/new/address" || got.WebURL != "https://new.example/title" || got.AddressMode.String() != "url_search" {
		t.Errorf("provider address after stale refresh = %q/%q/%q, want committed replacement", got.URL, got.WebURL, got.AddressMode)
	}
	gotProviderChapter := client.ProviderChapter.GetX(ctx, providerChapter.ID)
	if gotProviderChapter.URL != "/new/chapter-1" || gotProviderChapter.Name != "Chapter 1 current" {
		t.Errorf("provider chapter after stale refresh = %q/%q, want committed replacement feed", gotProviderChapter.URL, gotProviderChapter.Name)
	}
}
