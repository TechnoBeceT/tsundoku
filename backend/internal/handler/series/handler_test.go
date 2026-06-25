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
	"time"

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
	e         *echo.Echo
	client    *ent.Client
	token     string
	storage   string
	mangaID   uuid.UUID
	manhwaID  uuid.UUID
	triggered *int
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
	svc := seriessvc.NewService(client, storage, 14)
	triggered := new(int)
	h := handler.NewHandler(svc, func() { *triggered++ })

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc))
	authed.GET("/series", h.List)
	authed.GET("/series/:id", h.Detail)
	authed.PATCH("/series/:id/category", h.SetCategory)
	authed.PATCH("/series/:id/monitored", h.SetMonitored)
	authed.PATCH("/series/:id/providers", h.ReorderProviders)
	authed.DELETE("/series/:id/providers/:providerId", h.RemoveProvider)
	authed.GET("/categories", h.Categories)
	authed.GET("/health", h.LibraryHealth)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}

	return &testEnv{e: e, client: client, token: token, storage: storage, triggered: triggered}
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
	id := uuid.New().String()
	cases := []struct {
		method, target string
	}{
		{http.MethodGet, "/api/series"},
		{http.MethodGet, "/api/series/" + id},
		{http.MethodPatch, "/api/series/" + id + "/category"},
		{http.MethodPatch, "/api/series/" + id + "/monitored"},
		{http.MethodPatch, "/api/series/" + id + "/providers"},
		{http.MethodDelete, "/api/series/" + id + "/providers/" + id},
		{http.MethodGet, "/api/categories"},
		{http.MethodGet, "/api/health"},
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

// TestSetMonitored_OK confirms PATCH /api/series/:id/monitored with monitored=false
// returns 200 and the updated SeriesSummaryDTO with monitored=false, and that the
// change is persisted (full round-trip per §16).
func TestSetMonitored_OK(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)

	rec := env.do(http.MethodPatch, "/api/series/"+env.mangaID.String()+"/monitored", `{"monitored":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("SetMonitored: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	var got seriessvc.SeriesSummaryDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("SetMonitored: decode: %v", err)
	}
	if got.Monitored {
		t.Fatalf("SetMonitored: response monitored want false, got true")
	}

	// Round-trip: DB must reflect the new value.
	reread := env.client.Series.GetX(ctx, env.mangaID)
	if reread.Monitored {
		t.Fatalf("SetMonitored: DB monitored want false, got true")
	}
}

// TestSetMonitored_NotFound checks that a missing series id yields 404.
func TestSetMonitored_NotFound(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPatch, "/api/series/"+uuid.New().String()+"/monitored", `{"monitored":false}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("SetMonitored missing: want 404, got %d", rec.Code)
	}
}

