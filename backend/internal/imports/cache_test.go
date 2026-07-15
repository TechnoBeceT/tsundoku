// Package imports_test — Search cache (Task C1) + discovery chapter cache (C2).
//
// These paths are read-only client calls (no DB), so the tests use the in-memory
// fakeClient (service_test.go) wrapped in a counting client — no testdb needed.
package imports_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/imports"
	"github.com/technobecet/tsundoku/internal/ingest"
	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// constTTL wraps a fixed duration in the per-Get TTL-provider closure the search
// cache expects.
func constTTL(d time.Duration) func(context.Context) time.Duration {
	return func(context.Context) time.Duration { return d }
}

// countingClient wraps a fakeClient and counts Search (per source) and
// Chapters (per manga url) calls so a test can prove a cache hit did ZERO
// upstream work.
type countingClient struct {
	*fakeClient
	mu          sync.Mutex
	searchCalls map[int64]int
	fetchCalls  map[string]int
}

func newCountingClient(fc *fakeClient) *countingClient {
	return &countingClient{fakeClient: fc, searchCalls: map[int64]int{}, fetchCalls: map[string]int{}}
}

func (c *countingClient) Search(ctx context.Context, sourceID int64, query string, page int) (sourceengine.SearchResult, error) {
	c.mu.Lock()
	c.searchCalls[sourceID]++
	c.mu.Unlock()
	return c.fakeClient.Search(ctx, sourceID, query, page)
}

func (c *countingClient) Chapters(ctx context.Context, sourceID int64, url string) ([]sourceengine.Chapter, error) {
	c.mu.Lock()
	c.fetchCalls[url]++
	c.mu.Unlock()
	return c.fakeClient.Chapters(ctx, sourceID, url)
}

func (c *countingClient) searchCount(sourceID int64) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.searchCalls[sourceID]
}

func (c *countingClient) fetchCount(url string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fetchCalls[url]
}

// twoSourceClient builds a counting client over two sources each returning one
// result — the fan-out surface for the search-cache tests.
func twoSourceClient() *countingClient {
	return newCountingClient(&fakeClient{
		sources: []sourceengine.Source{{ID: 1, Name: "S1"}, {ID: 2, Name: "S2"}},
		searchResults: map[int64]sourceengine.SearchResult{
			1: {Manga: []sourceengine.MangaEntry{{Title: "Alpha"}}},
			2: {Manga: []sourceengine.MangaEntry{{Title: "Beta"}}},
		},
	})
}

// TestSearch_CacheHitSkipsFanout proves Task C1: two identical searches within the
// TTL fan out to each source exactly once.
func TestSearch_CacheHitSkipsFanout(t *testing.T) {
	ctx := context.Background()
	cc := twoSourceClient()
	svc := imports.NewService(cc, nil, nil, "", testSearchTimeout, nil)
	imports.SetSearchCacheForTest(svc, constTTL(2*time.Minute), time.Now)

	if _, err := svc.Search(ctx, "naruto", nil); err != nil {
		t.Fatalf("first Search: %v", err)
	}
	if _, err := svc.Search(ctx, "naruto", nil); err != nil {
		t.Fatalf("second Search: %v", err)
	}
	if a, b := cc.searchCount(1), cc.searchCount(2); a != 1 || b != 1 {
		t.Fatalf("Search fan-out counts s1=%d s2=%d, want 1/1 (2nd search must hit cache)", a, b)
	}
}

