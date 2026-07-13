package metadata_test

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/technobecet/tsundoku/internal/metadata"
)

// fakeProvider is a minimal, fully-configurable metadata.Provider double
// for exercising Registry's fan-out logic without any network access.
type fakeProvider struct {
	key      string
	id       int
	priority int

	searchResults []metadata.SearchResult
	searchErr     error
	searchCalls   int32 // atomic; incremented by every Search call, proving (or disproving) a fan-out reached this provider

	matchResult *metadata.SearchResult
	matchErr    error

	// metas is keyed by remoteID so GetSeriesMetadata can return the right
	// record for whatever RemoteID Match reported.
	metas   map[string]metadata.SeriesMetadata
	metaErr error
}

var _ metadata.Provider = (*fakeProvider)(nil)

func (f *fakeProvider) Key() string   { return f.key }
func (f *fakeProvider) ID() int       { return f.id }
func (f *fakeProvider) Priority() int { return f.priority }

func (f *fakeProvider) Search(_ context.Context, _ string, _ int) ([]metadata.SearchResult, error) {
	atomic.AddInt32(&f.searchCalls, 1)
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.searchResults, nil
}

func (f *fakeProvider) GetSeriesMetadata(_ context.Context, remoteID string) (metadata.SeriesMetadata, error) {
	if f.metaErr != nil {
		return metadata.SeriesMetadata{}, f.metaErr
	}
	m, ok := f.metas[remoteID]
	if !ok {
		return metadata.SeriesMetadata{}, errors.New("fakeProvider: no metadata for remote id " + remoteID)
	}
	return m, nil
}

func (f *fakeProvider) GetSeriesCover(_ context.Context, _ string) ([]byte, string, error) {
	return nil, "", errors.New("fakeProvider: GetSeriesCover not implemented")
}

func (f *fakeProvider) Match(_ context.Context, _ metadata.MatchQuery) (*metadata.SearchResult, error) {
	if f.matchErr != nil {
		return nil, f.matchErr
	}
	return f.matchResult, nil
}

// --- Search ---

func TestRegistry_Search_MergesAcrossProvidersAndSkipsErroringOnes(t *testing.T) {
	p1 := &fakeProvider{key: "p1", priority: 0, searchResults: []metadata.SearchResult{
		{Provider: "p1", RemoteID: "1", Title: "One"},
	}}
	p2 := &fakeProvider{key: "p2", priority: 1, searchErr: errors.New("upstream boom")}
	p3 := &fakeProvider{key: "p3", priority: 2, searchResults: []metadata.SearchResult{
		{Provider: "p3", RemoteID: "3", Title: "Three"},
	}}

	reg := metadata.NewRegistry(p1, p2, p3)

	got, err := reg.Search(context.Background(), "query", nil)
	if err != nil {
		t.Fatalf("Search returned an error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 results (p2 skipped on error), got %d: %+v", len(got), got)
	}
	// Deterministic registry order: p1's hit first, then p3's (p2 contributes nothing).
	if got[0].Provider != "p1" || got[1].Provider != "p3" {
		t.Fatalf("want results ordered [p1, p3], got %+v", got)
	}
}

func TestRegistry_Search_KeysSubsetOnlyQueriesSelectedProviders(t *testing.T) {
	p1 := &fakeProvider{key: "p1", searchResults: []metadata.SearchResult{{Provider: "p1", RemoteID: "1"}}}
	p2 := &fakeProvider{key: "p2", searchResults: []metadata.SearchResult{{Provider: "p2", RemoteID: "2"}}}
	p3 := &fakeProvider{key: "p3", searchResults: []metadata.SearchResult{{Provider: "p3", RemoteID: "3"}}}

	reg := metadata.NewRegistry(p1, p2, p3)

	got, err := reg.Search(context.Background(), "query", []string{"p2"})
	if err != nil {
		t.Fatalf("Search returned an error: %v", err)
	}
	if len(got) != 1 || got[0].Provider != "p2" {
		t.Fatalf("want only p2's result, got %+v", got)
	}
	if atomic.LoadInt32(&p1.searchCalls) != 0 {
		t.Errorf("p1 was queried despite being outside the keys filter")
	}
	if atomic.LoadInt32(&p3.searchCalls) != 0 {
		t.Errorf("p3 was queried despite being outside the keys filter")
	}
	if atomic.LoadInt32(&p2.searchCalls) != 1 {
		t.Errorf("p2.searchCalls = %d, want 1", p2.searchCalls)
	}
}

