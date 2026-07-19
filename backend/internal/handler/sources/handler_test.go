// Package sources_test exercises the source-metrics + warm-up HTTP handlers
// end-to-end through a real Echo instance (with RequireOwner wired) against an
// ephemeral PostgreSQL instance (testdb). Tests require Docker.
package sources_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entsourcecircuitstate "github.com/technobecet/tsundoku/internal/ent/sourcecircuitstate"
	handler "github.com/technobecet/tsundoku/internal/handler/sources"
	"github.com/technobecet/tsundoku/internal/metrics"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/warmup"
)

const testSecret = "sources-handler-test-secret"

// fakeClient is a minimal sourceengine.Client for the warm-up path (embeds the
// interface, overrides only Sources + Popular). warmed (when non-nil) is closed
// on the first Popular call so a test can confirm the detached background
// WarmAll actually ran after the handler returned 202.
type fakeClient struct {
	sourceengine.Client
	sources    []sourceengine.Source
	sourcesErr error

	warmOnce sync.Once
	warmed   chan struct{}
}

func (f *fakeClient) Sources(context.Context) ([]sourceengine.Source, error) {
	return f.sources, f.sourcesErr
}
func (f *fakeClient) Popular(context.Context, int64, int) (sourceengine.SearchResult, error) {
	if f.warmed != nil {
		f.warmOnce.Do(func() { close(f.warmed) })
	}
	return sourceengine.SearchResult{}, nil
}

type testEnv struct {
	e      *echo.Echo
	client *ent.Client
	token  string
}

// newTestEnv stands up an Echo with the two source routes behind RequireOwner, a
// metrics + warm-up service over a fresh testdb, and a valid owner Bearer token.
// The fake engine-host client is provided by the caller so warm-up behaviour can
// be steered per test.
func newTestEnv(t *testing.T, fc sourceengine.Client) *testEnv {
	t.Helper()
	client := testdb.New(t)
	authSvc := auth.NewService(testSecret)

	metricsSvc := metrics.NewService(client)
	threshold := settings.Static{WarmupSlow: 5000, SourcesFailureThresh: 3, SourcesCooldownIv: 10 * time.Minute}
	warmupSvc := warmup.NewService(fc, metricsSvc, threshold, nil)
	gate := sourcegate.NewService(client, threshold)
	h := handler.NewHandler(metricsSvc, warmupSvc, threshold, gate, fc)

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.GET("/sources/metrics", h.Metrics)
	authed.POST("/sources/warmup", h.Warmup)
	authed.POST("/sources/:sourceId/reset-breaker", h.ResetBreaker)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}
	return &testEnv{e: e, client: client, token: token}
}

func (env *testEnv) do(method, target string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	r.Header.Set("Authorization", "Bearer "+env.token)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	return rec
}

// TestMetrics_OK proves GET returns the rows sorted slowest-first with a derived
// isSlow flag.
func TestMetrics_OK(t *testing.T) {
	env := newTestEnv(t, &fakeClient{})
	seed(t, env.client, "fast", 1000)
	seed(t, env.client, "slow", 9000)

	rec := env.do(http.MethodGet, "/api/sources/metrics")
	if rec.Code != http.StatusOK {
		t.Fatalf("Metrics: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got []handler.SourceMetricDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 || got[0].SourceID != "slow" {
		t.Fatalf("rows = %+v, want slowest-first", got)
	}
	if !got[0].IsSlow {
		t.Error("slow row should have isSlow=true")
	}
	if got[1].IsSlow {
		t.Error("fast row should have isSlow=false")
	}
}

// TestMetrics_Unauthorized proves the route is behind RequireOwner.
func TestMetrics_Unauthorized(t *testing.T) {
	env := newTestEnv(t, &fakeClient{})
	r := httptest.NewRequest(http.MethodGet, "/api/sources/metrics", nil)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Metrics without token: want 401, got %d", rec.Code)
	}
}

// TestWarmup_OK proves POST returns 202 + {started:true} IMMEDIATELY (the pass
// runs detached in the background) and that the background WarmAll then actually
// warms the sources (the fake's Popular fires within a bounded wait).
func TestWarmup_OK(t *testing.T) {
	fc := &fakeClient{
		sources: []sourceengine.Source{
			{ID: 1, Name: "A", Lang: "en"},
			{ID: 2, Name: "B", Lang: "en"},
		},
		warmed: make(chan struct{}),
	}
	env := newTestEnv(t, fc)
	rec := env.do(http.MethodPost, "/api/sources/warmup")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("Warmup: want 202, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got handler.WarmStartedDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Started {
		t.Errorf("started = %v, want true", got.Started)
	}

	// The detached WarmAll must actually run: its first Popular fires promptly.
	select {
	case <-fc.warmed:
	case <-time.After(2 * time.Second):
		t.Fatal("background WarmAll did not warm any source within 2s of the 202")
	}
}

// TestWarmup_Unauthorized proves the route is behind RequireOwner.
func TestWarmup_Unauthorized(t *testing.T) {
	env := newTestEnv(t, &fakeClient{})
	r := httptest.NewRequest(http.MethodPost, "/api/sources/warmup", nil)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Warmup without token: want 401, got %d", rec.Code)
	}
}

// TestWarmup_UpstreamFailureStill202 proves the endpoint STILL returns 202 even
// when Suwayomi is unreachable: the pass runs detached, so a background failure is
// logged (not returned) and never surfaces as a request error. The owner sees the
// per-source failure as lastError in GET /api/sources/metrics.
func TestWarmup_UpstreamFailureStill202(t *testing.T) {
	env := newTestEnv(t, &fakeClient{sourcesErr: errors.New("suwayomi down")})
	rec := env.do(http.MethodPost, "/api/sources/warmup")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("Warmup with upstream failure: want 202, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got handler.WarmStartedDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Started {
		t.Errorf("started = %v, want true even on upstream failure", got.Started)
	}
}

