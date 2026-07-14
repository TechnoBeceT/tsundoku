// Package series_test exercises the library read service against an ephemeral
// PostgreSQL instance (testdb). Tests require Docker.
package series_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/metadata"
	"github.com/technobecet/tsundoku/internal/series"
)

// catID resolves a seeded default category's id by name (testdb seeds the five
// defaults), for linking a fixture series to a category via SetCategoryID.
func catID(ctx context.Context, db *ent.Client, name string) uuid.UUID {
	id, err := category.IDByName(ctx, db, name)
	if err != nil {
		panic(fmt.Sprintf("catID %q: %v", name, err))
	}
	return id
}

// seriesCategoryName reads a series' linked category name via the edge.
func seriesCategoryName(ctx context.Context, db *ent.Client, id uuid.UUID) string {
	return db.Series.Query().Where(entseries.IDEQ(id)).QueryCategory().OnlyX(ctx).Name
}

// seededLibrary holds the ids of the fixture series so tests can target them.
type seededLibrary struct {
	mangaID  uuid.UUID
	manhwaID uuid.UUID
}

// seedLibrary creates two series in different categories:
//   - "Alpha Saga" (Manga): 1 downloaded + 1 wanted chapter, one provider.
//   - "Beta Quest" (Manhwa): 1 downloaded + 1 failed + 1 wanted chapter, two providers.
//
// The non-trivial state mix makes the chapter-count rollup assertions non-vacuous.
func seedLibrary(ctx context.Context, t *testing.T, client *ent.Client) seededLibrary {
	t.Helper()

	manga := client.Series.Create().
		SetTitle("Alpha Saga").
		SetSlug("alpha-saga").
		SetCoverURL("https://example.test/alpha.jpg").
		SetCategoryID(catID(ctx, client, "Manga")).
		SaveX(ctx)

	num1, num2 := 1.0, 2.0
	pages := 20
	client.Chapter.Create().
		SetSeriesID(manga.ID).
		SetChapterKey("alpha-1").
		SetNumber(num1).
		SetState(entchapter.StateDownloaded).
		SetFilename("[mangadex][en] Alpha Saga 001.cbz").
		SetPageCount(pages).
		SaveX(ctx)
	client.Chapter.Create().
		SetSeriesID(manga.ID).
		SetChapterKey("alpha-2").
		SetNumber(num2).
		SetState(entchapter.StateWanted).
		SaveX(ctx)

	client.SeriesProvider.Create().
		SetSeriesID(manga.ID).
		SetProvider("mangadex").
		SetScanlator("ScanGroup").
		SetLanguage("en").
		SetImportance(10).
		SaveX(ctx)

	manhwa := client.Series.Create().
		SetTitle("Beta Quest").
		SetSlug("beta-quest").
		SetCategoryID(catID(ctx, client, "Manhwa")).
		SaveX(ctx)

	bnum1, bnum2, bnum3 := 1.0, 2.0, 3.0
	client.Chapter.Create().
		SetSeriesID(manhwa.ID).
		SetChapterKey("beta-1").
		SetNumber(bnum1).
		SetState(entchapter.StateDownloaded).
		SaveX(ctx)
	client.Chapter.Create().
		SetSeriesID(manhwa.ID).
		SetChapterKey("beta-2").
		SetNumber(bnum2).
		SetState(entchapter.StateFailed).
		SaveX(ctx)
	client.Chapter.Create().
		SetSeriesID(manhwa.ID).
		SetChapterKey("beta-3").
		SetNumber(bnum3).
		SetState(entchapter.StateWanted).
		SaveX(ctx)

	client.SeriesProvider.Create().
		SetSeriesID(manhwa.ID).
		SetProvider("asura").
		SetLanguage("en").
		SetImportance(5).
		SaveX(ctx)
	// flame carries a display name; asura (above) does not — so the ProviderDTO
	// exercises both the provider_name path and the id-fallback path.
	client.SeriesProvider.Create().
		SetSeriesID(manhwa.ID).
		SetProvider("flame").
		SetProviderName("Flame Scans").
		SetLanguage("en").
		SetImportance(8).
		SaveX(ctx)

	return seededLibrary{mangaID: manga.ID, manhwaID: manhwa.ID}
}

