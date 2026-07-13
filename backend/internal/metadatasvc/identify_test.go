package metadatasvc_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/metadata"
	"github.com/technobecet/tsundoku/internal/metadatasvc"
)

// TestIdentify_PickedProviderIsAlwaysPrimary is the anchor-then-aggregate
// proof: the owner picks mangadex/5 even though anilist would win on
// registry priority alone. mangadex must land as metadata_source (Order[0]
// equivalent), and anilist's OWN matched fields must still fold in as
// gap-fill/union (aggregate), never override the picked primary's scalars.
func TestIdentify_PickedProviderIsAlwaysPrimary(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	id := seedSeries(ctx, t, db, "Chainsaw Man", "chainsaw-man")

	mangadex := &fakeProvider{
		key: "mangadex", priority: 1,
		metas: map[string]metadata.SeriesMetadata{
			"5": {
				Title:       "Chainsaw Man",
				Description: "Denji becomes Chainsaw Man.",
				Genres:      []string{"Action"},
			},
		},
		// resolvePrimaryURL's Search-by-title lookup for the picked provider.
		searchResults: []metadata.SearchResult{
			{Provider: "mangadex", RemoteID: "5", URL: "https://mangadex.org/title/5"},
		},
	}
	anilist := &fakeProvider{
		key: "anilist", priority: 0, // lower number = higher registry priority than mangadex
		matchResult: &metadata.SearchResult{Provider: "anilist", RemoteID: "9"},
		metas: map[string]metadata.SeriesMetadata{
			"9": {
				Title:  "Chainsaw Man (should not override)",
				Genres: []string{"Fantasy"}, // unions with mangadex's "Action"
				Tags:   []string{"Devil"},
			},
		},
	}
	registry := metadata.NewRegistry(anilist, mangadex)
	svc := metadatasvc.NewService(db, registry, storage)

	if err := svc.Identify(ctx, id, "mangadex", "5"); err != nil {
		t.Fatalf("Identify: %v", err)
	}

	row := db.Series.GetX(ctx, id)
	assertPickedProviderIsPrimary(t, row)
}

// assertPickedProviderIsPrimary is a standalone helper (not a closure) so
// its own branches count toward ITS complexity budget, not the calling
// test's.
func assertPickedProviderIsPrimary(t *testing.T, row *ent.Series) {
	t.Helper()
	if row.MetadataSource == nil || row.MetadataSource.Ref != "mangadex" || row.MetadataSource.RemoteID != "5" {
		t.Fatalf("MetadataSource = %+v, want the picked provider (mangadex/5) as primary", row.MetadataSource)
	}
	if row.MetadataSource.RemoteURL != "https://mangadex.org/title/5" {
		t.Errorf("MetadataSource.RemoteURL = %q, want the resolved mangadex URL", row.MetadataSource.RemoteURL)
	}
	assertPickedProviderMergedFields(t, row)
}

func assertPickedProviderMergedFields(t *testing.T, row *ent.Series) {
	t.Helper()
	// Scalar gap-fill: the picked primary's title wins over anilist's, even
	// though anilist has the numerically lower priority.
	if row.Description != "Denji becomes Chainsaw Man." {
		t.Errorf("Description = %q, want the picked primary's description", row.Description)
	}
	// Collection union: anilist's aggregate contribution still lands.
	if len(row.Genres) != 2 || row.Genres[0] != "Action" || row.Genres[1] != "Fantasy" {
		t.Errorf("Genres = %v, want [Action Fantasy] (primary + aggregated anilist)", row.Genres)
	}
	if len(row.Tags) != 1 || row.Tags[0] != "Devil" {
		t.Errorf("Tags = %v, want [Devil] contributed by the aggregated anilist match", row.Tags)
	}
}

