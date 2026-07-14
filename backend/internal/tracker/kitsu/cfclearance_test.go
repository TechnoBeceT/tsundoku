// Package kitsu_test — cf-clearance transport (cfclearance.go) end-to-end
// behaviour, exercised through Client.Search (the simplest unauthenticated
// call) against a fake Kitsu server that answers a Cloudflare-style challenge
// until the request carries a cf_clearance cookie, and a fake FlareSolverr
// server that solves it.
package kitsu_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/tracker/kitsu"
)

// newChallengedKitsuServer returns a fake Kitsu manga-search endpoint that
// answers a Cloudflare 403 challenge ("Just a moment...") until the request
// carries a cf_clearance cookie, at which point it answers a valid (empty)
// search page. It also asserts every clearance-bearing request carries the
// expected browser User-Agent.
func newChallengedKitsuServer(t *testing.T, wantUA string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("cf_clearance"); err != nil {
			w.Header().Set("Server", "cloudflare")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("<html><title>Just a moment...</title></html>"))
			return
		}
		if got := r.Header.Get("User-Agent"); got != wantUA {
			t.Errorf("clearance-bearing request User-Agent = %q, want %q", got, wantUA)
		}
		w.Header().Set("Content-Type", "application/vnd.api+json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
}

// newFakeFlareSolverr returns a fake FlareSolverr /v1 endpoint that always
// solves successfully, incrementing calls on every hit — tests assert on
// calls to prove the cf_clearance cache actually suppresses repeat solves.
func newFakeFlareSolverr(t *testing.T, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status": "ok",
			"message": "",
			"solution": {
				"url": "https://kitsu.app/",
				"status": 200,
				"cookies": [{"name": "cf_clearance", "value": "earned-clearance", "domain": "kitsu.app", "path": "/"}],
				"userAgent": "FlareSolverr/Chrome-Fake-UA"
			}
		}`))
	}))
}

// redirectingClient builds an *http.Client whose transport rewrites every
// outgoing request's scheme+host to target — kitsu.Client posts to hardcoded
// endpoint constants, so this is how the whole package's tests point it at a
// fake server (mirrors client_test.go's redirectTransport).
func redirectingClient(t *testing.T, target string) *http.Client {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parse target url: %v", err)
	}
	return &http.Client{Transport: &redirectTransport{target: u}}
}

// TestFlareSolverrTransport_ChallengeThenSuccessThenCached proves the full
// happy path: a Cloudflare-challenged first request triggers exactly one
// FlareSolverr Solve, the retry succeeds carrying the earned cf_clearance
// cookie + User-Agent, and a SECOND unrelated call within the session TTL
// reuses the cached clearance (no second Solve).
func TestFlareSolverrTransport_ChallengeThenSuccessThenCached(t *testing.T) {
	kitsuSrv := newChallengedKitsuServer(t, "FlareSolverr/Chrome-Fake-UA")
	defer kitsuSrv.Close()

	var solveCalls atomic.Int32
	flareSrv := newFakeFlareSolverr(t, &solveCalls)
	defer flareSrv.Close()

	c := kitsu.New(redirectingClient(t, kitsuSrv.URL)).WithFlareSolverrGate(
		func(context.Context) kitsu.FlareSolverrConfig {
			return kitsu.FlareSolverrConfig{
				Enabled:     true,
				URL:         flareSrv.URL,
				Timeout:     10 * time.Second,
				SessionName: "tsundoku",
				SessionTTL:  15 * time.Minute,
			}
		},
	)

	if _, err := c.Search(context.Background(), "", "one piece"); err != nil {
		t.Fatalf("first Search: %v", err)
	}
	if got := solveCalls.Load(); got != 1 {
		t.Fatalf("Solve calls after first Search = %d, want 1", got)
	}

	// A second, independent call within the TTL must reuse the cached
	// clearance rather than re-solving.
	if _, err := c.Search(context.Background(), "", "another title"); err != nil {
		t.Fatalf("second Search: %v", err)
	}
	if got := solveCalls.Load(); got != 1 {
		t.Fatalf("Solve calls after second Search = %d, want still 1 (cached)", got)
	}
}

// TestFlareSolverrTransport_DisabledIsPassthrough proves a disabled (or
// blank-URL) gate never calls FlareSolverr at all — the request goes out
// exactly as built, so an un-configured deployment behaves identically to
// before this feature existed.
func TestFlareSolverrTransport_DisabledIsPassthrough(t *testing.T) {
	// This Kitsu fake would 403-challenge forever (there is no way for the
	// disabled transport to ever earn a cf_clearance cookie) — proving that a
	// disabled gate makes NO attempt to solve is exactly what "still gets the
	// challenge response back, unmodified" demonstrates. A real, unchallenged
	// deployment behaves the same way trivially; this variant makes the
	// negative case unambiguous — Search must return an error, not hang or
	// silently synthesize a success.
	kitsuSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "cloudflare")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<html><title>Just a moment...</title></html>"))
	}))
	defer kitsuSrv.Close()

	var solveCalls atomic.Int32
	flareSrv := newFakeFlareSolverr(t, &solveCalls)
	defer flareSrv.Close()

	cases := []struct {
		name string
		cfg  kitsu.FlareSolverrConfig
	}{
		{"disabled", kitsu.FlareSolverrConfig{Enabled: false, URL: flareSrv.URL}},
		{"blank url", kitsu.FlareSolverrConfig{Enabled: true, URL: ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			solveCalls.Store(0)
			c := kitsu.New(redirectingClient(t, kitsuSrv.URL)).WithFlareSolverrGate(
				func(context.Context) kitsu.FlareSolverrConfig { return tc.cfg },
			)
			_, err := c.Search(context.Background(), "", "one piece")
			if err == nil {
				t.Fatal("Search: want the raw 403 challenge error to surface, got nil")
			}
			if got := solveCalls.Load(); got != 0 {
				t.Errorf("Solve calls = %d, want 0 (passthrough never solves)", got)
			}
		})
	}
}
