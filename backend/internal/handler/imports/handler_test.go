// Package imports_test exercises the imports HTTP handlers end-to-end through a
// real Echo instance (with RequireOwner middleware wired) against an ephemeral
// PostgreSQL instance (testdb). Tests require Docker.
package imports_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	handler "github.com/technobecet/tsundoku/internal/handler/imports"
	"github.com/technobecet/tsundoku/internal/imports"
	"github.com/technobecet/tsundoku/internal/ingest"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	seriessvc "github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sourceengine"
)

const testSecret = "imports-handler-test-secret"

// fakeEngineClient is a local in-test implementation of sourceengine.Client —
// it backs the imports.Service (via ingest.NewIngest) in every handler test.
//
// Results/errors are keyed by whatever addressing the real client uses:
// sources by their numeric ID, chapters/details by the source-relative manga
// URL (P2 Suwayomi-removal — the engine host is URL-addressed, not
// manga-id-addressed).
type fakeEngineClient struct {
	sources    []sourceengine.Source
	sourcesErr error

	// searchResults / searchErrs back Search, keyed by sourceID.
	searchResults map[int64]sourceengine.SearchResult
	searchErrs    map[int64]error

	// popularResults / latestResults back Popular/Latest respectively, keyed
	// by sourceID — kept SEPARATE (unlike the retired suwayomi.Client's single
	// Browse(type) call) so a test can configure different Popular vs Latest
	// pages for the same source.
	popularResults map[int64]sourceengine.SearchResult
	latestResults  map[int64]sourceengine.SearchResult

	// chaptersByURL / chapterErrs back Chapters, keyed by the source-relative
	// manga URL.
	chaptersByURL map[string][]sourceengine.Chapter
	chapterErrs   map[string]error

	// detailsByURL / detailsErrs back MangaDetails, keyed by the
	// source-relative manga URL.
	detailsByURL map[string]sourceengine.MangaDetails
	detailsErrs  map[string]error
}

func (f *fakeEngineClient) Health(context.Context) (sourceengine.Health, error) {
	return sourceengine.Health{}, nil
}

func (f *fakeEngineClient) Search(_ context.Context, sourceID int64, _ string, _ int) (sourceengine.SearchResult, error) {
	if f.searchErrs != nil {
		if err, ok := f.searchErrs[sourceID]; ok {
			return sourceengine.SearchResult{}, err
		}
	}
	if f.searchResults != nil {
		if res, ok := f.searchResults[sourceID]; ok {
			return res, nil
		}
	}
	return sourceengine.SearchResult{}, nil
}

func (f *fakeEngineClient) Popular(_ context.Context, sourceID int64, _ int) (sourceengine.SearchResult, error) {
	if f.popularResults != nil {
		if res, ok := f.popularResults[sourceID]; ok {
			return res, nil
		}
	}
	return sourceengine.SearchResult{}, nil
}

func (f *fakeEngineClient) Latest(_ context.Context, sourceID int64, _ int) (sourceengine.SearchResult, error) {
	if f.latestResults != nil {
		if res, ok := f.latestResults[sourceID]; ok {
			return res, nil
		}
	}
	return sourceengine.SearchResult{}, nil
}

func (f *fakeEngineClient) MangaDetails(_ context.Context, _ int64, url string) (sourceengine.MangaDetails, error) {
	if f.detailsErrs != nil {
		if err, ok := f.detailsErrs[url]; ok {
			return sourceengine.MangaDetails{}, err
		}
	}
	if f.detailsByURL != nil {
		if m, ok := f.detailsByURL[url]; ok {
			return m, nil
		}
	}
	return sourceengine.MangaDetails{}, nil
}

func (f *fakeEngineClient) Chapters(_ context.Context, _ int64, url string) ([]sourceengine.Chapter, error) {
	if f.chapterErrs != nil {
		if err, ok := f.chapterErrs[url]; ok {
			return nil, err
		}
	}
	if f.chaptersByURL != nil {
		return f.chaptersByURL[url], nil
	}
	return nil, nil
}

func (f *fakeEngineClient) Pages(context.Context, int64, string) ([]sourceengine.Page, error) {
	return nil, nil
}

func (f *fakeEngineClient) Image(context.Context, int64, string, string) ([]byte, string, error) {
	return nil, "", nil
}

func (f *fakeEngineClient) Sources(context.Context) ([]sourceengine.Source, error) {
	return f.sources, f.sourcesErr
}

func (f *fakeEngineClient) Preferences(context.Context, int64) ([]sourceengine.Preference, error) {
	return nil, nil
}

func (f *fakeEngineClient) SetPreferences(context.Context, int64, map[string]any) ([]sourceengine.Preference, error) {
	return nil, nil
}

func (f *fakeEngineClient) Extensions(context.Context) ([]sourceengine.Extension, error) {
	return nil, nil
}

func (f *fakeEngineClient) InstallExtension(context.Context, string, string) ([]sourceengine.Extension, error) {
	return nil, nil
}

