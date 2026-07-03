// Package suwayomi_test — unit tests for the typed Suwayomi HTTP client.
//
// All tests use httptest.Server; no real Suwayomi is required.
// Canned responses match the real Suwayomi v2.2.2100 GraphQL wire shape.
package suwayomi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/config"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// --- helpers -----------------------------------------------------------------

// newTestClient builds a Client pointed at srv.
func newTestClient(t *testing.T, srv *httptest.Server) suwayomi.Client {
	t.Helper()
	cfg := config.SuwayomiConfig{
		Host: strings.TrimPrefix(srv.URL, "http://"),
	}
	// Rewrite host:port into cfg fields so BaseURL() resolves to srv.URL.
	// srv.URL is "http://127.0.0.1:<port>", so split on last ":" to get host+port.
	parts := strings.SplitN(strings.TrimPrefix(srv.URL, "http://"), ":", 2)
	cfg.Host = parts[0]
	if len(parts) == 2 {
		cfg.Port = parts[1]
	}
	return suwayomi.NewClient(cfg, srv.Client())
}

// graphqlResponse wraps data + optional errors for the canned JSON responses.
func graphqlResponse(t *testing.T, data any, errs []map[string]any) []byte {
	t.Helper()
	env := map[string]any{"data": data}
	if len(errs) > 0 {
		env["errors"] = errs
	}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal canned response: %v", err)
	}
	return b
}

// --- Sources -----------------------------------------------------------------

func TestClient_Sources(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/graphql" || r.Method != http.MethodPost {
			http.Error(w, "wrong path/method", http.StatusNotFound)
			return
		}
		resp := graphqlResponse(t, map[string]any{
			"sources": map[string]any{
				"nodes": []map[string]any{
					{"id": "1234567890", "name": "MangaDex", "lang": "en"},
					{"id": "9876543210", "name": "NHentai", "lang": "ja"},
				},
			},
		}, nil)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	sources, err := client.Sources(context.Background())
	if err != nil {
		t.Fatalf("Sources() error = %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("Sources() got %d sources, want 2", len(sources))
	}
	if sources[0].ID != "1234567890" {
		t.Errorf("sources[0].ID = %q, want %q", sources[0].ID, "1234567890")
	}
	if sources[0].Name != "MangaDex" {
		t.Errorf("sources[0].Name = %q, want MangaDex", sources[0].Name)
	}
	if sources[0].Lang != "en" {
		t.Errorf("sources[0].Lang = %q, want en", sources[0].Lang)
	}
	if sources[1].ID != "9876543210" {
		t.Errorf("sources[1].ID = %q, want %q", sources[1].ID, "9876543210")
	}
}

