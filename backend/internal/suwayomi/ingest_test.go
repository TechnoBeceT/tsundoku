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

// ingestStubClient implements suwayomi.Client with canned responses for Search,
// FetchChapters, and MangaMeta. Other methods panic if called — Ingest must not
// call them.
type ingestStubClient struct {
	// searchResults is the slice returned by Search.
	searchResults []suwayomi.Manga
	// searchErr is the error returned by Search (nil = success).
	searchErr error
	// chapters is the slice returned by FetchChapters.
	chapters []suwayomi.Chapter
	// chaptersErr is the error returned by FetchChapters (nil = success).
	chaptersErr error
	// mangaMeta is the Manga returned by MangaMeta.
	mangaMeta suwayomi.Manga
	// mangaMetaErr is the error returned by MangaMeta (nil = success).
	mangaMetaErr error
}

func (s *ingestStubClient) Search(_ context.Context, _, _ string) ([]suwayomi.Manga, error) {
	return s.searchResults, s.searchErr
}

func (s *ingestStubClient) Browse(_ context.Context, _ string, _ suwayomi.BrowseType, _ int) (suwayomi.BrowseResult, error) {
	return suwayomi.BrowseResult{}, nil
}

func (s *ingestStubClient) FetchChapters(_ context.Context, _ int) ([]suwayomi.Chapter, error) {
	return s.chapters, s.chaptersErr
}

func (s *ingestStubClient) MangaMeta(_ context.Context, _ int) (suwayomi.Manga, error) {
	return s.mangaMeta, s.mangaMetaErr
}