func (f *fakeEngineClient) RefreshExtensions(context.Context) ([]sourceengine.Extension, error) {
	return nil, nil
}

func (f *fakeEngineClient) UpdateExtension(context.Context, string) ([]sourceengine.Extension, error) {
	return nil, nil
}

func (f *fakeEngineClient) UninstallExtension(context.Context, string) ([]sourceengine.Extension, error) {
	return nil, nil
}

func (f *fakeEngineClient) Repos(context.Context) ([]string, error) { return nil, nil }

func (f *fakeEngineClient) SetRepos(context.Context, []string) ([]string, error) { return nil, nil }

func (f *fakeEngineClient) SetFlareSolverr(context.Context, sourceengine.FlareSolverrPatch) (sourceengine.FlareSolverrConfig, error) {
	return sourceengine.FlareSolverrConfig{}, nil
}

func (f *fakeEngineClient) SetSocks(context.Context, sourceengine.SocksPatch) (sourceengine.SocksConfig, error) {
	return sourceengine.SocksConfig{}, nil
}

// makeChapters builds n stub chapters anchored under urlPrefix, numbered 1..n.
func makeChapters(urlPrefix string, n int) []sourceengine.Chapter {
	chs := make([]sourceengine.Chapter, n)
	for i := range n {
		chs[i] = sourceengine.Chapter{
			URL:    fmt.Sprintf("%s/chapter-%d", urlPrefix, i+1),
			Name:   fmt.Sprintf("Chapter %d", i+1),
			Number: float64(i + 1),
		}
	}
	return chs
}

// testEnv bundles the wired Echo app, a valid token, and helper methods.
type testEnv struct {
	e         *echo.Echo
	token     string
	client    *fakeEngineClient
	triggered *int
}

// newTestEnv wires a full Echo with the imports routes behind RequireOwner,
// backing imports.Service with fc (sourceengine.Client). The series.Service is
// backed by a real testdb (needed for Adopt round-trips).
func newTestEnv(t *testing.T, fc *fakeEngineClient) *testEnv {
	t.Helper()

	db := testdb.New(t)
	authSvc := auth.NewService(testSecret)

	ing := ingest.NewIngest(fc, db)
	importsSvc := imports.NewService(fc, ing, db, "", 30*time.Second, nil)
	seriesSvc := seriessvc.NewService(db, "", 14)

	triggered := new(int)
	h := handler.NewHandler(importsSvc, seriesSvc, func() { *triggered++ })

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.GET("/sources", h.Sources)
	authed.GET("/search", h.Search)
	authed.GET("/sources/:sourceId/browse", h.Browse)
	authed.GET("/sources/:sourceId/manga/:mangaId/chapters", h.InspectChapters)
	authed.GET("/sources/:sourceId/manga/:mangaId/details", h.Details)
	authed.GET("/sources/:sourceId/manga/:mangaId/breakdown", h.Breakdown)
	authed.POST("/series", h.Adopt)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}

	return &testEnv{e: e, token: token, client: fc, triggered: triggered}
}

// do issues an authenticated request.
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

// doUnauth issues a request WITHOUT Authorization header.
func (env *testEnv) doUnauth(method, target string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	return rec
}

// --- 401 unauth proofs ---------------------------------------------------------

func TestSources_Unauth(t *testing.T) {
	fc := &fakeEngineClient{sources: []sourceengine.Source{{ID: 1, Name: "A", Lang: "en"}}}
	env := newTestEnv(t, fc)
	rec := env.doUnauth(http.MethodGet, "/api/sources")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Sources unauth: want 401, got %d", rec.Code)
	}
}

func TestSearch_Unauth(t *testing.T) {
	env := newTestEnv(t, &fakeEngineClient{})
	rec := env.doUnauth(http.MethodGet, "/api/search?q=test")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Search unauth: want 401, got %d", rec.Code)
	}
}

func TestBrowse_Unauth(t *testing.T) {
	env := newTestEnv(t, &fakeEngineClient{})
	rec := env.doUnauth(http.MethodGet, "/api/sources/1/browse?type=popular")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Browse unauth: want 401, got %d", rec.Code)
	}
}

func TestInspectChapters_Unauth(t *testing.T) {
	env := newTestEnv(t, &fakeEngineClient{})
	rec := env.doUnauth(http.MethodGet, "/api/sources/1/manga/1/chapters?url=/manga/1")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("InspectChapters unauth: want 401, got %d", rec.Code)
	}
}

func TestDetails_Unauth(t *testing.T) {
	env := newTestEnv(t, &fakeEngineClient{})
	rec := env.doUnauth(http.MethodGet, "/api/sources/1/manga/1/details?url=/manga/1")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Details unauth: want 401, got %d", rec.Code)
	}
}

