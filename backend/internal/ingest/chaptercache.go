// Package ingest — interactive chapter-fetch cache.
//
// ChapterCache memoizes the RAW, unfiltered result of client.Chapters (the
// all-scanlators chapter list for one source-manga) so the interactive
// coverage→configure→adopt flow — SourceBreakdown, InspectChapters, and the
// Adopt ingest — hits an upstream source AT MOST ONCE per source-manga within
// the TTL, instead of re-triggering a live engine-host source fetch on every
// step. This is an anti-ban de-amplification: Chapters is a live source
// fetch, and the discovery layer used to fire it repeatedly for the same manga.
//
// The cache is INTERACTIVE-ONLY: the refresh discovery sweep deliberately does
// NOT read it (it fetches fresh via Ingest.FetchChaptersUncached), so this TTL
// can be long — and runtime-tunable — without ever staling-out new-chapter
// discovery. The TTL is read PER-Get from a provider closure, so a settings
// change (jobs.chapter_cache_ttl) applies to entries already in the map.
package ingest

import (
	"context"
	"sync"
	"time"

	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// chapterCacheKey identifies one cached fetch by its physical source id and the
// source-relative manga url — the engine host's own addressing pair. Both are
// kept in the key so a caller's (source, manga) intent is explicit and the key
// never collides across sources reusing the same URL shape.
type chapterCacheKey struct {
	sourceID int64
	url      string
}

// chapterCacheEntry is one memoized fetch plus the instant it was WRITTEN. The
// entry's freshness is judged at READ time against the cache's CURRENT TTL
// (ttl(ctx)), not a precomputed expiry — so a runtime TTL change (shorter or
// longer) applies immediately to entries already stored (true hot reload).
type chapterCacheEntry struct {
	chapters []sourceengine.Chapter
	written  time.Time
}

// ChapterCache is a concurrency-safe memo of client.Chapters keyed by
// (sourceID, url). Construct one with NewChapterCache and SHARE the instance
// across every interactive discovery/ingest path so they collapse onto a
// single upstream fetch. A zero (nil) *ChapterCache is not usable — always
// construct via NewChapterCache.
type ChapterCache struct {
	ttl func(context.Context) time.Duration
	now func() time.Time

	mu      sync.Mutex
	entries map[chapterCacheKey]chapterCacheEntry
}

// NewChapterCache builds a ChapterCache whose entry lifetime is read PER-Get from
// ttl(ctx) — production passes a settings-backed closure so an owner can retune
// jobs.chapter_cache_ttl live. A ttl(ctx) of 0 (or negative) DISABLES the cache
// entirely: every Get fetches upstream and stores nothing, so the owner can turn
// caching off at runtime. Tests wanting a fixed TTL use NewChapterCacheConst.
func NewChapterCache(ttl func(context.Context) time.Duration) *ChapterCache {
	return &ChapterCache{
		ttl:     ttl,
		now:     time.Now,
		entries: make(map[chapterCacheKey]chapterCacheEntry),
	}
}

// NewChapterCacheConst builds a ChapterCache with a FIXED ttl — a convenience for
// wiring/tests that need no runtime tuning. It wraps ttl in the constant closure
// NewChapterCache expects.
func NewChapterCacheConst(ttl time.Duration) *ChapterCache {
	return NewChapterCache(func(context.Context) time.Duration { return ttl })
}

// Get returns the cached chapter list for (sourceID, url) when a live (not
// yet expired) entry exists, otherwise it calls fetch, stores the result, and
// returns it. Freshness is judged against the CURRENT ttl(ctx): an entry is live
// while now-written <= ttl(ctx). A ttl(ctx) of 0 or less disables caching — Get
// fetches every time and stores nothing.
//
// A fetch ERROR is never cached — the next Get retries the source — so a
// transient upstream failure cannot wedge the manga out of the cache for a whole
// TTL. fetch is invoked WITHOUT the lock held so concurrent Gets for DIFFERENT
// keys never serialize on each other's upstream latency; two simultaneous misses
// for the SAME key may both fetch (the sequential coverage→adopt flow never does
// — grouping/ordering dedupes it), which is race-clean and merely forfeits the
// dedup for that one rare overlap.
func (c *ChapterCache) Get(ctx context.Context, sourceID int64, url string, fetch func() ([]sourceengine.Chapter, error)) ([]sourceengine.Chapter, error) {
	ttl := c.ttl(ctx)
	// A non-positive TTL disables the cache: always fetch, never store.
	if ttl <= 0 {
		return fetch()
	}
	key := chapterCacheKey{sourceID: sourceID, url: url}

	c.mu.Lock()
	if entry, ok := c.entries[key]; ok && c.now().Sub(entry.written) <= ttl {
		c.mu.Unlock()
		return entry.chapters, nil
	}
	c.mu.Unlock()

	chapters, err := fetch()
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.entries[key] = chapterCacheEntry{chapters: chapters, written: c.now()}
	c.mu.Unlock()
	return chapters, nil
}