// TestSetMonitored_BadBody checks that a missing or malformed body yields 400.
func TestSetMonitored_BadBody(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)

	cases := []struct {
		name string
		body string
	}{
		{"empty body", ""},
		{"non-bool value", `{"monitored":"yes"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := env.do(http.MethodPatch, "/api/series/"+env.mangaID.String()+"/monitored", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("SetMonitored bad body (%s): want 400, got %d (%s)", tc.name, rec.Code, rec.Body.String())
			}
		})
	}
}

// firstProviderID fetches the series detail and returns the first SeriesProvider ID.
// It skips the test if no provider is present.
func firstProviderID(t *testing.T, env *testEnv, seriesID string) string {
	t.Helper()
	rec := env.do(http.MethodGet, "/api/series/"+seriesID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("firstProviderID: get series: want 200, got %d", rec.Code)
	}
	var detail seriessvc.SeriesDetailDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("firstProviderID: decode: %v", err)
	}
	if len(detail.Providers) == 0 {
		t.Fatalf("firstProviderID: no provider seeded for manga series")
	}
	return detail.Providers[0].ID
}

// assertProviderImportance checks that the SeriesDetailDTO response body contains
// the given provider id with the expected importance value.
func assertProviderImportance(t *testing.T, body []byte, provID string, want int) {
	t.Helper()
	var got seriessvc.SeriesDetailDTO
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("assertProviderImportance: decode: %v", err)
	}
	if len(got.Providers) == 0 {
		t.Fatal("assertProviderImportance: no providers in response")
	}
	for _, p := range got.Providers {
		if p.ID == provID && p.Importance == want {
			return
		}
	}
	t.Fatalf("assertProviderImportance: provider %s importance want %d in %+v", provID, want, got.Providers)
}

// TestReorderProviders_OK confirms PATCH /api/series/:id/providers updates provider
// importance and returns the updated SeriesDetailDTO with the new importance value
// (full round-trip per §16).
func TestReorderProviders_OK(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)

	provID := firstProviderID(t, env, env.mangaID.String())

	body := `{"providers":[{"id":"` + provID + `","importance":5}]}`
	rec := env.do(http.MethodPatch, "/api/series/"+env.mangaID.String()+"/providers", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("ReorderProviders: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	assertProviderImportance(t, rec.Body.Bytes(), provID, 5)

	// DB round-trip: the importance must be persisted, not just echoed in the response.
	provUUID, err := uuid.Parse(provID)
	if err != nil {
		t.Fatalf("ReorderProviders: parse provID: %v", err)
	}
	dbProv := env.client.SeriesProvider.GetX(ctx, provUUID)
	if dbProv.Importance != 5 {
		t.Fatalf("ReorderProviders: DB importance want 5, got %d", dbProv.Importance)
	}
}

// TestReorderProviders_WrongSeries checks that supplying a provider id from
// another series yields 400 (ErrProviderNotInSeries → 400).
func TestReorderProviders_WrongSeries(t *testing.T) {
	env := newTestEnv(t)
	env.seed(context.Background(), t)

	// Use the manga series' provider id against the manhwa series.
	provID := firstProviderID(t, env, env.mangaID.String())

	body := `{"providers":[{"id":"` + provID + `","importance":5}]}`
	rec := env.do(http.MethodPatch, "/api/series/"+env.manhwaID.String()+"/providers", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ReorderProviders wrong-series: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestReorderProviders_BadUUID checks that a malformed provider id yields 400.
func TestReorderProviders_BadUUID(t *testing.T) {
	env := newTestEnv(t)
	env.seed(context.Background(), t)

	body := `{"providers":[{"id":"not-a-uuid","importance":5}]}`
	rec := env.do(http.MethodPatch, "/api/series/"+env.mangaID.String()+"/providers", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ReorderProviders bad uuid: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestReorderProviders_NotFound checks that a missing series id yields 404.
func TestReorderProviders_NotFound(t *testing.T) {
	env := newTestEnv(t)
	body := `{"providers":[{"id":"` + uuid.New().String() + `","importance":5}]}`
	rec := env.do(http.MethodPatch, "/api/series/"+uuid.New().String()+"/providers", body)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("ReorderProviders missing: want 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestRemoveProvider_OK removes a provider and asserts 200 + a SeriesDetail that
// no longer lists it.
func TestRemoveProvider_OK(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)
	provID := firstProviderID(t, env, env.mangaID.String())

	rec := env.do(http.MethodDelete, "/api/series/"+env.mangaID.String()+"/providers/"+provID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var detail seriessvc.SeriesDetailDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, p := range detail.Providers {
		if p.ID == provID {
			t.Errorf("removed provider %s still present in detail", provID)
		}
	}
}

// TestRemoveProvider_RequiresOwner asserts the route is behind RequireOwner.
func TestRemoveProvider_RequiresOwner(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)
	provID := firstProviderID(t, env, env.mangaID.String())

	// env.do attaches the owner token; here we send WITHOUT it.
	req := httptest.NewRequest(http.MethodDelete, "/api/series/"+env.mangaID.String()+"/providers/"+provID, nil)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

// TestRemoveProvider_BadIDs asserts a malformed series id and a malformed
// provider id each yield a 400 whose body names the OFFENDING param — proving
// validateID's subject label is threaded correctly (a malformed providerId must
// not be mislabelled "invalid series id").
func TestRemoveProvider_BadIDs(t *testing.T) {
	env := newTestEnv(t)
	good := uuid.NewString()
	cases := []struct {
		name    string
		target  string
		wantMsg string
	}{
		{"bad series id", "/api/series/not-a-uuid/providers/" + good, "invalid series id"},
		{"bad provider id", "/api/series/" + good + "/providers/not-a-uuid", "invalid provider id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := env.do(http.MethodDelete, tc.target, "")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantMsg) {
				t.Errorf("body = %s, want message %q", rec.Body.String(), tc.wantMsg)
			}
		})
	}
}

// TestRemoveProvider_UnknownSeries asserts a 404 for a valid-but-missing series.
func TestRemoveProvider_UnknownSeries(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodDelete, "/api/series/"+uuid.NewString()+"/providers/"+uuid.NewString(), "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
}

// TestReorderProviders_TriggersConvergeOnSuccess asserts ReorderProviders fires
// the auto-converge trigger exactly once on success and never on a failure path.
func TestReorderProviders_TriggersConvergeOnSuccess(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)

	provID := firstProviderID(t, env, env.mangaID.String())

	body := `{"providers":[{"id":"` + provID + `","importance":7}]}`
	rec := env.do(http.MethodPatch, "/api/series/"+env.mangaID.String()+"/providers", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("ReorderProviders trigger: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if *env.triggered != 1 {
		t.Errorf("trigger fired %d times, want 1", *env.triggered)
	}

	// Failure path: invalid series id is rejected before the service runs.
	*env.triggered = 0
	rec = env.do(http.MethodPatch, "/api/series/not-a-uuid/providers", body)
	if rec.Code == http.StatusOK {
		t.Fatal("invalid-uuid series id must not succeed")
	}
	if *env.triggered != 0 {
		t.Errorf("trigger fired %d times on failure, want 0", *env.triggered)
	}
}

// TestLibraryHealthEndpoint seeds a 2-source series where one source is stale
// (last chapter > staleGraceDays old) and asserts GET /api/health returns 200
// with that series listed with its one stale source.
func TestLibraryHealthEndpoint(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	// Seed a 2-source series with a stale source via the ent client directly.
	old := time.Now().UTC().AddDate(0, 0, -40)
	recent := time.Now().UTC().AddDate(0, 0, -1)
	s := env.client.Series.Create().SetTitle("Sick").SetSlug("sick").SetCategory(entseries.CategoryManga).SaveX(ctx)
	a := env.client.SeriesProvider.Create().SetSeriesID(s.ID).SetProvider("a").SetImportance(20).SaveX(ctx)
	b := env.client.SeriesProvider.Create().SetSeriesID(s.ID).SetProvider("b").SetImportance(10).SaveX(ctx)
	for _, k := range []struct {
		key string
		n   float64
	}{{"c1", 1}, {"c2", 2}} {
		env.client.Chapter.Create().SetSeriesID(s.ID).SetChapterKey(k.key).SetNumber(k.n).SetState("downloaded").SaveX(ctx)
		env.client.ProviderChapter.Create().SetSeriesProviderID(a.ID).SetChapterKey(k.key).SetNumber(k.n).SetProviderUploadDate(recent).SaveX(ctx)
	}
	env.client.ProviderChapter.Create().SetSeriesProviderID(b.ID).SetChapterKey("c1").SetNumber(1).SetProviderUploadDate(old).SaveX(ctx)

	rec := env.do(http.MethodGet, "/api/health", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/health = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Series []struct {
			ID      string `json:"id"`
			Sources []struct {
				Health string `json:"health"`
			} `json:"sources"`
		} `json:"series"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Series) != 1 || len(body.Series[0].Sources) != 1 || body.Series[0].Sources[0].Health != "stale" {
		t.Fatalf("body = %+v, want one series with one stale source", body)
	}
}

// TestLibraryHealth_EmptyLibrary asserts GET /api/health on an empty library
// returns 200 with {"series":[]} (a non-null empty array, not null — see §series).
func TestLibraryHealth_EmptyLibrary(t *testing.T) {
	env := newTestEnv(t)

	rec := env.do(http.MethodGet, "/api/health", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/health empty = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body seriessvc.LibraryHealthDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Series == nil {
		t.Fatal("empty library: Series must be a non-nil slice (not null in JSON), got nil")
	}
	if len(body.Series) != 0 {
		t.Fatalf("empty library: want 0 series, got %d", len(body.Series))
	}
}
