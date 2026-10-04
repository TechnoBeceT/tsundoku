package imports

import (
	"context"
	"errors"
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
	flights map[string]*searchFlight
	active  int
	callers int
}

// newSearchCache builds a searchCache whose entry lifetime is read PER-Get from
// ttl(ctx). A ttl(ctx) of 0 or less disables memoization; current shared demand is still coalesced.
func newSearchCache(ttl func(context.Context) time.Duration) *searchCache {
	return &searchCache{
		ttl:     ttl,
		now:     time.Now,
		entries: make(map[string]searchCacheEntry),
		flights: make(map[string]*searchFlight),
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

// searchFlight retains only the latest accumulated snapshot. Each caller writes its own
// response, so a stalled consumer cannot hold up fanout or another consumer.
type searchFlight struct {
	cancel      context.CancelFunc
	subscribers map[chan struct{}]struct{}
	latest      SearchSnapshotDTO
	version     uint64
	finished    bool
	err         error
	written     time.Time
}

var errSearchCapacity = errors.New("search in-flight capacity exhausted")

// Get shares current demand even when memoization is disabled. No operation outlives
// its last caller's demand, except while cancelled upstream code physically returns.
func (c *searchCache) Get(ctx context.Context, query string, sourceIDs []string,
	fetch func(context.Context, func(SearchSnapshotDTO) error) ([]SearchGroupDTO, error),
	emit func(SearchSnapshotDTO) error,
) ([]SearchGroupDTO, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := searchCacheKey(query, sourceIDs)
	wake := make(chan struct{}, 1)
	entry, flight, err := c.subscribe(ctx, key, wake, fetch)
	if err != nil {
		return nil, err
	}
	if flight == nil {
		if err := emitSearchSnapshot(emit, SearchSnapshotDTO{Groups: entry.groups, PendingSources: []SourceDTO{}, Done: true}); err != nil {
			return nil, err
		}
		return entry.groups, ctx.Err()
	}
	defer c.unsubscribe(key, flight, wake)
	return c.await(ctx, key, flight, wake, emit)
}

func (c *searchCache) subscribe(ctx context.Context, key string, wake chan struct{},
	fetch func(context.Context, func(SearchSnapshotDTO) error) ([]SearchGroupDTO, error),
) (searchCacheEntry, *searchFlight, error) {
	ttl := c.ttl(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.entries[key]; ttl > 0 && ok && c.now().Sub(entry.written) <= ttl {
		return entry, nil, nil
	}
	if c.callers >= 1024 {
		return searchCacheEntry{}, nil, errSearchCapacity
	}
	flight := c.flights[key]
	// Finished producers retain their existing consumers, but cannot accept new demand.
	if flight == nil || flight.finished {
		if c.active >= 128 {
			return searchCacheEntry{}, nil, errSearchCapacity
		}
		workCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		flight = &searchFlight{cancel: cancel, subscribers: make(map[chan struct{}]struct{})}
		c.flights[key] = flight
		c.active++
		go c.fetch(workCtx, flight, fetch)
	}
	flight.subscribers[wake] = struct{}{}
	c.callers++
	return searchCacheEntry{}, flight, nil
}

func (c *searchCache) unsubscribe(key string, flight *searchFlight, wake chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(flight.subscribers, wake)
	c.callers--
	if len(flight.subscribers) == 0 {
		flight.cancel()
		if c.flights[key] == flight {
			delete(c.flights, key)
		}
	}
}

func (c *searchCache) await(ctx context.Context, key string, flight *searchFlight, wake chan struct{}, emit func(SearchSnapshotDTO) error) ([]SearchGroupDTO, error) {
	var seen uint64
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		c.mu.Lock()
		snapshot, version, finished, err := flight.latest, flight.version, flight.finished, flight.err
		c.mu.Unlock()
		// The producer's terminal snapshot is delivered only after its result succeeds.
		if version != seen && (!snapshot.Done || finished) && err == nil {
			if err := emitSearchSnapshot(emit, snapshot); err != nil {
				return nil, err
			}
			seen = version
		}
		if finished {
			return c.finish(ctx, key, flight, snapshot.Groups, err)
		}

		if err := waitSearchWake(ctx, wake); err != nil {
			return nil, err
		}

	}
}

func waitSearchWake(ctx context.Context, wake <-chan struct{}) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-wake:
		return nil
	}
}

func (c *searchCache) finish(ctx context.Context, key string, flight *searchFlight, groups []SearchGroupDTO, err error) ([]SearchGroupDTO, error) {
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.store(ctx, key, flight, groups)
	return groups, nil
}

func (c *searchCache) store(ctx context.Context, key string, flight *searchFlight, groups []SearchGroupDTO) {
	ttl := c.ttl(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	// A delayed terminal consumer must never replace a newer same-key producer's memo.
	if ttl > 0 && c.flights[key] == flight {
		c.entries[key] = searchCacheEntry{groups: groups, written: flight.written}
	}
}

func (c *searchCache) fetch(ctx context.Context, flight *searchFlight,
	fetch func(context.Context, func(SearchSnapshotDTO) error) ([]SearchGroupDTO, error),
) {
	publish := func(snapshot SearchSnapshotDTO) error {
		c.mu.Lock()
		flight.latest = snapshot
		flight.version++
		for wake := range flight.subscribers {
			select {
			case wake <- struct{}{}:
			default:
			}
		}
		c.mu.Unlock()
		return ctx.Err()
	}
	groups, err := fetch(ctx, publish)
	if err == nil {
		err = ctx.Err()
	}
	c.mu.Lock()
	flight.finished = true
	flight.err = err
	flight.written = c.now()
	if err == nil {
		flight.latest = SearchSnapshotDTO{Groups: groups, PendingSources: []SourceDTO{}, Done: true}
		flight.version++
	}
	c.active--
	for wake := range flight.subscribers {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	c.mu.Unlock()
}
