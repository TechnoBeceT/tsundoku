package library_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/ingest"
	"github.com/technobecet/tsundoku/internal/library"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sse"
)

// fakeNamedSourceClient reuses fakeAddProviderClient's full Client surface but
// overrides Sources so ingest.Ingest.resolveProviderName resolves a specific
// display name for the attached source id — the ONLY signal that lets
// merge-at-attach recognise a live source as the same physical source as a disk
// import (whose provider field holds that same display name). sourceName "" =
// unresolved, so the base fake's nil Sources already covers the empty-name case.
type fakeNamedSourceClient struct {
	fakeAddProviderClient
	sourceID   int64
	sourceName string
	// scanlator tags the chapters Chapters reports, so ingest.Ingest's
	// scanlator filter (which drops chapters not matching the ingest scanlator)
	// keeps them when the merge attaches under this same scanlation group.
	scanlator string
	// emptyFeed makes Chapters report NO chapters — the source name still
	// matches a disk provider, but the live row ingests an empty feed, so
	// merge-at-attach must fall back to the ordinary new-row path (no merge).
	emptyFeed bool
}

func (f *fakeNamedSourceClient) Sources(ctx context.Context) ([]sourceengine.Source, error) {
	return []sourceengine.Source{{ID: f.sourceID, Name: f.sourceName, Lang: "en"}}, nil
}

func (f *fakeNamedSourceClient) Chapters(ctx context.Context, sourceID int64, url string) ([]sourceengine.Chapter, error) {
	if f.emptyFeed {
		return nil, nil
	}
	return []sourceengine.Chapter{
		{URL: "/ch/1", Name: "Chapter 1", Number: 1, Scanlator: f.scanlator},
		{URL: "/ch/2", Name: "Chapter 2", Number: 2, Scanlator: f.scanlator},
	}, nil
}

// TestProviderNameMatches is the pure table test for the name-equality rule:
// case-insensitive, whitespace-trimmed, and blank-on-either-side never matches.
func TestProviderNameMatches(t *testing.T) {
	cases := []struct {
		name       string
		disk, live string
		want       bool
	}{
		{"exact", "mangadex", "mangadex", true},
		{"case-insensitive", "MangaDex", "mangadex", true},
		{"trims whitespace", "  mangadex  ", "mangadex", true},
		{"different names", "mangadex", "weebcentral", false},
		{"empty disk", "", "mangadex", false},
		{"empty live", "mangadex", "", false},
		{"both empty", "", "", false},
		{"whitespace-only live", "mangadex", "   ", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := library.ProviderNameMatches(tc.disk, tc.live); got != tc.want {
				t.Errorf("ProviderNameMatches(%q, %q) = %v, want %v", tc.disk, tc.live, got, tc.want)
			}
		})
	}
}

// TestAddProvider_MergesMatchingDiskProvider is the core merge-at-attach proof:
// a disk-imported series carries a disk-origin provider named "mangadex"
// (scanlator "Alpha"); attaching a live source whose resolved provider_name is
// also "mangadex" under the same scanlator must FOLD the disk row into the live
// one — leaving a single linked provider, both chapters re-pointed onto it at
// the requested importance, the disk row deleted, and ZERO upgrades flagged (no
// re-download).
func TestAddProvider_MergesMatchingDiskProvider(t *testing.T) {
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

	fake := &fakeNamedSourceClient{sourceID: 1, sourceName: "mangadex", scanlator: "Alpha"}
	ingestSvc := ingest.NewIngest(fake, client)
	seriesSvc := series.NewService(client, storage, 14)
	svc := library.NewService(client, ingestSvc, nil, seriesSvc, func() {}, storage, sse.NewHub())

	dto, err := svc.AddProvider(ctx, ser.ID, "1", "/manga/99", 5, "Alpha")
	if err != nil {
		t.Fatalf("AddProvider: %v", err)
	}
	if len(dto.Providers) != 1 {
		t.Fatalf("providers = %d, want 1 (disk row folded into the live source)", len(dto.Providers))
	}

	newSP := client.SeriesProvider.Query().Where(seriesprovider.SeriesID(ser.ID)).OnlyX(ctx)
	if newSP.Provider != "1" || newSP.Importance != 5 {
		t.Fatalf("merged provider = %+v, want provider=1 importance=5", newSP)
	}
	for _, key := range []string{"1", "2"} {
		assertChapterSatisfaction(t, client, ctx, ser.ID, key, &newSP.ID, 5)
	}
	assertNoUpgradesFlagged(t, ctx, client)
}