// TestMetrics_JoinsBreakerState proves GET /api/sources/metrics joins each
// source's circuit-breaker state (by name): a tripped source's row carries a
// breaker with isCoolingDown=true, and a healthy source's row has no breaker.
func TestMetrics_JoinsBreakerState(t *testing.T) {
	env := newTestEnv(t, &fakeClient{})
	seed(t, env.client, "healthy", 1000)
	seed(t, env.client, "tripped", 2000)
	tripBreaker(t, env.client, "tripped", time.Now().Add(10*time.Minute))

	rec := env.do(http.MethodGet, "/api/sources/metrics")
	if rec.Code != http.StatusOK {
		t.Fatalf("Metrics: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got []handler.SourceMetricDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	byID := map[string]handler.SourceMetricDTO{}
	for _, m := range got {
		byID[m.SourceID] = m
	}
	if b := byID["tripped"].Breaker; b == nil || !b.IsCoolingDown {
		t.Fatalf("tripped source should carry a cooling-down breaker, got %+v", byID["tripped"].Breaker)
	}
	if byID["tripped"].Breaker.ConsecutiveFailures != 3 {
		t.Errorf("tripped failures = %d, want 3", byID["tripped"].Breaker.ConsecutiveFailures)
	}
	if byID["tripped"].Breaker.FailingSince == nil {
		t.Errorf("tripped breaker should carry a non-nil failingSince, got %+v", byID["tripped"].Breaker)
	}
	if byID["healthy"].Breaker != nil {
		t.Errorf("healthy source should have no breaker, got %+v", byID["healthy"].Breaker)
	}
}

// TestResetBreaker_ClearsAndReturnsRefreshed proves the endpoint resolves the
// source id → name, clears its tripped breaker, and returns the refreshed state
// (breaker absent once cleared) — the §16 round-trip.
func TestResetBreaker_ClearsAndReturnsRefreshed(t *testing.T) {
	fc := &fakeClient{sources: []sourceengine.Source{{ID: 7, Name: "Comix", Lang: "en"}}}
	env := newTestEnv(t, fc)
	tripBreaker(t, env.client, "Comix", time.Now().Add(10*time.Minute))

	rec := env.do(http.MethodPost, "/api/sources/7/reset-breaker")
	if rec.Code != http.StatusOK {
		t.Fatalf("ResetBreaker: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got handler.BreakerResetDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.SourceID != "7" || got.SourceName != "Comix" {
		t.Errorf("identity = %q/%q, want 7/Comix", got.SourceID, got.SourceName)
	}
	if got.Breaker != nil {
		t.Errorf("breaker should be null after a clean reset, got %+v", got.Breaker)
	}

	// The row is really gone.
	if n, err := env.client.SourceCircuitState.Query().
		Where(entsourcecircuitstate.SourceKeyEQ("Comix")).Count(context.Background()); err != nil || n != 0 {
		t.Errorf("breaker row count = %d (err %v), want 0", n, err)
	}
}

// TestResetBreaker_UnknownSource404 proves a source id not in the loaded set is a
// 404 (mirrors imports.resolveSource: miss → 404).
func TestResetBreaker_UnknownSource404(t *testing.T) {
	env := newTestEnv(t, &fakeClient{sources: []sourceengine.Source{{ID: 1, Name: "A"}}})
	rec := env.do(http.MethodPost, "/api/sources/999/reset-breaker")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown source: want 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestResetBreaker_BadId400 proves a non-numeric source id is a 400.
func TestResetBreaker_BadId400(t *testing.T) {
	env := newTestEnv(t, &fakeClient{})
	rec := env.do(http.MethodPost, "/api/sources/not-a-number/reset-breaker")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestResetBreaker_Unauthorized proves the route is behind RequireOwner.
func TestResetBreaker_Unauthorized(t *testing.T) {
	env := newTestEnv(t, &fakeClient{})
	r := httptest.NewRequest(http.MethodPost, "/api/sources/1/reset-breaker", nil)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("ResetBreaker without token: want 401, got %d", rec.Code)
	}
}

// tripBreaker inserts a tripped circuit-breaker row for a source name (the
// breaker's key), with 3 consecutive failures and a cooldown at cooldownUntil.
func tripBreaker(t *testing.T, client *ent.Client, sourceKey string, cooldownUntil time.Time) {
	t.Helper()
	if err := client.SourceCircuitState.Create().
		SetSourceKey(sourceKey).
		SetConsecutiveFailures(3).
		SetLastError("cloudflare block").
		SetCooldownUntil(cooldownUntil).
		// A tripped breaker realistically carries a failure-streak start.
		SetFailingSince(time.Now().Add(-2 * time.Hour)).
		Exec(context.Background()); err != nil {
		t.Fatalf("trip breaker %q: %v", sourceKey, err)
	}
}

// seed inserts a measured metric row with the given EWMA latency.
func seed(t *testing.T, client *ent.Client, sourceID string, ewmaMs int) {
	t.Helper()
	if err := client.SourceMetric.Create().
		SetSourceID(sourceID).
		SetSourceName(sourceID).
		SetEwmaLatencyMs(ewmaMs).
		Exec(context.Background()); err != nil {
		t.Fatalf("seed %q: %v", sourceID, err)
	}
}
