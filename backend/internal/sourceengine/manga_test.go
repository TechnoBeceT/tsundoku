package sourceengine_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// TestMangaDetails_Success proves POST /manga sends {sourceId,url} and
// decodes the full MangaDetails shape.
func TestMangaDetails_Success(t *testing.T) {
	var request map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/manga" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request body: %v", err)
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
			"realUrl":      "https://example-source.test/manga/one-piece",
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
		RealURL:      "https://example-source.test/manga/one-piece",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MangaDetails = %+v, want %+v", got, want)
	}
	if request["addressMode"] != "unknown" {
		t.Errorf("request addressMode = %v, want unknown compatibility default", request["addressMode"])
	}
}

// TestMangaDetailsRef_PropagatesAddressContext catches either provenance field
// being dropped before the engine resolver and proves the resolved response mode
// reaches the caller.
func TestMangaDetailsRef_PropagatesAddressContext(t *testing.T) {
	var request map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decodeBody(t, r, &request)
		writeJSON(t, w, http.StatusOK, map[string]any{
			"url": "/opaque/354", "title": "Opaque", "addressMode": "direct",
		})
	}))
	defer srv.Close()

	got, err := sourceengine.MangaDetailsFor(context.Background(), newTestClient(t, srv), sourceengine.ProviderRef{
		SourceID: 354, URL: "/opaque/354", AddressMode: sourceengine.AddressModeDirect, WebURL: "https://source.test/manga/354",
	})
	if err != nil {
		t.Fatalf("MangaDetailsRef: %v", err)
	}
	if request["addressMode"] != "direct" || request["webUrl"] != "https://source.test/manga/354" {
		t.Fatalf("request address context = %+v, want direct + webUrl witness", request)
	}
	if got.AddressMode != sourceengine.AddressModeDirect {
		t.Fatalf("resolved address mode = %q, want direct", got.AddressMode.Wire())
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