// --- Identify ---

func TestRegistry_Identify_MergesMatchedProvidersAndExcludesMisses(t *testing.T) {
	// p0 is registered first (lowest priority number) but never matches —
	// its priority number must NOT leak into Order once it misses.
	p0 := &fakeProvider{key: "p0", priority: 0, matchResult: nil}
	p1 := &fakeProvider{
		key: "p1", priority: 1,
		matchResult: &metadata.SearchResult{Provider: "p1", RemoteID: "r1", URL: "https://p1.example/r1", Title: "Match One", Year: 2020},
		metas: map[string]metadata.SeriesMetadata{
			"r1": {Title: "Primary Title", Description: "from p1", Year: 2020},
		},
	}
	p2 := &fakeProvider{
		key: "p2", priority: 2,
		matchResult: &metadata.SearchResult{Provider: "p2", RemoteID: "r2", URL: "https://p2.example/r2", Title: "Match Two", CoverURL: "https://p2.example/cover.jpg"},
		metas: map[string]metadata.SeriesMetadata{
			"r2": {Title: "Secondary Title", Description: "from p2", Genres: []string{"Action"}},
		},
	}

	reg := metadata.NewRegistry(p0, p1, p2)

	got, err := reg.Identify(context.Background(), metadata.MatchQuery{Title: "Match"}, nil)
	if err != nil {
		t.Fatalf("Identify returned an error: %v", err)
	}

	// Order excludes the missed p0 and keeps the matched two in registry
	// (ascending priority) order — p1 (priority 1) is the lowest-priority-
	// number MATCHED provider, so it must be primary.
	t.Run("order", func(t *testing.T) { assertIdentifyOrder(t, got, []string{"p1", "p2"}) })

	// Scalar gap-fill: Title/Description come from the primary (p1); Genres
	// (collection field) unions in p2's contribution.
	t.Run("merged", func(t *testing.T) { assertMergedFromP1AndP2(t, got.Merged) })

	t.Run("matches", func(t *testing.T) { assertMatchesFromP1AndP2(t, got.Matches) })
}

// assertIdentifyOrder is a standalone helper (not a closure) so its own
// branches are counted toward ITS complexity budget, not the calling
// test's — keeping the caller's cyclomatic complexity low even as
// assertions grow.
func assertIdentifyOrder(t *testing.T, got metadata.IdentifyResult, want []string) {
	t.Helper()
	if !slices.Equal(got.Order, want) {
		t.Fatalf("Order = %v, want %v", got.Order, want)
	}
}

func assertMergedFromP1AndP2(t *testing.T, merged metadata.SeriesMetadata) {
	t.Helper()
	if merged.Title != "Primary Title" {
		t.Errorf("Merged.Title = %q, want primary's title", merged.Title)
	}
	if merged.Description != "from p1" {
		t.Errorf("Merged.Description = %q, want primary's description", merged.Description)
	}
	if len(merged.Genres) != 1 || merged.Genres[0] != "Action" {
		t.Errorf("Merged.Genres = %v, want [Action] contributed by p2", merged.Genres)
	}
}

func assertMatchesFromP1AndP2(t *testing.T, matches []metadata.ProviderMatch) {
	t.Helper()
	if len(matches) != 2 {
		t.Fatalf("want 2 Matches, got %d: %+v", len(matches), matches)
	}
	m1 := matches[0]
	if m1.ProviderKey != "p1" || m1.RemoteID != "r1" || m1.RemoteURL != "https://p1.example/r1" || m1.Year != 2020 {
		t.Errorf("Matches[0] = %+v, want p1's remote id/url/year carried through", m1)
	}
	m2 := matches[1]
	if m2.ProviderKey != "p2" || m2.RemoteID != "r2" || m2.RemoteURL != "https://p2.example/r2" || m2.CoverURL != "https://p2.example/cover.jpg" {
		t.Errorf("Matches[1] = %+v, want p2's remote id/url/cover carried through", m2)
	}
}