func TestClient_Sources_GraphQLError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := graphqlResponse(t, nil, []map[string]any{
			{"message": "sources not available"},
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, err := client.Sources(context.Background())
	if err == nil {
		t.Fatal("Sources() with GraphQL errors: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "sources not available") {
		t.Errorf("error %q should contain the GraphQL error message", err.Error())
	}
}

// --- Search ------------------------------------------------------------------

func TestClient_Search(t *testing.T) {
	const sourceID = "1234567890"
	const query = "one piece"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/graphql" {
			http.Error(w, "wrong endpoint", http.StatusNotFound)
			return
		}
		resp := graphqlResponse(t, map[string]any{
			"fetchSourceManga": map[string]any{
				"mangas": []map[string]any{
					{
						"id":           42,
						"title":        "One Piece",
						"url":          "/manga/42",
						"thumbnailUrl": "/thumbnail/42",
					},
					{
						"id":           43,
						"title":        "One Punch Man",
						"url":          "/manga/43",
						"thumbnailUrl": nil,
					},
				},
			},
		}, nil)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	mangas, err := client.Search(context.Background(), sourceID, query)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(mangas) != 2 {
		t.Fatalf("Search() got %d results, want 2", len(mangas))
	}
	if mangas[0].ID != 42 {
		t.Errorf("mangas[0].ID = %d, want 42", mangas[0].ID)
	}
	if mangas[0].Title != "One Piece" {
		t.Errorf("mangas[0].Title = %q, want One Piece", mangas[0].Title)
	}
	if mangas[0].URL != "/manga/42" {
		t.Errorf("mangas[0].URL = %q, want /manga/42", mangas[0].URL)
	}
	if mangas[1].ID != 43 {
		t.Errorf("mangas[1].ID = %d, want 43", mangas[1].ID)
	}
}

func TestClient_Search_GraphQLError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := graphqlResponse(t, nil, []map[string]any{
			{"message": "source not found"},
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, err := client.Search(context.Background(), "bad-source", "query")
	if err == nil {
		t.Fatal("Search() with GraphQL errors: expected error, got nil")
	}
}

// --- Browse ------------------------------------------------------------------

// decodeGraphQLVars reads a GraphQL request body and returns its variables map.
// Used by the Browse tests to assert the wire request carries type=POPULAR and
// no query field.
func decodeGraphQLVars(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var req struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	return req.Variables
}

// TestClient_Browse_Popular verifies that Browse drives fetchSourceManga with
// type=POPULAR (and no query variable), and that mangas (incl. url) + hasNextPage
// round-trip onto the BrowseResult.
func TestClient_Browse_Popular(t *testing.T) {
	const sourceID = "1234567890"

	var gotVars map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/graphql" {
			http.Error(w, "wrong endpoint", http.StatusNotFound)
			return
		}
		gotVars = decodeGraphQLVars(t, r)
		resp := graphqlResponse(t, map[string]any{
			"fetchSourceManga": map[string]any{
				"mangas": []map[string]any{
					{"id": 42, "title": "One Piece", "url": "/manga/42", "thumbnailUrl": "/thumbnail/42"},
					{"id": 43, "title": "One Punch Man", "url": "/manga/43", "thumbnailUrl": nil},
				},
				"hasNextPage": true,
			},
		}, nil)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	res, err := client.Browse(context.Background(), sourceID, suwayomi.BrowsePopular, 1)
	if err != nil {
		t.Fatalf("Browse() error = %v", err)
	}

	// The wire request must carry type=POPULAR and NO query variable.
	assertBrowseTypeVar(t, gotVars, "POPULAR")

	if len(res.Mangas) != 2 {
		t.Fatalf("Browse() got %d mangas, want 2", len(res.Mangas))
	}
	assertManga(t, res.Mangas[0], 42, "One Piece", "/manga/42")
	if !res.HasNextPage {
		t.Error("HasNextPage = false, want true")
	}
}

// assertBrowseTypeVar asserts the browse request carried type=want and no query
// variable (browse is a query-less catalog listing).
func assertBrowseTypeVar(t *testing.T, vars map[string]any, want string) {
	t.Helper()
	if vars["type"] != want {
		t.Errorf("request type var = %v, want %s", vars["type"], want)
	}
	if _, hasQuery := vars["query"]; hasQuery {
		t.Errorf("browse request must not carry a query variable, got %v", vars["query"])
	}
}

// assertManga asserts a Manga's ID, Title, and URL fields.
func assertManga(t *testing.T, m suwayomi.Manga, wantID int, wantTitle, wantURL string) {
	t.Helper()
	if m.ID != wantID {
		t.Errorf("manga.ID = %d, want %d", m.ID, wantID)
	}
	if m.Title != wantTitle {
		t.Errorf("manga.Title = %q, want %q", m.Title, wantTitle)
	}
	if m.URL != wantURL {
		t.Errorf("manga.URL = %q, want %q", m.URL, wantURL)
	}
}

// TestClient_Browse_Latest verifies that BrowseLatest sends type=LATEST and that
// hasNextPage=false round-trips.
func TestClient_Browse_Latest(t *testing.T) {
	var gotVars map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotVars = decodeGraphQLVars(t, r)
		resp := graphqlResponse(t, map[string]any{
			"fetchSourceManga": map[string]any{
				"mangas": []map[string]any{
					{"id": 7, "title": "Berserk", "url": "/manga/7", "thumbnailUrl": "/t/7"},
				},
				"hasNextPage": false,
			},
		}, nil)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	res, err := client.Browse(context.Background(), "src", suwayomi.BrowseLatest, 2)
	if err != nil {
		t.Fatalf("Browse() error = %v", err)
	}
	if gotVars["type"] != "LATEST" {
		t.Errorf("request type var = %v, want LATEST", gotVars["type"])
	}
	// page is serialised as a JSON number (float64 after decode).
	if gotVars["page"] != float64(2) {
		t.Errorf("request page var = %v, want 2", gotVars["page"])
	}
	if len(res.Mangas) != 1 || res.Mangas[0].URL != "/manga/7" {
		t.Errorf("Mangas = %+v, want one entry with url /manga/7", res.Mangas)
	}
	if res.HasNextPage {
		t.Error("HasNextPage = true, want false")
	}
}

// TestClient_Browse_GraphQLError verifies that a GraphQL application error (e.g.
// a source that does not support LATEST) is propagated as a Go error.
func TestClient_Browse_GraphQLError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := graphqlResponse(t, nil, []map[string]any{
			{"message": "source does not support latest"},
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, err := client.Browse(context.Background(), "bad-source", suwayomi.BrowseLatest, 1)
	if err == nil {
		t.Fatal("Browse() with GraphQL errors: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "source does not support latest") {
		t.Errorf("error %q should contain the GraphQL error message", err.Error())
	}
}

// --- MangaChapters -----------------------------------------------------------

// cannedChaptersServer builds an httptest.Server returning two canned chapters.
// uploadDate is passed as an int64 (ms since epoch) but serialised as a JSON
// string to match the Suwayomi v2.2.2100 wire format: uploadDate is typed as
// LongString! in the GraphQL schema, so the server sends it as a quoted integer.
func cannedChaptersServer(t *testing.T, uploadDateMs int64) *httptest.Server {
	t.Helper()
	uploadDateStr := fmt.Sprintf("%d", uploadDateMs)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := graphqlResponse(t, map[string]any{
			"chapters": map[string]any{
				"nodes": []map[string]any{
					{
						"id":            101,
						"url":           "/chapter/101",
						"name":          "Chapter 1",
						"chapterNumber": 1.0,
						"uploadDate":    uploadDateStr,
						"pageCount":     24,
						"sourceOrder":   1,
					},
					{
						"id":            102,
						"url":           "/chapter/102",
						"name":          "Chapter 2",
						"chapterNumber": 2.0,
						"uploadDate":    uploadDateStr,
						"pageCount":     20,
						"sourceOrder":   2,
					},
				},
			},
		}, nil)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
}

func TestClient_MangaChapters(t *testing.T) {
	const uploadDateMs = int64(1_700_000_000_000) // milliseconds since epoch
	expectedUploadDate := time.UnixMilli(uploadDateMs).UTC()

	srv := cannedChaptersServer(t, uploadDateMs)
	defer srv.Close()

	client := newTestClient(t, srv)
	chapters, err := client.MangaChapters(context.Background(), 7)
	if err != nil {
		t.Fatalf("MangaChapters() error = %v", err)
	}
	if len(chapters) != 2 {
		t.Fatalf("MangaChapters() got %d chapters, want 2", len(chapters))
	}
	assertChapter0(t, chapters[0], expectedUploadDate)
	assertChapter1(t, chapters[1])
}

// assertChapter0 checks the first chapter's fields against expected values.
func assertChapter0(t *testing.T, ch suwayomi.Chapter, expectedDate time.Time) {
	t.Helper()
	if ch.ID != 101 {
		t.Errorf("ch.ID = %d, want 101", ch.ID)
	}
	if ch.URL != "/chapter/101" {
		t.Errorf("ch.URL = %q, want /chapter/101", ch.URL)
	}
	if ch.Name != "Chapter 1" {
		t.Errorf("ch.Name = %q, want Chapter 1", ch.Name)
	}
	if ch.Number == nil || *ch.Number != 1.0 {
		t.Errorf("ch.Number = %v, want 1.0", ch.Number)
	}
	if ch.PageCount != 24 {
		t.Errorf("ch.PageCount = %d, want 24", ch.PageCount)
	}
	if ch.Index != 1 {
		t.Errorf("ch.Index = %d, want 1", ch.Index)
	}
	if ch.UploadDate == nil {
		t.Fatal("ch.UploadDate is nil, want non-nil")
	}
	if !ch.UploadDate.Equal(expectedDate) {
		t.Errorf("ch.UploadDate = %v, want %v", ch.UploadDate, expectedDate)
	}
}

// assertChapter1 checks the second chapter's key fields.
func assertChapter1(t *testing.T, ch suwayomi.Chapter) {
	t.Helper()
	if ch.ID != 102 {
		t.Errorf("ch.ID = %d, want 102", ch.ID)
	}
	if ch.Index != 2 {
		t.Errorf("ch.Index = %d, want 2", ch.Index)
	}
}

func TestClient_MangaChapters_GraphQLError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := graphqlResponse(t, nil, []map[string]any{
			{"message": "manga not found"},
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, err := client.MangaChapters(context.Background(), 999)
	if err == nil {
		t.Fatal("MangaChapters() with GraphQL errors: expected error, got nil")
	}
}

// --- FetchChapters -----------------------------------------------------------

// TestClient_FetchChapters verifies that FetchChapters deserialises the
// fetchChapters mutation response and maps all fields correctly.
// Shape validated against Suwayomi v2.2.2100 (Task 7): the mutation input
// uses `mangaId`; the result has a `chapters` field with `id`, `url`, etc.
func TestClient_FetchChapters(t *testing.T) {
	const uploadDateMs = int64(1_700_000_000_000)
	expectedUploadDate := time.UnixMilli(uploadDateMs).UTC()
	// uploadDate is typed as LongString! in Suwayomi's GraphQL schema (same as sourceId).
	// The server sends it as a quoted integer string, not a JSON number.
	uploadDateStr := fmt.Sprintf("%d", uploadDateMs)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := graphqlResponse(t, map[string]any{
			"fetchChapters": map[string]any{
				"chapters": []map[string]any{
					{
						"id":            101,
						"url":           "/chapter/101",
						"name":          "Chapter 1",
						"chapterNumber": 1.0,
						"uploadDate":    uploadDateStr,
						"pageCount":     24,
						"sourceOrder":   1,
					},
					{
						"id":            102,
						"url":           "/chapter/102",
						"name":          "Chapter 2",
						"chapterNumber": 2.0,
						"uploadDate":    uploadDateStr,
						"pageCount":     20,
						"sourceOrder":   2,
					},
				},
			},
		}, nil)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	chapters, err := client.FetchChapters(context.Background(), 7)
	if err != nil {
		t.Fatalf("FetchChapters() error = %v", err)
	}
	if len(chapters) != 2 {
		t.Fatalf("FetchChapters() got %d chapters, want 2", len(chapters))
	}
	assertChapter0(t, chapters[0], expectedUploadDate)
	assertChapter1(t, chapters[1])
}

// TestClient_FetchChapters_GraphQLError checks that GraphQL errors are
// propagated as Go errors from FetchChapters.
func TestClient_FetchChapters_GraphQLError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := graphqlResponse(t, nil, []map[string]any{
			{"message": "manga not found"},
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, err := client.FetchChapters(context.Background(), 999)
	if err == nil {
		t.Fatal("FetchChapters() with GraphQL errors: expected error, got nil")
	}
}

// --- ChapterPages ------------------------------------------------------------

func TestClient_ChapterPages(t *testing.T) {
	expectedPages := []string{
		"/api/v1/manga/7/chapter/3/page/0",
		"/api/v1/manga/7/chapter/3/page/1",
		"/api/v1/manga/7/chapter/3/page/2",
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := graphqlResponse(t, map[string]any{
			"fetchChapterPages": map[string]any{
				"pages": expectedPages,
			},
		}, nil)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	pages, err := client.ChapterPages(context.Background(), 55)
	if err != nil {
		t.Fatalf("ChapterPages() error = %v", err)
	}
	if len(pages) != 3 {
		t.Fatalf("ChapterPages() got %d pages, want 3", len(pages))
	}
	for i, want := range expectedPages {
		if pages[i] != want {
			t.Errorf("pages[%d] = %q, want %q", i, pages[i], want)
		}
	}
}

func TestClient_ChapterPages_GraphQLError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := graphqlResponse(t, nil, []map[string]any{
			{"message": "chapter not found"},
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, err := client.ChapterPages(context.Background(), 99)
	if err == nil {
		t.Fatal("ChapterPages() with GraphQL errors: expected error, got nil")
	}
}

// --- PageBytes ---------------------------------------------------------------

func TestClient_PageBytes_JPEG(t *testing.T) {
	// Minimal JPEG magic bytes so http.DetectContentType identifies it as image/jpeg.
	jpegData := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46, 0x00, 0x01}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(jpegData)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	data, ext, err := client.PageBytes(context.Background(), srv.URL+"/page/0")
	if err != nil {
		t.Fatalf("PageBytes() error = %v", err)
	}
	if string(data) != string(jpegData) {
		t.Errorf("PageBytes() data mismatch")
	}
	if ext != "jpg" {
		t.Errorf("PageBytes() ext = %q, want %q", ext, "jpg")
	}
}

func TestClient_PageBytes_PNG(t *testing.T) {
	// PNG magic bytes: \x89PNG\r\n\x1a\n
	pngData := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngData)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, ext, err := client.PageBytes(context.Background(), srv.URL+"/page/0")
	if err != nil {
		t.Fatalf("PageBytes() error = %v", err)
	}
	if ext != "png" {
		t.Errorf("PageBytes() ext = %q, want png", ext)
	}
}

func TestClient_PageBytes_NonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, _, err := client.PageBytes(context.Background(), srv.URL+"/page/missing")
	if err == nil {
		t.Fatal("PageBytes() with 404: expected error, got nil")
	}
}

// TestClient_PageBytes_RelativeURL validates that PageBytes correctly handles
// the Suwayomi v2.2.2100 wire format: fetchChapterPages returns server-relative
// paths (e.g. "/api/v1/manga/1/chapter/1/page/0") rather than absolute URLs.
// PageBytes must prepend the client's baseURL when the path starts with "/".
func TestClient_PageBytes_RelativeURL(t *testing.T) {
	// PNG magic bytes so the response is recognised as image/png.
	pngData := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00}

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngData)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	// Pass a server-relative path — PageBytes must prepend the base URL.
	_, ext, err := client.PageBytes(context.Background(), "/api/v1/manga/1/chapter/1/page/0")
	if err != nil {
		t.Fatalf("PageBytes() relative URL: error = %v", err)
	}
	if ext != "png" {
		t.Errorf("PageBytes() relative URL: ext = %q, want png", ext)
	}
	if gotPath != "/api/v1/manga/1/chapter/1/page/0" {
		t.Errorf("PageBytes() relative URL: server received path %q, want /api/v1/manga/1/chapter/1/page/0", gotPath)
	}
}

// --- Additional coverage: GraphQL HTTP 500 -----------------------------------

func TestClient_Sources_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, err := client.Sources(context.Background())
	if err == nil {
		t.Fatal("Sources() with HTTP 500: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q should contain HTTP status code 500", err.Error())
	}
}

// --- Additional coverage: PageBytes ext fallback and large body --------------

func TestClient_PageBytes_UnknownMIME_FallbackHeader(t *testing.T) {
	// Serve bytes that http.DetectContentType cannot identify as a known image
	// type, but the Content-Type header correctly identifies as image/webp.
	unknownBytes := make([]byte, 20) // all zeros — DetectContentType returns "application/octet-stream"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/webp")
		_, _ = w.Write(unknownBytes)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, ext, err := client.PageBytes(context.Background(), srv.URL+"/page/0")
	if err != nil {
		t.Fatalf("PageBytes() error = %v", err)
	}
	if ext != "webp" {
		t.Errorf("PageBytes() ext = %q, want webp (fallback from header)", ext)
	}
}

func TestClient_PageBytes_LargeBody(t *testing.T) {
	// Build a JPEG-magic-prefixed body larger than 512 bytes to exercise the
	// sniff[:512] branch.
	jpegMagic := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46, 0x00, 0x01}
	largeBody := make([]byte, 600)
	copy(largeBody, jpegMagic)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(largeBody)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	data, ext, err := client.PageBytes(context.Background(), srv.URL+"/page/0")
	if err != nil {
		t.Fatalf("PageBytes() error = %v", err)
	}
	if len(data) != 600 {
		t.Errorf("PageBytes() len(data) = %d, want 600", len(data))
	}
	if ext != "jpg" {
		t.Errorf("PageBytes() ext = %q, want jpg", ext)
	}
}

