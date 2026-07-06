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
	"github.com/technobecet/tsundoku/internal/library"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sse"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// fakeAddProviderClient is a minimal suwayomi.Client implementation for the
// AddProvider upgrade-flagging test: FetchChapters returns two chapters whose
// Number is 1 and 2 so their normalized keys ("1"/"2") match the disk fixture
// written by writeKaizokuSeries, and MangaMeta returns a valid Manga so
// suwayomi.Ingest.upsertSeriesProvider does not fail. All other methods are
// zero-value stubs — the interface is large but unused by this test.
//
// searchTitle configures Sources/Search to return one candidate manga (for
// the MatchCandidates test, via newFakeClientWithSearch); it is left zero for
// newFakeClientWithFeed, preserving that constructor's original empty-search
// behavior. chapters overrides FetchChapters' fixed 2-chapter ("1","2") feed
// when non-nil — used by the MatchDiskProvider partial-overlap test (see
// newFakeClientWithChapters, match_disk_provider_test.go) to simulate a real
// source that only offers SOME of the disk-imported chapter keys.
type fakeAddProviderClient struct {
	searchTitle string
	chapters    []suwayomi.Chapter
}

func newFakeClientWithFeed(t *testing.T) *fakeAddProviderClient {
	t.Helper()
	return &fakeAddProviderClient{}
}

// newFakeClientWithChapters returns a fake whose FetchChapters reports exactly
// chapters (overriding the default fixed 2-chapter feed) — used to simulate a
// real source with partial (or otherwise custom) coverage of a series.
func newFakeClientWithChapters(t *testing.T, chapters []suwayomi.Chapter) *fakeAddProviderClient {
	t.Helper()
	return &fakeAddProviderClient{chapters: chapters}
}

// newFakeClientWithSearch returns a fake whose Sources/Search report one
// source ("weeb") carrying one manga candidate titled title — enough for
// imports.Service.Search to fan out and return a non-empty group.
func newFakeClientWithSearch(t *testing.T, title string) *fakeAddProviderClient {
	t.Helper()
	return &fakeAddProviderClient{searchTitle: title}
}