func TestListSeriesReturnsAllWithRollup(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seedLibrary(ctx, t, client)

	svc := series.NewService(client, t.TempDir(), 14)
	got, err := svc.ListSeries(ctx, series.ListFilter{})
	if err != nil {
		t.Fatalf("ListSeries: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("ListSeries: want 2 series, got %d", len(got))
	}

	// Deterministic title-ASC order: "Alpha Saga" then "Beta Quest".
	if got[0].Title != "Alpha Saga" || got[1].Title != "Beta Quest" {
		t.Fatalf("ListSeries: want title-ASC order [Alpha Saga, Beta Quest], got [%s, %s]", got[0].Title, got[1].Title)
	}

	alpha := got[0]
	// CoverURL is the proxy path (empty: the mangadex provider has no cover_url).
	// DisplayName falls back to the canonical Series.title (mangadex has no title).
	// Category is separately exercised by TestListSeriesFiltersByCategory.
	if alpha.Slug != "alpha-saga" || alpha.CoverURL != "" || alpha.DisplayName != "Alpha Saga" {
		t.Fatalf("ListSeries: alpha summary mismatch: %+v", alpha)
	}
	// Non-vacuous rollup: 1 downloaded + 1 wanted = total 2. The downloaded
	// chapter defaults to unread (fixtures never set Read), so Unread == Downloaded.
	wantAlpha := series.ChapterCounts{Total: 2, Downloaded: 1, Wanted: 1, Failed: 0, Unread: 1}
	if alpha.ChapterCounts != wantAlpha {
		t.Fatalf("ListSeries: alpha counts: want %+v, got %+v", wantAlpha, alpha.ChapterCounts)
	}

	beta := got[1]
	wantBeta := series.ChapterCounts{Total: 3, Downloaded: 1, Wanted: 1, Failed: 1, Unread: 1}
	if beta.ChapterCounts != wantBeta {
		t.Fatalf("ListSeries: beta counts: want %+v, got %+v", wantBeta, beta.ChapterCounts)
	}
}

func TestListSeriesFiltersByCategory(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seedLibrary(ctx, t, client)

	svc := series.NewService(client, t.TempDir(), 14)
	cat := "Manhwa"
	got, err := svc.ListSeries(ctx, series.ListFilter{Category: &cat})
	if err != nil {
		t.Fatalf("ListSeries: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("ListSeries(Manhwa): want 1 series, got %d", len(got))
	}
	if got[0].Title != "Beta Quest" || got[0].Category != "Manhwa" {
		t.Fatalf("ListSeries(Manhwa): wrong series: %+v", got[0])
	}
}

// TestListSeriesUnknownCategory verifies that filtering by a category name that
// matches no series returns an empty page (categories are now user-defined, so
// an unknown name is not an error — it simply matches nothing).
func TestListSeriesUnknownCategory(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seedLibrary(ctx, t, client)

	svc := series.NewService(client, t.TempDir(), 14)
	unknown := "No Such Category"
	got, err := svc.ListSeries(ctx, series.ListFilter{Category: &unknown})
	if err != nil {
		t.Fatalf("ListSeries(unknown): unexpected error %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListSeries(unknown): want empty page, got %+v", got)
	}
}

func TestListSeriesPaginates(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seedLibrary(ctx, t, client)

	svc := series.NewService(client, t.TempDir(), 14)

	page1, err := svc.ListSeries(ctx, series.ListFilter{Limit: 1, Offset: 0})
	if err != nil {
		t.Fatalf("ListSeries page1: %v", err)
	}
	page2, err := svc.ListSeries(ctx, series.ListFilter{Limit: 1, Offset: 1})
	if err != nil {
		t.Fatalf("ListSeries page2: %v", err)
	}

	if len(page1) != 1 || len(page2) != 1 {
		t.Fatalf("pagination: want 1 per page, got %d and %d", len(page1), len(page2))
	}
	if page1[0].Title != "Alpha Saga" {
		t.Fatalf("pagination: page1 want Alpha Saga, got %s", page1[0].Title)
	}
	if page2[0].Title != "Beta Quest" {
		t.Fatalf("pagination: page2 want Beta Quest, got %s", page2[0].Title)
	}
}

func TestGetSeriesReturnsDetail(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	lib := seedLibrary(ctx, t, client)

	svc := series.NewService(client, t.TempDir(), 14)
	got, err := svc.GetSeries(ctx, lib.manhwaID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}

	if got.Title != "Beta Quest" || got.Category != "Manhwa" {
		t.Fatalf("GetSeries: summary mismatch: %+v", got)
	}
	if got.ChapterCounts != (series.ChapterCounts{Total: 3, Downloaded: 1, Wanted: 1, Failed: 1, Unread: 1}) {
		t.Fatalf("GetSeries: counts: %+v", got.ChapterCounts)
	}

	if len(got.Chapters) != 3 {
		t.Fatalf("GetSeries: want 3 chapters, got %d", len(got.Chapters))
	}
	// Ordered by number then chapter_key.
	if got.Chapters[0].ChapterKey != "beta-1" || got.Chapters[2].ChapterKey != "beta-3" {
		t.Fatalf("GetSeries: chapter order wrong: %+v", got.Chapters)
	}
	if got.Chapters[1].State != "failed" {
		t.Fatalf("GetSeries: beta-2 state want failed, got %s", got.Chapters[1].State)
	}

	if len(got.Providers) != 2 {
		t.Fatalf("GetSeries: want 2 providers, got %d", len(got.Providers))
	}
	assertProviderNames(t, got.Providers)
}

// TestGetSeriesLinksIncludeSourceLinks verifies the detail DTO's Links field
// merges the metadata-engine links with the library's actual SOURCE links
// (SeriesProvider.URL — the scanlation/aggregator site each provider was
// adopted from), deduping a source URL a metadata link already lists
// (case-insensitive exact match) and leaving a provider with no URL out
// entirely. A deduped source keeps the METADATA link's label (metadata links
// come first; sourceLinks only appends what isn't already present).
func TestGetSeriesLinksIncludeSourceLinks(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()

	s := client.Series.Create().
		SetTitle("Delta Rising").
		SetSlug("delta-rising").
		SetCategoryID(catID(ctx, client, "Manga")).
		SetLinks([]metadata.Link{
			{Label: "MyAnimeList", URL: "https://myanimelist.net/manga/1"},
			// Deliberately the SAME site as the "flame" provider below, differing
			// only by case — proves the dedup is case-insensitive.
			{Label: "Flame Scans", URL: "HTTPS://FLAME.example/delta"},
		}).
		SaveX(ctx)

	client.SeriesProvider.Create().
		SetSeriesID(s.ID).
		SetProvider("flame").
		SetProviderName("Flame Scans").
		SetURL("https://flame.example/delta"). // dup of the metadata link above
		SetImportance(10).
		SaveX(ctx)
	client.SeriesProvider.Create().
		SetSeriesID(s.ID).
		SetProvider("asura").
		SetProviderName("Asura Scans").
		SetURL("https://asura.example/delta").
		SetImportance(5).
		SaveX(ctx)
	// A provider with no URL (e.g. a disk-origin/unlinked row) contributes no link.
	client.SeriesProvider.Create().
		SetSeriesID(s.ID).
		SetProvider("disk-import").
		SetImportance(1).
		SaveX(ctx)

	svc := series.NewService(client, t.TempDir(), 14)
	got, err := svc.GetSeries(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}

	if got.Links == nil {
		t.Fatalf("GetSeries: Links must be non-nil")
	}
	// 2 metadata links + 1 new source link (asura) = 3; flame's URL is a dup.
	if len(got.Links) != 3 {
		t.Fatalf("GetSeries: want 3 links (2 metadata + 1 deduped source), got %d: %+v", len(got.Links), got.Links)
	}

	byURL := map[string]string{}
	occurrences := map[string]int{}
	for _, l := range got.Links {
		key := strings.ToLower(l.URL)
		byURL[key] = l.Label
		occurrences[key]++
	}
	if _, ok := byURL["https://myanimelist.net/manga/1"]; !ok {
		t.Errorf("GetSeries: missing metadata link MyAnimeList: %+v", got.Links)
	}
	if label := byURL["https://flame.example/delta"]; label != "Flame Scans" {
		t.Errorf("GetSeries: flame link should keep its metadata label (deduped), got %q", label)
	}
	if occurrences["https://flame.example/delta"] != 1 {
		t.Errorf("GetSeries: flame URL should appear exactly once (deduped), got %d: %+v", occurrences["https://flame.example/delta"], got.Links)
	}
	if label := byURL["https://asura.example/delta"]; label != "Asura Scans" {
		t.Errorf("GetSeries: want asura source link with label 'Asura Scans', got %q", label)
	}
}

// TestChapterCountsExcludeSuperseded verifies both ListSeries' rollup and
// GetSeries' in-memory Total exclude entchapter.StateSuperseded chapters: a
// superseded part is merged into its whole and must not inflate the visible
// chapter count. Fixture: a downloaded whole (chapter 1) + two superseded
// parts (1.1, 1.2, split chapters folded into the whole) + one wanted chapter
// (2) — want Total == 2 (the whole + chapter 2), NOT 4.
func TestChapterCountsExcludeSuperseded(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()

	s := client.Series.Create().
		SetTitle("Gamma Split").
		SetSlug("gamma-split").
		SetCategoryID(catID(ctx, client, "Manga")).
		SaveX(ctx)

	whole := 1.0
	client.Chapter.Create().
		SetSeriesID(s.ID).
		SetChapterKey("gamma-1").
		SetNumber(whole).
		SetState(entchapter.StateDownloaded).
		SaveX(ctx)

	part1, part2 := 1.1, 1.2
	client.Chapter.Create().
		SetSeriesID(s.ID).
		SetChapterKey("gamma-1.1").
		SetNumber(part1).
		SetState(entchapter.StateSuperseded).
		SaveX(ctx)
	client.Chapter.Create().
		SetSeriesID(s.ID).
		SetChapterKey("gamma-1.2").
		SetNumber(part2).
		SetState(entchapter.StateSuperseded).
		SaveX(ctx)

	two := 2.0
	client.Chapter.Create().
		SetSeriesID(s.ID).
		SetChapterKey("gamma-2").
		SetNumber(two).
		SetState(entchapter.StateWanted).
		SaveX(ctx)

	svc := series.NewService(client, t.TempDir(), 14)

	list, err := svc.ListSeries(ctx, series.ListFilter{})
	if err != nil {
		t.Fatalf("ListSeries: %v", err)
	}
	var summary series.SeriesSummaryDTO
	found := false
	for _, sm := range list {
		if sm.Title == "Gamma Split" {
			summary, found = sm, true
		}
	}
	if !found {
		t.Fatalf("ListSeries: Gamma Split not found in %+v", list)
	}
	wantCounts := series.ChapterCounts{Total: 2, Downloaded: 1, Wanted: 1, Failed: 0, Unread: 1}
	if summary.ChapterCounts != wantCounts {
		t.Fatalf("ListSeries: Gamma Split counts: want %+v, got %+v (superseded parts must not inflate Total)", wantCounts, summary.ChapterCounts)
	}

	detail, err := svc.GetSeries(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if detail.ChapterCounts != wantCounts {
		t.Fatalf("GetSeries: counts: want %+v, got %+v", wantCounts, detail.ChapterCounts)
	}
	// The DTO chapter slice still carries every chapter, incl. superseded ones —
	// only the rollup Total excludes them.
	if len(detail.Chapters) != 4 {
		t.Fatalf("GetSeries: want 4 chapters in DTO slice (incl. superseded), got %d", len(detail.Chapters))
	}
}

// assertProviderNames checks the ProviderDTO display-vs-id fallback: flame has a
// provider_name ("Flame Scans") so it is shown, asura has none so it falls back
// to the raw provider id ("asura").
func assertProviderNames(t *testing.T, providers []series.ProviderDTO) {
	t.Helper()
	names := map[string]string{}
	for _, p := range providers {
		names[p.Provider] = p.ProviderName
	}
	if names["flame"] != "Flame Scans" {
		t.Errorf("GetSeries: flame providerName want 'Flame Scans', got %q", names["flame"])
	}
	if names["asura"] != "asura" {
		t.Errorf("GetSeries: asura providerName want id fallback 'asura', got %q", names["asura"])
	}
}

// TestGetSeriesChapterNameFromBestProvider verifies ChapterDTO.Name is populated
// from the HIGHEST-importance provider's ProviderChapter.name (mirroring M1's
// best-provider rule: higher importance = higher priority), and that an empty
// name on the highest-importance provider does NOT shadow a real name from a
// lower-importance one.
func TestGetSeriesChapterNameFromBestProvider(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	storage := t.TempDir()

	sr := client.Series.Create().
		SetTitle("Tower Climb").
		SetSlug("tower-climb").
		SetCategoryID(catID(ctx, client, "Manhwa")).
		SaveX(ctx)

	// Two chapters sharing keys across two providers.
	num1, num2 := 1.0, 2.0
	client.Chapter.Create().
		SetSeriesID(sr.ID).SetChapterKey("tc-1").SetNumber(num1).
		SetState(entchapter.StateWanted).SaveX(ctx)
	client.Chapter.Create().
		SetSeriesID(sr.ID).SetChapterKey("tc-2").SetNumber(num2).
		SetState(entchapter.StateWanted).SaveX(ctx)

	// Low-importance provider: has a real name for BOTH chapters.
	low := client.SeriesProvider.Create().
		SetSeriesID(sr.ID).SetProvider("asura").SetLanguage("en").
		SetImportance(5).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(low.ID).SetChapterKey("tc-1").SetName("Low Name C1").SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(low.ID).SetChapterKey("tc-2").SetName("Low Name C2").SaveX(ctx)

	// High-importance provider: a real name for tc-1, but an EMPTY name for tc-2.
	high := client.SeriesProvider.Create().
		SetSeriesID(sr.ID).SetProvider("flame").SetLanguage("en").
		SetImportance(8).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(high.ID).SetChapterKey("tc-1").SetName("High Name C1").SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(high.ID).SetChapterKey("tc-2").SetName("").SaveX(ctx)

	svc := series.NewService(client, storage, 14)
	got, err := svc.GetSeries(ctx, sr.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}

	names := map[string]string{}
	for _, ch := range got.Chapters {
		names[ch.ChapterKey] = ch.Name
	}
	// tc-1: highest-importance provider has a real name → wins.
	if names["tc-1"] != "High Name C1" {
		t.Fatalf("tc-1 name: want %q (highest importance), got %q", "High Name C1", names["tc-1"])
	}
	// tc-2: highest-importance provider's name is empty → fall through to the
	// real (lower-importance) name rather than shadowing it with "".
	if names["tc-2"] != "Low Name C2" {
		t.Fatalf("tc-2 name: want %q (highest provider empty → lower wins), got %q", "Low Name C2", names["tc-2"])
	}
}

// TestGetSeriesChapterNameFallsBackToNumber verifies the title-less display-name
// fallback at the DTO boundary: a frozen 0-provider series (all sources removed
// via M6) keeps its Chapter rows but has no provider feed to supply a title, so
// ChapterDTO.Name must derive from the chapter number — integer 12 → "Chapter 12",
// decimal 12.5 → "Chapter 12.5" — and stay blank when the number itself is nil.
func TestGetSeriesChapterNameFallsBackToNumber(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()

	// No SeriesProvider / ProviderChapter rows: nothing can supply a title.
	sr := client.Series.Create().
		SetTitle("Frozen Archive").
		SetSlug("frozen-archive").
		SetCategoryID(catID(ctx, client, "Manga")).
		SaveX(ctx)

	intNum, decNum := 12.0, 12.5
	client.Chapter.Create().
		SetSeriesID(sr.ID).SetChapterKey("fa-int").SetNumber(intNum).
		SetState(entchapter.StateDownloaded).SaveX(ctx)
	client.Chapter.Create().
		SetSeriesID(sr.ID).SetChapterKey("fa-dec").SetNumber(decNum).
		SetState(entchapter.StateDownloaded).SaveX(ctx)
	// No number at all → the fallback has nothing to format, name stays blank.
	client.Chapter.Create().
		SetSeriesID(sr.ID).SetChapterKey("fa-nil").
		SetState(entchapter.StateDownloaded).SaveX(ctx)

	svc := series.NewService(client, t.TempDir(), 14)
	got, err := svc.GetSeries(ctx, sr.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}

	names := map[string]string{}
	for _, ch := range got.Chapters {
		names[ch.ChapterKey] = ch.Name
	}
	if names["fa-int"] != "Chapter 12" {
		t.Errorf("integer fallback: want %q, got %q", "Chapter 12", names["fa-int"])
	}
	if names["fa-dec"] != "Chapter 12.5" {
		t.Errorf("decimal fallback: want %q, got %q", "Chapter 12.5", names["fa-dec"])
	}
	if names["fa-nil"] != "" {
		t.Errorf("nil-number fallback: want blank, got %q", names["fa-nil"])
	}
}

func TestGetSeriesNotFound(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seedLibrary(ctx, t, client)

	svc := series.NewService(client, t.TempDir(), 14)
	_, err := svc.GetSeries(ctx, uuid.New())
	if !errors.Is(err, series.ErrSeriesNotFound) {
		t.Fatalf("GetSeries(random): want ErrSeriesNotFound, got %v", err)
	}
}

// seedSeriesDir creates a real on-disk series directory at <storage>/<cat>/<title>
// with one CBZ and a tsundoku.json sidecar (written via the real WriteSidecar so
// the on-disk shape is genuine). It mirrors the disk-test seeding so SetCategory
// exercises a real folder move. Returns the CBZ bytes for integrity checks.
func seedSeriesDir(t *testing.T, storage, category, title string) []byte {
	t.Helper()

	dir := disk.SeriesDir(storage, category, title)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("seedSeriesDir MkdirAll: %v", err)
	}

	cbzBytes := []byte("fake-cbz-archive-contents")
	num := 1.0
	cbzPath := filepath.Join(dir, "["+category+"][en] "+title+" 001.cbz")
	if err := os.WriteFile(cbzPath, cbzBytes, 0o600); err != nil {
		t.Fatalf("seedSeriesDir WriteFile: %v", err)
	}

	sidecar := disk.Sidecar{
		Title:    title,
		Category: category,
		Chapters: []disk.ChapterProvenance{{
			ChapterKey: "1",
			Number:     &num,
			Provider:   "mangadex",
			Importance: 1,
			Filename:   filepath.Base(cbzPath),
			PageCount:  10,
		}},
	}
	if err := disk.WriteSidecar(dir, sidecar); err != nil {
		t.Fatalf("seedSeriesDir WriteSidecar: %v", err)
	}

	return cbzBytes
}

// TestSetCategoryMovesDiskAndUpdatesDB is the core invariant proof: recategorizing
// a series with a real on-disk folder moves the folder to the new category dir AND
// updates the DB, with both sides asserted (DB↔disk consistency).
func TestSetCategoryMovesDiskAndUpdatesDB(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	storage := t.TempDir()

	const title = "Solo Leveling"
	row := client.Series.Create().
		SetTitle(title).
		SetSlug("solo-leveling").
		SetCategoryID(catID(ctx, client, "Other")).
		SaveX(ctx)

	cbzBytes := seedSeriesDir(t, storage, "Other", title)

	svc := series.NewService(client, storage, 14)
	if err := svc.SetCategory(ctx, row.ID, catID(ctx, client, "Manhwa")); err != nil {
		t.Fatalf("SetCategory: %v", err)
	}

	// DB side: category updated.
	if got := seriesCategoryName(ctx, client, row.ID); got != "Manhwa" {
		t.Fatalf("SetCategory: DB category want Manhwa, got %s", got)
	}

	// Disk side: folder moved to the new category, old folder gone, CBZ intact.
	oldDir := disk.SeriesDir(storage, "Other", title)
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("SetCategory: old dir %q should be gone, stat err = %v", oldDir, err)
	}
	newDir := disk.SeriesDir(storage, "Manhwa", title)
	if _, err := os.Stat(newDir); err != nil {
		t.Fatalf("SetCategory: new dir %q should exist: %v", newDir, err)
	}
	// The CBZ filename does NOT encode the category, so the archive keeps its
	// original name after the move (it is carried over untouched).
	gotBytes, err := os.ReadFile(filepath.Join(newDir, "[Other][en] "+title+" 001.cbz")) //nolint:gosec // test-only, path is from t.TempDir()
	if err != nil {
		t.Fatalf("SetCategory: read moved CBZ: %v", err)
	}
	if string(gotBytes) != string(cbzBytes) {
		t.Fatalf("SetCategory: moved CBZ bytes changed")
	}
	// Sidecar at the new home reflects the new category.
	sidecar, err := disk.ReadSidecar(newDir)
	if err != nil {
		t.Fatalf("SetCategory: read moved sidecar: %v", err)
	}
	if sidecar == nil || sidecar.Category != "Manhwa" {
		t.Fatalf("SetCategory: moved sidecar category want Manhwa, got %+v", sidecar)
	}
}

// TestSetCategorySameCategoryNoOp verifies that setting the current category is a
// true no-op: no error, DB and disk unchanged.
func TestSetCategorySameCategoryNoOp(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	storage := t.TempDir()

	const title = "Solo Leveling"
	row := client.Series.Create().
		SetTitle(title).
		SetSlug("solo-leveling").
		SetCategoryID(catID(ctx, client, "Manhwa")).
		SaveX(ctx)
	seedSeriesDir(t, storage, "Manhwa", title)

	svc := series.NewService(client, storage, 14)
	if err := svc.SetCategory(ctx, row.ID, catID(ctx, client, "Manhwa")); err != nil {
		t.Fatalf("SetCategory(same): %v", err)
	}

	if got := seriesCategoryName(ctx, client, row.ID); got != "Manhwa" {
		t.Fatalf("SetCategory(same): category changed to %s", got)
	}
	if _, err := os.Stat(disk.SeriesDir(storage, "Manhwa", title)); err != nil {
		t.Fatalf("SetCategory(same): dir should be untouched: %v", err)
	}
}

// TestSetCategoryUnknownCategory verifies that recategorizing to a category id
// that does not exist is rejected with category.ErrCategoryNotFound and nothing
// changes on either DB or disk.
func TestSetCategoryUnknownCategory(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	storage := t.TempDir()

	const title = "Solo Leveling"
	row := client.Series.Create().
		SetTitle(title).
		SetSlug("solo-leveling").
		SetCategoryID(catID(ctx, client, "Other")).
		SaveX(ctx)
	seedSeriesDir(t, storage, "Other", title)

	svc := series.NewService(client, storage, 14)
	err := svc.SetCategory(ctx, row.ID, uuid.New())
	if !errors.Is(err, category.ErrCategoryNotFound) {
		t.Fatalf("SetCategory(unknown): want ErrCategoryNotFound, got %v", err)
	}

	if got := seriesCategoryName(ctx, client, row.ID); got != "Other" {
		t.Fatalf("SetCategory(unknown): DB category changed to %s", got)
	}
	if _, err := os.Stat(disk.SeriesDir(storage, "Other", title)); err != nil {
		t.Fatalf("SetCategory(unknown): dir should be untouched: %v", err)
	}
}

// TestSetCategoryNoDiskFolderUpdatesDBOnly verifies the not-yet-downloaded branch:
// a series with no folder on disk is still recategorizable — the DB is updated and
// no error is raised (there is simply nothing to move).
func TestSetCategoryNoDiskFolderUpdatesDBOnly(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	storage := t.TempDir()

	row := client.Series.Create().
		SetTitle("No Downloads Yet").
		SetSlug("no-downloads-yet").
		SetCategoryID(catID(ctx, client, "Other")).
		SaveX(ctx)

	svc := series.NewService(client, storage, 14)
	if err := svc.SetCategory(ctx, row.ID, catID(ctx, client, "Manhua")); err != nil {
		t.Fatalf("SetCategory(no folder): %v", err)
	}

	if got := seriesCategoryName(ctx, client, row.ID); got != "Manhua" {
		t.Fatalf("SetCategory(no folder): DB category want Manhua, got %s", got)
	}
	// No folder was ever created on either side.
	if _, err := os.Stat(disk.SeriesDir(storage, "Manhua", "No Downloads Yet")); !os.IsNotExist(err) {
		t.Fatalf("SetCategory(no folder): no dir should have been created, stat err = %v", err)
	}
}

// TestMonitoredDefaultsTrue verifies that a newly created series exposes
// Monitored==true in both GetSeries (detail) and ListSeries (summary), and that
// ProviderDTO.ID matches the SeriesProvider UUID (needed by Task 5/7 re-rank).
func TestMonitoredDefaultsTrue(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()

	sr := client.Series.Create().
		SetTitle("Watch Series").
		SetSlug("watch-series").
		SetCategoryID(catID(ctx, client, "Manga")).
		SaveX(ctx)

	sp := client.SeriesProvider.Create().
		SetSeriesID(sr.ID).
		SetProvider("mangadex").
		SetScanlator("ScanGroup").
		SetLanguage("en").
		SetImportance(10).
		SaveX(ctx)

	svc := series.NewService(client, t.TempDir(), 14)

	detail, err := svc.GetSeries(ctx, sr.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if !detail.Monitored {
		t.Fatalf("GetSeries: default Monitored want true, got false")
	}
	if len(detail.Providers) != 1 {
		t.Fatalf("GetSeries: want 1 provider, got %d", len(detail.Providers))
	}
	if detail.Providers[0].ID != sp.ID.String() {
		t.Fatalf("GetSeries: ProviderDTO.ID want %s, got %q", sp.ID.String(), detail.Providers[0].ID)
	}

	summaries, err := svc.ListSeries(ctx, series.ListFilter{})
	if err != nil {
		t.Fatalf("ListSeries: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("ListSeries: want 1 series, got %d", len(summaries))
	}
	if !summaries[0].Monitored {
		t.Fatalf("ListSeries: default Monitored want true, got false")
	}
}

// TestMonitoredToggle verifies that after setting Monitored=false on the DB row
// directly, both GetSeries and ListSeries report false — confirming the field
// is read from the DB and not cached or hardcoded.
func TestMonitoredToggle(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()

	sr := client.Series.Create().
		SetTitle("Drop Series").
		SetSlug("drop-series").
		SetCategoryID(catID(ctx, client, "Manga")).
		SaveX(ctx)

	svc := series.NewService(client, t.TempDir(), 14)

	client.Series.UpdateOneID(sr.ID).SetMonitored(false).ExecX(ctx)

	detail, err := svc.GetSeries(ctx, sr.ID)
	if err != nil {
		t.Fatalf("GetSeries (after toggle): %v", err)
	}
	if detail.Monitored {
		t.Fatalf("GetSeries: after toggle Monitored want false, got true")
	}

	summaries, err := svc.ListSeries(ctx, series.ListFilter{})
	if err != nil {
		t.Fatalf("ListSeries (after toggle): %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("ListSeries: want 1 series, got %d", len(summaries))
	}
	if summaries[0].Monitored {
		t.Fatalf("ListSeries: after toggle Monitored want false, got true")
	}
}

// TestSetCategoryNotFound verifies an unknown series id yields ErrSeriesNotFound.
func TestSetCategoryNotFound(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()

	svc := series.NewService(client, t.TempDir(), 14)
	err := svc.SetCategory(ctx, uuid.New(), catID(ctx, client, "Manga"))
	if !errors.Is(err, series.ErrSeriesNotFound) {
		t.Fatalf("SetCategory(random): want ErrSeriesNotFound, got %v", err)
	}
}

// TestSetMonitoredFlipsField verifies that SetMonitored(false) persists the value
// and that a re-read via GetSeries reports the updated flag.
func TestSetMonitoredFlipsField(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()

	sr := client.Series.Create().
		SetTitle("Monitor Me").
		SetSlug("monitor-me").
		SetCategoryID(catID(ctx, client, "Manga")).
		SaveX(ctx)

	svc := series.NewService(client, t.TempDir(), 14)

	// Default is true; flip to false.
	if err := svc.SetMonitored(ctx, sr.ID, false); err != nil {
		t.Fatalf("SetMonitored(false): %v", err)
	}

	detail, err := svc.GetSeries(ctx, sr.ID)
	if err != nil {
		t.Fatalf("GetSeries after SetMonitored: %v", err)
	}
	if detail.Monitored {
		t.Fatalf("SetMonitored(false): GetSeries still reports Monitored=true")
	}

	// Flip back to true.
	if err := svc.SetMonitored(ctx, sr.ID, true); err != nil {
		t.Fatalf("SetMonitored(true): %v", err)
	}

	detail, err = svc.GetSeries(ctx, sr.ID)
	if err != nil {
		t.Fatalf("GetSeries after second SetMonitored: %v", err)
	}
	if !detail.Monitored {
		t.Fatalf("SetMonitored(true): GetSeries still reports Monitored=false")
	}
}

// TestSetMonitoredNotFound verifies that a random UUID yields ErrSeriesNotFound.
func TestSetMonitoredNotFound(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()

	svc := series.NewService(client, t.TempDir(), 14)
	err := svc.SetMonitored(ctx, uuid.New(), false)
	if !errors.Is(err, series.ErrSeriesNotFound) {
		t.Fatalf("SetMonitored(random): want ErrSeriesNotFound, got %v", err)
	}
}

// seedProviders creates a series with two SeriesProviders (importances 5 and 8)
// and a separate series with one provider. It returns the series IDs and the two
// provider IDs of the first series, plus the foreign provider ID (from the second
// series) for the cross-ownership test.
type seededProviders struct {
	seriesID          uuid.UUID
	otherSeriesID     uuid.UUID
	providerAID       uuid.UUID // importance 5
	providerBID       uuid.UUID // importance 8
	foreignProviderID uuid.UUID // belongs to otherSeriesID, not seriesID
}

func seedProviders(ctx context.Context, t *testing.T, client *ent.Client) seededProviders {
	t.Helper()

	sr := client.Series.Create().
		SetTitle("Rank Me").
		SetSlug("rank-me").
		SetCategoryID(catID(ctx, client, "Manga")).
		SaveX(ctx)

	spA := client.SeriesProvider.Create().
		SetSeriesID(sr.ID).
		SetProvider("mangadex").
		SetLanguage("en").
		SetImportance(5).
		SaveX(ctx)

	spB := client.SeriesProvider.Create().
		SetSeriesID(sr.ID).
		SetProvider("asura").
		SetLanguage("en").
		SetImportance(8).
		SaveX(ctx)

	other := client.Series.Create().
		SetTitle("Other Series").
		SetSlug("other-series").
		SetCategoryID(catID(ctx, client, "Manhwa")).
		SaveX(ctx)

	foreignSP := client.SeriesProvider.Create().
		SetSeriesID(other.ID).
		SetProvider("flame").
		SetLanguage("en").
		SetImportance(3).
		SaveX(ctx)

	return seededProviders{
		seriesID:          sr.ID,
		otherSeriesID:     other.ID,
		providerAID:       spA.ID,
		providerBID:       spB.ID,
		foreignProviderID: foreignSP.ID,
	}
}

// TestReorderProvidersUpdatesImportances verifies that ReorderProviders persists
// the new importance values and that a re-read via GetSeries reflects them.
func TestReorderProvidersUpdatesImportances(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seed := seedProviders(ctx, t, client)

	svc := series.NewService(client, t.TempDir(), 14)

	ranks := []series.ProviderRank{
		{SeriesProviderID: seed.providerAID, Importance: 20},
		{SeriesProviderID: seed.providerBID, Importance: 10},
	}
	if err := svc.ReorderProviders(ctx, seed.seriesID, ranks); err != nil {
		t.Fatalf("ReorderProviders: %v", err)
	}

	detail, err := svc.GetSeries(ctx, seed.seriesID)
	if err != nil {
		t.Fatalf("GetSeries after ReorderProviders: %v", err)
	}
	got := map[string]int{}
	for _, p := range detail.Providers {
		got[p.Provider] = p.Importance
	}
	if got["mangadex"] != 20 {
		t.Fatalf("ReorderProviders: mangadex importance want 20, got %d", got["mangadex"])
	}
	if got["asura"] != 10 {
		t.Fatalf("ReorderProviders: asura importance want 10, got %d", got["asura"])
	}
}

// TestReorderProvidersNormalizesNegativeInput proves the self-healing normalization:
// a reorder whose submitted list contains a NEGATIVE importance (legacy bad data
// from the old below-existing spread) succeeds and persists a clean non-negative
// spread that preserves the submitted ORDER (higher submitted importance ranks
// higher), rather than being rejected.
func TestReorderProvidersNormalizesNegativeInput(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seed := seedProviders(ctx, t, client)

	svc := series.NewService(client, t.TempDir(), 14)

	// A submitted above B; B carries a negative importance. Order by submitted
	// importance: A(10) > B(-3) ⇒ normalized A=20, B=10, both non-negative.
	ranks := []series.ProviderRank{
		{SeriesProviderID: seed.providerAID, Importance: 10},
		{SeriesProviderID: seed.providerBID, Importance: -3},
	}
	if err := svc.ReorderProviders(ctx, seed.seriesID, ranks); err != nil {
		t.Fatalf("ReorderProviders(negative input): want success, got %v", err)
	}

	detail, err := svc.GetSeries(ctx, seed.seriesID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	got := map[string]int{}
	for _, p := range detail.Providers {
		if p.Importance < 0 {
			t.Fatalf("provider %s importance = %d is negative after normalization", p.Provider, p.Importance)
		}
		got[p.Provider] = p.Importance
	}
	if got["mangadex"] != 20 { // providerA, submitted highest
		t.Fatalf("mangadex (A) importance = %d, want 20 (highest of the spread)", got["mangadex"])
	}
	if got["asura"] != 10 { // providerB, submitted negative → still ranks below A, non-negative
		t.Fatalf("asura (B) importance = %d, want 10 (below A, non-negative)", got["asura"])
	}
}

// TestReorderProvidersNotFound verifies that a random series UUID yields ErrSeriesNotFound.
func TestReorderProvidersNotFound(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seed := seedProviders(ctx, t, client)

	svc := series.NewService(client, t.TempDir(), 14)

	ranks := []series.ProviderRank{
		{SeriesProviderID: seed.providerAID, Importance: 99},
	}
	err := svc.ReorderProviders(ctx, uuid.New(), ranks)
	if !errors.Is(err, series.ErrSeriesNotFound) {
		t.Fatalf("ReorderProviders(random series): want ErrSeriesNotFound, got %v", err)
	}
}

// TestReorderProvidersForeignProviderAllOrNothing verifies the all-or-nothing
// invariant: a ProviderRank whose SeriesProviderID belongs to a DIFFERENT series
// causes ErrProviderNotInSeries and NO importances are changed (the whole tx rolls
// back, even for the valid provider rank that precedes the bad one).
func TestReorderProvidersForeignProviderAllOrNothing(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seed := seedProviders(ctx, t, client)

	svc := series.NewService(client, t.TempDir(), 14)

	// providerA is valid; foreignProviderID belongs to a different series — bad.
	ranks := []series.ProviderRank{
		{SeriesProviderID: seed.providerAID, Importance: 99},      // valid
		{SeriesProviderID: seed.foreignProviderID, Importance: 1}, // foreign — should abort all
	}
	err := svc.ReorderProviders(ctx, seed.seriesID, ranks)
	if !errors.Is(err, series.ErrProviderNotInSeries) {
		t.Fatalf("ReorderProviders(foreign provider): want ErrProviderNotInSeries, got %v", err)
	}

	// ALL-OR-NOTHING: providerA's importance must still be 5 (original), not 99.
	detail, getErr := svc.GetSeries(ctx, seed.seriesID)
	if getErr != nil {
		t.Fatalf("GetSeries after aborted ReorderProviders: %v", getErr)
	}
	for _, p := range detail.Providers {
		if p.Provider == "mangadex" && p.Importance != 5 {
			t.Fatalf("ReorderProviders(all-or-nothing): mangadex importance changed to %d; want 5 (rolled back)", p.Importance)
		}
	}
}

// seedTwoProviderSeries creates a monitored series with two providers
// A(importance 50) and B(importance 10). It gives A a ProviderChapter + a
// SuwayomiSyncState, and a downloaded Chapter satisfied by A (importance 50,
// with a filename so we can assert the row is preserved). Returns the series id
// and both provider ids.
func seedTwoProviderSeries(t *testing.T, ctx context.Context, db *ent.Client) (sid, aID, bID uuid.UUID) {
	t.Helper()
	s := db.Series.Create().SetTitle("Removal Series").SetSlug("removal-series").SetMonitored(true).SaveX(ctx)
	a := db.SeriesProvider.Create().SetSeries(s).SetProvider("src-a").SetSuwayomiID(1).SetImportance(50).SaveX(ctx)
	b := db.SeriesProvider.Create().SetSeries(s).SetProvider("src-b").SetSuwayomiID(2).SetImportance(10).SaveX(ctx)
	db.ProviderChapter.Create().SetSeriesProviderID(a.ID).SetChapterKey("1").SetURL("u1").SetProviderIndex(0).SaveX(ctx)
	db.SuwayomiSyncState.Create().SetSeriesProviderID(a.ID).SetState("ok").SaveX(ctx)
	db.Chapter.Create().
		SetSeries(s).SetChapterKey("1").SetState(entchapter.StateDownloaded).
		SetSatisfiedByProviderID(a.ID).SetSatisfiedImportance(50).
		SetFilename("[src-a][en] Removal Series 0001.cbz").SaveX(ctx)
	return s.ID, a.ID, b.ID
}

// TestRemoveProvider_KeepsChaptersAndSibling removes provider A and asserts:
// A's row + ProviderChapter + SuwayomiSyncState are gone; B survives; the
// downloaded Chapter is UNTOUCHED except satisfied_by is now null while
// satisfied_importance (50) is preserved.
func TestRemoveProvider_KeepsChaptersAndSibling(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	svc := series.NewService(db, t.TempDir(), 14)
	sid, aID, bID := seedTwoProviderSeries(t, ctx, db)

	if err := svc.RemoveProvider(ctx, sid, aID); err != nil {
		t.Fatalf("RemoveProvider: %v", err)
	}

	if n := db.SeriesProvider.Query().Where(entseriesprovider.IDEQ(aID)).CountX(ctx); n != 0 {
		t.Errorf("provider A still present (%d), want 0", n)
	}
	if n := db.SeriesProvider.Query().Where(entseriesprovider.IDEQ(bID)).CountX(ctx); n != 1 {
		t.Errorf("provider B count = %d, want 1 (sibling must survive)", n)
	}
	if n := db.ProviderChapter.Query().Where(entproviderchapter.SeriesProviderID(aID)).CountX(ctx); n != 0 {
		t.Errorf("A's provider chapters still present (%d), want 0", n)
	}
	ch := db.Chapter.Query().Where(entchapter.ChapterKey("1")).OnlyX(ctx)
	if ch.State != entchapter.StateDownloaded {
		t.Errorf("chapter state = %s, want downloaded (must NOT be deleted/changed)", ch.State)
	}
	if ch.SatisfiedByProviderID != nil {
		t.Errorf("satisfied_by_provider_id = %v, want nil (FK cleared)", ch.SatisfiedByProviderID)
	}
	if ch.SatisfiedImportance == nil || *ch.SatisfiedImportance != 50 {
		t.Errorf("satisfied_importance = %v, want 50 (watermark preserved)", ch.SatisfiedImportance)
	}
	if ch.Filename == "" {
		t.Error("chapter filename was cleared; the downloaded CBZ reference must survive")
	}
}

// TestRemoveProvider_LastSourceLeavesZeroProviderSeries removes the only
// provider and asserts the series row persists with zero providers.
func TestRemoveProvider_LastSourceLeavesZeroProviderSeries(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	svc := series.NewService(db, t.TempDir(), 14)
	s := db.Series.Create().SetTitle("Solo").SetSlug("solo").SetMonitored(true).SaveX(ctx)
	p := db.SeriesProvider.Create().SetSeries(s).SetProvider("only").SetSuwayomiID(9).SetImportance(10).SaveX(ctx)

	if err := svc.RemoveProvider(ctx, s.ID, p.ID); err != nil {
		t.Fatalf("RemoveProvider: %v", err)
	}
	if n := db.Series.Query().Where(entseries.IDEQ(s.ID)).CountX(ctx); n != 1 {
		t.Errorf("series count = %d, want 1 (must persist with 0 providers)", n)
	}
	if n := db.SeriesProvider.Query().CountX(ctx); n != 0 {
		t.Errorf("provider count = %d, want 0", n)
	}
	// Detail must still read on a 0-provider series.
	if _, err := svc.GetSeries(ctx, s.ID); err != nil {
		t.Errorf("GetSeries on 0-provider series: %v", err)
	}
}

// TestRemoveProvider_ProviderNotInSeries returns ErrProviderNotInSeries when the
// provider belongs to a different series, and performs no mutation.
func TestRemoveProvider_ProviderNotInSeries(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	svc := series.NewService(db, t.TempDir(), 14)
	s1 := db.Series.Create().SetTitle("One").SetSlug("one").SaveX(ctx)
	s2 := db.Series.Create().SetTitle("Two").SetSlug("two").SaveX(ctx)
	pOther := db.SeriesProvider.Create().SetSeries(s2).SetProvider("x").SetImportance(10).SaveX(ctx)

	err := svc.RemoveProvider(ctx, s1.ID, pOther.ID)
	if !errors.Is(err, series.ErrProviderNotInSeries) {
		t.Fatalf("err = %v, want ErrProviderNotInSeries", err)
	}
	if n := db.SeriesProvider.Query().Where(entseriesprovider.IDEQ(pOther.ID)).CountX(ctx); n != 1 {
		t.Errorf("provider was deleted despite ownership failure (%d), want still 1", n)
	}
}

// TestRemoveProvider_UnknownSeries returns ErrSeriesNotFound for a missing series.
func TestRemoveProvider_UnknownSeries(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	svc := series.NewService(db, t.TempDir(), 14)
	if err := svc.RemoveProvider(ctx, uuid.New(), uuid.New()); !errors.Is(err, series.ErrSeriesNotFound) {
		t.Fatalf("err = %v, want ErrSeriesNotFound", err)
	}
}

// TestSetCompleted proves the flag flips both ways and a missing id is reported.
func TestSetCompleted(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	svc := series.NewService(db, t.TempDir(), 14)
	s := db.Series.Create().SetTitle("Finale").SetSlug("finale").SaveX(ctx)

	if err := svc.SetCompleted(ctx, s.ID, true); err != nil {
		t.Fatalf("SetCompleted(true): %v", err)
	}
	if got := db.Series.GetX(ctx, s.ID); !got.Completed {
		t.Fatal("Completed = false after SetCompleted(true)")
	}

	if err := svc.SetCompleted(ctx, s.ID, false); err != nil {
		t.Fatalf("SetCompleted(false): %v", err)
	}
	if got := db.Series.GetX(ctx, s.ID); got.Completed {
		t.Fatal("Completed = true after SetCompleted(false) — not reversible")
	}

	if err := svc.SetCompleted(ctx, uuid.New(), true); !errors.Is(err, series.ErrSeriesNotFound) {
		t.Fatalf("SetCompleted(unknown id) err = %v, want ErrSeriesNotFound", err)
	}
}

// newTestSeries creates a minimal series fixture (no category, no cover) for
// tests that only care about chapter-state/read rollups.
func newTestSeries(t *testing.T, ctx context.Context, client *ent.Client, title string) *ent.Series {
	t.Helper()
	return client.Series.Create().
		SetTitle(title).
		SetSlug(disk.Slugify(title)).
		SaveX(ctx)
}

// mkChapter creates a chapter fixture with the given key, state, and read flag —
// the minimal shape the unread-count rollup tests need.
func mkChapter(t *testing.T, ctx context.Context, client *ent.Client, s *ent.Series, key string, state entchapter.State, read bool) *ent.Chapter {
	t.Helper()
	return client.Chapter.Create().
		SetSeriesID(s.ID).
		SetChapterKey(key).
		SetState(state).
		SetRead(read).
		SaveX(ctx)
}

// TestListSeries_UnreadCount proves ChapterCounts.Unread counts exactly the
// chapters that are downloaded AND unread — not every chapter a source knows
// about (a wanted chapter is not readable yet, so it must not count).
func TestListSeries_UnreadCount(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	s := newTestSeries(t, ctx, client, "Unread Fixture")

	// 2 downloaded+unread, 1 downloaded+read, 1 wanted (not downloadable ⇒ not unread).
	mkChapter(t, ctx, client, s, "1", entchapter.StateDownloaded, false)
	mkChapter(t, ctx, client, s, "2", entchapter.StateDownloaded, false)
	mkChapter(t, ctx, client, s, "3", entchapter.StateDownloaded, true)
	mkChapter(t, ctx, client, s, "4", entchapter.StateWanted, false)

	page, err := series.NewService(client, t.TempDir(), 14).ListSeries(ctx, series.ListFilter{Limit: 50})
	if err != nil {
		t.Fatalf("ListSeries: %v", err)
	}
	if len(page) != 1 {
		t.Fatalf("ListSeries: want 1 series, got %d", len(page))
	}
	if page[0].ChapterCounts.Unread != 2 {
		t.Fatalf("ListSeries: Unread = %d, want 2", page[0].ChapterCounts.Unread)
	}
	if page[0].ChapterCounts.Total != 4 {
		t.Fatalf("ListSeries: Total = %d, want 4", page[0].ChapterCounts.Total)
	}
}

// findSummary returns the SeriesSummaryDTO with the given title from a page, or
// fails the test if it is absent.
func findSummary(t *testing.T, page []series.SeriesSummaryDTO, title string) series.SeriesSummaryDTO {
	t.Helper()
	for _, s := range page {
		if s.Title == title {
			return s
		}
	}
	t.Fatalf("series %q not found in page", title)
	return series.SeriesSummaryDTO{}
}

// TestListSeriesCarriesSortKeys pins the two library-grid sort keys on the
// summary row: CreatedAt (always present) and LastChapterDownloadedAt
// (MAX(first_downloaded_at), nullable). It is deliberately NOT MAX(download_date)
// — see the DTO doc — so a chapter that never became readable yields a nil key,
// never the zero time and never now().
func TestListSeriesCarriesSortKeys(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()

	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	// Series A: two downloaded chapters, first_downloaded_at t1 < t2 -> MAX == t2.
	a := client.Series.Create().SetTitle("Sort Key A").SetSlug("sort-key-a").
		SetCategoryID(catID(ctx, client, "Manga")).SaveX(ctx)
	client.Chapter.Create().SetSeriesID(a.ID).SetChapterKey("ska-1").
		SetState(entchapter.StateDownloaded).SetFirstDownloadedAt(t1).SaveX(ctx)
	client.Chapter.Create().SetSeriesID(a.ID).SetChapterKey("ska-2").
		SetState(entchapter.StateDownloaded).SetFirstDownloadedAt(t2).SaveX(ctx)

	// Series B: a chapter that never carried a first_downloaded_at -> nil key.
	b := client.Series.Create().SetTitle("Sort Key B").SetSlug("sort-key-b").
		SetCategoryID(catID(ctx, client, "Manga")).SaveX(ctx)
	client.Chapter.Create().SetSeriesID(b.ID).SetChapterKey("skb-1").
		SetState(entchapter.StateWanted).SaveX(ctx)

	svc := series.NewService(client, t.TempDir(), 14)
	page, err := svc.ListSeries(ctx, series.ListFilter{})
	if err != nil {
		t.Fatalf("ListSeries: %v", err)
	}

	got := findSummary(t, page, "Sort Key A")
	if got.CreatedAt == "" {
		t.Errorf("series A CreatedAt is empty, want a timestamp")
	}
	if got.LastChapterDownloadedAt == nil {
		t.Fatalf("series A LastChapterDownloadedAt is nil, want %s (the MAX)", t2)
	}
	parsed, perr := time.Parse(time.RFC3339, *got.LastChapterDownloadedAt)
	if perr != nil {
		t.Fatalf("series A LastChapterDownloadedAt %q not RFC3339: %v", *got.LastChapterDownloadedAt, perr)
	}
	if !parsed.Equal(t2) {
		t.Errorf("series A LastChapterDownloadedAt = %s, want %s (MAX, not MIN)", parsed, t2)
	}

	gotB := findSummary(t, page, "Sort Key B")
	if gotB.CreatedAt == "" {
		t.Errorf("series B CreatedAt is empty, want a timestamp")
	}
	if gotB.LastChapterDownloadedAt != nil {
		t.Errorf("series B LastChapterDownloadedAt = %v, want nil (no chapter ever carried one)", *gotB.LastChapterDownloadedAt)
	}
}
