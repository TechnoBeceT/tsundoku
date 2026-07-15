package sourceengine_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// TestImage_Success proves POST /image sends {sourceId,pageUrl,imageUrl} and
// returns the RAW response bytes + Content-Type header, NOT a JSON decode.
func TestImage_Success(t *testing.T) {
	var captured map[string]any
	want := []byte{0xFF, 0xD8, 0xFF, 0x00, 0x01, 0x02}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/image" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		decodeBody(t, r, &captured)
		w.Header().Set("Content-Type", "image/jpeg")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(want)
	}))
	defer srv.Close()

	data, contentType, err := newTestClient(t, srv).Image(context.Background(), 7, "/manga/1/ch/1/page/0", "https://x/p0.jpg")
	if err != nil {
		t.Fatalf("Image: %v", err)
	}
	if !bytes.Equal(data, want) {
		t.Errorf("Image data = %v, want %v", data, want)
	}
	if contentType != "image/jpeg" {
		t.Errorf("Image contentType = %q, want %q", contentType, "image/jpeg")
	}
	if captured["pageUrl"] != "/manga/1/ch/1/page/0" || captured["imageUrl"] != "https://x/p0.jpg" {
		t.Errorf("request body = %+v", captured)
	}
}

// TestImage_OmitsEmptyImageURL proves that an empty imageURL is OMITTED from
// the request body entirely (not sent as ""), so the engine host treats it as
// null and falls back to its own getImageUrl resolution.
func TestImage_OmitsEmptyImageURL(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decodeBody(t, r, &captured)
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte{0x89, 0x50})
	}))
	defer srv.Close()

	if _, _, err := newTestClient(t, srv).Image(context.Background(), 7, "/manga/1/ch/1/page/0", ""); err != nil {
		t.Fatalf("Image: %v", err)
	}
	if _, ok := captured["imageUrl"]; ok {
		t.Errorf("imageUrl must be omitted when empty, got %+v", captured)
	}
}

// TestImage_BadRequest proves a 400 from /image maps to *BadRequestError.
func TestImage_BadRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadRequest, map[string]string{"error": "unknown sourceId 1"})
	}))
	defer srv.Close()

	_, _, err := newTestClient(t, srv).Image(context.Background(), 1, "/p", "")
	assertBadRequestError(t, err)
}

// TestImage_UpstreamFailure proves a 502 from /image maps to *UpstreamError.
func TestImage_UpstreamFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadGateway, map[string]string{"error": "fetch failed"})
	}))
	defer srv.Close()

	_, _, err := newTestClient(t, srv).Image(context.Background(), 1, "/p", "")
	assertUpstreamError(t, err, http.StatusBadGateway)
}

// TestImage_NetworkFailure_IsWrapped proves a transport-level failure on the
// raw-bytes path (doRaw) is wrapped and returned, mirroring the JSON path's
// TestClient_NetworkFailure_IsWrapped.
func TestImage_NetworkFailure_IsWrapped(t *testing.T) {
	c := sourceengine.New("http://engine-host.invalid", failingDoer{})
	if _, _, err := c.Image(context.Background(), 1, "/p", ""); err == nil {
		t.Fatal("Image: want error from a failing doer, got nil")
	}
}