// Ingest must never call these methods; panic loudly if reached.
func (s *ingestStubClient) Sources(_ context.Context) ([]suwayomi.Source, error) {
	panic("ingestStubClient.Sources: must not be called by Ingest")
}
func (s *ingestStubClient) FetchMangaDetails(_ context.Context, _ int) (suwayomi.Manga, error) {
	panic("ingestStubClient.FetchMangaDetails: must not be called by Ingest")
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
func (s *ingestStubClient) ServerSettings(_ context.Context) (suwayomi.SuwayomiSettings, error) {
	panic("ingestStubClient.ServerSettings: must not be called by Ingest")
}
func (s *ingestStubClient) SetServerSettings(_ context.Context, _ suwayomi.SuwayomiSettingsPatch) error {
	panic("ingestStubClient.SetServerSettings: must not be called by Ingest")
}
func (s *ingestStubClient) Extensions(_ context.Context) ([]suwayomi.Extension, error) {
	panic("ingestStubClient.Extensions: must not be called by Ingest")
}
func (s *ingestStubClient) SetExtensionState(_ context.Context, _ string, _ suwayomi.ExtensionAction) error {
	panic("ingestStubClient.SetExtensionState: must not be called by Ingest")
}
func (s *ingestStubClient) FetchExtensions(_ context.Context) ([]suwayomi.Extension, error) {
	panic("ingestStubClient.FetchExtensions: must not be called by Ingest")
}
func (s *ingestStubClient) ExtensionRepos(_ context.Context) ([]string, error) {
	panic("ingestStubClient.ExtensionRepos: must not be called by Ingest")
}
func (s *ingestStubClient) SetExtensionRepos(_ context.Context, _ []string) error {
	panic("ingestStubClient.SetExtensionRepos: must not be called by Ingest")
}
func (s *ingestStubClient) SourcePreferences(_ context.Context, _ string) ([]suwayomi.SourcePreference, error) {
	panic("ingestStubClient.SourcePreferences: must not be called by Ingest")
}
func (s *ingestStubClient) SetSourcePreference(_ context.Context, _ string, _ int, _ suwayomi.PreferenceValue) ([]suwayomi.SourcePreference, error) {
	panic("ingestStubClient.SetSourcePreference: must not be called by Ingest")
}
func (s *ingestStubClient) ExtensionSources(_ context.Context, _ string) ([]suwayomi.Source, error) {
	panic("ingestStubClient.ExtensionSources: must not be called by Ingest")
}
func (s *ingestStubClient) SetSourceEnabled(_ context.Context, _ string, _ bool) error {
	panic("ingestStubClient.SetSourceEnabled: must not be called by Ingest")
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
		// MangaMeta must return the source title — the same value as the adopt title
		// here because this test does not distinguish canonical from source titles.
		mangaMeta: suwayomi.Manga{Title: mangaTitle},
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
	sc := &ingestStubClient{
		chapters: stubs,
		// MangaMeta returns the same title on every call — idempotency test does
		// not exercise per-source title divergence.
		mangaMeta: suwayomi.Manga{Title: mangaTitle},
	}
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
	sc := &ingestStubClient{
		chapters:  stubs,
		mangaMeta: suwayomi.Manga{Title: "Special Series"},
	}
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
	sc := &ingestStubClient{
		chapters: stubs,
		// MangaMeta returns a fixed source title — this test focuses on Series.Title
		// (canonical) updating, not on per-source title divergence.
		mangaMeta: suwayomi.Manga{Title: "source title"},
	}
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
// stores the source's own title (from MangaMeta) in SeriesProvider.Title on
// both the create and the update path — NOT the canonical adopt title.
// A non-empty title is required so that downstream CBZ rendering writes a
// non-empty ComicInfo.Series element for Komga series grouping.
func TestIngest_AddSeries_SeriesProviderTitle(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		mangaID        = 77
		canonicalTitle = "Dragon Reborn"
		// sourceTitle is what the source (Suwayomi) knows the manga as — it can
		// differ in casing or localisation from the canonical adopt title.
		sourceTitle = "Dragon Reborn (Source)"
		sourceName  = "test-source"
	)

	stubs := makeChapters(1)
	sc := &ingestStubClient{
		chapters:  stubs,
		mangaMeta: suwayomi.Manga{Title: sourceTitle},
	}
	ing := suwayomi.NewIngest(sc, client)

	// ── Create path: source title must be stored on first AddSeries ──────────
	if _, err := ing.AddSeries(ctx, sourceName, mangaID, canonicalTitle); err != nil {
		t.Fatalf("first AddSeries: %v", err)
	}

	sp := client.SeriesProvider.Query().OnlyX(ctx)
	if sp.Title != sourceTitle {
		t.Errorf("SeriesProvider.Title after create: got %q, want %q (source title from MangaMeta)", sp.Title, sourceTitle)
	}
	// Series.Title must remain the canonical title, not the source title.
	series := client.Series.Query().OnlyX(ctx)
	if series.Title != canonicalTitle {
		t.Errorf("Series.Title: got %q, want %q (canonical must not be changed by source title)", series.Title, canonicalTitle)
	}

	// ── Update path: source title must be refreshed on re-add ────────────────
	// Simulate a source title change by updating the stub.
	updatedSourceTitle := "Dragon Reborn (Source v2)"
	sc.mangaMeta = suwayomi.Manga{Title: updatedSourceTitle}

	if _, err := ing.AddSeries(ctx, sourceName, mangaID, canonicalTitle); err != nil {
		t.Fatalf("second AddSeries: %v", err)
	}

	sp = client.SeriesProvider.Query().OnlyX(ctx)
	if sp.Title != updatedSourceTitle {
		t.Errorf("SeriesProvider.Title after update: got %q, want %q", sp.Title, updatedSourceTitle)
	}

	// Only one SeriesProvider row must exist (idempotent upsert).
	if n := len(client.SeriesProvider.Query().AllX(ctx)); n != 1 {
		t.Errorf("SeriesProvider count: got %d, want 1", n)
	}
}

// --- per-source metadata tests -----------------------------------------------

// metaClientStub is a purpose-built stub for the per-source metadata tests.
// Unlike ingestStubClient (which panics on MangaMeta), this stub returns
// configurable values for MangaMeta, exercising the T3 code path.
// Methods that Ingest must not call still panic — this keeps the invariant that
// AddSeries only touches FetchChapters and MangaMeta.
type metaClientStub struct {
	chapters     []suwayomi.Chapter
	chaptersErr  error
	mangaMeta    suwayomi.Manga
	mangaMetaErr error
}

func (s *metaClientStub) Sources(_ context.Context) ([]suwayomi.Source, error) {
	panic("metaClientStub: Sources must not be called by Ingest")
}
func (s *metaClientStub) FetchMangaDetails(_ context.Context, _ int) (suwayomi.Manga, error) {
	panic("metaClientStub: FetchMangaDetails must not be called by Ingest")
}
func (s *metaClientStub) Browse(_ context.Context, _ string, _ suwayomi.BrowseType, _ int) (suwayomi.BrowseResult, error) {
	panic("metaClientStub: Browse must not be called by Ingest")
}
func (s *metaClientStub) Search(_ context.Context, _, _ string) ([]suwayomi.Manga, error) {
	panic("metaClientStub: Search must not be called by Ingest")
}
func (s *metaClientStub) FetchChapters(_ context.Context, _ int) ([]suwayomi.Chapter, error) {
	return s.chapters, s.chaptersErr
}
func (s *metaClientStub) MangaChapters(_ context.Context, _ int) ([]suwayomi.Chapter, error) {
	panic("metaClientStub: MangaChapters must not be called by Ingest (use FetchChapters)")
}
func (s *metaClientStub) MangaMeta(_ context.Context, _ int) (suwayomi.Manga, error) {
	return s.mangaMeta, s.mangaMetaErr
}
func (s *metaClientStub) ChapterPages(_ context.Context, _ int) ([]string, error) {
	panic("metaClientStub: ChapterPages must not be called by Ingest")
}
func (s *metaClientStub) PageBytes(_ context.Context, _ string) ([]byte, string, error) {
	panic("metaClientStub: PageBytes must not be called by Ingest")
}
func (s *metaClientStub) ServerSettings(_ context.Context) (suwayomi.SuwayomiSettings, error) {
	panic("metaClientStub: ServerSettings must not be called by Ingest")
}
func (s *metaClientStub) SetServerSettings(_ context.Context, _ suwayomi.SuwayomiSettingsPatch) error {
	panic("metaClientStub: SetServerSettings must not be called by Ingest")
}
func (s *metaClientStub) Extensions(_ context.Context) ([]suwayomi.Extension, error) {
	panic("metaClientStub: Extensions must not be called by Ingest")
}
func (s *metaClientStub) SetExtensionState(_ context.Context, _ string, _ suwayomi.ExtensionAction) error {
	panic("metaClientStub: SetExtensionState must not be called by Ingest")
}
func (s *metaClientStub) FetchExtensions(_ context.Context) ([]suwayomi.Extension, error) {
	panic("metaClientStub: FetchExtensions must not be called by Ingest")
}
func (s *metaClientStub) ExtensionRepos(_ context.Context) ([]string, error) {
	panic("metaClientStub: ExtensionRepos must not be called by Ingest")
}
func (s *metaClientStub) SetExtensionRepos(_ context.Context, _ []string) error {
	panic("metaClientStub: SetExtensionRepos must not be called by Ingest")
}
func (s *metaClientStub) SourcePreferences(_ context.Context, _ string) ([]suwayomi.SourcePreference, error) {
	panic("metaClientStub: SourcePreferences must not be called by Ingest")
}
func (s *metaClientStub) SetSourcePreference(_ context.Context, _ string, _ int, _ suwayomi.PreferenceValue) ([]suwayomi.SourcePreference, error) {
	panic("metaClientStub: SetSourcePreference must not be called by Ingest")
}
func (s *metaClientStub) ExtensionSources(_ context.Context, _ string) ([]suwayomi.Source, error) {
	panic("metaClientStub: ExtensionSources must not be called by Ingest")
}
func (s *metaClientStub) SetSourceEnabled(_ context.Context, _ string, _ bool) error {
	panic("metaClientStub: SetSourceEnabled must not be called by Ingest")
}

// ptrStr returns a pointer to v.
func ptrStr(v string) *string { return &v }

// TestIngest_AddSeries_PerSourceMetadata verifies that AddSeries stores the
// source's own title (from MangaMeta) on SeriesProvider.Title instead of the
// canonical adopt title, and stores the source thumbnail as SeriesProvider.CoverURL.
// Series.Title must remain the canonical adopt title, unchanged.
func TestIngest_AddSeries_PerSourceMetadata(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		mangaID        = 7
		canonicalTitle = "Canonical"
		sourceTitle    = "Source-Specific Title"
		sourceCover    = "/api/v1/manga/7/thumbnail"
		sourceName     = "test-source"
	)

	sc := &metaClientStub{
		chapters: makeChapters(1),
		mangaMeta: suwayomi.Manga{
			ID:           mangaID,
			Title:        sourceTitle,
			ThumbnailURL: ptrStr(sourceCover),
		},
	}

	ing := suwayomi.NewIngest(sc, client)
	_, err := ing.AddSeries(ctx, sourceName, mangaID, canonicalTitle)
	if err != nil {
		t.Fatalf("AddSeries: unexpected error: %v", err)
	}

	// Series.Title must be the canonical adopt title, not the source title.
	series := client.Series.Query().OnlyX(ctx)
	if series.Title != canonicalTitle {
		t.Errorf("Series.Title: got %q, want %q (canonical must not be overwritten by source title)",
			series.Title, canonicalTitle)
	}

	// SeriesProvider.Title must be the source-specific title from MangaMeta.
	sp := client.SeriesProvider.Query().OnlyX(ctx)
	if sp.Title != sourceTitle {
		t.Errorf("SeriesProvider.Title: got %q, want %q (must use source title from MangaMeta, NOT canonical)",
			sp.Title, sourceTitle)
	}
	// SeriesProvider.CoverURL must be the source thumbnail from MangaMeta.
	if sp.CoverURL != sourceCover {
		t.Errorf("SeriesProvider.CoverURL: got %q, want %q",
			sp.CoverURL, sourceCover)
	}
}

// TestIngest_AddSeries_MangaMetaError verifies that a MangaMeta client error is
// propagated and no SeriesProvider row is created (the series row is created
// first, but the provider/chapter rows must not be).
func TestIngest_AddSeries_MangaMetaError(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	sentinel := errors.New("suwayomi: manga meta unavailable")
	sc := &metaClientStub{
		chapters:     makeChapters(1),
		mangaMetaErr: sentinel,
	}
	ing := suwayomi.NewIngest(sc, client)

	_, err := ing.AddSeries(ctx, "src", 7, "Some Series")
	if !errors.Is(err, sentinel) {
		t.Errorf("AddSeries: err got %v, want to wrap %v", err, sentinel)
	}
	// No SeriesProvider rows should have been created.
	if n := len(client.SeriesProvider.Query().AllX(ctx)); n != 0 {
		t.Errorf("SeriesProvider count after MangaMeta error: got %d, want 0", n)
	}
}