// TestSearch_CancelledParentNotCached proves that a search whose PARENT request
// context is cancelled (client disconnected mid-fan-out) returns an error and is
// NOT written to the cache — so a truncated/empty result can never poison the
// shared searchCache and be served to unrelated later callers for the TTL. A
// subsequent live search for the same key must re-fan-out (a cache miss).
func TestSearch_CancelledParentNotCached(t *testing.T) {
	cc := twoSourceClient()
	svc := imports.NewService(cc, nil, nil, "", testSearchTimeout, nil)
	imports.SetSearchCacheForTest(svc, constTTL(2*time.Minute), time.Now)

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel() // parent gone before the fan-out completes

	if _, err := svc.Search(cancelledCtx, "naruto", nil); err == nil {
		t.Fatal("Search with a cancelled parent ctx: want error, got nil (must not silently cache a truncated result)")
	}

	// The cancelled fan-out may have raced a source or two before dropping, so
	// snapshot the counts and require the NEXT live search to add exactly one
	// fan-out per source — proving it was a cache MISS (nothing was poisoned).
	before1, before2 := cc.searchCount(1), cc.searchCount(2)
	if _, err := svc.Search(context.Background(), "naruto", nil); err != nil {
		t.Fatalf("live Search after cancelled one: %v", err)
	}
	if a, b := cc.searchCount(1), cc.searchCount(2); a != before1+1 || b != before2+1 {
		t.Fatalf("live search fanned out s1 +%d s2 +%d, want +1/+1 (cancelled search must not have cached)", a-before1, b-before2)
	}
}

// TestSearch_CacheKeyNormalisation proves whitespace/case folding + source-set
// order do not change the key: "  Naruto " and "naruto" with reversed source
// lists are ONE cache entry.
func TestSearch_CacheKeyNormalisation(t *testing.T) {
	ctx := context.Background()
	cc := twoSourceClient()
	svc := imports.NewService(cc, nil, nil, "", testSearchTimeout, nil)
	imports.SetSearchCacheForTest(svc, constTTL(2*time.Minute), time.Now)

	if _, err := svc.Search(ctx, "naruto", []string{"1", "2"}); err != nil {
		t.Fatalf("first Search: %v", err)
	}
	if _, err := svc.Search(ctx, "  Naruto ", []string{"2", "1"}); err != nil {
		t.Fatalf("second Search: %v", err)
	}
	if a := cc.searchCount(1); a != 1 {
		t.Fatalf("s1 searched %d times, want 1 (normalised key must match)", a)
	}
}

// TestSearch_CacheMissOnDifferentKey proves a different query OR a different
// source-set is a cache MISS (fresh fan-out).
func TestSearch_CacheMissOnDifferentKey(t *testing.T) {
	ctx := context.Background()
	cc := twoSourceClient()
	svc := imports.NewService(cc, nil, nil, "", testSearchTimeout, nil)
	imports.SetSearchCacheForTest(svc, constTTL(2*time.Minute), time.Now)

	// All sources for "a", then a narrowed source-set for "a": different key ⇒ s1
	// is fanned out again.
	if _, err := svc.Search(ctx, "a", nil); err != nil {
		t.Fatalf("Search all: %v", err)
	}
	if _, err := svc.Search(ctx, "a", []string{"1"}); err != nil {
		t.Fatalf("Search subset: %v", err)
	}
	if a := cc.searchCount(1); a != 2 {
		t.Fatalf("s1 searched %d times, want 2 (different source-set is a miss)", a)
	}
	// A different query is also a miss.
	if _, err := svc.Search(ctx, "b", nil); err != nil {
		t.Fatalf("Search b: %v", err)
	}
	if a := cc.searchCount(1); a != 3 {
		t.Fatalf("s1 searched %d times, want 3 (different query is a miss)", a)
	}
}

// TestSearch_CacheExpiryRefetches proves an expired search entry refetches
// (deterministic via an injected clock).
func TestSearch_CacheExpiryRefetches(t *testing.T) {
	ctx := context.Background()
	cc := twoSourceClient()
	svc := imports.NewService(cc, nil, nil, "", testSearchTimeout, nil)

	now := time.Unix(0, 0)
	imports.SetSearchCacheForTest(svc, constTTL(time.Minute), func() time.Time { return now })

	if _, err := svc.Search(ctx, "q", nil); err != nil {
		t.Fatalf("first Search: %v", err)
	}
	now = now.Add(61 * time.Second) // past the TTL
	if _, err := svc.Search(ctx, "q", nil); err != nil {
		t.Fatalf("second Search: %v", err)
	}
	if a := cc.searchCount(1); a != 2 {
		t.Fatalf("s1 searched %d times, want 2 (expired entry must refetch)", a)
	}
}