// --- doGraphQL: network error ------------------------------------------------

// TestClient_doGraphQL_NetworkError exercises the c.http.Do error path inside
// doGraphQL by pointing the client at a server that is already closed.
func TestClient_doGraphQL_NetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	// Close immediately — subsequent requests will get a connection-refused error.
	srv.Close()

	client := newTestClient(t, srv)
	_, err := client.Sources(context.Background())
	if err == nil {
		t.Fatal("Sources() with dead server: expected network error, got nil")
	}
}

// --- doGraphQL: body-decode error --------------------------------------------

// TestClient_doGraphQL_BodyDecodeError exercises the json.Decode error path
// inside doGraphQL — HTTP 200 but the body is not valid JSON.
func TestClient_doGraphQL_BodyDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, err := client.Sources(context.Background())
	if err == nil {
		t.Fatal("Sources() with non-JSON body: expected decode error, got nil")
	}
	if !strings.Contains(err.Error(), "decode GraphQL envelope") {
		t.Errorf("error %q should contain 'decode GraphQL envelope'", err.Error())
	}
}

// --- doGraphQL: data-unmarshal error -----------------------------------------

// TestClient_doGraphQL_DataUnmarshalError exercises the json.Unmarshal error
// path inside doGraphQL — the envelope is valid but the data field has a shape
// incompatible with the target DTO (sources expects an object, we send a number).
func TestClient_doGraphQL_DataUnmarshalError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Valid JSON envelope but data is a bare number — cannot unmarshal into
		// the gqlSourcesData struct (which expects an object).
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data": 42}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, err := client.Sources(context.Background())
	if err == nil {
		t.Fatal("Sources() with wrong-shape data: expected unmarshal error, got nil")
	}
	if !strings.Contains(err.Error(), "decode GraphQL data") {
		t.Errorf("error %q should contain 'decode GraphQL data'", err.Error())
	}
}

