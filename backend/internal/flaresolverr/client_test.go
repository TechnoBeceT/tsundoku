// Package flaresolverr_test exercises Solve against a fake FlareSolverr HTTP
// server (httptest) — no real FlareSolverr instance is required.
package flaresolverr_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/flaresolverr"
)

// TestSolve_OK proves a successful FlareSolverr response is parsed into a
// Solution carrying cf_clearance + the browser User-Agent, and that the
// request body sent to FlareSolverr carries the expected command shape.
func TestSolve_OK(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1" {
			t.Errorf("path = %q, want /v1", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status": "ok",
			"message": "",
			"solution": {
				"url": "https://kitsu.app/api/edge/manga",
				"status": 200,
				"cookies": [
					{"name": "cf_clearance", "value": "abc123", "domain": ".kitsu.app", "path": "/"},
					{"name": "other", "value": "xyz", "domain": "kitsu.app", "path": "/"}
				],
				"userAgent": "Mozilla/5.0 (test browser)"
			}
		}`))
	}))
	defer srv.Close()

	sol, err := flaresolverr.Solve(context.Background(), srv.Client(), srv.URL, "https://kitsu.app/api/edge/manga", "tsundoku", 30*time.Second)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	assertSolutionOK(t, sol)
	assertSolveRequestBody(t, gotBody)
}

// assertSolutionOK checks the parsed Solution shape (split out of
// TestSolve_OK purely to keep that test's own cyclomatic complexity low).
func assertSolutionOK(t *testing.T, sol flaresolverr.Solution) {
	t.Helper()
	if sol.UserAgent != "Mozilla/5.0 (test browser)" {
		t.Errorf("UserAgent = %q, want the fake browser UA", sol.UserAgent)
	}
	if len(sol.Cookies) != 2 {
		t.Fatalf("Cookies len = %d, want 2", len(sol.Cookies))
	}
	found := false
	for _, c := range sol.Cookies {
		if c.Name != "cf_clearance" {
			continue
		}
		found = true
		if c.Value != "abc123" {
			t.Errorf("cf_clearance value = %q, want abc123", c.Value)
		}
		if c.Domain != "kitsu.app" {
			t.Errorf("cf_clearance domain = %q, want kitsu.app (leading dot stripped)", c.Domain)
		}
	}
	if !found {
		t.Error("cf_clearance cookie not found in Solution.Cookies")
	}
}

// assertSolveRequestBody checks the request body Solve sent to FlareSolverr
// carries the expected command shape.
func assertSolveRequestBody(t *testing.T, gotBody map[string]any) {
	t.Helper()
	if gotBody["cmd"] != "request.get" {
		t.Errorf("cmd = %v, want request.get", gotBody["cmd"])
	}
	if gotBody["url"] != "https://kitsu.app/api/edge/manga" {
		t.Errorf("url = %v, want the target URL", gotBody["url"])
	}
	if gotBody["session"] != "tsundoku" {
		t.Errorf("session = %v, want tsundoku", gotBody["session"])
	}
	if gotBody["maxTimeout"] != float64(30000) {
		t.Errorf("maxTimeout = %v, want 30000 (30s in ms)", gotBody["maxTimeout"])
	}
}

// TestSolve_NonOKStatus proves a FlareSolverr "error" status (a challenge it
// could not solve) surfaces as an error naming the message.
func TestSolve_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"error","message":"Unable to evaluate the Cloudflare challenge","solution":{"url":"","status":0,"cookies":[],"userAgent":""}}`))
	}))
	defer srv.Close()

	_, err := flaresolverr.Solve(context.Background(), srv.Client(), srv.URL, "https://kitsu.app/", "", 30*time.Second)
	if err == nil {
		t.Fatal("Solve: want an error for a non-ok FlareSolverr status, got nil")
	}
	if !strings.Contains(err.Error(), "Unable to evaluate") {
		t.Errorf("error = %v, want it to carry the FlareSolverr message", err)
	}
}

// TestSolve_HTTPError proves a non-200 HTTP response from FlareSolverr itself
// (not a well-formed FlareSolverr error payload) is surfaced as an error.
func TestSolve_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal server error"))
	}))
	defer srv.Close()

	_, err := flaresolverr.Solve(context.Background(), srv.Client(), srv.URL, "https://kitsu.app/", "", 30*time.Second)
	if err == nil {
		t.Fatal("Solve: want an error for HTTP 500, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %v, want it to mention the HTTP 500 status", err)
	}
}

// TestSolve_NoClearanceCookie proves an "ok" response that carries no
// cf_clearance cookie is still treated as a failure — the caller has nothing
// usable to replay.
func TestSolve_NoClearanceCookie(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","message":"","solution":{"url":"","status":200,"cookies":[{"name":"other","value":"x","domain":"kitsu.app","path":"/"}],"userAgent":"UA"}}`))
	}))
	defer srv.Close()

	_, err := flaresolverr.Solve(context.Background(), srv.Client(), srv.URL, "https://kitsu.app/", "", 30*time.Second)
	if err == nil {
		t.Fatal("Solve: want an error when no cf_clearance cookie is present, got nil")
	}
	if !strings.Contains(err.Error(), "cf_clearance") {
		t.Errorf("error = %v, want it to mention cf_clearance", err)
	}
}

// TestSolve_EndpointTrailingSlashAndV1 proves the /v1 suffix is appended
// exactly once regardless of how the endpoint is spelled.
func TestSolve_EndpointTrailingSlashAndV1(t *testing.T) {
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","message":"","solution":{"url":"","status":200,"cookies":[{"name":"cf_clearance","value":"v","domain":"x","path":"/"}],"userAgent":"UA"}}`))
	}))
	defer srv.Close()

	for _, endpoint := range []string{srv.URL, srv.URL + "/", srv.URL + "/v1"} {
		if _, err := flaresolverr.Solve(context.Background(), srv.Client(), endpoint, "https://example.com", "", 5*time.Second); err != nil {
			t.Fatalf("Solve(%q): %v", endpoint, err)
		}
	}
	for _, p := range gotPaths {
		if p != "/v1" {
			t.Errorf("request path = %q, want /v1 for every endpoint spelling", p)
		}
	}
}

// TestSolve_DefaultTimeout proves a non-positive timeout falls back to a sane
// default rather than sending maxTimeout: 0 (which FlareSolverr would treat
// as "no timeout budget").
func TestSolve_DefaultTimeout(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","message":"","solution":{"url":"","status":200,"cookies":[{"name":"cf_clearance","value":"v","domain":"x","path":"/"}],"userAgent":"UA"}}`))
	}))
	defer srv.Close()

	if _, err := flaresolverr.Solve(context.Background(), srv.Client(), srv.URL, "https://example.com", "", 0); err != nil {
		t.Fatalf("Solve: %v", err)
	}
	maxTimeout, _ := gotBody["maxTimeout"].(float64)
	if maxTimeout <= 0 {
		t.Errorf("maxTimeout = %v, want a positive default", gotBody["maxTimeout"])
	}
}
