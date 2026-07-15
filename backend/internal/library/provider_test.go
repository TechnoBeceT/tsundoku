package library_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/ingest"
	"github.com/technobecet/tsundoku/internal/library"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sse"
)

// fakeAddProviderClient is a minimal sourceengine.Client implementation for
// the AddProvider upgrade-flagging test: Chapters returns two chapters whose
// Number is 1 and 2 so their normalized keys ("1"/"2") match the disk fixture
// written by writeKaizokuSeries, and MangaDetails returns a valid manga so
// ingest.Ingest.upsertSeriesProvider does not fail. All other methods are
// zero-value stubs — the interface is large but unused by this test.
//
// searchTitle configures Sources/Search to return one candidate manga (for
// the MatchCandidates test, via newFakeClientWithSearch); it is left zero for
// newFakeClientWithFeed, preserving that constructor's original empty-search
// behavior. chapters overrides Chapters' fixed 2-chapter ("1","2") feed when
// non-nil — used by the MatchDiskProvider partial-overlap test (see
// newFakeClientWithChapters, match_disk_provider_test.go) to simulate a real
// source that only offers SOME of the disk-imported chapter keys.
type fakeAddProviderClient struct {
	searchTitle string
	chapters    []sourceengine.Chapter
}

func newFakeClientWithFeed(t *testing.T) *fakeAddProviderClient {
	t.Helper()
	return &fakeAddProviderClient{}
}

// newFakeClientWithChapters returns a fake whose Chapters reports exactly
// chapters (overriding the default fixed 2-chapter feed) — used to simulate a
// real source with partial (or otherwise custom) coverage of a series.
func newFakeClientWithChapters(t *testing.T, chapters []sourceengine.Chapter) *fakeAddProviderClient {
	t.Helper()
	return &fakeAddProviderClient{chapters: chapters}
}

// newFakeClientWithSearch returns a fake whose Sources/Search report one
// source (id 1) carrying one manga candidate titled title — enough for
// imports.Service.Search to fan out and return a non-empty group.
func newFakeClientWithSearch(t *testing.T, title string) *fakeAddProviderClient {
	t.Helper()
	return &fakeAddProviderClient{searchTitle: title}
}

func (f *fakeAddProviderClient) Health(ctx context.Context) (sourceengine.Health, error) {
	return sourceengine.Health{}, nil
}
func (f *fakeAddProviderClient) Sources(ctx context.Context) ([]sourceengine.Source, error) {
	if f.searchTitle == "" {
		return nil, nil
	}
	return []sourceengine.Source{{ID: 1, Name: "Weeb Source", Lang: "en"}}, nil
}
func (f *fakeAddProviderClient) Search(ctx context.Context, sourceID int64, query string, page int) (sourceengine.SearchResult, error) {
	if f.searchTitle == "" {
		return sourceengine.SearchResult{}, nil
	}
	return sourceengine.SearchResult{Manga: []sourceengine.MangaEntry{{URL: "/manga/1", Title: f.searchTitle}}}, nil
}
func (f *fakeAddProviderClient) Popular(ctx context.Context, sourceID int64, page int) (sourceengine.SearchResult, error) {
	return sourceengine.SearchResult{}, nil
}
func (f *fakeAddProviderClient) Latest(ctx context.Context, sourceID int64, page int) (sourceengine.SearchResult, error) {
	return sourceengine.SearchResult{}, nil
}
func (f *fakeAddProviderClient) MangaDetails(ctx context.Context, sourceID int64, url string) (sourceengine.MangaDetails, error) {
	return sourceengine.MangaDetails{URL: url, Title: "My Series"}, nil
}
func (f *fakeAddProviderClient) Chapters(ctx context.Context, sourceID int64, url string) ([]sourceengine.Chapter, error) {
	if f.chapters != nil {
		return f.chapters, nil
	}
	return []sourceengine.Chapter{
		{URL: "/ch/1", Name: "Chapter 1", Number: 1},
		{URL: "/ch/2", Name: "Chapter 2", Number: 2},
	}, nil
}
func (f *fakeAddProviderClient) Pages(ctx context.Context, sourceID int64, chapterURL string) ([]sourceengine.Page, error) {
	return nil, nil
}
func (f *fakeAddProviderClient) Image(ctx context.Context, sourceID int64, pageURL, imageURL string) ([]byte, string, error) {
	return nil, "", errors.New("Image: not configured")
}
func (f *fakeAddProviderClient) Preferences(ctx context.Context, sourceID int64) ([]sourceengine.Preference, error) {
	return nil, nil
}
func (f *fakeAddProviderClient) SetPreferences(ctx context.Context, sourceID int64, changes map[string]any) ([]sourceengine.Preference, error) {
	return nil, nil
}
func (f *fakeAddProviderClient) Extensions(ctx context.Context) ([]sourceengine.Extension, error) {
	return nil, nil
}
func (f *fakeAddProviderClient) InstallExtension(ctx context.Context, pkgName, apkURL string) ([]sourceengine.Extension, error) {
	return nil, nil
}
func (f *fakeAddProviderClient) RefreshExtensions(ctx context.Context) ([]sourceengine.Extension, error) {
	return nil, nil
}
func (f *fakeAddProviderClient) UpdateExtension(ctx context.Context, pkgName string) ([]sourceengine.Extension, error) {
	return nil, nil
}
func (f *fakeAddProviderClient) UninstallExtension(ctx context.Context, pkgName string) ([]sourceengine.Extension, error) {
	return nil, nil
}
func (f *fakeAddProviderClient) Repos(ctx context.Context) ([]string, error) {
	return nil, nil
}
func (f *fakeAddProviderClient) SetRepos(ctx context.Context, repos []string) ([]string, error) {
	return nil, nil
}
func (f *fakeAddProviderClient) SetFlareSolverr(ctx context.Context, patch sourceengine.FlareSolverrPatch) (sourceengine.FlareSolverrConfig, error) {
	return sourceengine.FlareSolverrConfig{}, nil
}
func (f *fakeAddProviderClient) SetSocks(ctx context.Context, patch sourceengine.SocksPatch) (sourceengine.SocksConfig, error) {
	return sourceengine.SocksConfig{}, nil
}

