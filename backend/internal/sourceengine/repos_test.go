package sourceengine_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/technobecet/tsundoku/internal/sourceengine"
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

func TestRepoTrust_SuccessAuthenticated(t *testing.T) {
	const token = "engine-control-token"
	trust := map[string]string{"https://repo.test/index.json": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/repos/trust" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("Authorization = %q", got)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{"trust": trust})
	}))
	defer srv.Close()

	got, err := sourceengine.New(srv.URL, srv.Client(), token).RepoTrust(context.Background())
	if err != nil {
		t.Fatalf("RepoTrust: %v", err)
	}
	if !reflect.DeepEqual(got, trust) {
		t.Fatalf("RepoTrust = %+v, want %+v", got, trust)
	}
}

func TestSetRepoTrust_SuccessAuthenticated(t *testing.T) {
	const (
		token       = "engine-control-token"
		repoURL     = "https://repo.test/index.json"
		fingerprint = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/repos/trust" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("Authorization = %q", got)
		}
		decodeBody(t, r, &captured)
		writeJSON(t, w, http.StatusOK, map[string]any{"trust": map[string]string{repoURL: fingerprint}})
	}))
	defer srv.Close()

	got, err := sourceengine.New(srv.URL, srv.Client(), token).SetRepoTrust(context.Background(), repoURL, fingerprint)
	if err != nil {
		t.Fatalf("SetRepoTrust: %v", err)
	}
	if captured["repoUrl"] != repoURL || captured["signerFingerprint"] != fingerprint {
		t.Fatalf("request body = %+v", captured)
	}
	if got[repoURL] != fingerprint {
		t.Fatalf("SetRepoTrust = %+v", got)
	}
}
