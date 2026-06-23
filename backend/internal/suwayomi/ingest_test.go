// Package suwayomi_test — unit + integration tests for Ingest.
//
// Tests use an in-process stub Client (no Java, no network) and the testdb
// ephemeral-Postgres harness (Docker required). The stub drives all error paths;
// real DB rows validate idempotency and suwayomi_chapter_id round-trips.
package suwayomi_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// --- stub Client for ingest tests --------------------------------------------

// ingestStubClient implements suwayomi.Client with canned responses for Search
// and FetchChapters. Other methods panic if called — Ingest must not call them.
type ingestStubClient struct {
	// searchResults is the slice returned by Search.
	searchResults []suwayomi.Manga
	// searchErr is the error returned by Search (nil = success).
	searchErr error
	// chapters is the slice returned by FetchChapters.
	chapters []suwayomi.Chapter
	// chaptersErr is the error returned by FetchChapters (nil = success).
	chaptersErr error
}

func (s *ingestStubClient) Search(_ context.Context, _, _ string) ([]suwayomi.Manga, error) {
	return s.searchResults, s.searchErr
}

func (s *ingestStubClient) FetchChapters(_ context.Context, _ int) ([]suwayomi.Chapter, error) {
	return s.chapters, s.chaptersErr
}

// Ingest must never call these methods in M2; panic loudly if reached.
func (s *ingestStubClient) Sources(_ context.Context) ([]suwayomi.Source, error) {
	panic("ingestStubClient.Sources: must not be called by Ingest")
}
func (s *ingestStubClient) MangaChapters(_ context.Context, _ int) ([]suwayomi.Chapter, error) {
	panic("ingestStubClient.MangaChapters: must not be called by Ingest (use FetchChapters)")
}
func (s *ingestStubClient) ChapterPages(_ context.Context, _ int) ([]string, error) {
	panic("ingestStubClient.ChapterPages: must not be called by Ingest")
}
func (s *ingestStubClient) PageBytes(_ context.Context, _ string) ([]byte, string, error) {
	panic("ingestStubClient.PageBytes: must not be called by Ingest")
}

// --- helpers -----------------------------------------------------------------

// ptrF64 returns a pointer to v.
func ptrF64(v float64) *float64 { return &v }

// ptrTime returns a pointer to v.
func ptrTime(v time.Time) *time.Time { return &v }

