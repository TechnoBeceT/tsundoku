// Package downloads_test exercises the download-activity HTTP handlers end-to-end
// through a real Echo instance (with RequireOwner wired) against an ephemeral
// PostgreSQL instance (testdb). Tests require Docker.
package downloads_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	downloadssvc "github.com/technobecet/tsundoku/internal/downloads"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	handler "github.com/technobecet/tsundoku/internal/handler/downloads"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
)

const testSecret = "downloads-handler-test-secret"

// catID resolves a seeded default category's id by name (testdb seeds them).
func catID(ctx context.Context, db *ent.Client, name string) uuid.UUID {
	id, err := category.IDByName(ctx, db, name)
	if err != nil {
		panic(err)
	}
	return id
}

type testEnv struct {
	e        *echo.Echo
	client   *ent.Client
	token    string
	failedID uuid.UUID
	wantedID uuid.UUID
}

// newTestEnv stands up a fully-wired Echo with the downloads routes behind
// RequireOwner (so the 401 proofs hit the real middleware), a downloads.Service
// over a fresh testdb client, and a valid owner Bearer token.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	client := testdb.New(t)
	authSvc := auth.NewService(testSecret)
	h := handler.NewHandler(downloadssvc.NewService(client))

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.GET("/downloads", h.List)
	authed.POST("/downloads/retry-all", h.RetryAll)
	authed.POST("/chapters/:id/retry", h.RetryChapter)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}
	return &testEnv{e: e, client: client, token: token}
}

// seed creates a single series with a failed chapter (full failure bookkeeping)
// and a wanted chapter, recording their ids.
func (env *testEnv) seed(ctx context.Context, t *testing.T) {
	t.Helper()

	s := env.client.Series.Create().
		SetTitle("Solo Leveling").SetSlug("solo-leveling").
		SetCategoryID(catID(ctx, env.client, "Manhwa")).SaveX(ctx)
	prov := env.client.SeriesProvider.Create().
		SetSeriesID(s.ID).SetProvider("mangadex").SetImportance(10).SaveX(ctx)
	env.client.ProviderChapter.Create().
		SetSeriesProviderID(prov.ID).SetChapterKey("ch-1").SetName("Chapter 1").SaveX(ctx)

	failed := env.client.Chapter.Create().
		SetSeriesID(s.ID).SetChapterKey("ch-1").SetNumber(1).
		SetState(entchapter.StateFailed).SetRetries(2).
		SetLastError("boom").SetErrorCategory("network").
		SetNextAttemptAt(time.Now().UTC().Add(time.Hour)).SaveX(ctx)
	wanted := env.client.Chapter.Create().
		SetSeriesID(s.ID).SetChapterKey("ch-2").SetNumber(2).
		SetState(entchapter.StateWanted).SaveX(ctx)
	env.failedID = failed.ID
	env.wantedID = wanted.ID
}

func (env *testEnv) do(method, target string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	r.Header.Set("Authorization", "Bearer "+env.token)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	return rec
}

func (env *testEnv) doUnauth(method, target string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	return rec
}

func TestList_OK(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)

	rec := env.do(http.MethodGet, "/api/downloads?state=failed")
	if rec.Code != http.StatusOK {
		t.Fatalf("List: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got downloadssvc.DownloadListDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Total != 1 || len(got.Items) != 1 {
		t.Fatalf("want total=1/items=1, got total=%d items=%d", got.Total, len(got.Items))
	}
	if got.Items[0].ID != env.failedID {
		t.Errorf("want failed chapter, got %s", got.Items[0].ID)
	}
	// camelCase contract keys present (matches OpenAPI schema).
	body := rec.Body.String()
	for _, key := range []string{`"seriesId"`, `"seriesTitle"`, `"seriesCoverUrl"`, `"chapterKey"`, `"nextAttemptAt"`, `"errorCategory"`} {
		if !strings.Contains(body, key) {
			t.Errorf("response missing key %s: %s", key, body)
		}
	}
}

func TestList_MissingState(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodGet, "/api/downloads")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing state: want 400, got %d", rec.Code)
	}
}

func TestList_UnknownState(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodGet, "/api/downloads?state=failed,bogus")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown state: want 400, got %d", rec.Code)
	}
}

func TestList_BadLimit(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodGet, "/api/downloads?state=failed&limit=-1")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad limit: want 400, got %d", rec.Code)
	}
}

func TestRetryChapter_OK(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)

	rec := env.do(http.MethodPost, "/api/chapters/"+env.failedID.String()+"/retry")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("retry: want 204, got %d (%s)", rec.Code, rec.Body.String())
	}
	// §16 round-trip: refetch shows the chapter now wanted with cleared fields.
	ch := env.client.Chapter.GetX(ctx, env.failedID)
	if ch.State != entchapter.StateWanted || ch.Retries != 0 || ch.LastError != "" ||
		ch.ErrorCategory != "" || ch.NextAttemptAt != nil {
		t.Errorf("retry did not reset: %+v", ch)
	}
}

func TestRetryChapter_BadUUID(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPost, "/api/chapters/not-a-uuid/retry")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad uuid: want 400, got %d", rec.Code)
	}
}

func TestRetryChapter_NotFound(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPost, "/api/chapters/"+uuid.New().String()+"/retry")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("not found: want 404, got %d", rec.Code)
	}
}

func TestRetryChapter_Conflict(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)

	rec := env.do(http.MethodPost, "/api/chapters/"+env.wantedID.String()+"/retry")
	if rec.Code != http.StatusConflict {
		t.Fatalf("non-retryable: want 409, got %d", rec.Code)
	}
}

func TestRetryAll_OK(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)

	rec := env.do(http.MethodPost, "/api/downloads/retry-all")
	if rec.Code != http.StatusOK {
		t.Fatalf("retry-all: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got downloadssvc.RetryAllResultDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Retried != 1 {
		t.Errorf("want retried=1, got %d", got.Retried)
	}
}

func TestRetryAll_NonRetryableState(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPost, "/api/downloads/retry-all?state=downloading")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("non-retryable state param: want 400, got %d", rec.Code)
	}
}

func TestRetryAll_BadSeriesID(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPost, "/api/downloads/retry-all?series_id=nope")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad series_id: want 400, got %d", rec.Code)
	}
}

// TestRoutes_RequireAuth proves every route is behind RequireOwner (401 without a
// Bearer token).
func TestRoutes_RequireAuth(t *testing.T) {
	env := newTestEnv(t)
	cases := []struct{ method, target string }{
		{http.MethodGet, "/api/downloads?state=failed"},
		{http.MethodPost, "/api/downloads/retry-all"},
		{http.MethodPost, "/api/chapters/" + uuid.New().String() + "/retry"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.target, func(t *testing.T) {
			rec := env.doUnauth(tc.method, tc.target)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("want 401, got %d", rec.Code)
			}
		})
	}
}
