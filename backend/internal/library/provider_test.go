package library_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/ent/chapter"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
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
	// chaptersErr, when non-nil, makes Chapters fail — used to simulate a genuine
	// engine-host upstream fetch failure so AddProvider/MatchDiskProvider must
	// classify it as ErrSourceUpstream (502), never the old phantom ErrSourceNotFound.
	chaptersErr error
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
func (f *fakeAddProviderClient) Status(ctx context.Context) (sourceengine.EngineStatus, error) {
	return sourceengine.EngineStatus{}, nil
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
func (f *fakeAddProviderClient) Chapters(ctx context.Context, sourceID int64, url string, mangaTitle string) ([]sourceengine.Chapter, error) {
	if f.chaptersErr != nil {
		return nil, f.chaptersErr
	}
	if f.chapters != nil {
		return f.chapters, nil
	}
	return []sourceengine.Chapter{
		{URL: "/ch/1", Name: "Chapter 1", Number: 1},
		{URL: "/ch/2", Name: "Chapter 2", Number: 2},
	}, nil
}
func (f *fakeAddProviderClient) Pages(ctx context.Context, sourceID int64, chapterURL, mangaURL string) ([]sourceengine.Page, error) {
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
func (f *fakeAddProviderClient) SetImpersonate(ctx context.Context, patch sourceengine.ImpersonatePatch) (sourceengine.ImpersonateConfig, error) {
	return sourceengine.ImpersonateConfig{}, nil
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

// TestAddProvider_LinkedThroughRealIngest is the P2 slice 3c regression proof:
// a provider attached through the REAL ingest.Ingest.AddSeries (not a
// hand-constructed row) must report Linked==true and the series must report
// NeedsSource==false. Before this slice, internal/ingest never set
// SeriesProvider.SuwayomiID on a newly-created row, so the old
// `SuwayomiID != 0` discriminator always read false for a freshly-adopted
// live source — this closes exactly that gap by exercising the real
// service→ingest chain (mirrors library.AddProvider/imports.Service.Adopt),
// not a fixture that hand-sets SuwayomiID.
func TestAddProvider_LinkedThroughRealIngest(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()

	// A bare series with NO providers at all — the "needs a source" case.
	ser := client.Series.Create().SetTitle("Fresh Series").SetSlug("fresh-series").SaveX(ctx)

	fake := newFakeClientWithFeed(t) // returns 2 chapters keyed "1","2" for any url
	ingestSvc := ingest.NewIngest(fake, client)
	seriesSvc := series.NewService(client, storage, 14)
	svc := library.NewService(client, ingestSvc, nil, seriesSvc, func() {}, storage, sse.NewHub())

	dto, err := svc.AddProvider(ctx, ser.ID, "1", "/manga/99", 5, "")
	if err != nil {
		t.Fatalf("AddProvider: %v", err)
	}
	if dto.NeedsSource {
		t.Errorf("SeriesDetailDTO.NeedsSource = true, want false (a real live source was just attached)")
	}
	if len(dto.Providers) != 1 {
		t.Fatalf("providers = %d, want 1", len(dto.Providers))
	}
	if p := dto.Providers[0]; !p.Linked {
		t.Errorf("provider Linked = false, want true (provider=%q was attached via the real ingest chain)", p.Provider)
	}
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

// newDriftedAttachFixture writes + imports a disk series ("mangadex"/"Alpha",
// 2 CBZs) and returns the service, the series, the disk-origin provider, and a
// live source whose resolved display name + scanlator MATCH the disk row — i.e.
// the exact shape where AddProvider takes the merge-at-attach branch. Shared by
// the three GAP-122 latch tests below so they differ only in what they perturb.
func newDriftedAttachFixture(t *testing.T, storage string, client *ent.Client) (*library.Service, *ent.Series, *ent.SeriesProvider) {
	t.Helper()
	ctx := context.Background()
	writeKaizokuSeries(t, storage, "Manga", "My Series", "mangadex", "Alpha", 2)
	facts, err := diskScanFirst(t, storage)
	if err != nil {
		t.Fatalf("diskScanFirst: %v", err)
	}
	importOneFromFacts(t, client, facts)
	ser := client.Series.Query().OnlyX(ctx)
	diskSP := client.SeriesProvider.Query().Where(entseriesprovider.SeriesID(ser.ID)).OnlyX(ctx)

	fake := &fakeNamedSourceClient{sourceID: 1, sourceName: "mangadex", scanlator: "Alpha"}
	svc := library.NewService(client, ingest.NewIngest(fake, client), nil,
		series.NewService(client, storage, 14), func() {}, storage, sse.NewHub())
	return svc, ser, diskSP
}

// TestAddProvider_MergeAtAttachYieldsToAnInFlightMerge is the GAP-122 proof for
// the first of the two merge entries that took no latch at all.
//
// Merge-at-attach folds CBZs through the SAME mergeDiskIntoLive core as every
// other merge path, and its window is the widest of them: it opens the instant
// the ingest commits the live twin's feed — which is exactly what makes the
// series eligible for the unattended self-heal — and stays open for the whole
// multi-minute relabel. Two concurrent folds over one series do not fail fast
// (relabelMoveIntoPlace is idempotent); the loser proceeds to a commitMatch that
// dies on the already-deleted disk row and then renames every CBZ BACK, leaving
// every file where the winner's committed rows are not looking.
//
// So the fold must YIELD. And it must yield LOUDLY: quietly falling through to
// the ordinary importance-set path would raise a live twin that now HAS a feed
// above the disk chapters' watermark, which is precisely what arms
// DetectUpgrades to re-download the entire imported series. The attach instead
// reports ErrMergeInFlight (409) and leaves the fresh row at the reserved park
// sentinel 0, so nothing can out-rank anything.
//
// FAILS on the unfixed code: AddProvider took no latch, so it merged straight
// over the in-flight merge — err was nil and one provider row was left.
func TestAddProvider_MergeAtAttachYieldsToAnInFlightMerge(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()

	svc, ser, diskSP := newDriftedAttachFixture(t, storage, client)

	// Stand in for a merge already in flight on this series (an owner Match, a
	// consolidation, a dedup, the collapse, or the unattended self-heal).
	if !svc.AcquireMerge(ser.ID) {
		t.Fatal("could not take the merge latch")
	}

	if _, err := svc.AddProvider(ctx, ser.ID, "1", "/manga/99", 5, "Alpha"); !errors.Is(err, library.ErrMergeInFlight) {
		t.Fatalf("AddProvider err = %v, want ErrMergeInFlight — the fold must never run over a merge already in flight", err)
	}

	// Nothing was folded: both rows survive and every disk chapter is still
	// satisfied by the disk provider at its original watermark.
	assertProviderCount(t, client, ctx, ser.ID, 2)
	for _, key := range []string{"1", "2"} {
		assertChapterSatisfaction(t, client, ctx, ser.ID, key, &diskSP.ID, 1)
	}

	// The freshly-ingested live row stays at the reserved park sentinel 0.
	live := client.SeriesProvider.Query().
		Where(entseriesprovider.SeriesID(ser.ID), entseriesprovider.Provider("1")).
		OnlyX(ctx)
	if live.Importance != 0 {
		t.Fatalf("live provider importance = %d after the yield, want 0 — any real rank re-downloads the whole imported series", live.Importance)
	}
	assertNoUpgradesFlagged(t, ctx, client)

	// It is a yield, not a wedge: once the in-flight merge lands, the ordinary
	// dedup path folds exactly the pair the attach left behind (and the
	// unattended self-heal does the same on its next sweep, unprompted).
	svc.ReleaseMerge(ser.ID)
	merged, skipped, err := svc.DedupProviders(ctx, ser.ID)
	if err != nil || merged != 1 || skipped != 0 {
		t.Fatalf("DedupProviders after the yield = (merged=%d, skipped=%d, err=%v), want (1, 0, nil)", merged, skipped, err)
	}
	assertProviderCount(t, client, ctx, ser.ID, 1)
}

// TestAddProvider_PlainAttachIsUnaffectedByAnInFlightMerge protects the common
// case from the fix: attaching a source to a series with NO drifted disk row to
// fold must keep working exactly as before, even while another merge holds that
// series' latch. The latch guards the MERGE BRANCH, not the attach.
//
// This is a GUARD, not a test that fails on the unfixed code — there no latch
// was taken at all, so of course the attach succeeded. It exists because the
// obvious over-correction (latching the whole of linkAttachedProvider, or the
// whole of AddProvider) would turn every ordinary attach into a 409 for the
// duration of an unrelated background merge, and nothing else would catch that.
func TestAddProvider_PlainAttachIsUnaffectedByAnInFlightMerge(t *testing.T) {
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

	// Resolved display name "WeebCentral" != the disk row's "mangadex", so there
	// is nothing to fold and the merge branch is never entered.
	fake := &fakeNamedSourceClient{sourceID: 1, sourceName: "WeebCentral"}
	svc := library.NewService(client, ingest.NewIngest(fake, client), nil,
		series.NewService(client, storage, 14), func() {}, storage, sse.NewHub())

	if !svc.AcquireMerge(ser.ID) {
		t.Fatal("could not take the merge latch")
	}
	defer svc.ReleaseMerge(ser.ID)

	dto, err := svc.AddProvider(ctx, ser.ID, "1", "/manga/99", 5, "Alpha")
	if err != nil {
		t.Fatalf("AddProvider: %v — an attach with nothing to fold must not be blocked by an unrelated merge", err)
	}
	if len(dto.Providers) != 2 {
		t.Fatalf("providers = %d, want 2 (disk + the newly attached source)", len(dto.Providers))
	}
	live := client.SeriesProvider.Query().
		Where(entseriesprovider.SeriesID(ser.ID), entseriesprovider.Provider("1")).
		OnlyX(ctx)
	if live.Importance != 5 {
		t.Fatalf("attached provider importance = %d, want 5 (the owner's requested rank still applied)", live.Importance)
	}
}

// TestAddProvider_ReleasesTheMergeLatchWhenTheFoldFails is the anti-wedge guard
// for the attach path. A fold that fails partway MUST still free the latch — a
// stranded latch would lock that series out of EVERY merge path (dedup, Match,
// consolidation, the collapse and the recurring self-heal) for the lifetime of
// the process, converting a recoverable disk error into a permanent one.
//
// The failure is real, not injected: the series folder is removed before the
// attach, so disk.RelabelChapterFile fails on the first chapter and
// mergeDiskIntoLive returns with the database untouched.
//
// This is a GUARD, not a test that fails on the unfixed code — there the attach
// took no latch, so there was none to strand. It exists because the release is a
// `defer` that one refactor could drop (Go also runs it while unwinding a panic,
// which is why the acquire and the release live in the same function), and
// nothing else would notice.
func TestAddProvider_ReleasesTheMergeLatchWhenTheFoldFails(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()

	svc, ser, _ := newDriftedAttachFixture(t, storage, client)

	// Make the disk phase fail: the CBZs the fold must relabel are gone.
	if err := os.RemoveAll(filepath.Join(storage, "Manga", "My Series")); err != nil {
		t.Fatalf("remove series folder: %v", err)
	}

	if _, err := svc.AddProvider(ctx, ser.ID, "1", "/manga/99", 5, "Alpha"); err == nil {
		t.Fatal("AddProvider succeeded with the series folder removed; want a relabel failure")
	} else if errors.Is(err, library.ErrMergeInFlight) {
		t.Fatalf("AddProvider err = %v, want the underlying merge failure", err)
	}

	if !svc.AcquireMerge(ser.ID) {
		t.Fatal("the merge latch is still held after a failed fold — the series is wedged out of every merge path")
	}
	svc.ReleaseMerge(ser.ID)
}
