// Package series_test exercises the library HTTP handlers end-to-end through a
// real Echo instance (with the RequireOwner middleware wired) against an
// ephemeral PostgreSQL instance (testdb). Tests require Docker.
package series_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	handler "github.com/technobecet/tsundoku/internal/handler/series"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	seriessvc "github.com/technobecet/tsundoku/internal/series"
)

const testSecret = "series-handler-test-secret"

// testEnv bundles the wired Echo app, a valid owner token, and the seeded ids.
type testEnv struct {
	e        *echo.Echo
	client   *ent.Client
	token    string
	storage  string
	mangaID  uuid.UUID
	manhwaID uuid.UUID
}

// newTestEnv stands up a fully-wired Echo: the series routes registered behind
// RequireOwner (so the 401 proofs exercise the real middleware), a series.Service
// over a fresh testdb client and a t.TempDir() storage root, and a valid owner
// Bearer token minted from the same auth secret.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	client := testdb.New(t)
	storage := t.TempDir()
	authSvc := auth.NewService(testSecret)
	svc := seriessvc.NewService(client, storage)
	h := handler.NewHandler(svc)

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc))
	authed.GET("/series", h.List)
	authed.GET("/series/:id", h.Detail)
	authed.PATCH("/series/:id/category", h.SetCategory)
	authed.GET("/categories", h.Categories)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}

	return &testEnv{e: e, client: client, token: token, storage: storage}
}

// seed populates two series (Alpha Saga/Manga, Beta Quest/Manhwa) with chapters
// and a provider feed whose ProviderChapter rows carry display names so the
// detail test can assert a chapter name is populated from the provider feed.
func (env *testEnv) seed(ctx context.Context, t *testing.T) {
	t.Helper()

	manga := env.client.Series.Create().
		SetTitle("Alpha Saga").
		SetSlug("alpha-saga").
		SetCoverURL("https://example.test/alpha.jpg").
		SetCategory(entseries.CategoryManga).
		SaveX(ctx)
	env.mangaID = manga.ID

	num1, num2, pages := 1.0, 2.0, 20
	env.client.Chapter.Create().
		SetSeriesID(manga.ID).SetChapterKey("alpha-1").SetNumber(num1).
		SetState("downloaded").SetFilename("[mangadex][en] Alpha Saga 001.cbz").
		SetPageCount(pages).SaveX(ctx)
	env.client.Chapter.Create().
		SetSeriesID(manga.ID).SetChapterKey("alpha-2").SetNumber(num2).
		SetState("wanted").SaveX(ctx)

	prov := env.client.SeriesProvider.Create().
		SetSeriesID(manga.ID).SetProvider("mangadex").SetLanguage("en").SetImportance(10).
		SaveX(ctx)
	// ProviderChapter carries the chapter display name (the Chapter row does not).
	env.client.ProviderChapter.Create().
		SetSeriesProviderID(prov.ID).SetChapterKey("alpha-1").SetName("The Beginning").SaveX(ctx)

	manhwa := env.client.Series.Create().
		SetTitle("Beta Quest").
		SetSlug("beta-quest").
		SetCategory(entseries.CategoryManhwa).
		SaveX(ctx)
	env.manhwaID = manhwa.ID
	env.client.Chapter.Create().
		SetSeriesID(manhwa.ID).SetChapterKey("beta-1").SetState("downloaded").SaveX(ctx)
}

// do issues an authenticated request and returns the recorder.
func (env *testEnv) do(method, target, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("Authorization", "Bearer "+env.token)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	return rec
}

// doUnauth issues a request WITHOUT a valid Authorization header.
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

	rec := env.do(http.MethodGet, "/api/series", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("List: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	var got []seriessvc.SeriesSummaryDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("List: decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List: want 2 series, got %d", len(got))
	}
	// title-ASC ordering: Alpha Saga before Beta Quest.
	if got[0].Title != "Alpha Saga" || got[1].Title != "Beta Quest" {
		t.Fatalf("List: unexpected order: %q, %q", got[0].Title, got[1].Title)
	}
	// JSON shape: camelCase keys must be present (matches OpenAPI schema).
	if !strings.Contains(rec.Body.String(), `"chapterCounts"`) ||
		!strings.Contains(rec.Body.String(), `"coverUrl"`) {
		t.Fatalf("List: response missing camelCase keys: %s", rec.Body.String())
	}
}

func TestList_CategoryFilter(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)

	rec := env.do(http.MethodGet, "/api/series?category=Manhwa", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("List filter: want 200, got %d", rec.Code)
	}
	var got []seriessvc.SeriesSummaryDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("List filter: decode: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Beta Quest" {
		t.Fatalf("List filter: want only Beta Quest, got %+v", got)
	}
}

