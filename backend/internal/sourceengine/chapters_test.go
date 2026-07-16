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

// TestChapters_Success proves POST /chapters sends {sourceId,url,mangaTitle}
// and the wrapped {chapters:[...]} response is unwrapped to []Chapter.
func TestChapters_Success(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/chapters" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{
			"chapters": []map[string]any{
				{"url": "/manga/1/ch/1", "name": "Chapter 1", "number": 1.0, "scanlator": "Reset Scans", "uploadDate": int64(1700000000000), "realUrl": "https://source.test/manga/1/ch/1"},
				{"url": "/manga/1/ch/2", "name": "Chapter 2", "number": 2.0, "scanlator": nil, "uploadDate": int64(0), "realUrl": nil},
			},
		})
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).Chapters(context.Background(), 7, "/manga/1", "My Series")
	if err != nil {
		t.Fatalf("Chapters: %v", err)
	}
	want := []sourceengine.Chapter{
		{URL: "/manga/1/ch/1", Name: "Chapter 1", Number: 1.0, Scanlator: "Reset Scans", UploadDate: 1700000000000, RealURL: "https://source.test/manga/1/ch/1"},
		{URL: "/manga/1/ch/2", Name: "Chapter 2", Number: 2.0, Scanlator: "", UploadDate: 0, RealURL: ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Chapters = %+v, want %+v", got, want)
	}
	if gotBody["mangaTitle"] != "My Series" {
		t.Errorf("request mangaTitle = %v, want %q", gotBody["mangaTitle"], "My Series")
	}
}

// TestChapters_BadRequest proves a 400 from /chapters maps to *BadRequestError.
func TestChapters_BadRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadRequest, map[string]string{"error": "unknown sourceId 1"})
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Chapters(context.Background(), 1, "/manga/1", "")
	assertBadRequestError(t, err)
}

// TestChapters_UpstreamFailure proves a 502 from /chapters maps to *UpstreamError.
func TestChapters_UpstreamFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadGateway, map[string]string{"error": "boom"})
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Chapters(context.Background(), 1, "/manga/1", "")
	assertUpstreamError(t, err, http.StatusBadGateway)
}
