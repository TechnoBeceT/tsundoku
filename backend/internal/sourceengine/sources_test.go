package sourceengine_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// TestSources_Success proves GET /sources decodes the plain array response
// into []Source.
func TestSources_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/sources" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		writeJSON(t, w, http.StatusOK, []map[string]any{
			{"id": 1, "name": "MangaDex", "lang": "en"},
			{"id": 2, "name": "Comix", "lang": "en"},
		})
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).Sources(context.Background())
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	want := []sourceengine.Source{
		{ID: 1, Name: "MangaDex", Lang: "en"},
		{ID: 2, Name: "Comix", Lang: "en"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Sources = %+v, want %+v", got, want)
	}
}

// TestSources_BadRequest proves a 400 from /sources maps to *BadRequestError.
func TestSources_BadRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadRequest, map[string]string{"error": "bad request"})
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Sources(context.Background())
	assertBadRequestError(t, err)
}

// TestSources_UpstreamFailure proves a 502 from /sources maps to *UpstreamError.
func TestSources_UpstreamFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadGateway, map[string]string{"error": "boom"})
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Sources(context.Background())
	assertUpstreamError(t, err, http.StatusBadGateway)
}