// diskScanFirst wraps disk.ScanLibrary and returns the first (and, for this
// test's single-series fixture, only) SeriesFacts found under storage.
func diskScanFirst(t *testing.T, storage string) (disk.SeriesFacts, error) {
	t.Helper()
	facts, err := disk.ScanLibrary(storage)
	if err != nil {
		return disk.SeriesFacts{}, err
	}
	if len(facts) == 0 {
		t.Fatal("diskScanFirst: no series found on disk")
	}
	return facts[0], nil
}

// importOneFromFacts wraps disk.ReconcileOne, importing a single already-
// scanned series (disk-only, satisfied_importance=1) into the database.
func importOneFromFacts(t *testing.T, client *ent.Client, sf disk.SeriesFacts) {
	t.Helper()
	if _, err := disk.ReconcileOne(context.Background(), client, sf); err != nil {
		t.Fatalf("importOneFromFacts: %v", err)
	}
}

func TestAddProvider_AttachesSourceAndFlagsUpgrade(t *testing.T) {
	storage := t.TempDir()
	writeKaizokuSeries(t, storage, "Manga", "My Series", "mangadex", "Alpha", 2)
	client := testdb.New(t)
	ctx := context.Background()

	// import disk-only (importance 1)
	facts, err := diskScanFirst(t, storage) // helper wrapping disk.ScanLibrary
	if err != nil {
		t.Fatalf("diskScanFirst: %v", err)
	}
	importOneFromFacts(t, client, facts) // helper wrapping disk.ReconcileOne
	ser := client.Series.Query().OnlyX(ctx)

	// fake engine-host client returns one manga + a matching chapter feed for source "1"
	fake := newFakeClientWithFeed(t) // returns 2 chapters keyed "1","2" for any url
	ingestSvc := ingest.NewIngest(fake, client)
	seriesSvc := series.NewService(client, storage, 14)
	svc := library.NewService(client, ingestSvc, nil, seriesSvc, func() {}, storage, sse.NewHub())

	dto, err := svc.AddProvider(ctx, ser.ID, "1", "/manga/99", 5, "")
	if err != nil {
		t.Fatalf("AddProvider: %v", err)
	}
	if len(dto.Providers) != 2 {
		t.Fatalf("providers = %d, want 2 (disk + weeb)", len(dto.Providers))
	}

	assertUpgradesFlagged(t, ctx, client, 2)
	assertAddProviderErrors(t, ctx, svc, ser.ID)
}