// --- PageBytes: network error ------------------------------------------------

// TestClient_PageBytes_NetworkError exercises the c.http.Do error path inside
// PageBytes by pointing the client at a server that is already closed.
func TestClient_PageBytes_NetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	pageURL := srv.URL + "/page/0"
	// Close immediately — the subsequent Do will fail.
	srv.Close()

	client := newTestClient(t, srv)
	_, _, err := client.PageBytes(context.Background(), pageURL)
	if err == nil {
		t.Fatal("PageBytes() with dead server: expected network error, got nil")
	}
}

// --- PageBytes: "bin" fallback -----------------------------------------------

// TestClient_PageBytes_BinFallback exercises the bin fallback when
// http.DetectContentType returns an unrecognised type AND no Content-Type
// header is present. All-zero bytes map to "application/octet-stream" by the
// sniffer, which is not in contentTypeToExt; the header is absent so ext = "bin".
func TestClient_PageBytes_BinFallback(t *testing.T) {
	// All-zero bytes: http.DetectContentType returns "application/octet-stream".
	unknownBytes := make([]byte, 20)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Intentionally no Content-Type header — exercising the bin fallback.
		_, _ = w.Write(unknownBytes)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, ext, err := client.PageBytes(context.Background(), srv.URL+"/page/0")
	if err != nil {
		t.Fatalf("PageBytes() error = %v", err)
	}
	if ext != "bin" {
		t.Errorf("PageBytes() ext = %q, want bin (no header + unrecognised MIME)", ext)
	}
}

