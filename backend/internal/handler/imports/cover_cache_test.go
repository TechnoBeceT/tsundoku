package imports_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	handler "github.com/technobecet/tsundoku/internal/handler/imports"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/sourcecover"
)

// TestSourceCover_WarmServeMakesZeroEngineCalls is THE anti-ban proof at the
// HTTP boundary for the Discover/Search source-cover proxy (GAP-085): once a
// (sourceId, url) cover is cached, re-serving it pings the engine host ZERO
// more times. Mirrors internal/handler/series/cover_cache_test.go's
// TestSeriesCover_CachedServeMakesZeroSuwayomiCalls and
// TestProviderCover_CachedServeMakesZeroEngineCalls for the third of the
// three cover-proxy endpoints.
func TestSourceCover_WarmServeMakesZeroEngineCalls(t *testing.T) {
	fc := &fakeEngineClient{imageData: []byte("fake-jpeg-bytes"), imageExt: "jpg"}
	env := newTestEnv(t, fc)

	target := "/api/sources/42/cover?url=https://example.com/cover.jpg"
	if rec := env.do(http.MethodGet, target, ""); rec.Code != http.StatusOK {
		t.Fatalf("SourceCover (warming): want 200, got %d", rec.Code)
	}
	if got := len(fc.imageCalls); got != 1 {
		t.Fatalf("cold serve: engine calls = %d, want 1", got)
	}

	for i := range 3 {
		rec := env.do(http.MethodGet, target, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("SourceCover (cached, i=%d): want 200, got %d", i, rec.Code)
		}
		if rec.Body.String() != "fake-jpeg-bytes" {
			t.Fatalf("SourceCover (cached): body mismatch")
		}
	}

	if got := len(fc.imageCalls); got != 1 {
		t.Fatalf("ANTI-BAN PROOF FAILED: cached serves made %d additional engine call(s), want the count to stay at 1", got)
	}
}

// blockingSourceEngine is a sourcecover.Engine whose Image call blocks until
// ctx is done, then returns ctx.Err() — models the real sourceengine
// httpClient's genuine context-aware behaviour (its underlying net/http
// request is built with the request context and aborts on
// cancellation/expiry; see internal/sourceengine/client.go's send/newRequest,
// and internal/sourcecover's own identical test double for the same
// reasoning). Used to prove the owner's hard no-hang rule holds at the actual
// HTTP handler, not merely inside the cache package.
type blockingSourceEngine struct{}

func (blockingSourceEngine) Image(ctx context.Context, _ int64, _, _ string) ([]byte, string, error) {
	<-ctx.Done()
	return nil, "", ctx.Err()
}

// TestSourceCover_FailsFastUnderSaturatingBurst is THE handler-level
// acceptance gate (GAP-085) for SourceCover: a burst of cold covers far
// exceeding the concurrency cap, against an engine that never responds on
// its own, must have every HTTP request return within the fail-fast
// deadline — never held open for minutes. The deadline bounds the WHOLE
// handler call (concurrency-slot wait AND the fetch together, see
// sourcecover.Cache.Get's doc comment), not just the fetch — a request stuck
// waiting for a free slot is exactly the failure mode a fetch-only timeout
// would miss, and is exactly what produced the original SPA hang (a burst of
// ~15 simultaneous cover fetches saturating both the browser's per-host
// connection cap and the backend).
func TestSourceCover_FailsFastUnderSaturatingBurst(t *testing.T) {
	authSvc := auth.NewService(testSecret)

	const concurrency = 2
	const deadline = 150 * time.Millisecond
	coverCache := sourcecover.NewCache(sourcecover.New(t.TempDir()), blockingSourceEngine{}, concurrency, deadline)
	h := handler.NewHandler(nil, nil, func() {}, coverCache)

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.GET("/sources/:sourceId/cover", h.SourceCover)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}

	const burst = 10 // far exceeds the concurrency cap
	var wg sync.WaitGroup
	codes := make([]int, burst)
	start := time.Now()
	for i := range burst {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// A DISTINCT url per goroutine: every one of these is a genuine
			// cache MISS competing for the same bounded pool, not a repeat of
			// an already-in-flight key.
			target := fmt.Sprintf("/api/sources/1/cover?url=https://example.com/cover-%d.jpg", i)
			r := httptest.NewRequest(http.MethodGet, target, nil)
			r.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, r)
			codes[i] = rec.Code
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)

	if maxWant := deadline * 6; elapsed > maxWant {
		t.Fatalf("burst of %d SourceCover requests took %v to fully resolve, want under %v — the handler must never hang under a saturating burst", burst, elapsed, maxWant)
	}
	for i, code := range codes {
		if code != http.StatusGatewayTimeout {
			t.Errorf("SourceCover burst[%d]: status = %d, want %d (fail-fast)", i, code, http.StatusGatewayTimeout)
		}
	}
}