// TestAddProvider_ScanlatorAware verifies that AddProvider treats the same
// source under two DIFFERENT scanlators as two independent SeriesProvider
// rows — each keeping its OWN importance — rather than colliding on
// provider name alone (the same bug class as imports.setImportances).
func TestAddProvider_ScanlatorAware(t *testing.T) {
	storage := t.TempDir()
	writeKaizokuSeries(t, storage, "Manga", "My Series", "mangadex", "Alpha", 2)
	client := testdb.New(t)
	ctx := context.Background()

	facts, err := diskScanFirst(t, storage)
	if err != nil {
		t.Fatalf("diskScanFirst: %v", err)
	}
	importOneFromFacts(t, client, facts)
	ser := client.Series.Query().OnlyX(ctx)

	fake := newFakeClientWithFeed(t)
	ingestSvc := ingest.NewIngest(fake, client)
	seriesSvc := series.NewService(client, storage, 14)
	svc := library.NewService(client, ingestSvc, nil, seriesSvc, func() {}, storage, sse.NewHub())

	// Attach "1" twice under two different scanlators, with different
	// importances, both for the same manga (mangaID 99 → url "/manga/99").
	// Neither call should be rejected as a duplicate.
	if _, err := svc.AddProvider(ctx, ser.ID, "1", "/manga/99", 5, "Alpha Scans"); err != nil {
		t.Fatalf("AddProvider (Alpha Scans): %v", err)
	}
	dto, err := svc.AddProvider(ctx, ser.ID, "1", "/manga/99", 3, "Beta Scans")
	if err != nil {
		t.Fatalf("AddProvider (Beta Scans): %v", err)
	}
	if len(dto.Providers) != 3 {
		t.Fatalf("providers = %d, want 3 (disk + weeb/Alpha Scans + weeb/Beta Scans)", len(dto.Providers))
	}

	rows := client.SeriesProvider.Query().AllX(ctx)
	gotImportance := make(map[string]int, len(rows))
	for _, sp := range rows {
		if sp.Provider == "1" {
			gotImportance[sp.Scanlator] = sp.Importance
		}
	}
	if gotImportance["Alpha Scans"] != 5 {
		t.Errorf("weeb/Alpha Scans importance: got %d, want 5", gotImportance["Alpha Scans"])
	}
	if gotImportance["Beta Scans"] != 3 {
		t.Errorf("weeb/Beta Scans importance: got %d, want 3", gotImportance["Beta Scans"])
	}

	// Re-adding the exact same (source, scanlator) pair is still rejected.
	if _, err := svc.AddProvider(ctx, ser.ID, "1", "/manga/99", 9, "Alpha Scans"); !errors.Is(err, library.ErrProviderAlreadyPresent) {
		t.Fatalf("want ErrProviderAlreadyPresent on duplicate (source, scanlator), got %v", err)
	}
}

// assertUpgradesFlagged runs download.DetectUpgrades and checks that exactly
// want chapters were flagged and now sit in state=upgrade_available — the
// on-disk chapters (satisfied_importance 1) become upgrade candidates once a
// strictly-higher-importance provider's feed covers the same chapter keys.
func assertUpgradesFlagged(t *testing.T, ctx context.Context, client *ent.Client, want int) {
	t.Helper()
	n, err := download.DetectUpgrades(ctx, client, 3)
	if err != nil {
		t.Fatal(err)
	}
	if n != want {
		t.Fatalf("DetectUpgrades = %d, want %d", n, want)
	}
	up := client.Chapter.Query().Where(chapter.StateEQ(chapter.StateUpgradeAvailable)).CountX(ctx)
	if up != want {
		t.Fatalf("upgrade_available = %d, want %d", up, want)
	}
}

// assertAddProviderErrors checks the two guard paths: attaching an
// already-present provider, and targeting an unknown series id.
func assertAddProviderErrors(t *testing.T, ctx context.Context, svc *library.Service, seriesID uuid.UUID) {
	t.Helper()
	if _, err := svc.AddProvider(ctx, seriesID, "1", "/manga/99", 5, ""); !errors.Is(err, library.ErrProviderAlreadyPresent) {
		t.Fatalf("want ErrProviderAlreadyPresent on duplicate add, got %v", err)
	}
	if _, err := svc.AddProvider(ctx, uuid.New(), "1", "/manga/99", 5, ""); !errors.Is(err, library.ErrSeriesNotFound) {
		t.Fatalf("want ErrSeriesNotFound on unknown series, got %v", err)
	}
}
