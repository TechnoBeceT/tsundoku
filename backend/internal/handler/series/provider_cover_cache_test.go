package series_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	handler "github.com/technobecet/tsundoku/internal/handler/series"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	seriessvc "github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sourcecover"
)

// TestProviderCover_CachedServeMakesZeroEngineCalls is ProviderCover's
// version of TestSeriesCover_CachedServeMakesZeroSuwayomiCalls (GAP-085):
// once a provider's cover is cached, re-serving it pings the engine host
// ZERO more times.
func TestProviderCover_CachedServeMakesZeroEngineCalls(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	pngBytes := []byte{0x89, 0x50, 0x4e, 0x47}
	const coverURL = "/api/v1/manga/3/cover"
	seedCoverImage(env, coverURL, pngBytes, "png")
	seriesID, providerID := seedWithCover(ctx, t, env, coverURL)

	target := "/api/series/" + seriesID.String() + "/providers/" + providerID.String() + "/cover"
	if rec := env.do(http.MethodGet, target, ""); rec.Code != http.StatusOK {
		t.Fatalf("ProviderCover (warming): want 200, got %d", rec.Code)
	}
	if got := env.sw.CallCount("Image"); got != 1 {
		t.Fatalf("cold serve: engine calls = %d, want 1", got)
	}

	for i := range 3 {
		rec := env.do(http.MethodGet, target, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("ProviderCover (cached, i=%d): want 200, got %d", i, rec.Code)
		}
		if rec.Body.String() != string(pngBytes) {
			t.Fatalf("ProviderCover (cached): body mismatch")
		}
	}

	if got := env.sw.CallCount("Image"); got != 1 {
		t.Fatalf("ANTI-BAN PROOF FAILED: cached serves made %d additional engine call(s), want the count to stay at 1", got)
	}
}

// blockingCoverEngine is a sourcecover.Engine whose Image call blocks until
// ctx is done, then returns ctx.Err() — models the real sourceengine
// httpClient's genuine context-aware behaviour (see
// internal/sourcecover.blockingEngine's identical doc comment for why this
// is realistic, not a test-only shortcut). Used to prove the owner's hard
// no-hang rule at the HTTP handler boundary, not just inside the cache.
type blockingCoverEngine struct{}

func (blockingCoverEngine) Image(ctx context.Context, _ int64, _, _ string) ([]byte, string, error) {
	<-ctx.Done()
	return nil, "", ctx.Err()
}

// TestProviderCover_FailsFastUnderSaturatingBurst is THE handler-level
// acceptance gate (GAP-085) for ProviderCover: a burst of cold covers far
// exceeding the concurrency cap, against an engine that never responds on
// its own, must have every HTTP request return within the fail-fast
// deadline — never held open for minutes — because the deadline bounds the
// WHOLE handler call (concurrency-slot wait AND the fetch), not just the
// fetch itself.
func TestProviderCover_FailsFastUnderSaturatingBurst(t *testing.T) {
	client := testdb.New(t)
	storage := t.TempDir()
	authSvc := auth.NewService(testSecret)
	svc := seriessvc.NewService(client, storage, 14)

	const concurrency = 2
	const deadline = 150 * time.Millisecond
	coverCache := sourcecover.NewCache(sourcecover.New(t.TempDir()), blockingCoverEngine{}, concurrency, deadline)
	h := handler.NewHandler(svc, func() {}, coverCache)

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.GET("/series/:id/providers/:providerId/cover", h.ProviderCover)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}

	// Seed ONE series with a burst of providers, each carrying a DISTINCT
	// cover_url — every one of these is a genuine cache MISS competing for
	// the same bounded pool, not a repeat of an already-in-flight key.
	ctx := context.Background()
	s := client.Series.Create().
		SetTitle("Burst Series").SetSlug("burst-series").
		SetCategoryID(catID(ctx, client, "Manga")).
		SaveX(ctx)

	const burst = 10 // far exceeds the concurrency cap
	targets := make([]string, burst)
	for i := range burst {
		p := client.SeriesProvider.Create().
			SetSeriesID(s.ID).
			SetProvider(strconv.Itoa(100 + i)).
			SetImportance(1).
			SetCoverURL(fmt.Sprintf("/api/v1/manga/%d/cover", i)).
			SaveX(ctx)
		targets[i] = "/api/series/" + s.ID.String() + "/providers/" + p.ID.String() + "/cover"
	}

	var wg sync.WaitGroup
	codes := make([]int, burst)
	start := time.Now()
	for i, target := range targets {
		wg.Add(1)
		go func(i int, target string) {
			defer wg.Done()
			r := httptest.NewRequest(http.MethodGet, target, nil)
			r.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, r)
			codes[i] = rec.Code
		}(i, target)
	}
	wg.Wait()
	elapsed := time.Since(start)

	if maxWant := deadline * 6; elapsed > maxWant {
		t.Fatalf("burst of %d ProviderCover requests took %v to fully resolve, want under %v — the handler must never hang under a saturating burst", burst, elapsed, maxWant)
	}
	for i, code := range codes {
		if code != http.StatusGatewayTimeout {
			t.Errorf("ProviderCover burst[%d]: status = %d, want %d (fail-fast)", i, code, http.StatusGatewayTimeout)
		}
	}
}