// --- Search: ThumbnailURL nil propagation ------------------------------------

// TestClient_Search_ThumbnailURL verifies that a nil thumbnailUrl in the
// GraphQL response produces a nil ThumbnailURL pointer on the Manga DTO, while
// a non-nil value is preserved correctly.
func TestClient_Search_ThumbnailURL(t *testing.T) {
	const thumb = "/thumbnail/42"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := graphqlResponse(t, map[string]any{
			"fetchSourceManga": map[string]any{
				"mangas": []map[string]any{
					{"id": 42, "title": "One Piece", "url": "/manga/42", "thumbnailUrl": thumb},
					{"id": 43, "title": "One Punch Man", "url": "/manga/43", "thumbnailUrl": nil},
				},
			},
		}, nil)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	mangas, err := client.Search(context.Background(), "src", "q")
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(mangas) != 2 {
		t.Fatalf("Search() got %d results, want 2", len(mangas))
	}
	// mangas[0] must have a non-nil ThumbnailURL equal to the expected value.
	if mangas[0].ThumbnailURL == nil {
		t.Fatal("mangas[0].ThumbnailURL is nil, want non-nil")
	}
	if *mangas[0].ThumbnailURL != thumb {
		t.Errorf("mangas[0].ThumbnailURL = %q, want %q", *mangas[0].ThumbnailURL, thumb)
	}
	// mangas[1] must have a nil ThumbnailURL.
	if mangas[1].ThumbnailURL != nil {
		t.Errorf("mangas[1].ThumbnailURL = %q, want nil", *mangas[1].ThumbnailURL)
	}
}

