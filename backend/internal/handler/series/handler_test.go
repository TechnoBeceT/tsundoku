// Package series_test exercises the library HTTP handlers end-to-end through a
// real Echo instance (with the RequireOwner middleware wired) against an
// ephemeral PostgreSQL instance (testdb). Tests require Docker.
package series_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	handler "github.com/technobecet/tsundoku/internal/handler/series"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	seriessvc "github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// catID resolves a seeded default category's id by name (testdb seeds them).
func catID(ctx context.Context, db *ent.Client, name string) uuid.UUID {
	id, err := category.IDByName(ctx, db, name)
	if err != nil {
		panic(err)
	}
	return id
}

// fakeSuwayomiClient is a minimal suwayomi.Client implementation for tests.
// Only PageBytes is exercised by the cover handlers; all other methods return
// zero values so they can be implemented with the interface without noise.
type fakeSuwayomiClient struct {
	// pageBytes, when set, is called by PageBytes instead of the default stub.
	pageBytes func(ctx context.Context, url string) ([]byte, string, error)
}

func (f *fakeSuwayomiClient) Sources(ctx context.Context) ([]suwayomi.Source, error) {
	return nil, nil
}
func (f *fakeSuwayomiClient) Search(ctx context.Context, sourceID, query string) ([]suwayomi.Manga, error) {
	return nil, nil
}
func (f *fakeSuwayomiClient) Browse(ctx context.Context, sourceID string, t suwayomi.BrowseType, page int) (suwayomi.BrowseResult, error) {
	return suwayomi.BrowseResult{}, nil
}
func (f *fakeSuwayomiClient) FetchChapters(ctx context.Context, mangaID int) ([]suwayomi.Chapter, error) {
	return nil, nil
}
func (f *fakeSuwayomiClient) MangaChapters(ctx context.Context, mangaID int) ([]suwayomi.Chapter, error) {
	return nil, nil
}
func (f *fakeSuwayomiClient) ChapterPages(ctx context.Context, chapterID int) ([]string, error) {
	return nil, nil
}
func (f *fakeSuwayomiClient) MangaMeta(ctx context.Context, mangaID int) (suwayomi.Manga, error) {
	return suwayomi.Manga{}, nil
}
func (f *fakeSuwayomiClient) FetchMangaDetails(ctx context.Context, mangaID int) (suwayomi.Manga, error) {
	return suwayomi.Manga{}, nil
}
func (f *fakeSuwayomiClient) PageBytes(ctx context.Context, pageURL string) ([]byte, string, error) {
	if f.pageBytes != nil {
		return f.pageBytes(ctx, pageURL)
	}
	return nil, "", errors.New("PageBytes: not configured")
}
func (f *fakeSuwayomiClient) ServerSettings(ctx context.Context) (suwayomi.SuwayomiSettings, error) {
	return suwayomi.SuwayomiSettings{}, nil
}
func (f *fakeSuwayomiClient) SetServerSettings(ctx context.Context, patch suwayomi.SuwayomiSettingsPatch) error {
	return nil
}
func (f *fakeSuwayomiClient) Extensions(ctx context.Context) ([]suwayomi.Extension, error) {
	return nil, nil
}
func (f *fakeSuwayomiClient) SetExtensionState(ctx context.Context, pkgName string, action suwayomi.ExtensionAction) error {
	return nil
}
func (f *fakeSuwayomiClient) FetchExtensions(ctx context.Context) ([]suwayomi.Extension, error) {
	return nil, nil
}
func (f *fakeSuwayomiClient) ExtensionRepos(ctx context.Context) ([]string, error) {
	return nil, nil
}
func (f *fakeSuwayomiClient) SetExtensionRepos(ctx context.Context, repos []string) error {
	return nil
}
func (f *fakeSuwayomiClient) SourcePreferences(ctx context.Context, sourceID string) ([]suwayomi.SourcePreference, error) {
	return nil, nil
}
func (f *fakeSuwayomiClient) SetSourcePreference(ctx context.Context, sourceID string, position int, value suwayomi.PreferenceValue) ([]suwayomi.SourcePreference, error) {
	return nil, nil
}
func (f *fakeSuwayomiClient) ExtensionSources(ctx context.Context, pkgName string) ([]suwayomi.Source, error) {
	return nil, nil
}
func (f *fakeSuwayomiClient) SetSourceEnabled(ctx context.Context, sourceID string, enabled bool) error {
	return nil
}

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
	sw        *fakeSuwayomiClient
}

