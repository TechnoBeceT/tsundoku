package library_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/library"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sse"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// fakeNamedSourceClient reuses fakeAddProviderClient's full Client surface but
// overrides Sources so suwayomi.Ingest.resolveProviderName resolves a specific
// display name for the attached source id — the ONLY signal that lets
// merge-at-attach recognise a live source as the same physical source as a disk
// import (whose provider field holds that same display name). sourceName "" =
// unresolved, so the base fake's nil Sources already covers the empty-name case.
type fakeNamedSourceClient struct {
	fakeAddProviderClient
	sourceID   string
	sourceName string
	// scanlator tags the chapters FetchChapters reports, so suwayomi.Ingest's
	// scanlator filter (which drops chapters not matching the ingest scanlator)
	// keeps them when the merge attaches under this same scanlation group.
	scanlator string
}

func (f *fakeNamedSourceClient) Sources(ctx context.Context) ([]suwayomi.Source, error) {
	return []suwayomi.Source{{ID: f.sourceID, Name: f.sourceName, Lang: "en"}}, nil
}

func (f *fakeNamedSourceClient) FetchChapters(ctx context.Context, mangaID int) ([]suwayomi.Chapter, error) {
	one, two := 1.0, 2.0
	return []suwayomi.Chapter{
		{ID: 101, Index: 0, Name: "Chapter 1", Number: &one, Scanlator: f.scanlator},
		{ID: 102, Index: 1, Name: "Chapter 2", Number: &two, Scanlator: f.scanlator},
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

	fake := &fakeNamedSourceClient{sourceID: "weeb", sourceName: "mangadex", scanlator: "Alpha"}
	ingest := suwayomi.NewIngest(fake, client)
	seriesSvc := series.NewService(client, storage, 14)
	svc := library.NewService(client, ingest, nil, seriesSvc, func() {}, storage, sse.NewHub())

	dto, err := svc.AddProvider(ctx, ser.ID, "weeb", 99, 5, "Alpha")
	if err != nil {
		t.Fatalf("AddProvider: %v", err)
	}
	if len(dto.Providers) != 1 {
		t.Fatalf("providers = %d, want 1 (disk row folded into the live source)", len(dto.Providers))
	}

	newSP := client.SeriesProvider.Query().Where(seriesprovider.SeriesID(ser.ID)).OnlyX(ctx)
	if newSP.Provider != "weeb" || newSP.SuwayomiID != 99 || newSP.Importance != 5 {
		t.Fatalf("merged provider = %+v, want provider=weeb suwayomi_id=99 importance=5", newSP)
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
	fake := &fakeNamedSourceClient{sourceID: "weeb", sourceName: "WeebCentral"}
	ingest := suwayomi.NewIngest(fake, client)
	seriesSvc := series.NewService(client, storage, 14)
	svc := library.NewService(client, ingest, nil, seriesSvc, func() {}, storage, sse.NewHub())

	dto, err := svc.AddProvider(ctx, ser.ID, "weeb", 99, 5, "Alpha")
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

	fake := &fakeNamedSourceClient{sourceID: "weeb", sourceName: "mangadex", scanlator: "Alpha"}
	ingest := suwayomi.NewIngest(fake, client)
	seriesSvc := series.NewService(client, storage, 14)
	svc := library.NewService(client, ingest, nil, seriesSvc, func() {}, storage, sse.NewHub())

	// Name matches but scanlator "Beta" != disk "Alpha" → no merge.
	dto, err := svc.AddProvider(ctx, ser.ID, "weeb", 99, 5, "Beta")
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
	ingest := suwayomi.NewIngest(fake, client)
	seriesSvc := series.NewService(client, storage, 14)
	svc := library.NewService(client, ingest, nil, seriesSvc, func() {}, storage, sse.NewHub())

	dto, err := svc.AddProvider(ctx, ser.ID, "weeb", 99, 5, "Alpha")
	if err != nil {
		t.Fatalf("AddProvider: %v", err)
	}
	if len(dto.Providers) != 2 {
		t.Fatalf("providers = %d, want 2 (empty provider_name: no merge)", len(dto.Providers))
	}
	assertProviderCount(t, client, ctx, ser.ID, 2)
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
