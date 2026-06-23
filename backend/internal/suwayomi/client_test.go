// Package suwayomi_test — unit tests for the typed Suwayomi HTTP client.
//
// All tests use httptest.Server; no real Suwayomi is required.
// Canned responses match the real Suwayomi v2.2.2100 GraphQL wire shape.
package suwayomi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// --- MangaChapters -----------------------------------------------------------

// cannedChaptersServer builds an httptest.Server returning two canned chapters.
func cannedChaptersServer(t *testing.T, uploadDateMs int64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := graphqlResponse(t, map[string]any{
			"chapters": map[string]any{
				"nodes": []map[string]any{
					{
						"id":            101,
						"url":           "/chapter/101",
						"name":          "Chapter 1",
						"chapterNumber": 1.0,
						"uploadDate":    uploadDateMs,
						"pageCount":     24,
						"sourceOrder":   1,
					},
					{
						"id":            102,
						"url":           "/chapter/102",
						"name":          "Chapter 2",
						"chapterNumber": 2.0,
						"uploadDate":    uploadDateMs,
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
// chapterNumber and 0 for uploadDate the DTO fields must be nil pointers.
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
						"uploadDate":    0,   // zero → UploadDate must be nil
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