// newTestEnv stands up a fully-wired Echo: the series routes registered behind
// RequireOwner (so the 401 proofs exercise the real middleware), a series.Service
// over a fresh testdb client and a t.TempDir() storage root, and a valid owner
// Bearer token minted from the same auth secret. A fakeSuwayomiClient is wired
// for the cover proxy endpoints; set env.sw.pageBytes to control PageBytes behaviour.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	client := testdb.New(t)
	storage := t.TempDir()
	authSvc := auth.NewService(testSecret)
	svc := seriessvc.NewService(client, storage, 14)
	triggered := new(int)
	sw := &fakeSuwayomiClient{}
	h := handler.NewHandler(svc, func() { *triggered++ }, sw)

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.GET("/series", h.List)
	authed.GET("/series/:id", h.Detail)
	authed.PATCH("/series/:id/category", h.SetCategory)
	authed.PATCH("/series/:id/monitored", h.SetMonitored)
	authed.PATCH("/series/:id/completed", h.SetCompleted)
	authed.PATCH("/series/:id/providers", h.ReorderProviders)
	authed.DELETE("/series/:id/providers/:providerId", h.RemoveProvider)
	authed.DELETE("/series/:id", h.DeleteSeries)
	authed.GET("/series/:id/cover", h.SeriesCover)
	authed.GET("/series/:id/providers/:providerId/cover", h.ProviderCover)
	authed.PATCH("/series/:id/metadata-source", h.SetMetadataSource)
	authed.GET("/health", h.LibraryHealth)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}

	return &testEnv{e: e, client: client, token: token, storage: storage, triggered: triggered, sw: sw}
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
		SetCategoryID(catID(ctx, env.client, "Manga")).
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
		SetCategoryID(catID(ctx, env.client, "Manhwa")).
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

func TestList_SetsTotalCountHeader(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t) // seeds 2 series: Alpha Saga (Manga) + Beta Quest (Manhwa)

	// Unfiltered: header must equal the total series count.
	rec := env.do(http.MethodGet, "/api/series", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("List: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count: want %q, got %q", "2", got)
	}

	// Filtered by category: header must reflect the filtered count, not the grand total.
	rec = env.do(http.MethodGet, "/api/series?category=Manga", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("List filtered: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count filtered: want %q, got %q", "1", got)
	}
}