// TestSearch_ZeroTTLDisablesCache proves a search-cache TTL provider returning 0
// disables the memo: every identical Search fans out again (an owner can turn the
// search cache off at runtime).
func TestSearch_ZeroTTLDisablesCache(t *testing.T) {
	ctx := context.Background()
	cc := twoSourceClient()
	svc := imports.NewService(cc, nil, nil, "", testSearchTimeout, nil)
	imports.SetSearchCacheForTest(svc, constTTL(0), time.Now)

	if _, err := svc.Search(ctx, "naruto", nil); err != nil {
		t.Fatalf("first Search: %v", err)
	}
	if _, err := svc.Search(ctx, "naruto", nil); err != nil {
		t.Fatalf("second Search: %v", err)
	}
	if a := cc.searchCount(1); a != 2 {
		t.Fatalf("s1 searched %d times, want 2 (0 TTL disables the cache)", a)
	}
}

// TestSearch_TTLHotReload proves the search-cache TTL is read PER-Get: an entry
// live under a long TTL becomes stale the moment the provider's value shrinks
// below the entry's age, without the clock moving.
func TestSearch_TTLHotReload(t *testing.T) {
	ctx := context.Background()
	cc := twoSourceClient()
	svc := imports.NewService(cc, nil, nil, "", testSearchTimeout, nil)

	now := time.Unix(0, 0)
	ttl := time.Hour
	imports.SetSearchCacheForTest(svc, func(context.Context) time.Duration { return ttl }, func() time.Time { return now })

	if _, err := svc.Search(ctx, "q", nil); err != nil {
		t.Fatalf("first Search: %v", err)
	}
	now = now.Add(30 * time.Minute) // still < 1h TTL ⇒ a hit
	if _, err := svc.Search(ctx, "q", nil); err != nil {
		t.Fatalf("second Search: %v", err)
	}
	if a := cc.searchCount(1); a != 1 {
		t.Fatalf("s1 searched %d times, want 1 (30m < 1h TTL is a hit)", a)
	}
	// Shrink the TTL below the entry's 30m age WITHOUT moving the clock ⇒ the next
	// identical search is now a miss.
	ttl = 10 * time.Minute
	if _, err := svc.Search(ctx, "q", nil); err != nil {
		t.Fatalf("third Search: %v", err)
	}
	if a := cc.searchCount(1); a != 2 {
		t.Fatalf("s1 searched %d times, want 2 (shrunk TTL must expire the entry)", a)
	}
}

// TestDiscovery_ChapterCacheSharedAcrossPaths proves Task C2 at the imports layer:
// SourceBreakdown and InspectChapters for the same (source, manga) share ONE
// cached Chapters call — the coverage→inspect part of the coverage→adopt flow
// fetches upstream once.
func TestDiscovery_ChapterCacheSharedAcrossPaths(t *testing.T) {
	ctx := context.Background()
	const url = "/manga/9"
	cc := newCountingClient(&fakeClient{
		sources: []sourceengine.Source{{ID: 1, Name: "S1"}},
		chaptersByURL: map[string][]sourceengine.Chapter{
			url: {{URL: "/ch/1", Number: 1, Scanlator: "X"}},
		},
	})
	svc := imports.NewService(cc, nil, nil, "", testSearchTimeout, nil)
	imports.SetChapterCacheForTest(svc, ingest.NewChapterCacheConst(time.Minute))

	if _, err := svc.SourceBreakdown(ctx, "1", url); err != nil {
		t.Fatalf("SourceBreakdown 1: %v", err)
	}
	if _, err := svc.SourceBreakdown(ctx, "1", url); err != nil {
		t.Fatalf("SourceBreakdown 2: %v", err)
	}
	if _, err := svc.InspectChapters(ctx, "1", url); err != nil {
		t.Fatalf("InspectChapters: %v", err)
	}
	if got := cc.fetchCount(url); got != 1 {
		t.Fatalf("Chapters called %d times for %q, want 1 (coverage+inspect share cache)", got, url)
	}
}