// TestIdentify_UnknownProviderReturnsErrProviderNotFound confirms the
// sentinel for a providerKey the registry doesn't hold.
func TestIdentify_UnknownProviderReturnsErrProviderNotFound(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	id := seedSeries(ctx, t, db, "Some Series", "some-series")
	registry := metadata.NewRegistry(&fakeProvider{key: "anilist"})
	svc := metadatasvc.NewService(db, registry, storage)

	err := svc.Identify(ctx, id, "does-not-exist", "1")
	if !errors.Is(err, metadatasvc.ErrProviderNotFound) {
		t.Fatalf("Identify(unknown provider) error = %v, want ErrProviderNotFound", err)
	}
}

// TestIdentify_UnknownSeriesReturnsErrSeriesNotFound confirms the sentinel
// for a series id the registered provider is otherwise happy to serve.
func TestIdentify_UnknownSeriesReturnsErrSeriesNotFound(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	provider := &fakeProvider{
		key: "anilist",
		metas: map[string]metadata.SeriesMetadata{
			"1": {Title: "Anything"},
		},
	}
	registry := metadata.NewRegistry(provider)
	svc := metadatasvc.NewService(db, registry, storage)

	err := svc.Identify(ctx, randomUUID(), "anilist", "1")
	if !errors.Is(err, metadatasvc.ErrSeriesNotFound) {
		t.Fatalf("Identify(unknown series) error = %v, want ErrSeriesNotFound", err)
	}
}

// TestIdentify_SingleProviderRegisteredFetchesPrimaryExactlyOnce guards the
// otherProviderKeys empty-slice footgun documented on that helper: with only
// ONE provider registered (the picked one), Identify must NOT pass an
// accidentally-empty keys slice through to Registry.Identify, which treats
// empty as "every provider" and would re-Match + re-fetch the primary a
// SECOND time (a wasted GetSeriesMetadata self-call).
//
// The guard is an anti-redundant-FETCH optimization, not a data-correctness
// gate — a double-included primary is INVISIBLE in merged output because
// metadata.Merge dedups collections. So this asserts the FETCH COUNT, not the
// merged data. The provider's matchResult points at ITSELF, so WITHOUT the
// guard (service.go `if len(otherKeys) > 0` → `if true`) the "all providers"
// path WOULD match and re-fetch remote id "1" a second time.
//
// BIDIRECTIONAL CHECK PERFORMED: temporarily flipping the guard to `if true`
// makes this FAIL (metaCalls == 2); the real guard makes it PASS
// (metaCalls == 1). Verified 2026-07-13.
func TestIdentify_SingleProviderRegisteredFetchesPrimaryExactlyOnce(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	id := seedSeries(ctx, t, db, "Solo Provider Series", "solo-provider-series")

	provider := &fakeProvider{
		key: "anilist",
		// matchResult points at the provider's OWN record: without the guard,
		// the empty-keys "all providers" path re-matches + re-fetches "1".
		matchResult: &metadata.SearchResult{Provider: "anilist", RemoteID: "1"},
		metas: map[string]metadata.SeriesMetadata{
			"1": {Title: "Solo Provider Series", Genres: []string{"Action"}},
		},
	}
	registry := metadata.NewRegistry(provider)
	svc := metadatasvc.NewService(db, registry, storage)

	if err := svc.Identify(ctx, id, "anilist", "1"); err != nil {
		t.Fatalf("Identify: %v", err)
	}

	// The anchor fetch is the ONLY fetch: the guard skips the redundant
	// re-fetch that the "all providers" path would otherwise trigger.
	if got := atomic.LoadInt32(&provider.metaCalls); got != 1 {
		t.Fatalf("provider GetSeriesMetadata calls = %d, want exactly 1 (guard must skip the redundant self-refetch)", got)
	}

	// Data correctness is a bonus assertion, NOT the discriminator (Merge
	// dedups, so it would pass even with a double-include).
	row := db.Series.GetX(ctx, id)
	if len(row.Genres) != 1 || row.Genres[0] != "Action" {
		t.Fatalf("Genres = %v, want exactly [Action]", row.Genres)
	}
}