func TestBreakdown_Unauth(t *testing.T) {
	env := newTestEnv(t, &fakeEngineClient{})
	rec := env.doUnauth(http.MethodGet, "/api/sources/1/manga/1/breakdown?url=/manga/1")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Breakdown unauth: want 401, got %d", rec.Code)
	}
}

func TestAdopt_Unauth(t *testing.T) {
	env := newTestEnv(t, &fakeEngineClient{})
	body := `{"title":"Test","providers":[{"source":"1","mangaId":1,"url":"/manga/1","importance":1}]}`
	r := httptest.NewRequest(http.MethodPost, "/api/series", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Adopt unauth: want 401, got %d", rec.Code)
	}
}

// --- GET /api/sources ----------------------------------------------------------

func TestSources_OK(t *testing.T) {
	fc := &fakeEngineClient{
		sources: []sourceengine.Source{
			{ID: 1, Name: "Alpha Source", Lang: "en"},
			{ID: 2, Name: "Beta Source", Lang: "ko"},
		},
	}
	env := newTestEnv(t, fc)
	rec := env.do(http.MethodGet, "/api/sources", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("Sources: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	var got []imports.SourceDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("Sources decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Sources: want 2, got %d", len(got))
	}
	if got[0].ID != "1" || got[0].Name != "Alpha Source" || got[0].Lang != "en" {
		t.Errorf("Sources[0]: got %+v", got[0])
	}
	if got[1].ID != "2" || got[1].Lang != "ko" {
		t.Errorf("Sources[1]: got %+v", got[1])
	}
}

func TestSources_Empty(t *testing.T) {
	fc := &fakeEngineClient{sources: []sourceengine.Source{}}
	env := newTestEnv(t, fc)
	rec := env.do(http.MethodGet, "/api/sources", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("Sources empty: want 200, got %d", rec.Code)
	}
	var got []imports.SourceDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Sources empty: want 0, got %d", len(got))
	}
}

// --- GET /api/search -----------------------------------------------------------