func (f *fakeAddProviderClient) Sources(ctx context.Context) ([]suwayomi.Source, error) {
	if f.searchTitle == "" {
		return nil, nil
	}
	return []suwayomi.Source{{ID: "weeb", Name: "Weeb Source", Lang: "en"}}, nil
}
func (f *fakeAddProviderClient) Search(ctx context.Context, sourceID, query string) ([]suwayomi.Manga, error) {
	if f.searchTitle == "" {
		return nil, nil
	}
	return []suwayomi.Manga{{ID: 1, Title: f.searchTitle}}, nil
}
func (f *fakeAddProviderClient) Browse(ctx context.Context, sourceID string, t suwayomi.BrowseType, page int) (suwayomi.BrowseResult, error) {
	return suwayomi.BrowseResult{}, nil
}
func (f *fakeAddProviderClient) FetchChapters(ctx context.Context, mangaID int) ([]suwayomi.Chapter, error) {
	if f.chapters != nil {
		return f.chapters, nil
	}
	one, two := 1.0, 2.0
	return []suwayomi.Chapter{
		{ID: 101, Index: 0, Name: "Chapter 1", Number: &one},
		{ID: 102, Index: 1, Name: "Chapter 2", Number: &two},
	}, nil
}
func (f *fakeAddProviderClient) MangaChapters(ctx context.Context, mangaID int) ([]suwayomi.Chapter, error) {
	return nil, nil
}
func (f *fakeAddProviderClient) ChapterPages(ctx context.Context, chapterID int) ([]string, error) {
	return nil, nil
}
func (f *fakeAddProviderClient) MangaMeta(ctx context.Context, mangaID int) (suwayomi.Manga, error) {
	return suwayomi.Manga{ID: mangaID, Title: "My Series"}, nil
}
func (f *fakeAddProviderClient) FetchMangaDetails(ctx context.Context, mangaID int) (suwayomi.Manga, error) {
	return suwayomi.Manga{ID: mangaID, Title: "My Series"}, nil
}
func (f *fakeAddProviderClient) PageBytes(ctx context.Context, pageURL string) ([]byte, string, error) {
	return nil, "", errors.New("PageBytes: not configured")
}
func (f *fakeAddProviderClient) ServerSettings(ctx context.Context) (suwayomi.SuwayomiSettings, error) {
	return suwayomi.SuwayomiSettings{}, nil
}
func (f *fakeAddProviderClient) SetServerSettings(ctx context.Context, patch suwayomi.SuwayomiSettingsPatch) error {
	return nil
}
func (f *fakeAddProviderClient) Extensions(ctx context.Context) ([]suwayomi.Extension, error) {
	return nil, nil
}
func (f *fakeAddProviderClient) SetExtensionState(ctx context.Context, pkgName string, action suwayomi.ExtensionAction) error {
	return nil
}
func (f *fakeAddProviderClient) FetchExtensions(ctx context.Context) ([]suwayomi.Extension, error) {
	return nil, nil
}
func (f *fakeAddProviderClient) ExtensionRepos(ctx context.Context) ([]string, error) {
	return nil, nil
}
func (f *fakeAddProviderClient) SetExtensionRepos(ctx context.Context, repos []string) error {
	return nil
}
func (f *fakeAddProviderClient) SourcePreferences(ctx context.Context, sourceID string) ([]suwayomi.SourcePreference, error) {
	return nil, nil
}
func (f *fakeAddProviderClient) SetSourcePreference(ctx context.Context, sourceID string, position int, value suwayomi.PreferenceValue) ([]suwayomi.SourcePreference, error) {
	return nil, nil
}
func (f *fakeAddProviderClient) ExtensionSources(ctx context.Context, pkgName string) ([]suwayomi.Source, error) {
	return nil, nil
}
func (f *fakeAddProviderClient) SetSourceEnabled(ctx context.Context, sourceID string, enabled bool) error {
	return nil
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

	// fake suwayomi client returns one manga + a matching chapter feed for source "weeb"
	fake := newFakeClientWithFeed(t) // returns 2 chapters keyed "1","2" for mangaID 99
	ingest := suwayomi.NewIngest(fake, client)
	seriesSvc := series.NewService(client, storage, 14)
	svc := library.NewService(client, ingest, nil, seriesSvc, func() {}, storage, sse.NewHub())

	dto, err := svc.AddProvider(ctx, ser.ID, "weeb", 99, 5, "")
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
	ingest := suwayomi.NewIngest(fake, client)
	seriesSvc := series.NewService(client, storage, 14)
	svc := library.NewService(client, ingest, nil, seriesSvc, func() {}, storage, sse.NewHub())

	// Attach "weeb" twice under two different scanlators, with different
	// importances. Neither call should be rejected as a duplicate.
	if _, err := svc.AddProvider(ctx, ser.ID, "weeb", 99, 5, "Alpha Scans"); err != nil {
		t.Fatalf("AddProvider (Alpha Scans): %v", err)
	}
	dto, err := svc.AddProvider(ctx, ser.ID, "weeb", 99, 3, "Beta Scans")
	if err != nil {
		t.Fatalf("AddProvider (Beta Scans): %v", err)
	}
	if len(dto.Providers) != 3 {
		t.Fatalf("providers = %d, want 3 (disk + weeb/Alpha Scans + weeb/Beta Scans)", len(dto.Providers))
	}

	rows := client.SeriesProvider.Query().AllX(ctx)
	gotImportance := make(map[string]int, len(rows))
	for _, sp := range rows {
		if sp.Provider == "weeb" {
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
	if _, err := svc.AddProvider(ctx, ser.ID, "weeb", 99, 9, "Alpha Scans"); !errors.Is(err, library.ErrProviderAlreadyPresent) {
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
	if _, err := svc.AddProvider(ctx, seriesID, "weeb", 99, 5, ""); !errors.Is(err, library.ErrProviderAlreadyPresent) {
		t.Fatalf("want ErrProviderAlreadyPresent on duplicate add, got %v", err)
	}
	if _, err := svc.AddProvider(ctx, uuid.New(), "weeb", 99, 5, ""); !errors.Is(err, library.ErrSeriesNotFound) {
		t.Fatalf("want ErrSeriesNotFound on unknown series, got %v", err)
	}
}
