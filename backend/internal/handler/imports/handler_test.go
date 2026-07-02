// Package imports_test exercises the imports HTTP handlers end-to-end through a
// real Echo instance (with RequireOwner middleware wired) against an ephemeral
// PostgreSQL instance (testdb). Tests require Docker.
package imports_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	handler "github.com/technobecet/tsundoku/internal/handler/imports"
	"github.com/technobecet/tsundoku/internal/imports"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	seriessvc "github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

const testSecret = "imports-handler-test-secret"

// fakeClient is a local in-test implementation of suwayomi.Client.
// It mirrors the one in internal/imports/service_test.go — we cannot reuse that
// because it lives in an unexported test file; each handler test defines its own.
type fakeClient struct {
	sources          []suwayomi.Source
	sourcesErr       error
	searchResults    map[string][]suwayomi.Manga
	searchErrs       map[string]error
	chaptersPerManga map[int][]suwayomi.Chapter
	chapterErrs      map[int]error
	browseResults    map[suwayomi.BrowseType]suwayomi.BrowseResult
	browseErr        error
	// pageBytes, when set, is called by PageBytes instead of the default stub
	// (exercised by the MangaCover tests).
	pageBytes func(ctx context.Context, url string) ([]byte, string, error)
}

func (f *fakeClient) Sources(_ context.Context) ([]suwayomi.Source, error) {
	return f.sources, f.sourcesErr
}

func (f *fakeClient) Search(_ context.Context, sourceID, _ string) ([]suwayomi.Manga, error) {
	if f.searchErrs != nil {
		if err, ok := f.searchErrs[sourceID]; ok {
			return nil, err
		}
	}
	if f.searchResults != nil {
		if res, ok := f.searchResults[sourceID]; ok {
			return res, nil
		}
	}
	return nil, nil
}

func (f *fakeClient) FetchChapters(_ context.Context, mangaID int) ([]suwayomi.Chapter, error) {
	if f.chapterErrs != nil {
		if err, ok := f.chapterErrs[mangaID]; ok {
			return nil, err
		}
	}
	if f.chaptersPerManga != nil {
		return f.chaptersPerManga[mangaID], nil
	}
	return nil, nil
}

func (f *fakeClient) Browse(_ context.Context, _ string, t suwayomi.BrowseType, _ int) (suwayomi.BrowseResult, error) {
	if f.browseErr != nil {
		return suwayomi.BrowseResult{}, f.browseErr
	}
	if f.browseResults != nil {
		return f.browseResults[t], nil
	}
	return suwayomi.BrowseResult{}, nil
}

func (f *fakeClient) MangaChapters(_ context.Context, _ int) ([]suwayomi.Chapter, error) {
	panic("MangaChapters must never be called by the imports service (use FetchChapters)")
}
func (f *fakeClient) MangaMeta(_ context.Context, _ int) (suwayomi.Manga, error) {
	return suwayomi.Manga{}, nil
}
func (f *fakeClient) ChapterPages(_ context.Context, _ int) ([]string, error) {
	return nil, nil
}
func (f *fakeClient) PageBytes(ctx context.Context, pageURL string) ([]byte, string, error) {
	if f.pageBytes != nil {
		return f.pageBytes(ctx, pageURL)
	}
	return nil, "", nil
}
func (f *fakeClient) ServerSettings(_ context.Context) (suwayomi.SuwayomiSettings, error) {
	return suwayomi.SuwayomiSettings{}, nil
}
func (f *fakeClient) SetServerSettings(_ context.Context, _ suwayomi.SuwayomiSettingsPatch) error {
	return nil
}
func (f *fakeClient) Extensions(_ context.Context) ([]suwayomi.Extension, error) { return nil, nil }
func (f *fakeClient) SetExtensionState(_ context.Context, _ string, _ suwayomi.ExtensionAction) error {
	return nil
}
func (f *fakeClient) FetchExtensions(_ context.Context) ([]suwayomi.Extension, error) {
	return nil, nil
}
func (f *fakeClient) ExtensionRepos(_ context.Context) ([]string, error)    { return nil, nil }
func (f *fakeClient) SetExtensionRepos(_ context.Context, _ []string) error { return nil }