func TestList_BadCategory(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodGet, "/api/series?category=Bogus", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("List bad category: want 400, got %d", rec.Code)
	}
}

func TestList_BadPagination(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodGet, "/api/series?limit=-1", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("List bad pagination: want 400, got %d", rec.Code)
	}
}

func TestDetail_OK(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)

	rec := env.do(http.MethodGet, "/api/series/"+env.mangaID.String(), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("Detail: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got seriessvc.SeriesDetailDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("Detail: decode: %v", err)
	}
	if got.Title != "Alpha Saga" || len(got.Chapters) != 2 {
		t.Fatalf("Detail: unexpected: %+v", got)
	}
	// The chapter Name is sourced from the provider feed (ProviderChapter.name).
	var found bool
	for _, ch := range got.Chapters {
		if ch.ChapterKey == "alpha-1" && ch.Name == "The Beginning" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Detail: chapter name not populated from provider feed: %+v", got.Chapters)
	}
}

func TestDetail_NotFound(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodGet, "/api/series/"+uuid.New().String(), "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("Detail missing: want 404, got %d", rec.Code)
	}
}

func TestDetail_BadUUID(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodGet, "/api/series/not-a-uuid", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Detail bad uuid: want 400, got %d", rec.Code)
	}
}

func TestSetCategory_OK(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)

	// Seed a real on-disk folder so the recategorize exercises the disk move.
	const title = "Alpha Saga"
	oldDir := disk.SeriesDir(env.storage, "Manga", title)
	if err := os.MkdirAll(oldDir, 0o750); err != nil {
		t.Fatalf("mkdir old dir: %v", err)
	}
	if err := disk.WriteSidecar(oldDir, disk.Sidecar{Title: title, Category: "Manga"}); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}

	rec := env.do(http.MethodPatch, "/api/series/"+env.mangaID.String()+"/category", `{"category":"Manhwa"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("SetCategory: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got seriessvc.SeriesSummaryDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("SetCategory: decode: %v", err)
	}
	if got.Category != "Manhwa" {
		t.Fatalf("SetCategory: response category want Manhwa, got %q", got.Category)
	}

	// DB updated.
	reread := env.client.Series.GetX(ctx, env.mangaID)
	if reread.Category != entseries.CategoryManhwa {
		t.Fatalf("SetCategory: DB category want Manhwa, got %s", reread.Category)
	}
	// Disk moved: old gone, new exists.
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("SetCategory: old dir should be gone, stat err = %v", err)
	}
	newDir := disk.SeriesDir(env.storage, "Manhwa", title)
	if _, err := os.Stat(filepath.Join(newDir, "tsundoku.json")); err != nil {
		t.Fatalf("SetCategory: new dir sidecar should exist: %v", err)
	}
}

func TestSetCategory_Invalid(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)

	rec := env.do(http.MethodPatch, "/api/series/"+env.mangaID.String()+"/category", `{"category":"Bogus"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("SetCategory invalid: want 400, got %d", rec.Code)
	}
	// Nothing changed.
	reread := env.client.Series.GetX(ctx, env.mangaID)
	if reread.Category != entseries.CategoryManga {
		t.Fatalf("SetCategory invalid: category should be unchanged, got %s", reread.Category)
	}
}

func TestSetCategory_NotFound(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPatch, "/api/series/"+uuid.New().String()+"/category", `{"category":"Manhwa"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("SetCategory missing: want 404, got %d", rec.Code)
	}
}

func TestCategories_OK(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)

	rec := env.do(http.MethodGet, "/api/categories", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("Categories: want 200, got %d", rec.Code)
	}
	var got []seriessvc.CategoryCountDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("Categories: decode: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("Categories: want 5 entries, got %d", len(got))
	}
}

// TestAuthz_AllRoutesReject401 proves EVERY series route returns 401 when called
// without a valid Bearer token (mandatory authz proof).
func TestAuthz_AllRoutesReject401(t *testing.T) {
	env := newTestEnv(t)
	cases := []struct {
		method, target string
	}{
		{http.MethodGet, "/api/series"},
		{http.MethodGet, "/api/series/" + uuid.New().String()},
		{http.MethodPatch, "/api/series/" + uuid.New().String() + "/category"},
		{http.MethodGet, "/api/categories"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.target, func(t *testing.T) {
			rec := env.doUnauth(tc.method, tc.target)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s: want 401, got %d", tc.method, tc.target, rec.Code)
			}
		})
	}
}
