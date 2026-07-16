package sourcecover_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/sourcecover"
)

// countingEngine is a trivial sourcecover.Engine test double returning a
// fixed result and recording how many times Image was called — the load-
// bearing assertion for "a warm cache serve makes ZERO engine calls" (mirrors
// internal/series/cover_test.go's TestCoverBytes_WarmMakesZeroSuwayomiCalls,
// which proves the identical property for the library-series cover cache).
type countingEngine struct {
	mu    sync.Mutex
	calls int
	data  []byte
	ext   string
	err   error
}

func (e *countingEngine) Image(context.Context, int64, string, string) ([]byte, string, error) {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
	if e.err != nil {
		return nil, "", e.err
	}
	return e.data, e.ext, nil
}

func (e *countingEngine) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

// TestCache_Get_ColdMissFetchesAndCaches proves a first request for an
// un-cached (sourceID, url) reaches the engine exactly once and returns its
// result.
func TestCache_Get_ColdMissFetchesAndCaches(t *testing.T) {
	engine := &countingEngine{data: []byte("cover-bytes"), ext: "image/png"}
	cache := sourcecover.NewCache(sourcecover.New(t.TempDir()), engine, sourcecover.DefaultConcurrency, sourcecover.DefaultDeadline)

	data, ext, err := cache.Get(context.Background(), 5, "https://source.example/a.png")
	if err != nil {
		t.Fatalf("Get: unexpected error: %v", err)
	}
	if string(data) != "cover-bytes" || ext != "image/png" {
		t.Errorf("Get: (data, ext) = (%q, %q), want (%q, %q)", data, ext, "cover-bytes", "image/png")
	}
	if got := engine.callCount(); got != 1 {
		t.Errorf("Get: engine called %d times, want 1", got)
	}
}

// TestCache_Get_WarmHitMakesZeroEngineCalls is the load-bearing proof: once a
// (sourceID, url) is cached, every subsequent Get is served from disk with
// ZERO engine calls — the entire point of restoring Suwayomi's dropped
// thumbnail cache (see the package doc). Mirrors
// internal/series/cover_test.go's TestCoverBytes_WarmMakesZeroSuwayomiCalls.
func TestCache_Get_WarmHitMakesZeroEngineCalls(t *testing.T) {
	engine := &countingEngine{data: []byte("cover-bytes"), ext: "image/png"}
	cache := sourcecover.NewCache(sourcecover.New(t.TempDir()), engine, sourcecover.DefaultConcurrency, sourcecover.DefaultDeadline)
	const sourceID = 5
	const url = "https://source.example/a.png"

	if _, _, err := cache.Get(context.Background(), sourceID, url); err != nil {
		t.Fatalf("first Get (cold): unexpected error: %v", err)
	}
	if got := engine.callCount(); got != 1 {
		t.Fatalf("after cold Get: engine called %d times, want 1", got)
	}

	// Multiple subsequent warm reads — a library grid re-render fires many —
	// must never add another engine call.
	for i := 0; i < 5; i++ {
		data, ext, err := cache.Get(context.Background(), sourceID, url)
		if err != nil {
			t.Fatalf("warm Get #%d: unexpected error: %v", i, err)
		}
		if string(data) != "cover-bytes" || ext != "image/png" {
			t.Errorf("warm Get #%d: (data, ext) = (%q, %q), want (%q, %q)", i, data, ext, "cover-bytes", "image/png")
		}
	}
	if got := engine.callCount(); got != 1 {
		t.Errorf("after 5 warm Gets: engine called %d times, want 1 (zero additional calls)", got)
	}
}