// makeChapters builds n stub chapters anchored to baseID.
func makeChapters(baseID, n int) []suwayomi.Chapter {
	chs := make([]suwayomi.Chapter, n)
	for i := range n {
		num := float64(i + 1)
		numCopy := num
		chs[i] = suwayomi.Chapter{
			ID:     baseID + i,
			Index:  i,
			Name:   fmt.Sprintf("Chapter %.0f", num),
			Number: &numCopy,
			URL:    fmt.Sprintf("https://test/ch/%d", i+1),
		}
	}
	return chs
}

// ptrF64 returns a pointer to v.
func ptrF64(v float64) *float64 { return &v }

// ptrStr returns a pointer to s.
func ptrStr(s string) *string { return &s }

// testEnv bundles the wired Echo app, a valid token, and helper methods.
type testEnv struct {
	e         *echo.Echo
	token     string
	client    *fakeClient
	triggered *int
}

// newTestEnv wires a full Echo with the imports routes behind RequireOwner.
// The series.Service is backed by a real testdb (needed for Adopt round-trips).
func newTestEnv(t *testing.T, fc *fakeClient) *testEnv {
	t.Helper()

	db := testdb.New(t)
	authSvc := auth.NewService(testSecret)

	ingest := suwayomi.NewIngest(fc, db)
	importsSvc := imports.NewService(fc, ingest, db, "")
	seriesSvc := seriessvc.NewService(db, "", 14)

	triggered := new(int)
	h := handler.NewHandler(importsSvc, seriesSvc, func() { *triggered++ }, fc)

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.GET("/sources", h.Sources)
	authed.GET("/search", h.Search)
	authed.GET("/sources/:sourceId/browse", h.Browse)
	authed.GET("/sources/:sourceId/manga/:mangaId/chapters", h.InspectChapters)
	authed.GET("/sources/:sourceId/manga/:mangaId/cover", h.MangaCover)
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
	fc := &fakeClient{sources: []suwayomi.Source{{ID: "a", Name: "A", Lang: "en"}}}
	env := newTestEnv(t, fc)
	rec := env.doUnauth(http.MethodGet, "/api/sources")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Sources unauth: want 401, got %d", rec.Code)
	}
}

func TestSearch_Unauth(t *testing.T) {
	env := newTestEnv(t, &fakeClient{})
	rec := env.doUnauth(http.MethodGet, "/api/search?q=test")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Search unauth: want 401, got %d", rec.Code)
	}
}

func TestBrowse_Unauth(t *testing.T) {
	env := newTestEnv(t, &fakeClient{})
	rec := env.doUnauth(http.MethodGet, "/api/sources/src/browse?type=popular")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Browse unauth: want 401, got %d", rec.Code)
	}
}

func TestInspectChapters_Unauth(t *testing.T) {
	env := newTestEnv(t, &fakeClient{})
	rec := env.doUnauth(http.MethodGet, "/api/sources/src/manga/1/chapters")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("InspectChapters unauth: want 401, got %d", rec.Code)
	}
}

