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
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
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

// TestListSeriesInvalidCategory verifies that an illegal category filter is
// rejected with ErrInvalidCategory rather than silently returning an empty page.
func TestListSeriesInvalidCategory(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seedLibrary(ctx, t, client)

	svc := series.NewService(client, t.TempDir(), 14)
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
		SetCategory(entseries.CategoryManga).
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
		SetCategory(entseries.CategoryOther).
		SaveX(ctx)

	cbzBytes := seedSeriesDir(t, storage, string(entseries.CategoryOther), title)

	svc := series.NewService(client, storage, 14)
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

	svc := series.NewService(client, storage, 14)
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

	svc := series.NewService(client, storage, 14)
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

	svc := series.NewService(client, storage, 14)
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

// TestMonitoredDefaultsTrue verifies that a newly created series exposes
// Monitored==true in both GetSeries (detail) and ListSeries (summary), and that
// ProviderDTO.ID matches the SeriesProvider UUID (needed by Task 5/7 re-rank).
func TestMonitoredDefaultsTrue(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()

	sr := client.Series.Create().
		SetTitle("Watch Series").
		SetSlug("watch-series").
		SetCategory(entseries.CategoryManga).
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
		SetCategory(entseries.CategoryManga).
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
	err := svc.SetCategory(ctx, uuid.New(), "Manga")
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
		SetCategory(entseries.CategoryManga).
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
		SetCategory(entseries.CategoryManga).
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
		SetCategory(entseries.CategoryManhwa).
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

	svc := series.NewService(client, storage, 14)
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
