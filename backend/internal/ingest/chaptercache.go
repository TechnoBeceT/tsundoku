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

// chapterCacheKey identifies one cached fetch by its physical source id, the
// exact source-owned manga address, and the manga title used for the fetch —
// the engine host's own addressing pair PLUS the recognition input. The address
// may be relative, opaque, or absolute cross-origin. mangaTitle is
// part of the key (not just an argument to fetch) because the engine host's
// chapter-number recognition and name-sanitize post-processing (see
// sourceengine.Client.Chapters's doc comment) are TITLE-SENSITIVE: the exact
// same (sourceID, url) fetched with a different mangaTitle can legitimately
// return DIFFERENT chapter Number/Name values (the title-strip step). Without
// mangaTitle in the key, a title=""  discovery preview (SourceBreakdown/
// InspectChapters) and a real-title adopt fetch (ingest.fetchForAdopt) would
// collide onto the SAME entry — whichever ran first would poison the other's
// result for the whole TTL, silently defeating the title-strip recognition
// for the path a human is actually watching. See the P2 chapter-fidelity
// review's "mangaTitle threading is defeated by the shared cache" finding.
type chapterCacheKey struct {
	sourceID    int64
	url         string
	mangaTitle  string
	addressMode sourceengine.AddressMode
	webURL      string
}

// chapterCacheEntry is one memoized fetch plus the instant it was WRITTEN. The
// entry's freshness is judged at READ time against the cache's CURRENT TTL
// (ttl(ctx)), not a precomputed expiry — so a runtime TTL change (shorter or
// longer) applies immediately to entries already stored (true hot reload).
type chapterCacheEntry struct {
	result  sourceengine.ChaptersResult
	written time.Time
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

// Get returns the cached chapter list for (sourceID, url, mangaTitle) when a
// live (not yet expired) entry exists, otherwise it calls fetch, stores the
// result, and returns it. Freshness is judged against the CURRENT ttl(ctx): an
// entry is live while now-written <= ttl(ctx). A ttl(ctx) of 0 or less disables
// caching — Get fetches every time and stores nothing.
//
// mangaTitle is part of the cache key (see chapterCacheKey's doc comment) — a
// "" preview and a real-title adopt for the SAME (sourceID, url) are DISTINCT
// entries, never one poisoning the other's recognition result.
//
// A fetch ERROR is never cached — the next Get retries the source — so a
// transient upstream failure cannot wedge the manga out of the cache for a whole
// TTL. fetch is invoked WITHOUT the lock held so concurrent Gets for DIFFERENT
// keys never serialize on each other's upstream latency; two simultaneous misses
// for the SAME key may both fetch (the sequential coverage→adopt flow never does
// — grouping/ordering dedupes it), which is race-clean and merely forfeits the
// dedup for that one rare overlap.
func (c *ChapterCache) Get(ctx context.Context, sourceID int64, url string, mangaTitle string, fetch func() ([]sourceengine.Chapter, error)) ([]sourceengine.Chapter, error) {
	result, err := c.GetRef(ctx, sourceengine.ProviderRef{SourceID: sourceID, URL: url}, mangaTitle, func() (sourceengine.ChaptersResult, error) {
		chapters, fetchErr := fetch()
		return sourceengine.ChaptersResult{Chapters: chapters}, fetchErr
	})
	return result.Chapters, err
}

// GetRef is the address-aware cache entry point. Mode and web URL are part of
// the key so an unresolved preview cannot hide a later resolved observation.
func (c *ChapterCache) GetRef(ctx context.Context, ref sourceengine.ProviderRef, mangaTitle string, fetch func() (sourceengine.ChaptersResult, error)) (sourceengine.ChaptersResult, error) {
	ttl := c.ttl(ctx)
	// A non-positive TTL disables the cache: always fetch, never store.
	if ttl <= 0 {
		return fetch()
	}
	key := chapterCacheKey{sourceID: ref.SourceID, url: ref.URL, mangaTitle: mangaTitle, addressMode: ref.AddressMode, webURL: ref.WebURL}

	c.mu.Lock()
	if entry, ok := c.entries[key]; ok && c.now().Sub(entry.written) <= ttl {
		c.mu.Unlock()
		return entry.result, nil
	}
	c.mu.Unlock()

	result, err := fetch()
	if err != nil {
		return sourceengine.ChaptersResult{}, err
	}

	c.mu.Lock()
	c.entries[key] = chapterCacheEntry{result: result, written: c.now()}
	c.mu.Unlock()
	return result, nil
}