func TestAdopt_Unauth(t *testing.T) {
	env := newTestEnv(t, &fakeClient{})
	body := `{"title":"Test","providers":[{"source":"s","mangaId":1,"importance":1}]}`
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
	fc := &fakeClient{
		sources: []suwayomi.Source{
			{ID: "src-a", Name: "Alpha Source", Lang: "en"},
			{ID: "src-b", Name: "Beta Source", Lang: "ko"},
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
	if got[0].ID != "src-a" || got[0].Name != "Alpha Source" || got[0].Lang != "en" {
		t.Errorf("Sources[0]: got %+v", got[0])
	}
	if got[1].ID != "src-b" || got[1].Lang != "ko" {
		t.Errorf("Sources[1]: got %+v", got[1])
	}
}

func TestSources_Empty(t *testing.T) {
	fc := &fakeClient{sources: []suwayomi.Source{}}
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
	fc := &fakeClient{
		sources: []suwayomi.Source{
			{ID: "src1", Name: "Source One", Lang: "en"},
		},
		searchResults: map[string][]suwayomi.Manga{
			"src1": {{ID: 10, Title: "Solo Leveling", ThumbnailURL: ptrStr("http://t/1")}},
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
	env := newTestEnv(t, &fakeClient{sources: []suwayomi.Source{{ID: "a", Name: "A", Lang: "en"}}})
	rec := env.do(http.MethodGet, "/api/search?q=", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Search blank q: want 400, got %d", rec.Code)
	}
}

func TestSearch_MissingQuery_400(t *testing.T) {
	env := newTestEnv(t, &fakeClient{sources: []suwayomi.Source{{ID: "a", Name: "A", Lang: "en"}}})
	rec := env.do(http.MethodGet, "/api/search", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Search missing q: want 400, got %d", rec.Code)
	}
}

func TestSearch_SourcesFilter(t *testing.T) {
	fc := &fakeClient{
		sources: []suwayomi.Source{
			{ID: "a", Name: "A Source", Lang: "en"},
			{ID: "b", Name: "B Source", Lang: "ko"},
		},
		searchResults: map[string][]suwayomi.Manga{
			"a": {{ID: 1, Title: "Tower of God"}},
			"b": {{ID: 2, Title: "Tower of God"}},
		},
	}
	env := newTestEnv(t, fc)
	rec := env.do(http.MethodGet, "/api/search?q=tower&sources=a", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("Search filter: want 200, got %d", rec.Code)
	}
	var got []imports.SearchGroupDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Only source a queried → 1 candidate.
	total := 0
	for _, g := range got {
		total += len(g.Candidates)
	}
	if total != 1 {
		t.Fatalf("Search filter: want 1 candidate total, got %d", total)
	}
}

func TestSearch_UnknownSource_EmptyResult(t *testing.T) {
	fc := &fakeClient{
		sources: []suwayomi.Source{{ID: "real", Name: "Real", Lang: "en"}},
		searchResults: map[string][]suwayomi.Manga{
			"real": {{ID: 1, Title: "Naruto"}},
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
	fc := &fakeClient{
		sources: []suwayomi.Source{{ID: "s1", Name: "Source One", Lang: "en"}},
		searchResults: map[string][]suwayomi.Manga{
			"s1": {{ID: 42, Title: "Berserk", ThumbnailURL: ptrStr("http://t/b")}},
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

// browseEnv builds a test env whose source "src1" returns one Popular page with
// a single candidate carrying a url, plus hasNextPage=true.
func browseEnv(t *testing.T) *testEnv {
	t.Helper()
	fc := &fakeClient{
		sources: []suwayomi.Source{{ID: "src1", Name: "Source One", Lang: "en"}},
		browseResults: map[suwayomi.BrowseType]suwayomi.BrowseResult{
			suwayomi.BrowsePopular: {
				Mangas:      []suwayomi.Manga{{ID: 10, Title: "Solo Leveling", URL: "/manga/10", ThumbnailURL: ptrStr("http://t/10")}},
				HasNextPage: true,
			},
			suwayomi.BrowseLatest: {
				Mangas:      []suwayomi.Manga{{ID: 11, Title: "Berserk", URL: "/manga/11"}},
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
	rec := env.do(http.MethodGet, "/api/sources/src1/browse?type=popular", "")
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
	if c := got.Manga[0]; c.URL != "/manga/10" || c.Source != "src1" {
		t.Errorf("Browse candidate: got %+v, want url /manga/10 source src1", c)
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
	rec := env.do(http.MethodGet, "/api/sources/src1/browse?type=latest&page=4", "")
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
	rec := env.do(http.MethodGet, "/api/sources/src1/browse", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Browse missing type: want 400, got %d", rec.Code)
	}
}

func TestBrowse_BadType_400(t *testing.T) {
	env := browseEnv(t)
	rec := env.do(http.MethodGet, "/api/sources/src1/browse?type=trending", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Browse bad type: want 400, got %d", rec.Code)
	}
}

func TestBrowse_BadPage_400(t *testing.T) {
	env := browseEnv(t)
	for _, p := range []string{"0", "-1", "abc"} {
		rec := env.do(http.MethodGet, "/api/sources/src1/browse?type=popular&page="+p, "")
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
	fc := &fakeClient{
		chaptersPerManga: map[int][]suwayomi.Chapter{
			12: {
				{ID: 1, Name: "Chapter 1", Number: ptrF64(1.0)},
				{ID: 2, Name: "Special", Number: nil},
			},
		},
	}
	env := newTestEnv(t, fc)
	rec := env.do(http.MethodGet, "/api/sources/src-x/manga/12/chapters", "")
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

func TestInspectChapters_NonIntMangaID_400(t *testing.T) {
	env := newTestEnv(t, &fakeClient{})
	rec := env.do(http.MethodGet, "/api/sources/src/manga/notanint/chapters", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("InspectChapters non-int: want 400, got %d", rec.Code)
	}
}

// --- GET /api/sources/:sourceId/manga/:mangaId/cover (B2) ----------------------

func TestMangaCover_Unauth(t *testing.T) {
	env := newTestEnv(t, &fakeClient{})
	rec := env.doUnauth(http.MethodGet, "/api/sources/src/manga/12/cover")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("MangaCover unauth: want 401, got %d", rec.Code)
	}
}

// TestMangaCover_OK verifies the handler streams the bytes PageBytes returns,
// with a Content-Type resolved from the reported extension, and that it calls
// PageBytes with Suwayomi's own REST thumbnail path (not whatever GraphQL
// thumbnailUrl string a source happened to report).
func TestMangaCover_OK(t *testing.T) {
	pngBytes := []byte{0x89, 0x50, 0x4E, 0x47}
	var gotURL string
	fc := &fakeClient{
		pageBytes: func(_ context.Context, url string) ([]byte, string, error) {
			gotURL = url
			return pngBytes, "png", nil
		},
	}
	env := newTestEnv(t, fc)
	rec := env.do(http.MethodGet, "/api/sources/src-x/manga/12/cover", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("MangaCover OK: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/png") {
		t.Errorf("MangaCover OK: Content-Type want image/png, got %q", ct)
	}
	if rec.Body.String() != string(pngBytes) {
		t.Errorf("MangaCover OK: body mismatch")
	}
	if gotURL != "/api/v1/manga/12/thumbnail" {
		t.Errorf("MangaCover OK: PageBytes called with %q, want /api/v1/manga/12/thumbnail", gotURL)
	}
}

// TestMangaCover_PageBytesFail_502 asserts a Suwayomi fetch failure yields 502,
// mirroring the series/provider cover proxies.
func TestMangaCover_PageBytesFail_502(t *testing.T) {
	fc := &fakeClient{
		pageBytes: func(_ context.Context, _ string) ([]byte, string, error) {
			return nil, "", errors.New("suwayomi down")
		},
	}
	env := newTestEnv(t, fc)
	rec := env.do(http.MethodGet, "/api/sources/src-x/manga/12/cover", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("MangaCover PageBytesFail: want 502, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestMangaCover_NonIntMangaID_400 asserts a non-integer :mangaId yields 400
// (parseMangaID is shared with InspectChapters).
func TestMangaCover_NonIntMangaID_400(t *testing.T) {
	env := newTestEnv(t, &fakeClient{})
	rec := env.do(http.MethodGet, "/api/sources/src/manga/notanint/cover", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("MangaCover non-int: want 400, got %d", rec.Code)
	}
}

// --- POST /api/series (Adopt) --------------------------------------------------

func TestAdopt_OK_FullRoundTrip(t *testing.T) {
	// §16: adopt must round-trip providers + importances + title.
	const (
		srcA    = "mangadex"
		mangaA  = 101
		importA = 10
		srcB    = "toonily"
		mangaB  = 202
		importB = 5
		title   = "Solo Leveling"
	)

	fc := &fakeClient{
		chaptersPerManga: map[int][]suwayomi.Chapter{
			mangaA: makeChapters(1000, 2),
			mangaB: makeChapters(2000, 3),
		},
	}
	env := newTestEnv(t, fc)

	body, _ := json.Marshal(map[string]any{
		"title": title,
		"providers": []map[string]any{
			{"source": srcA, "mangaId": mangaA, "importance": importA},
			{"source": srcB, "mangaId": mangaB, "importance": importB},
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

func TestAdopt_WithCategory(t *testing.T) {
	fc := &fakeClient{
		chaptersPerManga: map[int][]suwayomi.Chapter{
			301: makeChapters(3000, 1),
		},
	}
	env := newTestEnv(t, fc)
	body, _ := json.Marshal(map[string]any{
		"title":    "Berserk",
		"category": "Manga",
		"providers": []map[string]any{
			{"source": "mangadex", "mangaId": 301, "importance": 1},
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
	env := newTestEnv(t, &fakeClient{})
	body := `{"title":"Test","providers":[]}`
	rec := env.do(http.MethodPost, "/api/series", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Adopt empty providers: want 400, got %d", rec.Code)
	}
}

func TestAdopt_BlankTitle_400(t *testing.T) {
	env := newTestEnv(t, &fakeClient{})
	body := `{"title":"","providers":[{"source":"a","mangaId":1,"importance":1}]}`
	rec := env.do(http.MethodPost, "/api/series", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Adopt blank title: want 400, got %d", rec.Code)
	}
}

func TestAdopt_NegativeImportance_400(t *testing.T) {
	env := newTestEnv(t, &fakeClient{})
	body := `{"title":"Test","providers":[{"source":"a","mangaId":1,"importance":-1}]}`
	rec := env.do(http.MethodPost, "/api/series", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Adopt negative importance: want 400, got %d", rec.Code)
	}
}

// TestAdopt_DuplicateSource_400 verifies that two providers sharing the same
// source — even with different mangaIds — are rejected with 400. A series may
// carry at most one SeriesProvider per source; allowing two would silently
// collapse them onto a single row (last-write wins on suwayomi_id / importance).
func TestAdopt_DuplicateSource_400(t *testing.T) {
	env := newTestEnv(t, &fakeClient{})
	// Same source, different mangaIds — the tighter check must catch this.
	body := `{"title":"Test","providers":[{"source":"a","mangaId":1,"importance":1},{"source":"a","mangaId":2,"importance":2}]}`
	rec := env.do(http.MethodPost, "/api/series", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Adopt duplicate source (different mangaId): want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestAdopt_InvalidCategory_400(t *testing.T) {
	env := newTestEnv(t, &fakeClient{})
	// Categories are user-defined now; "invalid" means filesystem-UNSAFE (it
	// becomes a folder name), not "not in a fixed enum".
	body := `{"title":"Test","category":"bad/name","providers":[{"source":"a","mangaId":1,"importance":1}]}`
	rec := env.do(http.MethodPost, "/api/series", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Adopt invalid category: want 400, got %d", rec.Code)
	}
}

func TestAdopt_MissingBody_400(t *testing.T) {
	env := newTestEnv(t, &fakeClient{})
	r := httptest.NewRequest(http.MethodPost, "/api/series", bytes.NewReader(nil))
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
	fc := &fakeClient{chaptersPerManga: map[int][]suwayomi.Chapter{101: makeChapters(1000, 2)}}
	env := newTestEnv(t, fc)

	body, _ := json.Marshal(map[string]any{
		"title":     "Solo Leveling",
		"providers": []map[string]any{{"source": "mangadex", "mangaId": 101, "importance": 10}},
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
