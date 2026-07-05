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
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	handler "github.com/technobecet/tsundoku/internal/handler/sources"
	"github.com/technobecet/tsundoku/internal/metrics"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/suwayomi"
	"github.com/technobecet/tsundoku/internal/warmup"
)

const testSecret = "sources-handler-test-secret"

// fakeClient is a minimal suwayomi.Client for the warm-up path (embeds the
// interface, overrides only Sources + Browse).
type fakeClient struct {
	suwayomi.Client
	sources    []suwayomi.Source
	sourcesErr error
}

func (f *fakeClient) Sources(context.Context) ([]suwayomi.Source, error) {
	return f.sources, f.sourcesErr
}
func (f *fakeClient) Browse(context.Context, string, suwayomi.BrowseType, int) (suwayomi.BrowseResult, error) {
	return suwayomi.BrowseResult{}, nil
}

type testEnv struct {
	e      *echo.Echo
	client *ent.Client
	token  string
}

// newTestEnv stands up an Echo with the two source routes behind RequireOwner, a
// metrics + warm-up service over a fresh testdb, and a valid owner Bearer token.
// The fake Suwayomi client is provided by the caller so warm-up behaviour can be
// steered per test.
func newTestEnv(t *testing.T, fc suwayomi.Client) *testEnv {
	t.Helper()
	client := testdb.New(t)
	authSvc := auth.NewService(testSecret)

	metricsSvc := metrics.NewService(client)
	threshold := settings.Static{WarmupSlow: 5000}
	warmupSvc := warmup.NewService(fc, metricsSvc, threshold)
	h := handler.NewHandler(metricsSvc, warmupSvc, threshold)

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.GET("/sources/metrics", h.Metrics)
	authed.POST("/sources/warmup", h.Warmup)

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

// TestWarmup_OK proves POST triggers WarmAll and returns {warmed:N}.
func TestWarmup_OK(t *testing.T) {
	fc := &fakeClient{sources: []suwayomi.Source{
		{ID: "a", Name: "A", Lang: "en"},
		{ID: "b", Name: "B", Lang: "en"},
	}}
	env := newTestEnv(t, fc)
	rec := env.do(http.MethodPost, "/api/sources/warmup")
	if rec.Code != http.StatusOK {
		t.Fatalf("Warmup: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got handler.WarmResultDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Warmed != 2 {
		t.Errorf("warmed = %d, want 2", got.Warmed)
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

// TestWarmup_Upstream proves a Suwayomi failure (source list unreachable) maps to
// a 502.
func TestWarmup_Upstream(t *testing.T) {
	env := newTestEnv(t, &fakeClient{sourcesErr: errors.New("suwayomi down")})
	rec := env.do(http.MethodPost, "/api/sources/warmup")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("Warmup with upstream failure: want 502, got %d (%s)", rec.Code, rec.Body.String())
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
