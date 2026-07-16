package sourceengine_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// TestPages_Success proves POST /pages sends {sourceId,chapterUrl} and the
// wrapped {pages:[...]} response is unwrapped to []Page.
func TestPages_Success(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/pages" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		decodeBody(t, r, &captured)
		writeJSON(t, w, http.StatusOK, map[string]any{
			"pages": []map[string]any{
				{"index": 0, "url": "/manga/1/ch/1/page/0", "imageUrl": "https://x/p0.jpg"},
				{"index": 1, "url": "/manga/1/ch/1/page/1", "imageUrl": nil},
			},
		})
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).Pages(context.Background(), 7, "/manga/1/ch/1")
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	want := []sourceengine.Page{
		{Index: 0, URL: "/manga/1/ch/1/page/0", ImageURL: "https://x/p0.jpg"},
		{Index: 1, URL: "/manga/1/ch/1/page/1", ImageURL: ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Pages = %+v, want %+v", got, want)
	}
	if captured["chapterUrl"] != "/manga/1/ch/1" {
		t.Errorf("request body chapterUrl = %v, want /manga/1/ch/1", captured["chapterUrl"])
	}
}

// TestPages_BadRequest proves a 400 from /pages maps to *BadRequestError.
func TestPages_BadRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadRequest, map[string]string{"error": "unknown sourceId 1"})
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Pages(context.Background(), 1, "/manga/1/ch/1")
	assertBadRequestError(t, err)
}

// TestPages_UpstreamFailure proves a 502 from /pages maps to *UpstreamError.
func TestPages_UpstreamFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadGateway, map[string]string{"error": "boom"})
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Pages(context.Background(), 1, "/manga/1/ch/1")
	assertUpstreamError(t, err, http.StatusBadGateway)
}