func TestRegistry_Identify_ProviderMatchOrGetMetadataErrorsAreSkipped(t *testing.T) {
	matchFails := &fakeProvider{key: "match-fails", priority: 0, matchErr: errors.New("match boom")}
	metaFails := &fakeProvider{
		key: "meta-fails", priority: 1,
		matchResult: &metadata.SearchResult{Provider: "meta-fails", RemoteID: "x"},
		metaErr:     errors.New("metadata fetch boom"),
	}
	ok := &fakeProvider{
		key: "ok", priority: 2,
		matchResult: &metadata.SearchResult{Provider: "ok", RemoteID: "y"},
		metas:       map[string]metadata.SeriesMetadata{"y": {Title: "Only Survivor"}},
	}

	reg := metadata.NewRegistry(matchFails, metaFails, ok)

	got, err := reg.Identify(context.Background(), metadata.MatchQuery{Title: "q"}, nil)
	if err != nil {
		t.Fatalf("Identify returned an error: %v", err)
	}
	if want := []string{"ok"}; !slices.Equal(got.Order, want) {
		t.Fatalf("Order = %v, want %v (both failing providers skipped)", got.Order, want)
	}
	if got.Merged.Title != "Only Survivor" {
		t.Errorf("Merged.Title = %q, want %q", got.Merged.Title, "Only Survivor")
	}
}

func TestRegistry_Identify_NoMatchesReturnsEmptyResultAndNilError(t *testing.T) {
	p1 := &fakeProvider{key: "p1"}
	p2 := &fakeProvider{key: "p2"}

	reg := metadata.NewRegistry(p1, p2)

	got, err := reg.Identify(context.Background(), metadata.MatchQuery{Title: "nothing matches"}, nil)
	if err != nil {
		t.Fatalf("Identify returned an error: %v", err)
	}
	// SeriesMetadata carries slice fields, so IdentifyResult isn't
	// comparable with == — assert the zero-value shape field by field.
	if got.Merged.Title != "" || len(got.Matches) != 0 || len(got.Order) != 0 {
		t.Fatalf("want a zero-value IdentifyResult, got %+v", got)
	}
}

// --- Provider lookup ---

func TestRegistry_Provider_LookupHitAndMiss(t *testing.T) {
	p1 := &fakeProvider{key: "p1"}
	p2 := &fakeProvider{key: "p2"}
	reg := metadata.NewRegistry(p1, p2)

	got, ok := reg.Provider("p2")
	if !ok || got != p2 {
		t.Fatalf("Provider(%q) = (%v, %v), want (p2, true)", "p2", got, ok)
	}

	_, ok = reg.Provider("nonexistent")
	if ok {
		t.Fatalf("Provider(%q) reported found, want miss", "nonexistent")
	}
}

// --- empty registry ---

func TestRegistry_EmptyRegistryIsSafe(t *testing.T) {
	// The low-level NewRegistry with zero providers must be usable, not a
	// panic trap — Search/Identify against it degrade to "nothing to query",
	// which is a representable, non-error outcome for both.
	reg := metadata.NewRegistry()

	if _, ok := reg.Provider("anything"); ok {
		t.Errorf("Provider on an empty registry reported a hit")
	}

	results, err := reg.Search(context.Background(), "q", nil)
	if err != nil {
		t.Fatalf("Search on an empty registry returned an error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("Search on an empty registry returned %d results, want 0", len(results))
	}

	got, err := reg.Identify(context.Background(), metadata.MatchQuery{Title: "q"}, nil)
	if err != nil {
		t.Fatalf("Identify on an empty registry returned an error: %v", err)
	}
	if got.Merged.Title != "" || len(got.Matches) != 0 || len(got.Order) != 0 {
		t.Errorf("Identify on an empty registry returned %+v, want zero value", got)
	}
}