// TestCache_Get_EngineErrorIsNotCached proves a failed fetch is never
// persisted — a transient upstream failure must not poison the cache and
// permanently deny a cover that would succeed on a later, ordinary retry.
func TestCache_Get_EngineErrorIsNotCached(t *testing.T) {
	engine := &countingEngine{err: errors.New("engine unreachable")}
	cache := sourcecover.NewCache(sourcecover.New(t.TempDir()), engine, sourcecover.DefaultConcurrency, sourcecover.DefaultDeadline)

	if _, _, err := cache.Get(context.Background(), 1, "https://source.example/x.jpg"); err == nil {
		t.Fatal("Get: want error, got nil")
	}
	if _, _, err := cache.Get(context.Background(), 1, "https://source.example/x.jpg"); err == nil {
		t.Fatal("second Get: want error, got nil")
	}
	if got := engine.callCount(); got != 2 {
		t.Errorf("Get: engine called %d times across two failed attempts, want 2 (a failure must not be cached)", got)
	}
}

// blockingEngine is a sourcecover.Engine whose Image call blocks until ctx is
// done and then returns ctx.Err() — modelling the real sourceengine
// httpClient's genuine context-aware behaviour (its underlying net/http
// request is built with the request context and aborts when it is
// cancelled/expires; see internal/sourceengine/client.go's send/newRequest).
// It also tracks PEAK concurrent in-flight calls, the direct proof that the
// bounded-concurrency slot actually bounds concurrency.
type blockingEngine struct {
	current int32
	peak    int32
}

func (b *blockingEngine) Image(ctx context.Context, _ int64, _, _ string) ([]byte, string, error) {
	cur := atomic.AddInt32(&b.current, 1)
	defer atomic.AddInt32(&b.current, -1)
	for {
		p := atomic.LoadInt32(&b.peak)
		if cur <= p {
			break
		}
		if atomic.CompareAndSwapInt32(&b.peak, p, cur) {
			break
		}
	}
	<-ctx.Done()
	return nil, "", ctx.Err()
}

func (b *blockingEngine) peakConcurrency() int32 {
	return atomic.LoadInt32(&b.peak)
}

// TestCache_Get_FailsFastUnderSaturatingBurst is THE acceptance gate for the
// owner's hard no-hang rule: a burst of MISSES far exceeding the concurrency
// cap, against an engine that never responds on its own, must still have
// EVERY call return within the deadline budget — never "after minutes" —
// and the number of calls actually in flight at once must never exceed the
// configured concurrency. This proves the deadline bounds the WHOLE Get call
// (slot-wait AND fetch together), not just the fetch: a request stuck
// waiting for a free slot is exactly the failure mode a fetch-only timeout
// would miss.
func TestCache_Get_FailsFastUnderSaturatingBurst(t *testing.T) {
	engine := &blockingEngine{}
	const concurrency = 2
	const deadline = 150 * time.Millisecond
	cache := sourcecover.NewCache(sourcecover.New(t.TempDir()), engine, concurrency, deadline)

	const burst = 10 // far exceeds the concurrency cap
	var wg sync.WaitGroup
	errs := make([]error, burst)

	start := time.Now()
	for i := 0; i < burst; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// A DISTINCT url per goroutine: every one of these is a genuine
			// cache MISS competing for the same bounded pool, not a repeat of
			// one already-in-flight key.
			url := fmt.Sprintf("https://source.example/cover-%d.jpg", i)
			_, _, err := cache.Get(context.Background(), 1, url)
			errs[i] = err
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)

	// Generous slack over the deadline for goroutine scheduling — the bound
	// this guards is "never hangs for minutes", not exact-millisecond timing.
	if maxWant := deadline * 5; elapsed > maxWant {
		t.Fatalf("burst of %d took %v to fully resolve, want under %v — the no-hang guarantee requires every request to fail fast under saturation", burst, elapsed, maxWant)
	}
	for i, err := range errs {
		if !errors.Is(err, sourcecover.ErrTimeout) {
			t.Errorf("Get[%d]: err = %v, want ErrTimeout", i, err)
		}
	}
	if peak := engine.peakConcurrency(); peak > concurrency {
		t.Errorf("peak concurrent engine calls = %d, want <= %d (the concurrency cap)", peak, concurrency)
	}
}
