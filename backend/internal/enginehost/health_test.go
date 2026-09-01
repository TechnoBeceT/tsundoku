package enginehost_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/enginehost"
)

// TestHTTPHealthProber_OK proves a 200 on GET /health reports ready (nil).
func TestHTTPHealthProber_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("probed %q, want /health", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := enginehost.HTTPHealthProber(time.Second)(context.Background(), srv.URL); err != nil {
		t.Fatalf("probe on 200 = %v, want nil", err)
	}
}

// TestHTTPHealthProber_Non200 proves a non-200 status reports not-ready.
func TestHTTPHealthProber_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	if err := enginehost.HTTPHealthProber(time.Second)(context.Background(), srv.URL); err == nil {
		t.Fatal("probe on 503 = nil, want an error")
	}
}

// TestHTTPHealthProber_Unreachable proves an unreachable host reports not-ready
// (a transport error) rather than panicking.
func TestHTTPHealthProber_Unreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // now nothing is listening

	if err := enginehost.HTTPHealthProber(200*time.Millisecond)(context.Background(), url); err == nil {
		t.Fatal("probe on a dead server = nil, want a transport error")
	}
}

// TestHTTPHealthProber_HonorsContext proves an in-flight /health request cannot
// outlive the caller's launch readiness deadline.
func TestHTTPHealthProber_HonorsContext(t *testing.T) {
	started := make(chan struct{})
	var startedOnce sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		startedOnce.Do(func() { close(started) })
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- enginehost.HTTPHealthProber(time.Second)(ctx, srv.URL) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("health request did not reach server")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("probe error = %v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("health probe did not return after context cancellation")
	}
}