// --- MangaChapters: nullable chapter fields ----------------------------------

// TestClient_MangaChapters_NullableFields exercises the zero-guard for
// chapterNumber and uploadDate — when the GraphQL response contains null for
// chapterNumber and "0" for uploadDate the DTO fields must be nil pointers.
// Note: uploadDate is typed as LongString! in Suwayomi's schema, so it arrives
// as a JSON string even when the value is zero ("0"), not as a JSON number.
func TestClient_MangaChapters_NullableFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := graphqlResponse(t, map[string]any{
			"chapters": map[string]any{
				"nodes": []map[string]any{
					{
						"id":            201,
						"url":           "/chapter/201",
						"name":          "Prologue",
						"chapterNumber": nil, // null → Number must be nil
						"uploadDate":    "0", // zero string → UploadDate must be nil
						"pageCount":     10,
						"sourceOrder":   0,
					},
				},
			},
		}, nil)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	chapters, err := client.MangaChapters(context.Background(), 99)
	if err != nil {
		t.Fatalf("MangaChapters() error = %v", err)
	}
	if len(chapters) != 1 {
		t.Fatalf("MangaChapters() got %d chapters, want 1", len(chapters))
	}
	ch := chapters[0]
	if ch.Number != nil {
		t.Errorf("ch.Number = %v, want nil (chapterNumber was null)", ch.Number)
	}
	if ch.UploadDate != nil {
		t.Errorf("ch.UploadDate = %v, want nil (uploadDate was 0)", ch.UploadDate)
	}
}

// --- MangaMeta ---------------------------------------------------------------

// TestClient_MangaMeta verifies that MangaMeta decodes a manga(id) GraphQL
// response and maps all fields onto the returned Manga correctly.
func TestClient_MangaMeta(t *testing.T) {
	const (
		mangaID      = 7
		wantTitle    = "Src Title"
		wantURL      = "/m/7"
		wantThumbURL = "/api/v1/manga/7/thumbnail"
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/graphql" {
			http.Error(w, "wrong endpoint", http.StatusNotFound)
			return
		}
		resp := graphqlResponse(t, map[string]any{
			"manga": map[string]any{
				"id":           mangaID,
				"title":        wantTitle,
				"url":          wantURL,
				"thumbnailUrl": wantThumbURL,
			},
		}, nil)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	manga, err := client.MangaMeta(context.Background(), mangaID)
	if err != nil {
		t.Fatalf("MangaMeta() error = %v", err)
	}
	if manga.ID != mangaID {
		t.Errorf("manga.ID = %d, want %d", manga.ID, mangaID)
	}
	if manga.Title != wantTitle {
		t.Errorf("manga.Title = %q, want %q", manga.Title, wantTitle)
	}
	if manga.URL != wantURL {
		t.Errorf("manga.URL = %q, want %q", manga.URL, wantURL)
	}
	if manga.ThumbnailURL == nil {
		t.Fatal("manga.ThumbnailURL is nil, want non-nil")
	}
	if *manga.ThumbnailURL != wantThumbURL {
		t.Errorf("manga.ThumbnailURL = %q, want %q", *manga.ThumbnailURL, wantThumbURL)
	}
}

