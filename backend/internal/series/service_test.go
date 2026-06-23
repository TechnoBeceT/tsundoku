// Package series_test exercises the library read service against an ephemeral
// PostgreSQL instance (testdb). Tests require Docker.
package series_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	"github.com/technobecet/tsundoku/internal/series"
)

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
		SetCategory(entseries.CategoryManga).
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
		SetCategory(entseries.CategoryManhwa).
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
	client.SeriesProvider.Create().
		SetSeriesID(manhwa.ID).
		SetProvider("flame").
		SetLanguage("en").
		SetImportance(8).
		SaveX(ctx)

	return seededLibrary{mangaID: manga.ID, manhwaID: manhwa.ID}
}

func TestListSeriesReturnsAllWithRollup(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seedLibrary(ctx, t, client)

	svc := series.NewService(client, t.TempDir())
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
	if alpha.Slug != "alpha-saga" || alpha.Category != "Manga" || alpha.CoverURL != "https://example.test/alpha.jpg" {
		t.Fatalf("ListSeries: alpha summary mismatch: %+v", alpha)
	}
	// Non-vacuous rollup: 1 downloaded + 1 wanted = total 2.
	wantAlpha := series.ChapterCounts{Total: 2, Downloaded: 1, Wanted: 1, Failed: 0}
	if alpha.ChapterCounts != wantAlpha {
		t.Fatalf("ListSeries: alpha counts: want %+v, got %+v", wantAlpha, alpha.ChapterCounts)
	}

	beta := got[1]
	wantBeta := series.ChapterCounts{Total: 3, Downloaded: 1, Wanted: 1, Failed: 1}
	if beta.ChapterCounts != wantBeta {
		t.Fatalf("ListSeries: beta counts: want %+v, got %+v", wantBeta, beta.ChapterCounts)
	}
}