// TestAddProvider_NoNameMatchKeepsTwoRows proves a live source whose display
// name does NOT match the disk provider is attached as a SECOND, separate row
// (the ordinary AddProvider path) — no merge — and the disk chapters become
// upgrade candidates (the existing behaviour).
func TestAddProvider_NoNameMatchKeepsTwoRows(t *testing.T) {
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

	// Resolved display name "WeebCentral" != disk name "mangadex" → no merge.
	fake := &fakeNamedSourceClient{sourceID: 1, sourceName: "WeebCentral"}
	ingestSvc := ingest.NewIngest(fake, client)
	seriesSvc := series.NewService(client, storage, 14)
	svc := library.NewService(client, ingestSvc, nil, seriesSvc, func() {}, storage, sse.NewHub())

	dto, err := svc.AddProvider(ctx, ser.ID, "1", "/manga/99", 5, "Alpha")
	if err != nil {
		t.Fatalf("AddProvider: %v", err)
	}
	if len(dto.Providers) != 2 {
		t.Fatalf("providers = %d, want 2 (no merge: disk + weeb)", len(dto.Providers))
	}
	assertProviderCount(t, client, ctx, ser.ID, 2)
}

// TestAddProvider_ScanlatorMismatchNoMerge proves a name match under a
// DIFFERENT scanlator is not a merge: the disk row (scanlator "Alpha") and the
// live attach (scanlator "Beta") are distinct providers, so two rows remain.
func TestAddProvider_ScanlatorMismatchNoMerge(t *testing.T) {
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

	fake := &fakeNamedSourceClient{sourceID: 1, sourceName: "mangadex", scanlator: "Alpha"}
	ingestSvc := ingest.NewIngest(fake, client)
	seriesSvc := series.NewService(client, storage, 14)
	svc := library.NewService(client, ingestSvc, nil, seriesSvc, func() {}, storage, sse.NewHub())

	// Name matches but scanlator "Beta" != disk "Alpha" → no merge.
	dto, err := svc.AddProvider(ctx, ser.ID, "1", "/manga/99", 5, "Beta")
	if err != nil {
		t.Fatalf("AddProvider: %v", err)
	}
	if len(dto.Providers) != 2 {
		t.Fatalf("providers = %d, want 2 (scanlator mismatch: no merge)", len(dto.Providers))
	}
	assertProviderCount(t, client, ctx, ser.ID, 2)
}

// TestAddProvider_EmptyLiveProviderNameNoMerge proves that a live source whose
// provider_name could not be resolved (empty) is NEVER merged into a disk row,
// even under the same scanlator — an unknown name is not a wildcard. The base
// fake's nil Sources yields an empty provider_name.
func TestAddProvider_EmptyLiveProviderNameNoMerge(t *testing.T) {
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

	fake := newFakeClientWithFeed(t) // Sources() returns nil → provider_name ""
	ingestSvc := ingest.NewIngest(fake, client)
	seriesSvc := series.NewService(client, storage, 14)
	svc := library.NewService(client, ingestSvc, nil, seriesSvc, func() {}, storage, sse.NewHub())

	dto, err := svc.AddProvider(ctx, ser.ID, "1", "/manga/99", 5, "Alpha")
	if err != nil {
		t.Fatalf("AddProvider: %v", err)
	}
	if len(dto.Providers) != 2 {
		t.Fatalf("providers = %d, want 2 (empty provider_name: no merge)", len(dto.Providers))
	}
	assertProviderCount(t, client, ctx, ser.ID, 2)
}

// TestAddProvider_EmptyLiveFeedNoMerge is the FIX-1 guard: the attached live
// source's display name + scanlator MATCH the disk provider, but it ingested an
// EMPTY chapter feed (the source has no chapters for this scanlator). Merging
// would relabel nothing and delete the disk row, orphaning the downloaded
// chapters — so AddProvider must NOT merge: both rows remain and the disk
// chapters keep satisfied_by = the disk provider.
func TestAddProvider_EmptyLiveFeedNoMerge(t *testing.T) {
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
	diskSP := client.SeriesProvider.Query().Where(seriesprovider.SeriesID(ser.ID)).OnlyX(ctx)

	// Name matches ("mangadex") + same scanlator, but the source returns NO
	// chapters → empty live feed.
	fake := &fakeNamedSourceClient{sourceID: 1, sourceName: "mangadex", scanlator: "Alpha", emptyFeed: true}
	ingestSvc := ingest.NewIngest(fake, client)
	seriesSvc := series.NewService(client, storage, 14)
	svc := library.NewService(client, ingestSvc, nil, seriesSvc, func() {}, storage, sse.NewHub())

	dto, err := svc.AddProvider(ctx, ser.ID, "1", "/manga/99", 5, "Alpha")
	if err != nil {
		t.Fatalf("AddProvider: %v", err)
	}
	if len(dto.Providers) != 2 {
		t.Fatalf("providers = %d, want 2 (empty live feed: no merge)", len(dto.Providers))
	}
	assertProviderCount(t, client, ctx, ser.ID, 2)
	// Disk chapters must be untouched — still satisfied by the disk provider.
	for _, key := range []string{"1", "2"} {
		assertChapterSatisfaction(t, client, ctx, ser.ID, key, &diskSP.ID, 1)
	}
}