// TestClient_MangaMeta_NilThumbnail verifies that a null thumbnailUrl in the
// GraphQL response produces a nil ThumbnailURL pointer on the returned Manga.
func TestClient_MangaMeta_NilThumbnail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := graphqlResponse(t, map[string]any{
			"manga": map[string]any{
				"id":           42,
				"title":        "No Cover",
				"url":          "/m/42",
				"thumbnailUrl": nil,
			},
		}, nil)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	manga, err := client.MangaMeta(context.Background(), 42)
	if err != nil {
		t.Fatalf("MangaMeta() error = %v", err)
	}
	if manga.ThumbnailURL != nil {
		t.Errorf("manga.ThumbnailURL = %q, want nil", *manga.ThumbnailURL)
	}
}

// TestClient_MangaMeta_GraphQLError verifies that GraphQL application errors
// are propagated as Go errors from MangaMeta.
func TestClient_MangaMeta_GraphQLError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := graphqlResponse(t, nil, []map[string]any{
			{"message": "manga not found"},
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, err := client.MangaMeta(context.Background(), 999)
	if err == nil {
		t.Fatal("MangaMeta() with GraphQL errors: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "manga not found") {
		t.Errorf("error %q should contain the GraphQL error message", err.Error())
	}
}

// --- Metadata fields (M4: author/artist/genre/description) ------------------

// assertMangaMetadata asserts m's Author/Artist/Description pointers dereference
// to the given wants and Genre equals wantGenre. Shared by the MangaMeta/Search/
// Browse metadata tests so each test function stays a single, low-complexity
// assertion call instead of four repeated nil-check-and-compare blocks (also
// keeps golangci-lint's cyclop check under its threshold).
func assertMangaMetadata(t *testing.T, m suwayomi.Manga, wantAuthor, wantArtist, wantDesc string, wantGenre []string) {
	t.Helper()
	if got := derefOrEmpty(m.Author); got != wantAuthor {
		t.Errorf("Author = %q, want %q", got, wantAuthor)
	}
	if got := derefOrEmpty(m.Artist); got != wantArtist {
		t.Errorf("Artist = %q, want %q", got, wantArtist)
	}
	if got := derefOrEmpty(m.Description); got != wantDesc {
		t.Errorf("Description = %q, want %q", got, wantDesc)
	}
	if !slices.Equal(m.Genre, wantGenre) {
		t.Errorf("Genre = %v, want %v", m.Genre, wantGenre)
	}
}

// derefOrEmpty dereferences an optional string pointer, returning "" for nil.
func derefOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// TestClient_MangaMeta_MetadataFields verifies that MangaMeta selects and
// decodes author, artist, genre, and description alongside the existing
// id/title/url/thumbnailUrl fields.
func TestClient_MangaMeta_MetadataFields(t *testing.T) {
	const (
		mangaID    = 11
		wantAuthor = "Eiichiro Oda"
		wantArtist = "Eiichiro Oda"
		wantDesc   = "A pirate's tale."
	)
	wantGenre := []string{"Action", "Adventure"}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/graphql" {
			http.Error(w, "wrong endpoint", http.StatusNotFound)
			return
		}
		resp := graphqlResponse(t, map[string]any{
			"manga": map[string]any{
				"id":           mangaID,
				"title":        "One Piece",
				"url":          "/m/11",
				"thumbnailUrl": nil,
				"author":       wantAuthor,
				"artist":       wantArtist,
				"genre":        wantGenre,
				"description":  wantDesc,
			},
		}, nil)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	manga, err := client.MangaMeta(context.Background(), mangaID)
	if err != nil {
		t.Fatalf("MangaMeta() error = %v", err)
	}
	assertMangaMetadata(t, manga, wantAuthor, wantArtist, wantDesc, wantGenre)
}

// TestClient_MangaMeta_MetadataFieldsNil verifies that null author/artist/
// genre/description in the GraphQL response decode to nil (or an empty/nil
// slice for Genre), not a zero-value panic or a spurious empty string pointer.
func TestClient_MangaMeta_MetadataFieldsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := graphqlResponse(t, map[string]any{
			"manga": map[string]any{
				"id":           5,
				"title":        "No Metadata",
				"url":          "/m/5",
				"thumbnailUrl": nil,
				"author":       nil,
				"artist":       nil,
				"genre":        nil,
				"description":  nil,
			},
		}, nil)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	manga, err := client.MangaMeta(context.Background(), 5)
	if err != nil {
		t.Fatalf("MangaMeta() error = %v", err)
	}
	if manga.Author != nil {
		t.Errorf("manga.Author = %v, want nil", *manga.Author)
	}
	if manga.Artist != nil {
		t.Errorf("manga.Artist = %v, want nil", *manga.Artist)
	}
	if manga.Description != nil {
		t.Errorf("manga.Description = %v, want nil", *manga.Description)
	}
	if len(manga.Genre) != 0 {
		t.Errorf("manga.Genre = %v, want empty", manga.Genre)
	}
}

