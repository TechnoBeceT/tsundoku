package sourceengine_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// searchResponseBody is the canned {manga, hasNextPage} body every
// Search/Popular/Latest test writes back.
func searchResponseBody() map[string]any {
	return map[string]any{
		"manga": []map[string]any{
			{"url": "/manga/1", "title": "One Piece", "thumbnailUrl": "https://x/cover.jpg"},
			{"url": "/manga/2", "title": "Naruto", "thumbnailUrl": nil},
		},
		"hasNextPage": true,
	}
}

func wantSearchResult() sourceengine.SearchResult {
	return sourceengine.SearchResult{
		Manga: []sourceengine.MangaEntry{
			{URL: "/manga/1", Title: "One Piece", ThumbnailURL: "https://x/cover.jpg"},
			{URL: "/manga/2", Title: "Naruto", ThumbnailURL: ""},
		},
		HasNextPage: true,
	}
}

// TestSearch_Success proves POST /search sends {sourceId,query,page} and
// decodes the {manga,hasNextPage} response into a SearchResult.
func TestSearch_Success(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/search" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		writeJSON(t, w, http.StatusOK, searchResponseBody())
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).Search(context.Background(), 7, "one piece", 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !searchResultEqual(got, wantSearchResult()) {
		t.Errorf("Search result = %+v, want %+v", got, wantSearchResult())
	}
	if captured["sourceId"] != float64(7) || captured["query"] != "one piece" || captured["page"] != float64(2) {
		t.Errorf("request body = %+v, want sourceId=7 query=%q page=2", captured, "one piece")
	}
}

// TestPopular_Success proves POST /popular sends {sourceId,page} (no query)
// and decodes the same SearchResult shape.
func TestPopular_Success(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/popular" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&captured)
		writeJSON(t, w, http.StatusOK, searchResponseBody())
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).Popular(context.Background(), 7, 1)
	if err != nil {
		t.Fatalf("Popular: %v", err)
	}
	if !searchResultEqual(got, wantSearchResult()) {
		t.Errorf("Popular result = %+v, want %+v", got, wantSearchResult())
	}
	if _, hasQuery := captured["query"]; hasQuery {
		t.Errorf("Popular request must not carry a query field, got %+v", captured)
	}
}

// TestLatest_Success proves POST /latest sends {sourceId,page} and decodes
// the same SearchResult shape.
func TestLatest_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/latest" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		writeJSON(t, w, http.StatusOK, searchResponseBody())
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).Latest(context.Background(), 7, 1)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if !searchResultEqual(got, wantSearchResult()) {
		t.Errorf("Latest result = %+v, want %+v", got, wantSearchResult())
	}
}

// TestSearch_BadRequest proves a 400 from /search maps to *BadRequestError.
func TestSearch_BadRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadRequest, map[string]string{"error": "unknown sourceId 1"})
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Search(context.Background(), 1, "q", 1)
	assertBadRequestError(t, err)
}

// TestSearch_UpstreamFailure proves a 502 from /search maps to *UpstreamError.
func TestSearch_UpstreamFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadGateway, map[string]string{"error": "source timed out"})
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Search(context.Background(), 1, "q", 1)
	assertUpstreamError(t, err, http.StatusBadGateway)
}

// searchResultEqual compares two SearchResult values field-by-field (they
// contain a slice, so == does not apply).
func searchResultEqual(a, b sourceengine.SearchResult) bool {
	if a.HasNextPage != b.HasNextPage || len(a.Manga) != len(b.Manga) {
		return false
	}
	for i := range a.Manga {
		if a.Manga[i] != b.Manga[i] {
			return false
		}
	}
	return true
}
