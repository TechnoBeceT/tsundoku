// Package reporting_test exercises the Source Health Console reporting HTTP
// handlers end-to-end through a real Echo instance (with RequireOwner wired)
// against an ephemeral PostgreSQL instance (testdb). It asserts the happy-path
// DTO shapes, the __all__ sentinel, every validation 400, and that every route is
// behind RequireOwner (401 without a token). Tests require Docker.
package reporting_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entsourceevent "github.com/technobecet/tsundoku/internal/ent/sourceevent"
	handler "github.com/technobecet/tsundoku/internal/handler/reporting"
	"github.com/technobecet/tsundoku/internal/metrics"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/reporting"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourcegate"
)

const testSecret = "reporting-handler-test-secret"

type testEnv struct {
	e      *echo.Echo
	client *ent.Client
	token  string
}

// newTestEnv stands up an Echo with the four reporting routes behind RequireOwner,
// a reporting service over a fresh testdb, and a valid owner Bearer token.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	client := testdb.New(t)
	authSvc := auth.NewService(testSecret)

	metricsSvc := metrics.NewService(client)
	gate := sourcegate.NewService(client, settings.Static{SourcesFailureThresh: 3, SourcesCooldownIv: 10 * time.Minute})
	h := handler.NewHandler(reporting.NewService(client, metricsSvc, gate))

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.GET("/reporting/overview", h.Overview)
	authed.GET("/reporting/sources", h.Sources)
	authed.GET("/reporting/source/:sourceKey/events", h.Events)
	authed.GET("/reporting/source/:sourceKey/timeline", h.Timeline)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}
	return &testEnv{e: e, client: client, token: token}
}

func (env *testEnv) do(target string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	r.Header.Set("Authorization", "Bearer "+env.token)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	return rec
}

func (env *testEnv) noAuth(target string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	return rec
}

// seedEvent inserts one SourceEvent for the handler tests.
func seedEvent(t *testing.T, client *ent.Client, key string, typ entsourceevent.EventType, status entsourceevent.Status) {
	t.Helper()
	if err := client.SourceEvent.Create().
		SetSourceKey(key).SetSourceID("1").SetSourceName(key).
		SetEventType(typ).SetStatus(status).
		SetCreatedAt(time.Now().Add(-1 * time.Hour)).
		Exec(context.Background()); err != nil {
		t.Fatalf("seed event %q: %v", key, err)
	}
}

// TestOverview_OK proves GET /reporting/overview returns 200 with the KPI + slice
// shape (non-nil slices).
func TestOverview_OK(t *testing.T) {
	env := newTestEnv(t)
	seedEvent(t, env.client, "A", entsourceevent.EventTypeSearch, entsourceevent.StatusSuccess)
	seedEvent(t, env.client, "A", entsourceevent.EventTypeSearch, entsourceevent.StatusFailed)

	rec := env.do("/api/reporting/overview?period=24h")
	if rec.Code != http.StatusOK {
		t.Fatalf("Overview: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got handler.OverviewDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Kpis.TotalEvents != 2 || got.Kpis.FailedEvents != 1 {
		t.Errorf("kpis = %+v, want total 2 fail 1", got.Kpis)
	}
	if got.EventsByType == nil || got.SlowestSources == nil || got.FailingSources == nil || got.RecentErrors == nil {
		t.Error("all overview slices must serialize non-nil")
	}
}

// TestOverview_BadPeriod400 proves an unknown period is a 400.
func TestOverview_BadPeriod400(t *testing.T) {
	env := newTestEnv(t)
	if rec := env.do("/api/reporting/overview?period=1y"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad period: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestSources_OKAndBadSort proves the rollup returns 200 (default sort) and that a
// bad sort is a 400.
func TestSources_OKAndBadSort(t *testing.T) {
	env := newTestEnv(t)
	seedEvent(t, env.client, "A", entsourceevent.EventTypeSearch, entsourceevent.StatusFailed)

	rec := env.do("/api/reporting/sources?period=7d")
	if rec.Code != http.StatusOK {
		t.Fatalf("Sources: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got []handler.SourceReportDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].SourceKey != "A" || got[0].FailedEvents != 1 {
		t.Fatalf("rollup = %+v, want one A with 1 fail", got)
	}

	if rec := env.do("/api/reporting/sources?sort=nonsense"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad sort: want 400, got %d", rec.Code)
	}
}

// TestEvents_OKAllSentinelAndFilters proves the feed returns 200, the __all__
// sentinel spans sources, and a bad status/eventType is a 400.
func TestEvents_OKAllSentinelAndFilters(t *testing.T) {
	env := newTestEnv(t)
	seedEvent(t, env.client, "A", entsourceevent.EventTypeSearch, entsourceevent.StatusSuccess)
	seedEvent(t, env.client, "B", entsourceevent.EventTypeSearch, entsourceevent.StatusFailed)

	rec := env.do("/api/reporting/source/__all__/events")
	if rec.Code != http.StatusOK {
		t.Fatalf("Events(__all__): want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got handler.SourceEventListDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Total != 2 || len(got.Items) != 2 {
		t.Errorf("__all__ feed = total %d items %d, want 2/2", got.Total, len(got.Items))
	}

	if rec := env.do("/api/reporting/source/A/events?status=bogus"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad status: want 400, got %d", rec.Code)
	}
	if rec := env.do("/api/reporting/source/A/events?eventType=bogus"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad eventType: want 400, got %d", rec.Code)
	}
	if rec := env.do("/api/reporting/source/A/events?limit=-1"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad limit: want 400, got %d", rec.Code)
	}
}

// TestTimeline_OKAndBadBucket proves the timeline returns 200 and a bad bucket is
// a 400.
func TestTimeline_OKAndBadBucket(t *testing.T) {
	env := newTestEnv(t)
	seedEvent(t, env.client, "A", entsourceevent.EventTypeSearch, entsourceevent.StatusSuccess)

	rec := env.do("/api/reporting/source/A/timeline?bucket=hour&period=24h")
	if rec.Code != http.StatusOK {
		t.Fatalf("Timeline: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got handler.SourceTimelineDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Buckets == nil {
		t.Error("buckets must serialize non-nil")
	}

	if rec := env.do("/api/reporting/source/A/timeline?bucket=week"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad bucket: want 400, got %d", rec.Code)
	}
}

// TestReporting_Unauthorized proves every route is behind RequireOwner.
func TestReporting_Unauthorized(t *testing.T) {
	env := newTestEnv(t)
	for _, path := range []string{
		"/api/reporting/overview",
		"/api/reporting/sources",
		"/api/reporting/source/__all__/events",
		"/api/reporting/source/__all__/timeline",
	} {
		if rec := env.noAuth(path); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without token: want 401, got %d", path, rec.Code)
		}
	}
}
