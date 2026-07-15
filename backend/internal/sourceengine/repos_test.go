package sourceengine_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// TestRepos_Success proves GET /repos unwraps the {repos:[...]} response into
// []string.
func TestRepos_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/repos" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{"repos": []string{"https://a/index.min.json"}})
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).Repos(context.Background())
	if err != nil {
		t.Fatalf("Repos: %v", err)
	}
	want := []string{"https://a/index.min.json"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Repos = %+v, want %+v", got, want)
	}
}

// TestSetRepos_Success proves PUT /repos sends {repos:[...]} and returns the
// refreshed list.
func TestSetRepos_Success(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/repos" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		decodeBody(t, r, &captured)
		writeJSON(t, w, http.StatusOK, map[string]any{"repos": []string{"https://b/index.min.json"}})
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).SetRepos(context.Background(), []string{"https://b/index.min.json"})
	if err != nil {
		t.Fatalf("SetRepos: %v", err)
	}
	want := []string{"https://b/index.min.json"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SetRepos = %+v, want %+v", got, want)
	}
	sent, _ := captured["repos"].([]any)
	if len(sent) != 1 || sent[0] != "https://b/index.min.json" {
		t.Errorf("request body repos = %+v", captured["repos"])
	}
}

// TestRepos_BadRequest proves a 400 maps to *BadRequestError.
func TestRepos_BadRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).SetRepos(context.Background(), nil)
	assertBadRequestError(t, err)
}

// TestRepos_UpstreamFailure proves a 502 maps to *UpstreamError.
func TestRepos_UpstreamFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadGateway, map[string]string{"error": "boom"})
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Repos(context.Background())
	assertUpstreamError(t, err, http.StatusBadGateway)
}