// assertProviderCount fails unless the series has exactly want SeriesProvider
// rows in the database (the authoritative check behind the DTO count).
func assertProviderCount(t *testing.T, client *ent.Client, ctx context.Context, seriesID uuid.UUID, want int) {
	t.Helper()
	got := client.SeriesProvider.Query().Where(seriesprovider.SeriesID(seriesID)).CountX(ctx)
	if got != want {
		t.Fatalf("SeriesProvider rows = %d, want %d", got, want)
	}
}

// setupDriftedSeries writes+imports a disk series ("mangadex"/"Alpha", 2 CBZs),
// then manually attaches a LINKED twin — a real source whose provider_name
// ("mangadex") + scanlator ("Alpha") match the disk row, i.e. the exact
// source-identity drift DedupProviders exists to clean up. withFeed controls
// whether the linked twin carries a ProviderChapter feed for keys "1"/"2"
// (needed for a merge; an empty feed must be SKIPPED). Returns the series and
// the linked twin.
func setupDriftedSeries(t *testing.T, client *ent.Client, storage string, importance int, withFeed bool) (*ent.Series, *ent.SeriesProvider) {
	t.Helper()
	ctx := context.Background()
	writeKaizokuSeries(t, storage, "Manga", "My Series", "mangadex", "Alpha", 2)
	facts, err := diskScanFirst(t, storage)
	if err != nil {
		t.Fatalf("diskScanFirst: %v", err)
	}
	importOneFromFacts(t, client, facts)
	ser := client.Series.Query().OnlyX(ctx)

	live := client.SeriesProvider.Create().
		SetSeriesID(ser.ID).
		SetProvider("weeb").
		SetProviderName("mangadex").
		SetScanlator("Alpha").
		SetSuwayomiID(99).
		SetImportance(importance).
		SaveX(ctx)
	if withFeed {
		one, two := 1.0, 2.0
		client.ProviderChapter.Create().SetSeriesProviderID(live.ID).SetChapterKey("1").SetNumber(one).SaveX(ctx)
		client.ProviderChapter.Create().SetSeriesProviderID(live.ID).SetChapterKey("2").SetNumber(two).SaveX(ctx)
	}
	return ser, live
}

// TestDedupProviders_MergesDriftedPair is the core cleanup proof: a series
// carrying a disk-origin provider AND its already-drifted linked twin (same
// name+scanlator, feed present) collapses to ONE provider — merged=1, skipped=0,
// both chapters re-pointed onto the linked source, the disk row gone, no
// upgrades flagged.
func TestDedupProviders_MergesDriftedPair(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()

	ser, live := setupDriftedSeries(t, client, storage, 5, true)
	svc := library.NewService(client, nil, nil, series.NewService(client, storage, 14), func() {}, storage, sse.NewHub())

	merged, skipped, err := svc.DedupProviders(ctx, ser.ID)
	if err != nil {
		t.Fatalf("DedupProviders: %v", err)
	}
	if merged != 1 || skipped != 0 {
		t.Fatalf("DedupProviders = (merged=%d, skipped=%d), want (1, 0)", merged, skipped)
	}
	assertProviderCount(t, client, ctx, ser.ID, 1)
	for _, key := range []string{"1", "2"} {
		assertChapterSatisfaction(t, client, ctx, ser.ID, key, &live.ID, 5)
	}
	assertNoUpgradesFlagged(t, ctx, client)
}