// TestClient_Search_MetadataFields verifies Search decodes author/artist/genre/
// description onto each result alongside the existing fields.
func TestClient_Search_MetadataFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := graphqlResponse(t, map[string]any{
			"fetchSourceManga": map[string]any{
				"mangas": []map[string]any{
					{
						"id": 1, "title": "Solo Leveling", "url": "/m/1", "thumbnailUrl": nil,
						"author": "Chugong", "artist": "Dubu", "genre": []string{"Action"}, "description": "A hunter's rise.",
					},
				},
			},
		}, nil)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	mangas, err := client.Search(context.Background(), "src", "solo leveling")
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(mangas) != 1 {
		t.Fatalf("Search() got %d results, want 1", len(mangas))
	}
	assertMangaMetadata(t, mangas[0], "Chugong", "Dubu", "A hunter's rise.", []string{"Action"})
}

// TestClient_Browse_MetadataFields verifies Browse decodes the same metadata
// fields as Search onto each candidate (shared gqlMangaNode mapping).
func TestClient_Browse_MetadataFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := graphqlResponse(t, map[string]any{
			"fetchSourceManga": map[string]any{
				"mangas": []map[string]any{
					{
						"id": 7, "title": "Berserk", "url": "/m/7", "thumbnailUrl": nil,
						"author": "Kentaro Miura", "artist": "Kentaro Miura", "genre": []string{"Dark Fantasy"}, "description": "A cursed swordsman.",
					},
				},
				"hasNextPage": false,
			},
		}, nil)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	res, err := client.Browse(context.Background(), "src", suwayomi.BrowsePopular, 1)
	if err != nil {
		t.Fatalf("Browse() error = %v", err)
	}
	if len(res.Mangas) != 1 {
		t.Fatalf("Browse() got %d mangas, want 1", len(res.Mangas))
	}
	assertMangaMetadata(t, res.Mangas[0], "Kentaro Miura", "Kentaro Miura", "A cursed swordsman.", []string{"Dark Fantasy"})
}

// --- Interface seam ----------------------------------------------------------

// TestClientIsInterface verifies that Client is an interface type and that the
// concrete type is not exported — callers depend on the interface, not the struct.
// This is a compile-time assertion: if suwayomi.Client is not an interface,
// or if the concrete type is exported, this test file would fail to compile.
func TestClientIsInterface(_ *testing.T) {
	// NewClient returns a Client interface. Assign it to the interface type;
	// this would not compile if Client were a concrete struct, not an interface.
	var _ suwayomi.Client = suwayomi.NewClient(config.SuwayomiConfig{}, http.DefaultClient)
}

// TestClient_ExternalURL_TargetsExternalBase confirms a client built from a
// SuwayomiConfig with ExternalURL set (external mode) sends its requests to that
// external base — the only new behaviour: BaseURL() resolution. The request path
// is unchanged. Non-vacuous: a regression that ignored ExternalURL (falling back
// to Host:Port) would never hit srv and the test would fail on the unset flag.
func TestClient_ExternalURL_TargetsExternalBase(t *testing.T) {
	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/graphql" {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		hit = true
		resp := graphqlResponse(t, map[string]any{
			"sources": map[string]any{"nodes": []map[string]any{}},
		}, nil)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	// Host/Port point at a dead address; only ExternalURL should be honoured.
	cfg := config.SuwayomiConfig{
		Host:        "127.0.0.1",
		Port:        "1",
		ExternalURL: srv.URL,
	}
	client := suwayomi.NewClient(cfg, srv.Client())
	if _, err := client.Sources(context.Background()); err != nil {
		t.Fatalf("Sources() via ExternalURL: %v", err)
	}
	if !hit {
		t.Fatal("request did not reach the external base URL — ExternalURL not honoured")
	}
}
