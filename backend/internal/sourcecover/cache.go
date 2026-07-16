package sourcecover

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// DefaultDeadline bounds the WHOLE resolution of a cache MISS — waiting for a
// free concurrency slot AND the engine fetch itself, together — not just the
// fetch (see Cache.Get). This is the owner's hard no-hang rule: no cover
// request may hold a same-origin connection open longer than this, no matter
// how saturated the concurrency cap is. 6s is comfortably below anything a
// human reads as "hung" while still giving a cold, never-cached cover on a
// fast/open-CDN source a real chance to load.
const DefaultDeadline = 6 * time.Second

// DefaultConcurrency caps in-flight ENGINE fetches (cache MISSES only — a hit
// never touches this, see Cache.Get) so a burst of covers queues
// server-side in a bounded pool instead of each browser tab opening its own
// same-origin connection and all of them stacking up. This is the exact
// mechanism that hung the SPA: ~15 simultaneous live cover fetches saturated
// both the browser's ~6-per-host connection cap and the backend, and each
// slow Cloudflare rate-limit challenge held its connection for the duration.
const DefaultConcurrency = 4

// Engine is the narrow engine-host port Cache needs — exactly
// sourceengine.Client's own Image method (data, ext/content-type, err), so
// sourceengine.Client satisfies this interface directly with no adapter
// (structural typing; mirrors internal/series.CoverFetcher's identical
// narrow-port pattern, which keeps THAT domain free of internal/sourceengine
// too).
type Engine interface {
	Image(ctx context.Context, sourceID int64, pageURL, imageURL string) (data []byte, ext string, err error)
}

// ErrTimeout is returned by Cache.Get when a cache MISS could not be
// resolved within the deadline — either the bounded-concurrency slot never
// opened up, or the engine fetch itself did not finish in time. It is a
// distinct sentinel (never a bare context error) so an HTTP caller can map it
// to a deliberate fail-fast status (504) instead of a generic upstream 502.
var ErrTimeout = errors.New("sourcecover: fetch deadline exceeded")

// Cache is a disk-backed, cache-first, fail-fast fetcher for source-manga
// cover images. A HIT is served straight from disk with ZERO engine calls —
// the whole point of the cache (see the package doc: Suwayomi's dropped
// thumbnail cache is what stopped the original burst-refetch-on-every-render
// from ever happening). A MISS is bounded end-to-end by deadline and gated by
// a bounded concurrency pool — the safety net for a cover the cache cannot
// yet help with (never fetched before, or a disk write that previously
// failed).
type Cache struct {
	store    *Store
	engine   Engine
	slots    chan struct{}
	deadline time.Duration
}

// NewCache builds a Cache over store, fetching cache MISSES through engine.
// concurrency is clamped to at least 1 (a cache with zero fetch slots could
// never resolve a single miss, which would defeat the whole endpoint rather
// than merely bound its concurrency).
func NewCache(store *Store, engine Engine, concurrency int, deadline time.Duration) *Cache {
	if concurrency < 1 {
		concurrency = 1
	}
	return &Cache{
		store:    store,
		engine:   engine,
		slots:    make(chan struct{}, concurrency),
		deadline: deadline,
	}
}

// Get returns a cover's bytes and ext (the raw value engine.Image would
// return — see the Engine port) for (sourceID, url).
//
// A HIT reads straight from disk: zero engine calls, no deadline, no slot —
// a warm disk read is already fast enough that the fail-fast machinery below
// exists purely for the cold path.
//
// A MISS is where the hard no-hang guarantee lives: deadline is applied to
// the ENTIRE remainder of the call — first the wait for a free concurrency
// slot, THEN the engine fetch itself, as ONE continuous budget (not two
// separate timeouts) — so a request arriving when the pool is already full
// either gets a slot and finishes in time, or gives up and returns
// ErrTimeout, but never blocks past deadline holding the caller's connection
// open. This is deliberately stricter than "the fetch has a timeout": a
// request could otherwise wait indefinitely for a slot BEFORE the fetch's own
// timer even starts, which would reproduce the exact hang this cache exists
// to prevent.
func (c *Cache) Get(ctx context.Context, sourceID int64, url string) (data []byte, ext string, err error) {
	if cached, cachedExt, ok := c.store.Get(sourceID, url); ok {
		return cached, cachedExt, nil
	}

	ctx, cancel := context.WithTimeout(ctx, c.deadline)
	defer cancel()

	select {
	case c.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, "", ErrTimeout
	}
	defer func() { <-c.slots }()

	data, ext, err = c.engine.Image(ctx, sourceID, "", url)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, "", ErrTimeout
		}
		return nil, "", err
	}

	// Best-effort: the fetched bytes already answer this request regardless
	// of whether the cache write lands (mirrors series.fetchAndCacheCover's
	// identical "a cache that cannot persist must not break the page" stance
	// — see internal/series/cover.go).
	if putErr := c.store.Put(sourceID, url, data, ext); putErr != nil {
		slog.Warn("sourcecover: cache write failed", "source_id", sourceID, "url", url, "error", putErr)
	}
	return data, ext, nil
}