func TestListSeriesFiltersByCategory(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seedLibrary(ctx, t, client)

	svc := series.NewService(client, t.TempDir())
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

// TestListSeriesInvalidCategory verifies that an illegal category filter is
// rejected with ErrInvalidCategory rather than silently returning an empty page.
func TestListSeriesInvalidCategory(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seedLibrary(ctx, t, client)

	svc := series.NewService(client, t.TempDir())
	bogus := "Bogus"
	got, err := svc.ListSeries(ctx, series.ListFilter{Category: &bogus})
	if !errors.Is(err, series.ErrInvalidCategory) {
		t.Fatalf("ListSeries(Bogus): want ErrInvalidCategory, got %v", err)
	}
	if got != nil {
		t.Fatalf("ListSeries(Bogus): want nil result, got %+v", got)
	}
}

func TestListSeriesPaginates(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seedLibrary(ctx, t, client)

	svc := series.NewService(client, t.TempDir())

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

	svc := series.NewService(client, t.TempDir())
	got, err := svc.GetSeries(ctx, lib.manhwaID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}

	if got.Title != "Beta Quest" || got.Category != "Manhwa" {
		t.Fatalf("GetSeries: summary mismatch: %+v", got)
	}
	if got.ChapterCounts != (series.ChapterCounts{Total: 3, Downloaded: 1, Wanted: 1, Failed: 1}) {
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
		SetCategory(entseries.CategoryManhwa).
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

	svc := series.NewService(client, storage)
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

func TestGetSeriesNotFound(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seedLibrary(ctx, t, client)

	svc := series.NewService(client, t.TempDir())
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
		SetCategory(entseries.CategoryOther).
		SaveX(ctx)

	cbzBytes := seedSeriesDir(t, storage, string(entseries.CategoryOther), title)

	svc := series.NewService(client, storage)
	if err := svc.SetCategory(ctx, row.ID, "Manhwa"); err != nil {
		t.Fatalf("SetCategory: %v", err)
	}

	// DB side: category updated.
	reread := client.Series.GetX(ctx, row.ID)
	if reread.Category != entseries.CategoryManhwa {
		t.Fatalf("SetCategory: DB category want Manhwa, got %s", reread.Category)
	}

	// Disk side: folder moved to the new category, old folder gone, CBZ intact.
	oldDir := disk.SeriesDir(storage, string(entseries.CategoryOther), title)
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("SetCategory: old dir %q should be gone, stat err = %v", oldDir, err)
	}
	newDir := disk.SeriesDir(storage, string(entseries.CategoryManhwa), title)
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
	if sidecar == nil || sidecar.Category != string(entseries.CategoryManhwa) {
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
		SetCategory(entseries.CategoryManhwa).
		SaveX(ctx)
	seedSeriesDir(t, storage, string(entseries.CategoryManhwa), title)

	svc := series.NewService(client, storage)
	if err := svc.SetCategory(ctx, row.ID, "Manhwa"); err != nil {
		t.Fatalf("SetCategory(same): %v", err)
	}

	if client.Series.GetX(ctx, row.ID).Category != entseries.CategoryManhwa {
		t.Fatalf("SetCategory(same): category changed")
	}
	if _, err := os.Stat(disk.SeriesDir(storage, string(entseries.CategoryManhwa), title)); err != nil {
		t.Fatalf("SetCategory(same): dir should be untouched: %v", err)
	}
}

// TestSetCategoryInvalidCategory verifies an illegal enum value is rejected with
// ErrInvalidCategory and nothing changes on either DB or disk.
func TestSetCategoryInvalidCategory(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	storage := t.TempDir()

	const title = "Solo Leveling"
	row := client.Series.Create().
		SetTitle(title).
		SetSlug("solo-leveling").
		SetCategory(entseries.CategoryOther).
		SaveX(ctx)
	seedSeriesDir(t, storage, string(entseries.CategoryOther), title)

	svc := series.NewService(client, storage)
	err := svc.SetCategory(ctx, row.ID, "Bogus")
	if !errors.Is(err, series.ErrInvalidCategory) {
		t.Fatalf("SetCategory(Bogus): want ErrInvalidCategory, got %v", err)
	}

	if client.Series.GetX(ctx, row.ID).Category != entseries.CategoryOther {
		t.Fatalf("SetCategory(Bogus): DB category changed")
	}
	if _, err := os.Stat(disk.SeriesDir(storage, string(entseries.CategoryOther), title)); err != nil {
		t.Fatalf("SetCategory(Bogus): dir should be untouched: %v", err)
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
		SetCategory(entseries.CategoryOther).
		SaveX(ctx)

	svc := series.NewService(client, storage)
	if err := svc.SetCategory(ctx, row.ID, "Manhua"); err != nil {
		t.Fatalf("SetCategory(no folder): %v", err)
	}

	if client.Series.GetX(ctx, row.ID).Category != entseries.CategoryManhua {
		t.Fatalf("SetCategory(no folder): DB category want Manhua")
	}
	// No folder was ever created on either side.
	if _, err := os.Stat(disk.SeriesDir(storage, string(entseries.CategoryManhua), "No Downloads Yet")); !os.IsNotExist(err) {
		t.Fatalf("SetCategory(no folder): no dir should have been created, stat err = %v", err)
	}
}

// TestSetCategoryNotFound verifies an unknown series id yields ErrSeriesNotFound.
func TestSetCategoryNotFound(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()

	svc := series.NewService(client, t.TempDir())
	err := svc.SetCategory(ctx, uuid.New(), "Manga")
	if !errors.Is(err, series.ErrSeriesNotFound) {
		t.Fatalf("SetCategory(random): want ErrSeriesNotFound, got %v", err)
	}
}

// TestCategoriesReturnsAllEnumValuesWithCounts verifies Categories reports exactly
// the five enum values (including zero-count ones) with correct counts.
func TestCategoriesReturnsAllEnumValuesWithCounts(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	storage := t.TempDir()

	// Two series: one Manga, one Other (then moved to Manhwa below).
	manga := client.Series.Create().
		SetTitle("Alpha Saga").SetSlug("alpha-saga").
		SetCategory(entseries.CategoryManga).SaveX(ctx)
	_ = manga
	other := client.Series.Create().
		SetTitle("Solo Leveling").SetSlug("solo-leveling").
		SetCategory(entseries.CategoryOther).SaveX(ctx)
	seedSeriesDir(t, storage, string(entseries.CategoryOther), "Solo Leveling")

	svc := series.NewService(client, storage)
	if err := svc.SetCategory(ctx, other.ID, "Manhwa"); err != nil {
		t.Fatalf("SetCategory: %v", err)
	}

	got, err := svc.Categories(ctx)
	if err != nil {
		t.Fatalf("Categories: %v", err)
	}

	if len(got) != 5 {
		t.Fatalf("Categories: want 5 entries, got %d (%+v)", len(got), got)
	}

	want := map[string]int{"Manga": 1, "Manhwa": 1, "Manhua": 0, "Comic": 0, "Other": 0}
	for _, c := range got {
		exp, ok := want[c.Category]
		if !ok {
			t.Fatalf("Categories: unexpected category %q", c.Category)
		}
		if c.Count != exp {
			t.Fatalf("Categories: %s count want %d, got %d", c.Category, exp, c.Count)
		}
		delete(want, c.Category)
	}
	if len(want) != 0 {
		t.Fatalf("Categories: missing enum values: %+v", want)
	}
}
