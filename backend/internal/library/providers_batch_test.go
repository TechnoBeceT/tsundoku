package library_test

import (
	"context"
	"errors"
	"strings"
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

// newAddProvidersSvc mirrors TestAddProvider_AttachesSourceAndFlagsUpgrade's
// fixture setup (writeKaizokuSeries + disk.ScanLibrary + disk.ReconcileOne via
// the diskScanFirst/importOneFromFacts helpers in provider_test.go): a single
// imported series whose only provider is the disk-origin one at importance 1.
func newAddProvidersSvc(t *testing.T) (*library.Service, *ent.Client, context.Context) {
	t.Helper()
	storage := t.TempDir()
	writeKaizokuSeries(t, storage, "Manga", "My Series", "mangadex", "Alpha", 2)
	client := testdb.New(t)
	ctx := context.Background()

	facts, err := diskScanFirst(t, storage)
	if err != nil {
		t.Fatalf("diskScanFirst: %v", err)
	}
	importOneFromFacts(t, client, facts)

	fake := newFakeClientWithFeed(t)
	ingest := suwayomi.NewIngest(fake, client)
	seriesSvc := series.NewService(client, storage, 14)
	svc := library.NewService(client, ingest, nil, seriesSvc, func() {}, storage, sse.NewHub())
	return svc, client, ctx
}

// TestAddProviders_AttachesBelowExisting attaches two sources to a series whose
// only provider is the disk provider (importance 1); the batch lands strictly
// below it in list order (-9, -19) per decision E, so no upgrade re-download
// fires. Asserted both via a direct Ent query and via the returned DTO.
func TestAddProviders_AttachesBelowExisting(t *testing.T) {
	svc, client, ctx := newAddProvidersSvc(t)
	ser := client.Series.Query().OnlyX(ctx)

	// Sanity: the disk-origin provider sits at importance 1 (see
	// disk.ReconcileOne's disk-origin importance convention).
	diskImportance := client.SeriesProvider.Query().OnlyX(ctx).Importance
	if diskImportance != 1 {
		t.Fatalf("fixture assumption broken: disk provider importance = %d, want 1", diskImportance)
	}

	refs := []library.ProviderRef{
		{Source: "weebA", MangaID: 91, Scanlator: ""},
		{Source: "weebB", MangaID: 92, Scanlator: ""},
	}
	dto, err := svc.AddProviders(ctx, ser.ID, refs)
	if err != nil {
		t.Fatalf("AddProviders: %v", err)
	}
	if len(dto.Providers) != 3 {
		t.Fatalf("providers = %d, want 3 (disk + weebA + weebB)", len(dto.Providers))
	}

	wantImportance := map[string]int{"weebA": -9, "weebB": -19}
	for provider, wantImp := range wantImportance {
		sp, err := client.SeriesProvider.Query().
			Where(seriesprovider.SeriesID(ser.ID), seriesprovider.Provider(provider), seriesprovider.Scanlator("")).
			Only(ctx)
		if err != nil {
			t.Fatalf("query %s: %v", provider, err)
		}
		if sp.Importance != wantImp {
			t.Errorf("%s importance = %d, want %d", provider, sp.Importance, wantImp)
		}
	}

	found := make(map[string]int, len(dto.Providers))
	for _, p := range dto.Providers {
		found[p.Provider] = p.Importance
	}
	if found["weebA"] != -9 {
		t.Errorf("DTO weebA importance = %d, want -9", found["weebA"])
	}
	if found["weebB"] != -19 {
		t.Errorf("DTO weebB importance = %d, want -19", found["weebB"])
	}
}

// TestAddProviders_EmptyRefs rejects an empty batch with ErrNoProviders.
func TestAddProviders_EmptyRefs(t *testing.T) {
	svc, client, ctx := newAddProvidersSvc(t)
	ser := client.Series.Query().OnlyX(ctx)

	if _, err := svc.AddProviders(ctx, ser.ID, nil); !errors.Is(err, library.ErrNoProviders) {
		t.Fatalf("want ErrNoProviders, got %v", err)
	}
}

// TestAddProviders_UnknownSeries returns the not-found sentinel for an id with
// no series row (exercises the len(existing)==0 → Series.Get guard).
func TestAddProviders_UnknownSeries(t *testing.T) {
	svc, _, ctx := newAddProvidersSvc(t)

	refs := []library.ProviderRef{{Source: "weebA", MangaID: 91, Scanlator: ""}}
	if _, err := svc.AddProviders(ctx, uuid.New(), refs); !errors.Is(err, library.ErrSeriesNotFound) {
		t.Fatalf("want ErrSeriesNotFound, got %v", err)
	}
}

// TestAddProviders_DuplicateReportsAttachedSoFar proves the partial-failure
// contract: the second ref duplicates the first, so the batch fails wrapping
// ErrProviderAlreadyPresent, names the attached-so-far source, and does NOT roll
// back the first (already-attached) source.
func TestAddProviders_DuplicateReportsAttachedSoFar(t *testing.T) {
	svc, client, ctx := newAddProvidersSvc(t)
	ser := client.Series.Query().OnlyX(ctx)

	refs := []library.ProviderRef{
		{Source: "weebA", MangaID: 91, Scanlator: ""},
		{Source: "weebA", MangaID: 91, Scanlator: ""}, // duplicates the first ref
	}
	_, err := svc.AddProviders(ctx, ser.ID, refs)
	if !errors.Is(err, library.ErrProviderAlreadyPresent) {
		t.Fatalf("want error wrapping ErrProviderAlreadyPresent, got %v", err)
	}
	if !strings.Contains(err.Error(), "weebA") {
		t.Errorf("error message %q does not name the attached-so-far source weebA", err.Error())
	}

	// The first ref's attach was NOT rolled back.
	exists := client.SeriesProvider.Query().
		Where(seriesprovider.SeriesID(ser.ID), seriesprovider.Provider("weebA"), seriesprovider.Scanlator("")).
		ExistX(ctx)
	if !exists {
		t.Error("weebA should remain attached after the batch's second ref fails")
	}
}
