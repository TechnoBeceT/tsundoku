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
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
	"github.com/technobecet/tsundoku/internal/ingest"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	enginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
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