// TestDedupProviders_ScanlatorCaseInsensitiveMerge proves the FE↔BE parity fix:
// a disk row scanlator ("reset scans") and its linked twin's scanlator
// ("Reset Scans") differ only in case — scanlatorMatches must still recognise
// them as the same group and merge (merged=1, skipped=0), mirroring the
// frontend's already-case-insensitive norm() compare.
func TestDedupProviders_ScanlatorCaseInsensitiveMerge(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()

	writeKaizokuSeries(t, storage, "Manga", "My Series", "mangadex", "reset scans", 2)
	facts, err := diskScanFirst(t, storage)
	if err != nil {
		t.Fatalf("diskScanFirst: %v", err)
	}
	importOneFromFacts(t, client, facts)
	ser := client.Series.Query().OnlyX(ctx)

	live := client.SeriesProvider.Create().
		SetSeriesID(ser.ID).
		SetProvider("weeb").
		SetProviderName("mangadex").
		SetScanlator("Reset Scans").
		SetSuwayomiID(99).
		SetImportance(5).
		SaveX(ctx)
	one, two := 1.0, 2.0
	client.ProviderChapter.Create().SetSeriesProviderID(live.ID).SetChapterKey("1").SetNumber(one).SaveX(ctx)
	client.ProviderChapter.Create().SetSeriesProviderID(live.ID).SetChapterKey("2").SetNumber(two).SaveX(ctx)

	svc := library.NewService(client, nil, nil, series.NewService(client, storage, 14), func() {}, storage, sse.NewHub())

	merged, skipped, err := svc.DedupProviders(ctx, ser.ID)
	if err != nil {
		t.Fatalf("DedupProviders: %v", err)
	}
	if merged != 1 || skipped != 0 {
		t.Fatalf("DedupProviders = (merged=%d, skipped=%d), want (1, 0)", merged, skipped)
	}
	assertProviderCount(t, client, ctx, ser.ID, 1)
	for _, key := range []string{"1", "2"} {
		assertChapterSatisfaction(t, client, ctx, ser.ID, key, &live.ID, 5)
	}
}

// TestDedupProviders_SkipsEmptyFeedTwin proves the safety guard: when the
// drifted linked twin has NO ProviderChapter feed, the pair is SKIPPED (merging
// would relabel nothing then orphan the disk chapters) — merged=0, skipped=1,
// and BOTH providers remain untouched.
func TestDedupProviders_SkipsEmptyFeedTwin(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()

	ser, _ := setupDriftedSeries(t, client, storage, 5, false)
	svc := library.NewService(client, nil, nil, series.NewService(client, storage, 14), func() {}, storage, sse.NewHub())

	merged, skipped, err := svc.DedupProviders(ctx, ser.ID)
	if err != nil {
		t.Fatalf("DedupProviders: %v", err)
	}
	if merged != 0 || skipped != 1 {
		t.Fatalf("DedupProviders = (merged=%d, skipped=%d), want (0, 1)", merged, skipped)
	}
	assertProviderCount(t, client, ctx, ser.ID, 2)
}

// TestDedupProviders_NoPairsIsNoOp proves idempotence: a series with only a
// disk provider (no drifted twin) returns (0, 0) and changes nothing.
func TestDedupProviders_NoPairsIsNoOp(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()

	writeKaizokuSeries(t, storage, "Manga", "My Series", "mangadex", "Alpha", 2)
	facts, err := diskScanFirst(t, storage)
	if err != nil {
		t.Fatalf("diskScanFirst: %v", err)
	}
	importOneFromFacts(t, client, facts)
	ser := client.Series.Query().OnlyX(ctx)
	svc := library.NewService(client, nil, nil, series.NewService(client, storage, 14), func() {}, storage, sse.NewHub())

	merged, skipped, err := svc.DedupProviders(ctx, ser.ID)
	if err != nil {
		t.Fatalf("DedupProviders: %v", err)
	}
	if merged != 0 || skipped != 0 {
		t.Fatalf("DedupProviders = (merged=%d, skipped=%d), want (0, 0)", merged, skipped)
	}
	assertProviderCount(t, client, ctx, ser.ID, 1)
}

