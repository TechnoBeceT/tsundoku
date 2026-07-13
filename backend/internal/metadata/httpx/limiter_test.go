package httpx_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/metadata/httpx"
)

// countingTransport records how many times RoundTrip was called and always
// succeeds against the given test server.
type countingTransport struct {
	calls int
	rt    http.RoundTripper
}

func (c *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c.calls++
	return c.rt.RoundTrip(req)
}

func TestNewRateLimited_FirstRequestPassesThroughImmediately(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	base := &countingTransport{rt: http.DefaultTransport}
	// A generous per-minute budget so the burst-1 bucket's single starting
	// token is the only thing exercised — this asserts the first request is
	// never held up waiting on a fresh token.
	transport := httpx.NewRateLimited(base, 6000)
	client := &http.Client{Transport: transport}

	start := time.Now()
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	_ = resp.Body.Close()
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("first request took %v, want near-instant (burst=1 starts with a token)", elapsed)
	}
	if base.calls != 1 {
		t.Fatalf("base.calls = %d, want 1 (RoundTrip must delegate to base)", base.calls)
	}
}

func TestNewRateLimited_SecondRequestWaitsForNextToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// perMinute=1 => the bucket refills one token every 60s and starts with
	// exactly one — the first request drains it, so a second request must
	// wait. A short context deadline proves the limiter actually blocks
	// (rather than allowing an unbounded burst) without the test itself
	// waiting 60 seconds.
	transport := httpx.NewRateLimited(nil, 1)
	client := &http.Client{Transport: transport}

	first, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	_ = first.Body.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	if _, err := client.Do(req); err == nil {
		t.Fatal("second request succeeded immediately, want it to block on the drained token bucket")
	}
}

func TestNewRateLimited_NonPositivePerMinuteDoesNotPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &http.Client{Transport: httpx.NewRateLimited(nil, 0)}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	_ = resp.Body.Close()
}