func TestList_UnknownCategory(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)
	// Categories are user-defined; filtering by a name that matches no series is
	// not an error — it returns an empty page (200).
	rec := env.do(http.MethodGet, "/api/series?category=No+Such+Category", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("List unknown category: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got []seriessvc.SeriesSummaryDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("List unknown category: want empty, got %+v", got)
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

	manhwaID := catID(ctx, env.client, "Manhwa")
	rec := env.do(http.MethodPatch, "/api/series/"+env.mangaID.String()+"/category", `{"categoryId":"`+manhwaID.String()+`"}`)
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
	if name := env.client.Series.GetX(ctx, env.mangaID).QueryCategory().OnlyX(ctx).Name; name != "Manhwa" {
		t.Fatalf("SetCategory: DB category want Manhwa, got %s", name)
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

func TestSetCategory_UnknownCategory(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)

	// A well-formed but nonexistent category id is a bad request (400).
	rec := env.do(http.MethodPatch, "/api/series/"+env.mangaID.String()+"/category", `{"categoryId":"`+uuid.New().String()+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("SetCategory unknown category: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	// Nothing changed.
	if name := env.client.Series.GetX(ctx, env.mangaID).QueryCategory().OnlyX(ctx).Name; name != "Manga" {
		t.Fatalf("SetCategory unknown: category should be unchanged, got %s", name)
	}
}

func TestSetCategory_BadBody(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)

	// A malformed (non-UUID) categoryId is rejected at validation (400).
	rec := env.do(http.MethodPatch, "/api/series/"+env.mangaID.String()+"/category", `{"categoryId":"not-a-uuid"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("SetCategory bad body: want 400, got %d", rec.Code)
	}
}

func TestSetCategory_NotFound(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	manhwaID := catID(ctx, env.client, "Manhwa")
	rec := env.do(http.MethodPatch, "/api/series/"+uuid.New().String()+"/category", `{"categoryId":"`+manhwaID.String()+`"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("SetCategory missing: want 404, got %d", rec.Code)
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
		{http.MethodPatch, "/api/series/" + id + "/completed"},
		{http.MethodPatch, "/api/series/" + id + "/providers"},
		{http.MethodDelete, "/api/series/" + id + "/providers/" + id},
		{http.MethodDelete, "/api/series/" + id + "?deleteFiles=true"},
		{http.MethodGet, "/api/series/" + id + "/cover"},
		{http.MethodGet, "/api/series/" + id + "/providers/" + id + "/cover"},
		{http.MethodPatch, "/api/series/" + id + "/metadata-source"},
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
	s := env.client.Series.Create().SetTitle("Sick").SetSlug("sick").SetCategoryID(catID(ctx, env.client, "Manga")).SaveX(ctx)
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

// TestSetCompleted_OK proves PATCH /api/series/:id/completed persists and returns the
// completed flag (§16 round-trip).
func TestSetCompleted_OK(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)

	rec := env.do(http.MethodPatch, "/api/series/"+env.mangaID.String()+"/completed", `{"completed":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("SetCompleted: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	var got seriessvc.SeriesSummaryDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("SetCompleted: decode: %v", err)
	}
	if !got.Completed {
		t.Fatalf("SetCompleted: response completed want true, got false")
	}

	// Round-trip: DB must reflect the new value.
	reread := env.client.Series.GetX(ctx, env.mangaID)
	if !reread.Completed {
		t.Fatalf("SetCompleted: DB completed want true, got false")
	}
}

// TestSetCompleted_NotFound checks that a missing series id yields 404.
func TestSetCompleted_NotFound(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPatch, "/api/series/"+uuid.New().String()+"/completed", `{"completed":false}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("SetCompleted missing: want 404, got %d", rec.Code)
	}
}

// TestSetCompleted_BadBody checks that a missing completed field yields 400.
func TestSetCompleted_BadBody(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)

	cases := []struct {
		name string
		body string
	}{
		{"empty body", ""},
		{"non-bool value", `{"completed":"yes"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := env.do(http.MethodPatch, "/api/series/"+env.mangaID.String()+"/completed", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("SetCompleted bad body (%s): want 400, got %d (%s)", tc.name, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestSetCompleted_BadID proves a malformed id yields 400 "invalid series id".
func TestSetCompleted_BadID(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPatch, "/api/series/not-a-uuid/completed", `{"completed":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("SetCompleted bad id: want 400, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid series id") {
		t.Fatalf("SetCompleted bad id: body = %s, want 'invalid series id'", rec.Body.String())
	}
}

// TestSetMonitored_CompletedPreserved is the §16 regression proof for the
// detailToSummary mapper: if a series has completed=true and the owner patches
// its monitored flag, the response must carry completed=true (not silently
// zero-out the field). This test would FAIL against the old inline literal in
// SetMonitored which omitted the Completed field.
func TestSetMonitored_CompletedPreserved(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)

	// Mark the series as completed first so we can assert it survives the
	// SetMonitored round-trip.
	rec := env.do(http.MethodPatch, "/api/series/"+env.mangaID.String()+"/completed", `{"completed":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup SetCompleted: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	// Now toggle monitored — this is the call that used to drop completed.
	rec = env.do(http.MethodPatch, "/api/series/"+env.mangaID.String()+"/monitored", `{"monitored":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("SetMonitored: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	var got seriessvc.SeriesSummaryDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("SetMonitored: decode: %v", err)
	}
	if !got.Completed {
		t.Fatalf("SetMonitored on completed series: response completed want true, got false (§16 dropped-field bug)")
	}
	if got.Monitored {
		t.Fatalf("SetMonitored: response monitored want false, got true")
	}
}

// TestSetCategory_CompletedPreserved is the §16 regression proof for the
// detailToSummary mapper: if a series has completed=true and the owner changes
// its category, the response must carry completed=true (not silently zero it).
// This test would FAIL against the old inline literal in SetCategory.
func TestSetCategory_CompletedPreserved(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)

	// Prepare the on-disk folder so SetCategory can move it.
	const title = "Alpha Saga"
	oldDir := disk.SeriesDir(env.storage, "Manga", title)
	if err := os.MkdirAll(oldDir, 0o750); err != nil {
		t.Fatalf("mkdir old dir: %v", err)
	}
	if err := disk.WriteSidecar(oldDir, disk.Sidecar{Title: title, Category: "Manga"}); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}

	// Mark the series as completed.
	rec := env.do(http.MethodPatch, "/api/series/"+env.mangaID.String()+"/completed", `{"completed":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup SetCompleted: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	// Now change the category — this is the call that used to drop completed.
	manhwaID := catID(ctx, env.client, "Manhwa")
	rec = env.do(http.MethodPatch, "/api/series/"+env.mangaID.String()+"/category", `{"categoryId":"`+manhwaID.String()+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("SetCategory: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	var got seriessvc.SeriesSummaryDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("SetCategory: decode: %v", err)
	}
	if !got.Completed {
		t.Fatalf("SetCategory on completed series: response completed want true, got false (§16 dropped-field bug)")
	}
	if got.Category != "Manhwa" {
		t.Fatalf("SetCategory: response category want Manhwa, got %q", got.Category)
	}
}

// TestDeleteSeries_OK_DeleteFiles proves DELETE /api/series/:id?deleteFiles=true
// returns 204 and that the series row is gone from the DB (§16 round-trip).
func TestDeleteSeries_OK_DeleteFiles(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)

	rec := env.do(http.MethodDelete, "/api/series/"+env.mangaID.String()+"?deleteFiles=true", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DeleteSeries deleteFiles=true: want 204, got %d (%s)", rec.Code, rec.Body.String())
	}

	// §16 round-trip: the series must no longer exist in the DB.
	_, err := env.client.Series.Get(ctx, env.mangaID)
	if err == nil {
		t.Fatal("DeleteSeries deleteFiles=true: series row still present in DB after delete")
	}
}

// TestDeleteSeries_OK_KeepFiles proves DELETE /api/series/:id?deleteFiles=false
// returns 204 and removes the series row even when file deletion is skipped.
func TestDeleteSeries_OK_KeepFiles(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)

	rec := env.do(http.MethodDelete, "/api/series/"+env.mangaID.String()+"?deleteFiles=false", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DeleteSeries deleteFiles=false: want 204, got %d (%s)", rec.Code, rec.Body.String())
	}

	// §16 round-trip: the series row must be gone.
	_, err := env.client.Series.Get(ctx, env.mangaID)
	if err == nil {
		t.Fatal("DeleteSeries deleteFiles=false: series row still present in DB after delete")
	}
}

// TestDeleteSeries_MissingDeleteFiles proves that omitting deleteFiles yields 400
// "deleteFiles is required" (no default for an irreversible action).
func TestDeleteSeries_MissingDeleteFiles(t *testing.T) {
	env := newTestEnv(t)
	env.seed(context.Background(), t)

	rec := env.do(http.MethodDelete, "/api/series/"+env.mangaID.String(), "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("DeleteSeries missing param: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "deleteFiles is required") {
		t.Errorf("body = %s, want message 'deleteFiles is required'", rec.Body.String())
	}
}

// TestDeleteSeries_InvalidDeleteFiles proves that a non-boolean deleteFiles value
// yields 400 "deleteFiles must be true or false".
func TestDeleteSeries_InvalidDeleteFiles(t *testing.T) {
	env := newTestEnv(t)
	env.seed(context.Background(), t)

	rec := env.do(http.MethodDelete, "/api/series/"+env.mangaID.String()+"?deleteFiles=maybe", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("DeleteSeries invalid param: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "deleteFiles must be true or false") {
		t.Errorf("body = %s, want message 'deleteFiles must be true or false'", rec.Body.String())
	}
}

// TestDeleteSeries_BadID proves a malformed :id yields 400 "invalid series id".
func TestDeleteSeries_BadID(t *testing.T) {
	env := newTestEnv(t)

	rec := env.do(http.MethodDelete, "/api/series/not-a-uuid?deleteFiles=true", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("DeleteSeries bad id: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid series id") {
		t.Errorf("body = %s, want message 'invalid series id'", rec.Body.String())
	}
}

// TestDeleteSeries_NotFound proves an unknown but valid UUID yields 404.
func TestDeleteSeries_NotFound(t *testing.T) {
	env := newTestEnv(t)

	rec := env.do(http.MethodDelete, "/api/series/"+uuid.New().String()+"?deleteFiles=true", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("DeleteSeries unknown id: want 404, got %d (%s)", rec.Code, rec.Body.String())
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

// seedWithCover seeds the manga series with a provider that has a cover_url set.
// Returns the series id and the provider id.
func seedWithCover(ctx context.Context, t *testing.T, env *testEnv, coverURL string) (seriesID, providerID uuid.UUID) {
	t.Helper()
	s := env.client.Series.Create().
		SetTitle("Cover Test").SetSlug("cover-test").SetCategoryID(catID(ctx, env.client, "Manga")).
		SaveX(ctx)
	p := env.client.SeriesProvider.Create().
		SetSeriesID(s.ID).SetProvider("testprov").SetImportance(10).SetCoverURL(coverURL).
		SaveX(ctx)
	return s.ID, p.ID
}

// TestSeriesCover_OK seeds a series whose metadata provider has a cover_url, wires
// the fake client to return PNG bytes, and asserts GET /api/series/:id/cover
// returns 200 with Content-Type image/png and the correct body.
func TestSeriesCover_OK(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	pngBytes := []byte{0x89, 0x50, 0x4E, 0x47} // PNG magic
	env.sw.pageBytes = func(_ context.Context, _ string) ([]byte, string, error) {
		return pngBytes, "png", nil
	}

	seriesID, _ := seedWithCover(ctx, t, env, "/api/v1/manga/1/cover")
	rec := env.do(http.MethodGet, "/api/series/"+seriesID.String()+"/cover", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("SeriesCover OK: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/png") {
		t.Errorf("SeriesCover OK: Content-Type want image/png, got %q", ct)
	}
	if string(rec.Body.Bytes()) != string(pngBytes) {
		t.Errorf("SeriesCover OK: body mismatch")
	}
}

// TestSeriesCover_NoCover asserts GET /api/series/:id/cover returns 404 when the
// series has no provider with a cover_url (ErrNoCover → 404).
func TestSeriesCover_NoCover(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)

	// Beta Quest has no cover_url on its provider (not seeded).
	rec := env.do(http.MethodGet, "/api/series/"+env.manhwaID.String()+"/cover", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("SeriesCover NoCover: want 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestSeriesCover_NotFound asserts a missing series id yields 404.
func TestSeriesCover_NotFound(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodGet, "/api/series/"+uuid.New().String()+"/cover", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("SeriesCover NotFound: want 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestSeriesCover_PageBytesFail asserts a Suwayomi fetch failure yields 502.
func TestSeriesCover_PageBytesFail(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.sw.pageBytes = func(_ context.Context, _ string) ([]byte, string, error) {
		return nil, "", errors.New("suwayomi down")
	}
	seriesID, _ := seedWithCover(ctx, t, env, "/api/v1/manga/1/cover")
	rec := env.do(http.MethodGet, "/api/series/"+seriesID.String()+"/cover", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("SeriesCover PageBytesFail: want 502, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestProviderCover_OK asserts GET /api/series/:id/providers/:providerId/cover
// returns 200 with the correct Content-Type and body.
func TestProviderCover_OK(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	pngBytes := []byte{0x89, 0x50, 0x4E, 0x47}
	env.sw.pageBytes = func(_ context.Context, _ string) ([]byte, string, error) {
		return pngBytes, "png", nil
	}

	seriesID, provID := seedWithCover(ctx, t, env, "/api/v1/manga/2/cover")
	target := "/api/series/" + seriesID.String() + "/providers/" + provID.String() + "/cover"
	rec := env.do(http.MethodGet, target, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("ProviderCover OK: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/png") {
		t.Errorf("ProviderCover OK: Content-Type want image/png, got %q", ct)
	}
}

// TestProviderCover_NotInSeries asserts a provider from a different series yields 400.
func TestProviderCover_NotInSeries(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)

	// Use a provider ID that does not belong to env.manhwaID.
	mangaProvID := firstProviderID(t, env, env.mangaID.String())
	target := "/api/series/" + env.manhwaID.String() + "/providers/" + mangaProvID + "/cover"
	rec := env.do(http.MethodGet, target, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ProviderCover NotInSeries: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestSetMetadataSource_OK pins the metadata source to a provider and asserts the
// response SeriesDetailDTO has that provider with isMetadataSource:true (§16).
func TestSetMetadataSource_OK(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)

	provID := firstProviderID(t, env, env.mangaID.String())
	body := `{"providerId":"` + provID + `"}`
	rec := env.do(http.MethodPatch, "/api/series/"+env.mangaID.String()+"/metadata-source", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("SetMetadataSource OK: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	var got seriessvc.SeriesDetailDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("SetMetadataSource OK: decode: %v", err)
	}
	var found bool
	for _, p := range got.Providers {
		if p.ID == provID && p.IsMetadataSource {
			found = true
		}
	}
	if !found {
		t.Fatalf("SetMetadataSource OK: provider %s not marked isMetadataSource:true in %+v", provID, got.Providers)
	}
}

// TestSetMetadataSource_Null resets the metadata source pin and asserts the
// response is 200 (auto-resolution resumes).
func TestSetMetadataSource_Null(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)

	// First pin it.
	provID := firstProviderID(t, env, env.mangaID.String())
	env.do(http.MethodPatch, "/api/series/"+env.mangaID.String()+"/metadata-source", `{"providerId":"`+provID+`"}`)

	// Then reset with null.
	rec := env.do(http.MethodPatch, "/api/series/"+env.mangaID.String()+"/metadata-source", `{"providerId":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("SetMetadataSource Null: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestSetMetadataSource_BadBody asserts a malformed providerId yields 400.
func TestSetMetadataSource_BadBody(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)

	rec := env.do(http.MethodPatch, "/api/series/"+env.mangaID.String()+"/metadata-source", `{"providerId":"not-a-uuid"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("SetMetadataSource BadBody: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestSetMetadataSource_NotFound asserts a missing series id yields 404.
func TestSetMetadataSource_NotFound(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPatch, "/api/series/"+uuid.New().String()+"/metadata-source", `{"providerId":null}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("SetMetadataSource NotFound: want 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}
