package sourceengine_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// TestPages_Success proves POST /pages sends {sourceId,chapterUrl,mangaUrl} and
// the wrapped {pages:[...]} response is unwrapped to []Page.
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

	got, err := newTestClient(t, srv).Pages(context.Background(), 7, "/manga/1/ch/1", "/manga/1")
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
	// The series URL must ride the wire so the engine can run its series-scoped
	// memo repopulation (GAP-109) — a dropped mangaUrl silently reverts the fix.
	if captured["mangaUrl"] != "/manga/1" {
		t.Errorf("request body mangaUrl = %v, want /manga/1", captured["mangaUrl"])
	}
	if captured["addressMode"] != "unknown" {
		t.Errorf("request addressMode = %v, want unknown compatibility default", captured["addressMode"])
	}
}

// TestPagesRef_PropagatesAndReturnsAddressMode protects the page warm-up
// resolver input and its successful resolved mode.
func TestPagesRef_PropagatesAndReturnsAddressMode(t *testing.T) {
	var request map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decodeBody(t, r, &request)
		writeJSON(t, w, http.StatusOK, map[string]any{
			"pages":       []map[string]any{{"index": 0, "url": "/p/0", "imageUrl": "https://img.test/0.jpg"}},
			"addressMode": "direct",
		})
	}))
	defer srv.Close()

	got, err := sourceengine.PagesFor(context.Background(), newTestClient(t, srv), sourceengine.ProviderRef{
		SourceID: 354, URL: "/opaque/354", AddressMode: sourceengine.AddressModeDirect, WebURL: "https://source.test/manga/354",
	}, "/opaque/354/ch/1")
	if err != nil {
		t.Fatalf("PagesRef: %v", err)
	}
	if request["mangaUrl"] != "/opaque/354" || request["addressMode"] != "direct" || request["webUrl"] != "https://source.test/manga/354" {
		t.Fatalf("request address context = %+v, want manga URL + direct + webUrl witness", request)
	}
	if got.AddressMode != sourceengine.AddressModeDirect || len(got.Pages) != 1 {
		t.Fatalf("result = %+v, want one page resolved as direct", got)
	}
}

// TestPages_BadRequest proves a 400 from /pages maps to *BadRequestError.
func TestPages_BadRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadRequest, map[string]string{"error": "unknown sourceId 1"})
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Pages(context.Background(), 1, "/manga/1/ch/1", "")
	assertBadRequestError(t, err)
}

// TestPages_UpstreamFailure proves a 502 from /pages maps to *UpstreamError.
func TestPages_UpstreamFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadGateway, map[string]string{"error": "boom"})
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Pages(context.Background(), 1, "/manga/1/ch/1", "")
	assertUpstreamError(t, err, http.StatusBadGateway)
}