func TestSearch_OK(t *testing.T) {
	fc := &fakeEngineClient{
		sources: []sourceengine.Source{
			{ID: 1, Name: "Source One", Lang: "en"},
		},
		searchResults: map[int64]sourceengine.SearchResult{
			1: {Manga: []sourceengine.MangaEntry{{URL: "/manga/10", Title: "Solo Leveling", ThumbnailURL: "http://t/1"}}},
		},
	}
	env := newTestEnv(t, fc)
	rec := env.do(http.MethodGet, "/api/search?q=solo", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("Search OK: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got []imports.SearchGroupDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("Search decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Search: want 1 group, got %d", len(got))
	}
	if len(got[0].Candidates) != 1 {
		t.Fatalf("Search group[0]: want 1 candidate, got %d", len(got[0].Candidates))
	}
}

func TestSearch_BlankQuery_400(t *testing.T) {
	env := newTestEnv(t, &fakeEngineClient{sources: []sourceengine.Source{{ID: 1, Name: "A", Lang: "en"}}})
	rec := env.do(http.MethodGet, "/api/search?q=", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Search blank q: want 400, got %d", rec.Code)
	}
}

func TestSearch_MissingQuery_400(t *testing.T) {
	env := newTestEnv(t, &fakeEngineClient{sources: []sourceengine.Source{{ID: 1, Name: "A", Lang: "en"}}})
	rec := env.do(http.MethodGet, "/api/search", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Search missing q: want 400, got %d", rec.Code)
	}
}

func TestSearch_SourcesFilter(t *testing.T) {
	fc := &fakeEngineClient{
		sources: []sourceengine.Source{
			{ID: 1, Name: "A Source", Lang: "en"},
			{ID: 2, Name: "B Source", Lang: "ko"},
		},
		searchResults: map[int64]sourceengine.SearchResult{
			1: {Manga: []sourceengine.MangaEntry{{Title: "Tower of God"}}},
			2: {Manga: []sourceengine.MangaEntry{{Title: "Tower of God"}}},
		},
	}
	env := newTestEnv(t, fc)
	rec := env.do(http.MethodGet, "/api/search?q=tower&sources=1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("Search filter: want 200, got %d", rec.Code)
	}
	var got []imports.SearchGroupDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Only source 1 queried → 1 candidate.
	total := 0
	for _, g := range got {
		total += len(g.Candidates)
	}
	if total != 1 {
		t.Fatalf("Search filter: want 1 candidate total, got %d", total)
	}
}

func TestSearch_UnknownSource_EmptyResult(t *testing.T) {
	fc := &fakeEngineClient{
		sources: []sourceengine.Source{{ID: 99, Name: "Real", Lang: "en"}},
		searchResults: map[int64]sourceengine.SearchResult{
			99: {Manga: []sourceengine.MangaEntry{{Title: "Naruto"}}},
		},
	}
	env := newTestEnv(t, fc)
	// Unknown source id — service drops it silently and returns empty groups.
	rec := env.do(http.MethodGet, "/api/search?q=naruto&sources=nonexistent", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("Search unknown source: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got []imports.SearchGroupDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Search unknown source: want 0 groups, got %d", len(got))
	}
}

func TestSearch_JSONShape(t *testing.T) {
	fc := &fakeEngineClient{
		sources: []sourceengine.Source{{ID: 1, Name: "Source One", Lang: "en"}},
		searchResults: map[int64]sourceengine.SearchResult{
			1: {Manga: []sourceengine.MangaEntry{{Title: "Berserk", ThumbnailURL: "http://t/b"}}},
		},
	}
	env := newTestEnv(t, fc)
	rec := env.do(http.MethodGet, "/api/search?q=berserk", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("Search shape: want 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	// Verify camelCase keys that the OpenAPI schema requires.
	for _, key := range []string{`"sourceName"`, `"mangaId"`, `"thumbnailUrl"`} {
		if !strings.Contains(body, key) {
			t.Errorf("Search shape: body missing key %q: %s", key, body)
		}
	}
}

// --- GET /api/sources/:sourceId/browse -----------------------------------------

// browseEnv builds a test env whose source "1" returns one Popular page with
// a single candidate carrying a url, plus hasNextPage=true, and a DIFFERENT
// single-candidate Latest page.
func browseEnv(t *testing.T) *testEnv {
	t.Helper()
	fc := &fakeEngineClient{
		sources: []sourceengine.Source{{ID: 1, Name: "Source One", Lang: "en"}},
		popularResults: map[int64]sourceengine.SearchResult{
			1: {
				Manga:       []sourceengine.MangaEntry{{URL: "/manga/10", Title: "Solo Leveling", ThumbnailURL: "http://t/10"}},
				HasNextPage: true,
			},
		},
		latestResults: map[int64]sourceengine.SearchResult{
			1: {
				Manga:       []sourceengine.MangaEntry{{URL: "/manga/11", Title: "Berserk"}},
				HasNextPage: false,
			},
		},
	}
	return newTestEnv(t, fc)
}

// TestBrowse_OK_FullRoundTrip is the §16 proof: the response body carries every
// field the contract promises (manga[].url, hasNextPage, page) — asserted by
// reading the JSON back, not just the status code.
func TestBrowse_OK_FullRoundTrip(t *testing.T) {
	env := browseEnv(t)
	rec := env.do(http.MethodGet, "/api/sources/1/browse?type=popular", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("Browse OK: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	var got imports.BrowseResultDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("Browse decode: %v", err)
	}
	if got.Page != 1 {
		t.Errorf("Browse Page: got %d, want 1 (default)", got.Page)
	}
	if !got.HasNextPage {
		t.Error("Browse HasNextPage: got false, want true")
	}
	if len(got.Manga) != 1 {
		t.Fatalf("Browse Manga: got %d, want 1", len(got.Manga))
	}
	if c := got.Manga[0]; c.URL != "/manga/10" || c.Source != "1" {
		t.Errorf("Browse candidate: got %+v, want url /manga/10 source 1", c)
	}

	// Verify the camelCase keys the OpenAPI schema requires are present on the wire.
	assertBodyHasKeys(t, rec.Body.String(), `"url"`, `"hasNextPage"`, `"page"`, `"thumbnailUrl"`)
}

// assertBodyHasKeys asserts the response body contains each of the given JSON
// keys (the camelCase contract the OpenAPI schema promises).
func assertBodyHasKeys(t *testing.T, body string, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if !strings.Contains(body, key) {
			t.Errorf("body missing key %q: %s", key, body)
		}
	}
}

// TestBrowse_Latest_Page verifies the Latest listing with an explicit page is
// echoed back.
func TestBrowse_Latest_Page(t *testing.T) {
	env := browseEnv(t)
	rec := env.do(http.MethodGet, "/api/sources/1/browse?type=latest&page=4", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("Browse latest: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got imports.BrowseResultDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Page != 4 {
		t.Errorf("Browse latest Page: got %d, want 4", got.Page)
	}
	if got.HasNextPage {
		t.Error("Browse latest HasNextPage: got true, want false")
	}
}

func TestBrowse_MissingType_400(t *testing.T) {
	env := browseEnv(t)
	rec := env.do(http.MethodGet, "/api/sources/1/browse", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Browse missing type: want 400, got %d", rec.Code)
	}
}

func TestBrowse_BadType_400(t *testing.T) {
	env := browseEnv(t)
	rec := env.do(http.MethodGet, "/api/sources/1/browse?type=trending", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Browse bad type: want 400, got %d", rec.Code)
	}
}

func TestBrowse_BadPage_400(t *testing.T) {
	env := browseEnv(t)
	for _, p := range []string{"0", "-1", "abc"} {
		rec := env.do(http.MethodGet, "/api/sources/1/browse?type=popular&page="+p, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("Browse bad page %q: want 400, got %d", p, rec.Code)
		}
	}
}

func TestBrowse_UnknownSource_404(t *testing.T) {
	env := browseEnv(t)
	rec := env.do(http.MethodGet, "/api/sources/ghost/browse?type=popular", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("Browse unknown source: want 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// --- GET /api/sources/:sourceId/manga/:mangaId/chapters ------------------------

func TestInspectChapters_OK(t *testing.T) {
	fc := &fakeEngineClient{
		chaptersByURL: map[string][]sourceengine.Chapter{
			"/manga/12": {
				{URL: "/manga/12/ch1", Name: "Chapter 1", Number: 1.0},
				{URL: "/manga/12/ch2", Name: "Special", Number: -1},
			},
		},
	}
	env := newTestEnv(t, fc)
	rec := env.do(http.MethodGet, "/api/sources/5/manga/12/chapters?url=/manga/12", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("InspectChapters: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got []imports.ChapterInspectDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("InspectChapters: want 2, got %d", len(got))
	}
	if got[0].Number == nil || *got[0].Number != 1.0 {
		t.Errorf("InspectChapters[0].Number: got %v, want 1.0", got[0].Number)
	}
	if got[0].Name != "Chapter 1" {
		t.Errorf("InspectChapters[0].Name: got %q, want %q", got[0].Name, "Chapter 1")
	}
	if got[1].Number != nil {
		t.Errorf("InspectChapters[1].Number: want nil, got %v", got[1].Number)
	}
}

// TestInspectChapters_MissingURL_400 replaces the retired
// TestInspectChapters_NonIntMangaID_400: :mangaId is no longer parsed or
// validated on this route (P2 Suwayomi-removal, slice 3b — the backend is now
// URL-addressed via the REQUIRED ?url= query param), so a non-integer
// :mangaId no longer yields 400 on its own. The ACTUALLY-current 400 guard on
// this route is a missing ?url=; this test proves that guard instead.
func TestInspectChapters_MissingURL_400(t *testing.T) {
	env := newTestEnv(t, &fakeEngineClient{})
	rec := env.do(http.MethodGet, "/api/sources/5/manga/notanint/chapters", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("InspectChapters missing url: want 400, got %d", rec.Code)
	}
}

// --- GET /api/sources/:sourceId/manga/:mangaId/details -------------------------

// TestDetails_OK_FullRoundTrip is the §16 proof: a fetchManga-forced Manga
// (author/artist/description/genres populated) round-trips into the response
// body as a SearchCandidate, not just a 200.
func TestDetails_OK_FullRoundTrip(t *testing.T) {
	fc := &fakeEngineClient{
		sources: []sourceengine.Source{{ID: 1, Name: "Source One", Lang: "en"}},
		detailsByURL: map[string]sourceengine.MangaDetails{
			"/manga/10": {
				URL:         "/manga/10",
				Title:       "Solo Leveling",
				Author:      "Chugong",
				Artist:      "Jang Sung-rak",
				Description: "A weak hunter gains power.",
				Genres:      []string{"Action", "Fantasy"},
			},
		},
	}
	env := newTestEnv(t, fc)
	rec := env.do(http.MethodGet, "/api/sources/1/manga/10/details?url=/manga/10", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("Details OK: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	var got imports.SearchCandidateDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("Details decode: %v", err)
	}
	assertDetailsCandidate(t, got)
	assertBodyHasKeys(t, rec.Body.String(), `"author"`, `"artist"`, `"description"`, `"genres"`)
}

// assertDetailsCandidate asserts the enriched fields TestDetails_OK_FullRoundTrip
// expects on a SearchCandidateDTO. Extracted to keep the test function's
// cyclomatic complexity under the lint threshold (cyclop).
func assertDetailsCandidate(t *testing.T, got imports.SearchCandidateDTO) {
	t.Helper()
	if got.Source != "1" || got.SourceName != "Source One" {
		t.Errorf("Details: source/sourceName: got %q/%q, want 1/Source One", got.Source, got.SourceName)
	}
	if got.Author != "Chugong" {
		t.Errorf("Details: Author: got %q, want %q", got.Author, "Chugong")
	}
	if got.Artist != "Jang Sung-rak" {
		t.Errorf("Details: Artist: got %q, want %q", got.Artist, "Jang Sung-rak")
	}
	if got.Description != "A weak hunter gains power." {
		t.Errorf("Details: Description: got %q, want %q", got.Description, "A weak hunter gains power.")
	}
	if len(got.Genres) != 2 || got.Genres[0] != "Action" || got.Genres[1] != "Fantasy" {
		t.Errorf("Details: Genres: got %v, want [Action Fantasy]", got.Genres)
	}
}

// TestDetails_UnknownSource_404 asserts an unknown :sourceId maps to 404
// (mirrors TestBrowse_UnknownSource_404).
func TestDetails_UnknownSource_404(t *testing.T) {
	fc := &fakeEngineClient{sources: []sourceengine.Source{{ID: 1, Name: "Source One", Lang: "en"}}}
	env := newTestEnv(t, fc)
	rec := env.do(http.MethodGet, "/api/sources/ghost/manga/10/details?url=/manga/10", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("Details unknown source: want 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestDetails_UpstreamError_502 asserts an engine-host MangaDetails failure
// maps to 502, mirroring the cover-proxy's upstream error mapping — a source
// outage must never surface as a false 200.
func TestDetails_UpstreamError_502(t *testing.T) {
	fc := &fakeEngineClient{
		sources:     []sourceengine.Source{{ID: 1, Name: "Source One", Lang: "en"}},
		detailsErrs: map[string]error{"/manga/10": errors.New("source unreachable")},
	}
	env := newTestEnv(t, fc)
	rec := env.do(http.MethodGet, "/api/sources/1/manga/10/details?url=/manga/10", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("Details upstream error: want 502, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestDetails_MissingURL_400 replaces the retired TestDetails_NonIntMangaID_400:
// :mangaId is no longer parsed/validated on this route (see
// TestInspectChapters_MissingURL_400's doc comment for the same transition);
// the ACTUALLY-current 400 guard is a missing ?url=.
func TestDetails_MissingURL_400(t *testing.T) {
	env := newTestEnv(t, &fakeEngineClient{})
	rec := env.do(http.MethodGet, "/api/sources/src/manga/notanint/details", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Details missing url: want 400, got %d", rec.Code)
	}
}

// --- GET /api/sources/:sourceId/manga/:mangaId/breakdown ----------------------

// TestBreakdown_OK verifies the 200 shape: chapters grouped by scanlator with
// counts/ranges, sorted by count descending.
func TestBreakdown_OK(t *testing.T) {
	fc := &fakeEngineClient{
		sources: []sourceengine.Source{{ID: 1, Name: "Source One", Lang: "en"}},
		chaptersByURL: map[string][]sourceengine.Chapter{
			"/manga/10": {
				{URL: "/manga/10/ch1", Number: 1, Scanlator: "Alpha Scans"},
				{URL: "/manga/10/ch2", Number: 2, Scanlator: "Alpha Scans"},
				{URL: "/manga/10/ch3", Number: 1, Scanlator: "Beta Scans"},
			},
		},
	}
	env := newTestEnv(t, fc)
	rec := env.do(http.MethodGet, "/api/sources/1/manga/10/breakdown?url=/manga/10", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("Breakdown OK: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	var got imports.SourceBreakdownDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("Breakdown decode: %v", err)
	}
	if got.Total != 3 {
		t.Errorf("Breakdown: Total = %d, want 3", got.Total)
	}
	if len(got.Scanlators) != 2 {
		t.Fatalf("Breakdown: got %d groups, want 2", len(got.Scanlators))
	}
	if got.Scanlators[0].Scanlator != "Alpha Scans" || got.Scanlators[0].Count != 2 || got.Scanlators[0].Ranges != "1-2" {
		t.Errorf("Breakdown.Scanlators[0]: got %+v, want {Alpha Scans 2 1-2}", got.Scanlators[0])
	}
	if got.Scanlators[1].Scanlator != "Beta Scans" || got.Scanlators[1].Count != 1 {
		t.Errorf("Breakdown.Scanlators[1]: got %+v, want {Beta Scans 1 ...}", got.Scanlators[1])
	}
	assertBodyHasKeys(t, rec.Body.String(), `"total"`, `"scanlators"`, `"ranges"`)
}

// TestBreakdown_UnknownSource_404 asserts an unknown :sourceId maps to 404
// (mirrors TestDetails_UnknownSource_404).
func TestBreakdown_UnknownSource_404(t *testing.T) {
	fc := &fakeEngineClient{sources: []sourceengine.Source{{ID: 1, Name: "Source One", Lang: "en"}}}
	env := newTestEnv(t, fc)
	rec := env.do(http.MethodGet, "/api/sources/ghost/manga/10/breakdown?url=/manga/10", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("Breakdown unknown source: want 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestBreakdown_UpstreamError_502 asserts an engine-host Chapters failure
// maps to 502 (mirrors TestDetails_UpstreamError_502).
func TestBreakdown_UpstreamError_502(t *testing.T) {
	fc := &fakeEngineClient{
		sources:     []sourceengine.Source{{ID: 1, Name: "Source One", Lang: "en"}},
		chapterErrs: map[string]error{"/manga/10": errors.New("source unreachable")},
	}
	env := newTestEnv(t, fc)
	rec := env.do(http.MethodGet, "/api/sources/1/manga/10/breakdown?url=/manga/10", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("Breakdown upstream error: want 502, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestBreakdown_MissingURL_400 replaces the retired
// TestBreakdown_NonIntMangaID_400: :mangaId is no longer parsed/validated on
// this route (see TestInspectChapters_MissingURL_400's doc comment for the
// same transition); the ACTUALLY-current 400 guard is a missing ?url=.
func TestBreakdown_MissingURL_400(t *testing.T) {
	env := newTestEnv(t, &fakeEngineClient{})
	rec := env.do(http.MethodGet, "/api/sources/src/manga/notanint/breakdown", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Breakdown missing url: want 400, got %d", rec.Code)
	}
}

// --- POST /api/series (Adopt) --------------------------------------------------

func TestAdopt_OK_FullRoundTrip(t *testing.T) {
	// §16: adopt must round-trip providers + importances + title.
	const (
		srcA    = "1"
		urlA    = "/manga/101"
		importA = 10
		srcB    = "2"
		urlB    = "/manga/202"
		importB = 5
		title   = "Solo Leveling"
	)

	fc := &fakeEngineClient{
		chaptersByURL: map[string][]sourceengine.Chapter{
			urlA: makeChapters(urlA, 2),
			urlB: makeChapters(urlB, 3),
		},
	}
	env := newTestEnv(t, fc)

	body, _ := json.Marshal(map[string]any{
		"title": title,
		"providers": []map[string]any{
			{"source": srcA, "mangaId": 101, "url": urlA, "importance": importA},
			{"source": srcB, "mangaId": 202, "url": urlB, "importance": importB},
		},
	})

	rec := env.do(http.MethodPost, "/api/series", string(body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("Adopt: want 201, got %d (%s)", rec.Code, rec.Body.String())
	}

	// Decode response as SeriesDetailDTO (from seriessvc package).
	var detail seriessvc.SeriesDetailDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("Adopt decode detail: %v", err)
	}

	// Title round-trip.
	if detail.Title != title {
		t.Errorf("Adopt: Title: got %q, want %q", detail.Title, title)
	}
	// ID must be a non-zero UUID string.
	if detail.ID == "" || detail.ID == uuid.Nil.String() {
		t.Fatalf("Adopt: detail.ID is empty or zero: %q", detail.ID)
	}
	// Providers round-trip: both sources must appear with correct importances.
	if len(detail.Providers) != 2 {
		t.Fatalf("Adopt: Providers count: got %d, want 2", len(detail.Providers))
	}
	impBySource := make(map[string]int, 2)
	for _, p := range detail.Providers {
		impBySource[p.Provider] = p.Importance
	}
	if impBySource[srcA] != importA {
		t.Errorf("Adopt: %q importance: got %d, want %d", srcA, impBySource[srcA], importA)
	}
	if impBySource[srcB] != importB {
		t.Errorf("Adopt: %q importance: got %d, want %d", srcB, impBySource[srcB], importB)
	}
}

// TestAdopt_SameSourceDifferentScanlators is the HTTP-level companion to the
// service-level setImportances-by-scanlator proof: two providers naming the
// SAME source under two DIFFERENT scanlators (with different importances)
// must be accepted (not rejected as a duplicate source) and must round-trip
// as two distinct providers, each with its own importance.
func TestAdopt_SameSourceDifferentScanlators(t *testing.T) {
	const (
		src      = "3"
		mangaURL = "/manga/999"
		scanA    = "Alpha Scans"
		importA  = 5
		scanB    = "Beta Scans"
		importB  = 3
		title    = "Comix Series"
	)

	fc := &fakeEngineClient{
		chaptersByURL: map[string][]sourceengine.Chapter{
			mangaURL: {
				{URL: mangaURL + "/ch1", Number: 1, Scanlator: scanA},
				{URL: mangaURL + "/ch2", Number: 2, Scanlator: scanB},
			},
		},
	}
	env := newTestEnv(t, fc)

	body, _ := json.Marshal(map[string]any{
		"title": title,
		"providers": []map[string]any{
			{"source": src, "mangaId": 999, "url": mangaURL, "importance": importA, "scanlator": scanA},
			{"source": src, "mangaId": 999, "url": mangaURL, "importance": importB, "scanlator": scanB},
		},
	})

	rec := env.do(http.MethodPost, "/api/series", string(body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("Adopt (same source, different scanlators): want 201, got %d (%s)", rec.Code, rec.Body.String())
	}

	var detail seriessvc.SeriesDetailDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("Adopt decode detail: %v", err)
	}
	if len(detail.Providers) != 2 {
		t.Fatalf("Adopt: Providers count: got %d, want 2", len(detail.Providers))
	}
	impByScanlator := make(map[string]int, 2)
	for _, p := range detail.Providers {
		if p.Provider != src {
			t.Errorf("Adopt: provider = %q, want %q", p.Provider, src)
		}
		impByScanlator[p.Scanlator] = p.Importance
	}
	if impByScanlator[scanA] != importA {
		t.Errorf("Adopt: %q importance: got %d, want %d", scanA, impByScanlator[scanA], importA)
	}
	if impByScanlator[scanB] != importB {
		t.Errorf("Adopt: %q importance: got %d, want %d", scanB, impByScanlator[scanB], importB)
	}
}

func TestAdopt_WithCategory(t *testing.T) {
	fc := &fakeEngineClient{
		chaptersByURL: map[string][]sourceengine.Chapter{
			"/manga/301": makeChapters("/manga/301", 1),
		},
	}
	env := newTestEnv(t, fc)
	body, _ := json.Marshal(map[string]any{
		"title":    "Berserk",
		"category": "Manga",
		"providers": []map[string]any{
			{"source": "1", "mangaId": 301, "url": "/manga/301", "importance": 1},
		},
	})
	rec := env.do(http.MethodPost, "/api/series", string(body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("Adopt category: want 201, got %d (%s)", rec.Code, rec.Body.String())
	}
	var detail seriessvc.SeriesDetailDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if detail.Category != "Manga" {
		t.Errorf("Adopt category: got %q, want Manga", detail.Category)
	}
}

func TestAdopt_EmptyProviders_400(t *testing.T) {
	env := newTestEnv(t, &fakeEngineClient{})
	body := `{"title":"Test","providers":[]}`
	rec := env.do(http.MethodPost, "/api/series", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Adopt empty providers: want 400, got %d", rec.Code)
	}
}

func TestAdopt_BlankTitle_400(t *testing.T) {
	env := newTestEnv(t, &fakeEngineClient{})
	body := `{"title":"","providers":[{"source":"a","mangaId":1,"importance":1}]}`
	rec := env.do(http.MethodPost, "/api/series", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Adopt blank title: want 400, got %d", rec.Code)
	}
}

func TestAdopt_NegativeImportance_400(t *testing.T) {
	env := newTestEnv(t, &fakeEngineClient{})
	body := `{"title":"Test","providers":[{"source":"a","mangaId":1,"importance":-1}]}`
	rec := env.do(http.MethodPost, "/api/series", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Adopt negative importance: want 400, got %d", rec.Code)
	}
}

// TestAdopt_DuplicateSource_400 verifies that two providers sharing the same
// source — even with different urls — are rejected with 400. A series may
// carry at most one SeriesProvider per (source, scanlator); allowing two
// would silently collapse them onto a single row.
func TestAdopt_DuplicateSource_400(t *testing.T) {
	env := newTestEnv(t, &fakeEngineClient{})
	// Same source, different urls — the tighter check must catch this. Both
	// providers carry a valid url so the 400 is actually caused by the
	// duplicate-source guard, not the (unrelated) url-required guard.
	body := `{"title":"Test","providers":[{"source":"a","mangaId":1,"url":"/manga/1","importance":1},{"source":"a","mangaId":2,"url":"/manga/2","importance":2}]}`
	rec := env.do(http.MethodPost, "/api/series", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Adopt duplicate source (different url): want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestAdopt_InvalidCategory_400(t *testing.T) {
	env := newTestEnv(t, &fakeEngineClient{})
	// Categories are user-defined now; "invalid" means filesystem-UNSAFE (it
	// becomes a folder name), not "not in a fixed enum". The provider carries
	// a valid url so the 400 is actually caused by the category check, not
	// the (unrelated) url-required guard.
	body := `{"title":"Test","category":"bad/name","providers":[{"source":"a","mangaId":1,"url":"/manga/1","importance":1}]}`
	rec := env.do(http.MethodPost, "/api/series", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Adopt invalid category: want 400, got %d", rec.Code)
	}
}

func TestAdopt_MissingBody_400(t *testing.T) {
	env := newTestEnv(t, &fakeEngineClient{})
	r := httptest.NewRequest(http.MethodPost, "/api/series", strings.NewReader(""))
	r.Header.Set("Authorization", "Bearer "+env.token)
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Adopt missing body: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestAdopt_TriggersConvergeOnSuccess asserts Adopt fires the auto-converge
// trigger exactly once on success and never on a validation failure.
func TestAdopt_TriggersConvergeOnSuccess(t *testing.T) {
	fc := &fakeEngineClient{chaptersByURL: map[string][]sourceengine.Chapter{"/manga/101": makeChapters("/manga/101", 2)}}
	env := newTestEnv(t, fc)

	body, _ := json.Marshal(map[string]any{
		"title":     "Solo Leveling",
		"providers": []map[string]any{{"source": "1", "mangaId": 101, "url": "/manga/101", "importance": 10}},
	})
	rec := env.do(http.MethodPost, "/api/series", string(body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if *env.triggered != 1 {
		t.Errorf("trigger fired %d times, want 1", *env.triggered)
	}

	// Failure path: blank title is rejected by validation before the service runs.
	*env.triggered = 0
	rec = env.do(http.MethodPost, "/api/series", `{"title":"  ","providers":[]}`)
	if rec.Code == http.StatusCreated {
		t.Fatal("blank-title adopt must not succeed")
	}
	if *env.triggered != 0 {
		t.Errorf("trigger fired %d times on failure, want 0", *env.triggered)
	}
}
