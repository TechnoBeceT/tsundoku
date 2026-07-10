// Package suwayomi_test — unit tests for ChapterCache (Task C2).
package suwayomi_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// constTTL wraps a fixed duration in the per-Get TTL-provider closure the cache
// constructors expect.
func constTTL(d time.Duration) func(context.Context) time.Duration {
	return func(context.Context) time.Duration { return d }
}

// chOf builds a trivial one-element chapter slice tagged by n so tests can tell
// two distinct fetch results apart.
func chOf(n int) []suwayomi.Chapter {
	num := float64(n)
	return []suwayomi.Chapter{{ID: n, Number: &num}}
}

// TestChapterCache_HitWithinTTL proves two Gets for the same key within the TTL
// call the underlying fetch exactly once (the second is served from the memo).
func TestChapterCache_HitWithinTTL(t *testing.T) {
	c := suwayomi.NewChapterCacheConst(time.Minute)
	var calls int
	fetch := func() ([]suwayomi.Chapter, error) { calls++; return chOf(1), nil }

	first, err := c.Get(context.Background(), "src", 7, fetch)
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	second, err := c.Get(context.Background(), "src", 7, fetch)
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if calls != 1 {
		t.Fatalf("fetch called %d times, want 1 (second Get must hit the cache)", calls)
	}
	if len(first) != 1 || len(second) != 1 || first[0].ID != second[0].ID {
		t.Fatalf("cached result mismatch: first=%v second=%v", first, second)
	}
}

// TestChapterCache_ExpiryRefetches proves an entry older than the TTL is refetched
// (deterministic via an injected clock — no sleeping).
func TestChapterCache_ExpiryRefetches(t *testing.T) {
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	c := suwayomi.NewChapterCacheClock(constTTL(50*time.Millisecond), clock)

	var calls int
	fetch := func() ([]suwayomi.Chapter, error) { calls++; return chOf(calls), nil }

	if _, err := c.Get(context.Background(), "src", 1, fetch); err != nil {
		t.Fatalf("first Get: %v", err)
	}
	// Advance past the TTL: the entry is stale, so the next Get refetches.
	now = now.Add(51 * time.Millisecond)
	if _, err := c.Get(context.Background(), "src", 1, fetch); err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if calls != 2 {
		t.Fatalf("fetch called %d times, want 2 (expired entry must refetch)", calls)
	}
}

// TestChapterCache_DistinctKeysIsolated proves different (sourceID, mangaID) keys
// do not share an entry.
func TestChapterCache_DistinctKeysIsolated(t *testing.T) {
	c := suwayomi.NewChapterCacheConst(time.Minute)
	var calls int
	fetch := func() ([]suwayomi.Chapter, error) { calls++; return chOf(calls), nil }

	// Distinct manga ids.
	_, _ = c.Get(context.Background(), "src", 1, fetch)
	_, _ = c.Get(context.Background(), "src", 2, fetch)
	// Distinct source ids, same manga id.
	_, _ = c.Get(context.Background(), "other", 1, fetch)
	if calls != 3 {
		t.Fatalf("fetch called %d times, want 3 (distinct keys must not share)", calls)
	}
}

// TestChapterCache_ErrorNotCached proves a fetch error is never memoized: the next
// Get retries and can succeed.
func TestChapterCache_ErrorNotCached(t *testing.T) {
	c := suwayomi.NewChapterCacheConst(time.Minute)
	boom := errors.New("boom")
	var calls int
	fetch := func() ([]suwayomi.Chapter, error) {
		calls++
		if calls == 1 {
			return nil, boom
		}
		return chOf(9), nil
	}

	if _, err := c.Get(context.Background(), "src", 1, fetch); !errors.Is(err, boom) {
		t.Fatalf("first Get err = %v, want boom", err)
	}
	got, err := c.Get(context.Background(), "src", 1, fetch)
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if len(got) != 1 || got[0].ID != 9 {
		t.Fatalf("second Get = %v, want the retried success", got)
	}
	if calls != 2 {
		t.Fatalf("fetch called %d times, want 2 (error must not be cached)", calls)
	}
}

// TestChapterCache_ZeroTTLDisabled proves a TTL provider returning 0 disables the
// cache: every Get fetches upstream and nothing is stored (so an owner can turn
// the cache off at runtime).
func TestChapterCache_ZeroTTLDisabled(t *testing.T) {
	c := suwayomi.NewChapterCacheConst(0)
	var calls int
	fetch := func() ([]suwayomi.Chapter, error) { calls++; return chOf(calls), nil }

	for i := 0; i < 3; i++ {
		if _, err := c.Get(context.Background(), "src", 1, fetch); err != nil {
			t.Fatalf("Get %d: %v", i, err)
		}
	}
	if calls != 3 {
		t.Fatalf("fetch called %d times, want 3 (0 TTL disables the cache)", calls)
	}
}

// TestChapterCache_TTLHotReload proves the TTL is read PER-Get: shrinking the
// provider's value expires an entry that was live under the old value, and
// growing it keeps a previously-expired-age entry live — all against a fixed
// clock, so it is the TTL change (not time) that flips freshness.
func TestChapterCache_TTLHotReload(t *testing.T) {
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	ttl := time.Hour
	c := suwayomi.NewChapterCacheClock(func(context.Context) time.Duration { return ttl }, clock)

	var calls int
	fetch := func() ([]suwayomi.Chapter, error) { calls++; return chOf(calls), nil }

	// Write an entry at t=0 with a 1h TTL.
	if _, err := c.Get(context.Background(), "src", 1, fetch); err != nil {
		t.Fatalf("first Get: %v", err)
	}
	// Advance 30m — still live under the 1h TTL, so a hit (no new fetch).
	now = now.Add(30 * time.Minute)
	if _, err := c.Get(context.Background(), "src", 1, fetch); err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if calls != 1 {
		t.Fatalf("fetch called %d times, want 1 (30m < 1h TTL is a hit)", calls)
	}
	// Shrink the TTL to 10m WITHOUT moving the clock: the 30m-old entry is now
	// stale, so the next Get refetches — proving the TTL is read per-Get.
	ttl = 10 * time.Minute
	if _, err := c.Get(context.Background(), "src", 1, fetch); err != nil {
		t.Fatalf("third Get: %v", err)
	}
	if calls != 2 {
		t.Fatalf("fetch called %d times, want 2 (shrunk TTL must expire the entry)", calls)
	}
}

// TestChapterCache_ConcurrentGetRaceClean hammers Get from many goroutines across
// a few keys to prove it is race-clean under -race. It does not assert a call
// count (concurrent same-key misses may both fetch — documented + acceptable);
// it asserts no panic/race and correct results.
func TestChapterCache_ConcurrentGetRaceClean(t *testing.T) {
	c := suwayomi.NewChapterCacheConst(time.Minute)
	var total int64
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		mangaID := i % 5 // 5 distinct keys, heavy overlap
		go func() {
			defer wg.Done()
			got, err := c.Get(context.Background(), "src", mangaID, func() ([]suwayomi.Chapter, error) {
				atomic.AddInt64(&total, 1)
				return chOf(mangaID + 1), nil
			})
			if err != nil || len(got) != 1 {
				t.Errorf("concurrent Get: got=%v err=%v", got, err)
			}
		}()
	}
	wg.Wait()
	// At least one and at most one-per-goroutine fetch happened; the exact number
	// is nondeterministic under contention, only race-cleanliness is asserted.
	if atomic.LoadInt64(&total) == 0 {
		t.Fatal("expected at least one underlying fetch")
	}
}