// makeChapters builds N stub suwayomi.Chapter values with sequential IDs.
// Each chapter has a numeric chapter number equal to its 1-based index so that
// NormalizeChapterKey produces deterministic, distinct keys.
func makeChapters(n int) []suwayomi.Chapter {
	chs := make([]suwayomi.Chapter, n)
	for i := range n {
		num := float64(i + 1)
		chs[i] = suwayomi.Chapter{
			ID:     100 + i, // suwayomi chapter IDs start at 100
			Index:  i,
			Name:   "Chapter " + chapter.FormatChapterNumber(num),
			Number: ptrF64(num),
			URL:    "https://suwayomi.test/ch/" + chapter.FormatChapterNumber(num),
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
// expected provider name, suwayomi_id, and title.
func assertSeriesProvider(t *testing.T, ctx context.Context, client *ent.Client, wantProvider string, wantSuwayomiID int, wantTitle string) *ent.SeriesProvider {
	t.Helper()
	list := client.SeriesProvider.Query().AllX(ctx)
	if len(list) != 1 {
		t.Fatalf("SeriesProvider count: got %d, want 1", len(list))
	}
	sp := list[0]
	if sp.Provider != wantProvider {
		t.Errorf("SeriesProvider.Provider: got %q, want %q", sp.Provider, wantProvider)
	}
	if sp.SuwayomiID != wantSuwayomiID {
		t.Errorf("SeriesProvider.SuwayomiID: got %d, want %d", sp.SuwayomiID, wantSuwayomiID)
	}
	if sp.Title != wantTitle {
		t.Errorf("SeriesProvider.Title: got %q, want %q", sp.Title, wantTitle)
	}
	return sp
}

// assertProviderChapterIDs checks that K ProviderChapters exist for spID and
// that each one has the expected suwayomi_chapter_id from the key→id map.
func assertProviderChapterIDs(
	t *testing.T,
	ctx context.Context,
	client *ent.Client,
	spID uuid.UUID,
	wantIDs map[string]int,
) {
	t.Helper()
	pcs := client.ProviderChapter.Query().
		Where(entproviderchapter.SeriesProviderID(spID)).
		AllX(ctx)
	if len(pcs) != len(wantIDs) {
		t.Fatalf("ProviderChapter count: got %d, want %d", len(pcs), len(wantIDs))
	}
	for _, pc := range pcs {
		wantID, ok := wantIDs[pc.ChapterKey]
		if !ok {
			t.Errorf("ProviderChapter %q: unexpected chapter_key", pc.ChapterKey)
			continue
		}
		if pc.SuwayomiChapterID != wantID {
			t.Errorf("ProviderChapter %q: SuwayomiChapterID got %d, want %d",
				pc.ChapterKey, pc.SuwayomiChapterID, wantID)
		}
	}
}

// buildWantIDs converts a []suwayomi.Chapter to the chapter_key → suwayomi ID
// map expected by assertProviderChapterIDs.
func buildWantIDs(chs []suwayomi.Chapter) map[string]int {
	m := make(map[string]int, len(chs))
	for _, ch := range chs {
		m[chapter.NormalizeChapterKey(ch.Number, ch.Name)] = ch.ID
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
// (suwayomi_id = mangaID), K ProviderChapters each with suwayomi_chapter_id set
// to the corresponding suwayomi.Chapter.ID, and one Chapter per key at
// state=wanted (reusing the M1 dedup invariant).
func TestIngest_AddSeries_Basic(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		mangaID    = 42
		mangaTitle = "My Test Manga"
		sourceName = "mangadex"
		k          = 3
	)

	stubs := makeChapters(k)
	sc := &ingestStubClient{
		searchResults: []suwayomi.Manga{{ID: mangaID, Title: mangaTitle}},
		chapters:      stubs,
	}

	ing := suwayomi.NewIngest(sc, client)
	result, err := ing.AddSeries(ctx, sourceName, mangaID, mangaTitle)
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
	sp := assertSeriesProvider(t, ctx, client, sourceName, mangaID, mangaTitle)
	assertProviderChapterIDs(t, ctx, client, sp.ID, buildWantIDs(stubs))
	assertChapterCount(t, ctx, client, k)
}

// TestIngest_AddSeries_Idempotent verifies that calling AddSeries twice for the
// same manga produces no duplicate Series, SeriesProvider, or Chapter rows, and
// that suwayomi_chapter_id values remain correct after the second call.
func TestIngest_AddSeries_Idempotent(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		mangaID    = 99
		mangaTitle = "Idempotent Manga"
		sourceName = "test-source"
		k          = 2
	)

	stubs := makeChapters(k)
	sc := &ingestStubClient{chapters: stubs}
	ing := suwayomi.NewIngest(sc, client)

	// First call.
	if _, err := ing.AddSeries(ctx, sourceName, mangaID, mangaTitle); err != nil {
		t.Fatalf("first AddSeries: %v", err)
	}

	// Second call (idempotent re-add).
	result2, err := ing.AddSeries(ctx, sourceName, mangaID, mangaTitle)
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
	sp := assertSeriesProvider(t, ctx, client, sourceName, mangaID, mangaTitle)

	// K Chapter rows and K ProviderChapters with correct suwayomi_chapter_id.
	assertChapterCount(t, ctx, client, k)
	assertProviderChapterIDs(t, ctx, client, sp.ID, buildWantIDs(stubs))
}

// TestIngest_AddSeries_FetchChaptersError verifies that a FetchChapters client
// error is propagated as-is and no DB rows are created.
func TestIngest_AddSeries_FetchChaptersError(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	sentinel := errors.New("suwayomi: manga not found")
	sc := &ingestStubClient{chaptersErr: sentinel}
	ing := suwayomi.NewIngest(sc, client)

	_, err := ing.AddSeries(ctx, "src", 7, "Broken Manga")
	if !errors.Is(err, sentinel) {
		t.Errorf("AddSeries: err got %v, want to wrap %v", err, sentinel)
	}
	// No Series rows should have been created (client error fires before DB touch).
	if n := len(client.Series.Query().AllX(ctx)); n != 0 {
		t.Errorf("Series count after client error: got %d, want 0", n)
	}
}

// TestIngest_Search verifies that Search is a transparent passthrough to the
// underlying client, returning the same result and propagating errors.
func TestIngest_Search(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	want := []suwayomi.Manga{{ID: 1, Title: "Alpha"}, {ID: 2, Title: "Beta"}}
	sc := &ingestStubClient{searchResults: want}
	ing := suwayomi.NewIngest(sc, client)

	got, err := ing.Search(ctx, "src", "alpha")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("Search: got %d results, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].ID != want[i].ID || got[i].Title != want[i].Title {
			t.Errorf("Search[%d]: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestIngest_Search_Error verifies that a Search error from the client is
// propagated to the caller.
func TestIngest_Search_Error(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	sentinel := errors.New("search failed")
	sc := &ingestStubClient{searchErr: sentinel}
	ing := suwayomi.NewIngest(sc, client)

	_, err := ing.Search(ctx, "src", "query")
	if !errors.Is(err, sentinel) {
		t.Errorf("Search error: got %v, want to wrap %v", err, sentinel)
	}
}

// TestIngest_AddSeries_UnnumberedChapter verifies that chapters without a
// chapter number are keyed by name (NormalizeChapterKey nil-number path) and
// still get their suwayomi_chapter_id set correctly.
func TestIngest_AddSeries_UnnumberedChapter(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	specialTime := time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)
	stubs := []suwayomi.Chapter{
		{
			ID:         201,
			Index:      0,
			Name:       "Special Volume",
			Number:     nil, // no chapter number
			URL:        "https://suwayomi.test/special",
			UploadDate: ptrTime(specialTime),
			PageCount:  12,
		},
	}
	sc := &ingestStubClient{chapters: stubs}
	ing := suwayomi.NewIngest(sc, client)

	result, err := ing.AddSeries(ctx, "source", 55, "Special Series")
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
		t.Errorf("ChapterKey: got %q, want %q", pc.ChapterKey, wantKey)
	}
	if pc.SuwayomiChapterID != 201 {
		t.Errorf("SuwayomiChapterID: got %d, want 201", pc.SuwayomiChapterID)
	}
}

// TestIngest_AddSeries_TitleUpdate verifies that re-calling AddSeries with a
// changed title UPDATES Series.Title while keeping Series.Slug unchanged and
// creates no duplicate Series row (covers the upsertSeries title-update branch).
func TestIngest_AddSeries_TitleUpdate(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		mangaID      = 88
		initialTitle = "some manga title"
		// updatedTitle has the same slug ("some-manga-title") after Slugify but
		// different casing, exercising the update branch of upsertSeries.
		updatedTitle = "Some Manga Title"
		sourceName   = "test-source"
	)

	stubs := makeChapters(1)
	sc := &ingestStubClient{chapters: stubs}
	ing := suwayomi.NewIngest(sc, client)

	// First call: creates the Series row.
	if _, err := ing.AddSeries(ctx, sourceName, mangaID, initialTitle); err != nil {
		t.Fatalf("first AddSeries: %v", err)
	}

	initialSlug := disk.Slugify(initialTitle)
	assertSeries(t, ctx, client, initialTitle, initialSlug)

	// Second call with a changed title: Series.Title must be updated.
	if _, err := ing.AddSeries(ctx, sourceName, mangaID, updatedTitle); err != nil {
		t.Fatalf("second AddSeries (title change): %v", err)
	}

	// Still exactly one Series row.
	list := client.Series.Query().AllX(ctx)
	if len(list) != 1 {
		t.Fatalf("Series count after title update: got %d, want 1", len(list))
	}
	// Title must reflect the new value.
	if list[0].Title != updatedTitle {
		t.Errorf("Series.Title after update: got %q, want %q", list[0].Title, updatedTitle)
	}
	// Slug must be unchanged (identity is slug, not title).
	if list[0].Slug != initialSlug {
		t.Errorf("Series.Slug after title update: got %q, want %q (slug must not change)", list[0].Slug, initialSlug)
	}
}

// TestIngest_AddSeries_SeriesProviderTitle verifies that upsertSeriesProvider
// stores the manga display title in SeriesProvider.Title on both the create and
// the update path. A non-empty title is required so that downstream CBZ rendering
// writes a non-empty ComicInfo.Series element for Komga series grouping.
func TestIngest_AddSeries_SeriesProviderTitle(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		mangaID      = 77
		initialTitle = "Dragon Reborn"
		// updatedTitle differs only in casing so disk.Slugify produces the same
		// slug ("dragon-reborn") — the upsertSeries call reuses the existing
		// Series row and we get exactly one SeriesProvider to assert against.
		updatedTitle = "DRAGON REBORN"
		sourceName   = "test-source"
	)

	stubs := makeChapters(1)
	sc := &ingestStubClient{chapters: stubs}
	ing := suwayomi.NewIngest(sc, client)

	// ── Create path: title must be stored on first AddSeries ─────────────────
	if _, err := ing.AddSeries(ctx, sourceName, mangaID, initialTitle); err != nil {
		t.Fatalf("first AddSeries: %v", err)
	}

	sp := client.SeriesProvider.Query().OnlyX(ctx)
	if sp.Title != initialTitle {
		t.Errorf("SeriesProvider.Title after create: got %q, want %q", sp.Title, initialTitle)
	}

	// ── Update path: title must be refreshed on re-add with a changed title ──
	if _, err := ing.AddSeries(ctx, sourceName, mangaID, updatedTitle); err != nil {
		t.Fatalf("second AddSeries (title change): %v", err)
	}

	sp = client.SeriesProvider.Query().OnlyX(ctx)
	if sp.Title != updatedTitle {
		t.Errorf("SeriesProvider.Title after update: got %q, want %q", sp.Title, updatedTitle)
	}

	// Only one SeriesProvider row must exist (idempotent upsert).
	if n := len(client.SeriesProvider.Query().AllX(ctx)); n != 1 {
		t.Errorf("SeriesProvider count: got %d, want 1", n)
	}
}
