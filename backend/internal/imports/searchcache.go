package imports

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// searchCacheEntry is one memoized fan-out result plus the instant it was
// WRITTEN. Freshness is judged at READ time against the cache's CURRENT TTL
// (ttl(ctx)), not a precomputed expiry, so a runtime TTL change (jobs.search_
// cache_ttl) applies immediately to entries already stored (true hot reload).
type searchCacheEntry struct {
	groups  []SearchGroupDTO
	written time.Time
}

// searchCache is a concurrency-safe memo of Search results keyed by a stable
// (normalized query, sorted sources-set) key, so repeated identical searches
// within the TTL do ZERO upstream fan-out — the single heaviest anti-bot
// amplifier. A cached result reflects only the sources that RESPONDED at cache
// time (Search logs+skips per-source failures and returns partial results), so
// keep the TTL modest enough that a source recovering from a transient failure
// re-enters a later live search. The TTL is read PER-Get from a provider closure
// (jobs.search_cache_ttl) so an owner can retune or disable it live.
type searchCache struct {
	ttl func(context.Context) time.Duration
	now func() time.Time

	mu      sync.Mutex
	entries map[string]searchCacheEntry
}

// newSearchCache builds a searchCache whose entry lifetime is read PER-Get from
// ttl(ctx). A ttl(ctx) of 0 or less disables the cache (every Get fans out).
func newSearchCache(ttl func(context.Context) time.Duration) *searchCache {
	return &searchCache{
		ttl:     ttl,
		now:     time.Now,
		entries: make(map[string]searchCacheEntry),
	}
}

// searchCacheKey builds the stable cache key for a (query, sources) pair:
// the query trimmed + lowercased, joined with the SORTED source-id set (a nil/
// empty set — meaning "all enabled sources" — maps to a fixed sentinel). Sorting
// makes ["a","b"] and ["b","a"] the same key; case/space folding makes "Naruto "
// and "naruto" the same key.
func searchCacheKey(query string, sourceIDs []string) string {
	ids := append([]string(nil), sourceIDs...)
	sort.Strings(ids)
	set := "*" // all enabled sources
	if len(ids) > 0 {
		set = strings.Join(ids, ",")
	}
	return strings.ToLower(strings.TrimSpace(query)) + "\x00" + set
}

// Get returns the cached result for (query, sourceIDs) when a live entry exists,
// otherwise it calls fetch, stores the result, and returns it. Freshness is
// judged against the CURRENT ttl(ctx): an entry is live while now-written <=
// ttl(ctx). A ttl(ctx) of 0 or less disables caching — Get fans out every time
// and stores nothing. A fetch ERROR is never cached. fetch is invoked WITHOUT the
// lock held so a slow fan-out never blocks an unrelated cached lookup; two
// simultaneous misses for the same key may both fan out (rare for an
// owner-interactive search), which is race-clean.
func (c *searchCache) Get(ctx context.Context, query string, sourceIDs []string, fetch func() ([]SearchGroupDTO, error)) ([]SearchGroupDTO, error) {
	ttl := c.ttl(ctx)
	// A non-positive TTL disables the cache: always fan out, never store.
	if ttl <= 0 {
		return fetch()
	}
	key := searchCacheKey(query, sourceIDs)

	c.mu.Lock()
	if entry, ok := c.entries[key]; ok && c.now().Sub(entry.written) <= ttl {
		c.mu.Unlock()
		return entry.groups, nil
	}
	c.mu.Unlock()

	groups, err := fetch()
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.entries[key] = searchCacheEntry{groups: groups, written: c.now()}
	c.mu.Unlock()
	return groups, nil
}