// TestDedupProviders_PrefersFeedBearingTwin is the FIX-3 proof: when a disk row
// matches TWO linked twins — one with an empty feed, one with a feed — dedup
// merges into the FEED-BEARING twin (merged=1, skipped=0) rather than skipping
// the disk row because it happened to see the empty twin first. Afterwards the
// disk row is gone, the fed twin carries the chapters, and the empty twin
// remains as a separate provider.
func TestDedupProviders_PrefersFeedBearingTwin(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()

	ser, fedTwin := setupDriftedSeries(t, client, storage, 5, true)

	// A SECOND matching linked twin (same name + scanlator) with NO feed.
	emptyTwin := client.SeriesProvider.Create().
		SetSeriesID(ser.ID).
		SetProvider("weeb2").
		SetProviderName("mangadex").
		SetScanlator("Alpha").
		SetSuwayomiID(100).
		SetImportance(3).
		SaveX(ctx)

	svc := library.NewService(client, nil, nil, series.NewService(client, storage, 14), func() {}, storage, sse.NewHub())

	merged, skipped, err := svc.DedupProviders(ctx, ser.ID)
	if err != nil {
		t.Fatalf("DedupProviders: %v", err)
	}
	if merged != 1 || skipped != 0 {
		t.Fatalf("DedupProviders = (merged=%d, skipped=%d), want (1, 0)", merged, skipped)
	}
	// Disk row folded into the FED twin; both linked twins remain.
	assertProviderCount(t, client, ctx, ser.ID, 2)
	for _, key := range []string{"1", "2"} {
		assertChapterSatisfaction(t, client, ctx, ser.ID, key, &fedTwin.ID, 5)
	}
	if n := client.SeriesProvider.Query().Where(seriesprovider.IDEQ(emptyTwin.ID)).CountX(ctx); n != 1 {
		t.Fatalf("empty twin count = %d, want 1 (untouched)", n)
	}
}

// TestDedupProviders_RestoresImportanceOnRelabelFailure is the FIX-2 proof: the
// live twin is DB-parked at importance 0 during the relabel window; if the
// relabel fails, its ORIGINAL importance (5) must be restored — never left at 0
// — and nothing else changes (disk provider still present, chapters still
// satisfied by disk). A corrupted CBZ forces the relabel failure.
func TestDedupProviders_RestoresImportanceOnRelabelFailure(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()

	ser, live := setupDriftedSeries(t, client, storage, 5, true)
	// The disk-origin provider stores the display NAME in the provider field
	// ("mangadex"); the live twin's provider is "weeb". (Query by name rather
	// than suwayomi_id=0: reconcile leaves suwayomi_id NULL, which a `= 0`
	// predicate would not match — Go reads that NULL as 0, but SQL does not.)
	diskSP := client.SeriesProvider.Query().
		Where(seriesprovider.SeriesID(ser.ID), seriesprovider.Provider("mangadex")).
		OnlyX(ctx)

	// Corrupt chapter 2's CBZ so RelabelChapterFile fails mid-merge.
	ch2 := client.Chapter.Query().Where(chapter.SeriesID(ser.ID), chapter.ChapterKey("2")).OnlyX(ctx)
	ch2Path := filepath.Join(storage, "Manga", "My Series", ch2.Filename)
	if err := os.WriteFile(ch2Path, []byte("not a zip file"), 0o600); err != nil {
		t.Fatalf("corrupt chapter 2: %v", err)
	}

	svc := library.NewService(client, nil, nil, series.NewService(client, storage, 14), func() {}, storage, sse.NewHub())

	merged, _, err := svc.DedupProviders(ctx, ser.ID)
	if err == nil {
		t.Fatal("DedupProviders: want an error from the corrupted CBZ, got nil")
	}
	if merged != 0 {
		t.Fatalf("merged = %d, want 0 (merge failed)", merged)
	}

	// The live twin must be restored to its ORIGINAL importance (5), not left
	// parked at 0.
	got := client.SeriesProvider.Query().Where(seriesprovider.IDEQ(live.ID)).OnlyX(ctx)
	if got.Importance != 5 {
		t.Fatalf("live twin importance = %d after failed merge, want 5 (restored)", got.Importance)
	}
	// Disk provider still present; chapters still satisfied by disk.
	if n := client.SeriesProvider.Query().Where(seriesprovider.IDEQ(diskSP.ID)).CountX(ctx); n != 1 {
		t.Fatalf("disk provider count = %d, want 1 (merge rolled back)", n)
	}
	assertChapterSatisfaction(t, client, ctx, ser.ID, "1", &diskSP.ID, 1)
}

// TestDedupProviders_UnknownSeries proves the guard: an unknown series id yields
// ErrSeriesNotFound.
func TestDedupProviders_UnknownSeries(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()

	svc := library.NewService(client, nil, nil, series.NewService(client, storage, 14), func() {}, storage, sse.NewHub())
	if _, _, err := svc.DedupProviders(ctx, uuid.New()); !errors.Is(err, library.ErrSeriesNotFound) {
		t.Fatalf("want ErrSeriesNotFound, got %v", err)
	}
}
