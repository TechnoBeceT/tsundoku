package sourceengine_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// TestMangaDetails_Success proves POST /manga sends {sourceId,url} and
// decodes the full MangaDetails shape.
func TestMangaDetails_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/manga" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{
			"url":          "/manga/1",
			"title":        "One Piece",
			"author":       "Eiichiro Oda",
			"artist":       "Eiichiro Oda",
			"description":  "A pirate adventure.",
			"genres":       []string{"Action", "Adventure"},
			"status":       "ONGOING",
			"thumbnailUrl": "https://x/cover.jpg",
		})
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).MangaDetails(context.Background(), 7, "/manga/1")
	if err != nil {
		t.Fatalf("MangaDetails: %v", err)
	}
	want := sourceengine.MangaDetails{
		URL:          "/manga/1",
		Title:        "One Piece",
		Author:       "Eiichiro Oda",
		Artist:       "Eiichiro Oda",
		Description:  "A pirate adventure.",
		Genres:       []string{"Action", "Adventure"},
		Status:       "ONGOING",
		ThumbnailURL: "https://x/cover.jpg",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MangaDetails = %+v, want %+v", got, want)
	}
}

// TestMangaDetails_BadRequest proves a 400 from /manga maps to *BadRequestError.
func TestMangaDetails_BadRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadRequest, map[string]string{"error": "unknown sourceId 1"})
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).MangaDetails(context.Background(), 1, "/manga/1")
	assertBadRequestError(t, err)
}

// TestMangaDetails_UpstreamFailure proves a 502 from /manga maps to *UpstreamError.
func TestMangaDetails_UpstreamFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadGateway, map[string]string{"error": "source unreachable"})
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).MangaDetails(context.Background(), 1, "/manga/1")
	assertUpstreamError(t, err, http.StatusBadGateway)
}
