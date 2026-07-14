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

// TestIdentifyMerge_UnionsCollectionsAndAnchorsPrimary is the multi-select
// merge proof: TWO owner-picked providers merge via metadata.Merge exactly
// like the anchor-then-aggregate Identify flow — collections UNION, scalars
// gap-fill with selection[0] (mangadex) winning, metadata_source records the
// primary, and metadata_locked is set true (hand-curation).
func TestIdentifyMerge_UnionsCollectionsAndAnchorsPrimary(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	id := seedSeries(ctx, t, db, "Chainsaw Man", "chainsaw-man")

	mangadex := &fakeProvider{
		key: "mangadex",
		metas: map[string]metadata.SeriesMetadata{
			"5": {
				Title:       "Chainsaw Man",
				Description: "Denji becomes Chainsaw Man.",
				Genres:      []string{"Action"},
				Tags:        []string{"Devil Hunter"},
			},
		},
		searchResults: []metadata.SearchResult{
			{Provider: "mangadex", RemoteID: "5", URL: "https://mangadex.org/title/5"},
		},
	}
	anilist := &fakeProvider{
		key: "anilist",
		metas: map[string]metadata.SeriesMetadata{
			"9": {
				Title:  "Chainsaw Man (should not override)",
				Genres: []string{"Fantasy"}, // unions with mangadex's "Action"
				Tags:   []string{"Devil"},   // unions with mangadex's "Devil Hunter"
			},
		},
	}
	registry := metadata.NewRegistry(mangadex, anilist)
	svc := metadatasvc.NewService(db, registry, storage)

	err := svc.IdentifyMerge(ctx, id, []metadatasvc.Selection{
		{Provider: "mangadex", RemoteID: "5"},
		{Provider: "anilist", RemoteID: "9"},
	})
	if err != nil {
		t.Fatalf("IdentifyMerge: %v", err)
	}

	row := db.Series.GetX(ctx, id)
	if row.Description != "Denji becomes Chainsaw Man." {
		t.Errorf("Description = %q, want selection[0]'s (mangadex) description", row.Description)
	}
	if len(row.Genres) != 2 || row.Genres[0] != "Action" || row.Genres[1] != "Fantasy" {
		t.Errorf("Genres = %v, want [Action Fantasy] (union, mangadex first)", row.Genres)
	}
	if len(row.Tags) != 2 || row.Tags[0] != "Devil Hunter" || row.Tags[1] != "Devil" {
		t.Errorf("Tags = %v, want [Devil Hunter Devil] (union, mangadex first)", row.Tags)
	}
	assertIdentifyMergePrimaryAndLock(t, row)
}

func assertIdentifyMergePrimaryAndLock(t *testing.T, row *ent.Series) {
	t.Helper()
	if row.MetadataSource == nil || row.MetadataSource.Ref != "mangadex" || row.MetadataSource.RemoteID != "5" {
		t.Fatalf("MetadataSource = %+v, want selection[0] (mangadex/5) as primary", row.MetadataSource)
	}
	if row.MetadataSource.RemoteURL != "https://mangadex.org/title/5" {
		t.Errorf("MetadataSource.RemoteURL = %q, want the resolved mangadex URL", row.MetadataSource.RemoteURL)
	}
	if !row.MetadataLocked {
		t.Error("MetadataLocked = false, want true — IdentifyMerge is owner hand-curation")
	}
}

// TestIdentifyMerge_OneProviderFailsSkipsAndContinues proves a per-provider
// fetch failure is non-fatal: the surviving selection still merges, and (since
// selection[0] is the one that failed) the FIRST SUCCESSFUL selection becomes
// the effective primary.
func TestIdentifyMerge_OneProviderFailsSkipsAndContinues(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	id := seedSeries(ctx, t, db, "One Punch Man", "one-punch-man")

	failing := &fakeProvider{key: "mangadex", metaErr: errors.New("upstream 500")}
	surviving := &fakeProvider{
		key: "anilist",
		metas: map[string]metadata.SeriesMetadata{
			"7": {Title: "One Punch Man", Genres: []string{"Comedy"}},
		},
	}
	registry := metadata.NewRegistry(failing, surviving)
	svc := metadatasvc.NewService(db, registry, storage)

	err := svc.IdentifyMerge(ctx, id, []metadatasvc.Selection{
		{Provider: "mangadex", RemoteID: "1"}, // fails
		{Provider: "anilist", RemoteID: "7"},  // succeeds
	})
	if err != nil {
		t.Fatalf("IdentifyMerge with one failing provider: %v", err)
	}

	row := db.Series.GetX(ctx, id)
	if len(row.Genres) != 1 || row.Genres[0] != "Comedy" {
		t.Errorf("Genres = %v, want [Comedy] from the surviving provider only", row.Genres)
	}
	if row.MetadataSource == nil || row.MetadataSource.Ref != "anilist" {
		t.Fatalf("MetadataSource = %+v, want the surviving provider (anilist) as the effective primary", row.MetadataSource)
	}
	if !row.MetadataLocked {
		t.Error("MetadataLocked = false, want true even on a partial merge")
	}
}

