package engine_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/ent"
	handler "github.com/technobecet/tsundoku/internal/handler/engine"
	"github.com/technobecet/tsundoku/internal/metrics"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/sourcepurge"
)

// purgeEnv wires an Echo instance with the four /api/engine/purge-* routes behind
// RequireOwner, a purge service over a fresh testdb, and a valid owner token.
type purgeEnv struct {
	e      *echo.Echo
	client *ent.Client
	token  string
}

func newPurgeEnv(t *testing.T) *purgeEnv {
	t.Helper()
	client := testdb.New(t)
	authSvc := auth.NewService(testSecret)

	seriesSvc := series.NewService(client, t.TempDir(), 14)
	purgeSvc := sourcepurge.NewService(client, seriesSvc, metrics.NewService(client), sourcegate.NewService(client, settings.Static{}))
	h := handler.NewHandler(apkcache.New(t.TempDir()), client).WithPurge(purgeSvc)

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.POST("/engine/purge-source", h.PurgeSource)
	authed.GET("/engine/purge-source/preview", h.PreviewSource)
	authed.POST("/engine/purge-extension", h.PurgeExtension)
	authed.GET("/engine/purge-extension/preview", h.PreviewExtension)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}
	return &purgeEnv{e: e, client: client, token: token}
}

func (env *purgeEnv) do(method, target, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	r.Header.Set("Authorization", "Bearer "+env.token)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	return rec
}

// doOK issues an authed request, asserts 200, and decodes the JSON body into out.
func doOK[T any](t *testing.T, env *purgeEnv, method, target, body string, out *T) {
	t.Helper()
	rec := env.do(method, target, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s %s: want 200, got %d (%s)", method, target, rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

// seedSource seeds one live source's footprint: a series with a provider on it, a
// feed row, a metric row, and a breaker row.
func seedSource(t *testing.T, client *ent.Client, sourceID, sourceName string) {
	t.Helper()
	ctx := context.Background()
	s := client.Series.Create().SetTitle("S " + sourceID).SetSlug("s-" + sourceID).SaveX(ctx)
	p := client.SeriesProvider.Create().
		SetSeriesID(s.ID).SetProvider(sourceID).SetProviderName(sourceName).SetSuwayomiID(1).SaveX(ctx)
	client.ProviderChapter.Create().SetSeriesProviderID(p.ID).SetChapterKey("1").SaveX(ctx)
	client.SourceMetric.Create().SetSourceID(sourceID).SetSourceName(sourceName).SaveX(ctx)
	client.SourceCircuitState.Create().SetSourceKey(sourceName).SaveX(ctx)
}

// TestPurgeSource_OK proves POST runs the cascade and returns the summary, and
// that the source's footprint is actually gone.
func TestPurgeSource_OK(t *testing.T) {
	env := newPurgeEnv(t)
	seedSource(t, env.client, "100", "Purge Me")

	var got handler.SourceSummaryDTO
	doOK(t, env, http.MethodPost, "/api/engine/purge-source", `{"sourceId":"100","sourceName":"Purge Me"}`, &got)
	if got.ProvidersRemoved != 1 || got.MetricsDeleted != 1 || got.BreakerCleared != 1 || got.SeriesAffected != 1 {
		t.Fatalf("summary = %+v, want providers=1 metrics=1 breaker=1 series=1", got)
	}
	if n := env.client.SeriesProvider.Query().CountX(context.Background()); n != 0 {
		t.Errorf("providers after purge = %d, want 0", n)
	}
}

// TestPurgeSource_BadRequest proves a missing sourceName is a 400.
func TestPurgeSource_BadRequest(t *testing.T) {
	env := newPurgeEnv(t)
	rec := env.do(http.MethodPost, "/api/engine/purge-source", `{"sourceId":"100"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PurgeSource missing name: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestPurgeSource_Unauthorized proves the route is behind RequireOwner.
func TestPurgeSource_Unauthorized(t *testing.T) {
	env := newPurgeEnv(t)
	r := httptest.NewRequest(http.MethodPost, "/api/engine/purge-source", strings.NewReader(`{"sourceId":"100","sourceName":"X"}`))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("PurgeSource without token: want 401, got %d", rec.Code)
	}
}

// TestPreviewSource_OK proves the dry-run endpoint returns the blast-radius counts
// and mutates nothing.
func TestPreviewSource_OK(t *testing.T) {
	env := newPurgeEnv(t)
	seedSource(t, env.client, "100", "Preview Me")

	var got handler.SourcePreviewDTO
	doOK(t, env, http.MethodGet, "/api/engine/purge-source/preview?sourceId=100&sourceName=Preview+Me", "", &got)
	if got.Providers != 1 || got.ProviderChapters != 1 || got.Metrics != 1 || got.Breaker != 1 {
		t.Fatalf("preview = %+v, want providers=1 feed=1 metrics=1 breaker=1", got)
	}
	if n := env.client.SeriesProvider.Query().CountX(context.Background()); n != 1 {
		t.Errorf("providers after preview = %d, want 1 (dry run must not delete)", n)
	}
}

// TestPreviewSource_Unauthorized proves the preview route is behind RequireOwner.
func TestPreviewSource_Unauthorized(t *testing.T) {
	env := newPurgeEnv(t)
	r := httptest.NewRequest(http.MethodGet, "/api/engine/purge-source/preview?sourceId=1&sourceName=X", nil)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("PreviewSource without token: want 401, got %d", rec.Code)
	}
}

// TestPurgeExtension_OK proves the extension endpoint reads the durable
// HarvestedExtension source-ids map and purges each source.
func TestPurgeExtension_OK(t *testing.T) {
	env := newPurgeEnv(t)
	seedSource(t, env.client, "100", "Multi EN")
	seedSource(t, env.client, "101", "Multi ES")
	env.client.HarvestedExtension.Create().
		SetPkgName("com.example.multi").SetSourceIds([]int64{100, 101}).SaveX(context.Background())

	var got handler.ExtensionSummaryDTO
	doOK(t, env, http.MethodPost, "/api/engine/purge-extension", `{"pkgName":"com.example.multi"}`, &got)
	if got.ProvidersRemoved != 2 || len(got.Sources) != 2 {
		t.Fatalf("summary = %+v, want providers=2 across 2 sources", got)
	}
	if n := env.client.SeriesProvider.Query().CountX(context.Background()); n != 0 {
		t.Errorf("providers after extension purge = %d, want 0", n)
	}
}

// TestPurgeExtension_Unauthorized proves the extension route is behind RequireOwner.
func TestPurgeExtension_Unauthorized(t *testing.T) {
	env := newPurgeEnv(t)
	r := httptest.NewRequest(http.MethodPost, "/api/engine/purge-extension", strings.NewReader(`{"pkgName":"x"}`))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("PurgeExtension without token: want 401, got %d", rec.Code)
	}
}