// TestIdentifyMerge_UnknownProviderInSelectionsIsSkipped mirrors the
// fetch-failure test but for a provider key the registry does not hold at
// all — the same non-fatal skip-and-continue rule applies.
func TestIdentifyMerge_UnknownProviderInSelectionsIsSkipped(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	id := seedSeries(ctx, t, db, "Vinland Saga", "vinland-saga")

	known := &fakeProvider{
		key:   "anilist",
		metas: map[string]metadata.SeriesMetadata{"3": {Title: "Vinland Saga", Status: "ongoing"}},
	}
	registry := metadata.NewRegistry(known)
	svc := metadatasvc.NewService(db, registry, storage)

	err := svc.IdentifyMerge(ctx, id, []metadatasvc.Selection{
		{Provider: "does-not-exist", RemoteID: "1"},
		{Provider: "anilist", RemoteID: "3"},
	})
	if err != nil {
		t.Fatalf("IdentifyMerge with an unknown provider selection: %v", err)
	}

	row := db.Series.GetX(ctx, id)
	if row.Status != "ongoing" {
		t.Errorf("Status = %q, want %q from the known provider", row.Status, "ongoing")
	}
}

// TestIdentifyMerge_AllProvidersFailReturnsErrAllSelectionsFailed proves the
// discriminator: when EVERY selection fails (unknown key or fetch error),
// nothing is merged/persisted and the sentinel names the failure.
func TestIdentifyMerge_AllProvidersFailReturnsErrAllSelectionsFailed(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	id := seedSeries(ctx, t, db, "Berserk", "berserk")

	failing := &fakeProvider{key: "mangadex", metaErr: errors.New("timeout")}
	registry := metadata.NewRegistry(failing)
	svc := metadatasvc.NewService(db, registry, storage)

	err := svc.IdentifyMerge(ctx, id, []metadatasvc.Selection{
		{Provider: "does-not-exist", RemoteID: "1"},
		{Provider: "mangadex", RemoteID: "2"},
	})
	if !errors.Is(err, metadatasvc.ErrAllSelectionsFailed) {
		t.Fatalf("IdentifyMerge(all fail) error = %v, want ErrAllSelectionsFailed", err)
	}

	// Nothing was persisted — the series stays at its zero-value metadata.
	row := db.Series.GetX(ctx, id)
	if row.MetadataSource != nil || row.MetadataLocked {
		t.Fatalf("series was mutated despite every provider failing: MetadataSource=%+v MetadataLocked=%v",
			row.MetadataSource, row.MetadataLocked)
	}
}

// TestIdentifyMerge_NoSelectionsReturnsErrNoSelections confirms the guard
// against an empty selections slice (the HTTP layer also validates this, but
// a direct caller gets the same fail-closed behavior).
func TestIdentifyMerge_NoSelectionsReturnsErrNoSelections(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	id := seedSeries(ctx, t, db, "Empty Selections", "empty-selections")
	registry := metadata.NewRegistry(&fakeProvider{key: "anilist"})
	svc := metadatasvc.NewService(db, registry, storage)

	err := svc.IdentifyMerge(ctx, id, nil)
	if !errors.Is(err, metadatasvc.ErrNoSelections) {
		t.Fatalf("IdentifyMerge(nil selections) error = %v, want ErrNoSelections", err)
	}
}

// TestIdentifyMerge_UnknownSeriesReturnsErrSeriesNotFound confirms the
// sentinel for a series id that matches no row.
func TestIdentifyMerge_UnknownSeriesReturnsErrSeriesNotFound(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	provider := &fakeProvider{key: "anilist", metas: map[string]metadata.SeriesMetadata{"1": {Title: "X"}}}
	registry := metadata.NewRegistry(provider)
	svc := metadatasvc.NewService(db, registry, storage)

	err := svc.IdentifyMerge(ctx, randomUUID(), []metadatasvc.Selection{{Provider: "anilist", RemoteID: "1"}})
	if !errors.Is(err, metadatasvc.ErrSeriesNotFound) {
		t.Fatalf("IdentifyMerge(unknown series) error = %v, want ErrSeriesNotFound", err)
	}
}

// TestIdentifyMerge_DoesNotFetchNonSelectedProviders proves the multi-select
// merge is EXACTLY the owner's picks — unlike Identify's own anchor-then-
// aggregate, a registered-but-not-selected provider is never queried at all.
func TestIdentifyMerge_DoesNotFetchNonSelectedProviders(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	id := seedSeries(ctx, t, db, "Attack on Titan", "attack-on-titan")

	picked := &fakeProvider{
		key:   "anilist",
		metas: map[string]metadata.SeriesMetadata{"1": {Title: "Attack on Titan"}},
	}
	notPicked := &fakeProvider{key: "mangadex"}
	registry := metadata.NewRegistry(picked, notPicked)
	svc := metadatasvc.NewService(db, registry, storage)

	if err := svc.IdentifyMerge(ctx, id, []metadatasvc.Selection{{Provider: "anilist", RemoteID: "1"}}); err != nil {
		t.Fatalf("IdentifyMerge: %v", err)
	}

	if got := atomic.LoadInt32(&notPicked.metaCalls); got != 0 {
		t.Fatalf("notPicked.GetSeriesMetadata was called %d times, want 0 — multi-select never auto-aggregates", got)
	}
	if got := atomic.LoadInt32(&notPicked.matchCalls); got != 0 {
		t.Fatalf("notPicked.Match was called %d times, want 0 — multi-select never auto-matches", got)
	}
}
