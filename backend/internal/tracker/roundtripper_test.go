package tracker_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/tracker"
)

// memTokenSource is an in-memory tracker.TokenSource test double —
// mutex-guarded so a test can inspect what SetToken last wrote.
type memTokenSource struct {
	mu  sync.Mutex
	tok tracker.TokenSet
}

func (s *memTokenSource) Token() tracker.TokenSet {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tok
}

func (s *memTokenSource) SetToken(tok tracker.TokenSet) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tok = tok
}

var _ tracker.TokenSource = (*memTokenSource)(nil)

// TestAuthRoundTripper_AttachesBearer confirms every request through the
// RoundTripper carries "Authorization: Bearer <token>" from the source's
// current TokenSet, with no refresh needed.
func TestAuthRoundTripper_AttachesBearer(t *testing.T) {
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	source := &memTokenSource{tok: tracker.TokenSet{Access: "live-token"}}
	rt := tracker.NewAuthRoundTripper(http.DefaultTransport, &fakeTracker{}, source)
	client := &http.Client{Transport: rt}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, upstream.URL, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do: %v", err)
	}
	_ = resp.Body.Close()

	if gotAuth != "Bearer live-token" {
		t.Fatalf("Authorization header = %q, want %q", gotAuth, "Bearer live-token")
	}
}

// TestAuthRoundTripper_ProactiveRefreshOnExpiry confirms an ALREADY-expired
// token (per ExpiresAt) is refreshed BEFORE the request is sent — the
// upstream never sees the stale token at all.
func TestAuthRoundTripper_ProactiveRefreshOnExpiry(t *testing.T) {
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	past := time.Now().Add(-time.Hour)
	source := &memTokenSource{tok: tracker.TokenSet{Access: "stale", Refresh: "refresh-me", ExpiresAt: &past}}

	refreshCalls := 0
	ft := &fakeTracker{refreshFn: func(_ context.Context, refresh string) (tracker.TokenSet, error) {
		refreshCalls++
		if refresh != "refresh-me" {
			t.Fatalf("Refresh called with %q, want %q", refresh, "refresh-me")
		}
		return tracker.TokenSet{Access: "fresh-token"}, nil
	}}

	rt := tracker.NewAuthRoundTripper(http.DefaultTransport, ft, source)
	client := &http.Client{Transport: rt}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, upstream.URL, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do: %v", err)
	}
	_ = resp.Body.Close()

	if refreshCalls != 1 {
		t.Fatalf("refresh calls = %d, want 1", refreshCalls)
	}
	if gotAuth != "Bearer fresh-token" {
		t.Fatalf("Authorization header = %q, want %q", gotAuth, "Bearer fresh-token")
	}
	if got := source.Token().Access; got != "fresh-token" {
		t.Fatalf("source.Token().Access = %q, want %q (refreshed token not persisted)", got, "fresh-token")
	}
}

// TestAuthRoundTripper_ReactiveRefreshOn401 confirms a token that LOOKED
// fresh (no ExpiresAt, or one still in the future) but is rejected by the
// upstream with 401 triggers exactly one refresh + retry, and the retried
// request succeeds.
func TestAuthRoundTripper_ReactiveRefreshOn401(t *testing.T) {
	var seenAuths []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		seenAuths = append(seenAuths, auth)
		if auth == "Bearer old-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	source := &memTokenSource{tok: tracker.TokenSet{Access: "old-token", Refresh: "refresh-me"}}
	refreshCalls := 0
	ft := &fakeTracker{refreshFn: func(_ context.Context, _ string) (tracker.TokenSet, error) {
		refreshCalls++
		return tracker.TokenSet{Access: "new-token"}, nil
	}}

	rt := tracker.NewAuthRoundTripper(http.DefaultTransport, ft, source)
	client := &http.Client{Transport: rt}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, upstream.URL, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do: %v", err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("final status = %d, want 200", resp.StatusCode)
	}
	if refreshCalls != 1 {
		t.Fatalf("refresh calls = %d, want 1", refreshCalls)
	}
	if len(seenAuths) != 2 || seenAuths[0] != "Bearer old-token" || seenAuths[1] != "Bearer new-token" {
		t.Fatalf("seenAuths = %v, want [Bearer old-token Bearer new-token]", seenAuths)
	}
}

// TestAuthRoundTripper_ErrTokenExpiredWhenRefreshFails confirms a 401 with
// no usable refresh (ErrNoRefresh, or the refresher itself erroring) surfaces
// tracker.ErrTokenExpired to the caller — never a silent/looping retry.
func TestAuthRoundTripper_ErrTokenExpiredWhenRefreshFails(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer upstream.Close()

	// No refresh token at all (AniList-shaped TokenSet) — the RoundTripper
	// must not even attempt a refresh call.
	source := &memTokenSource{tok: tracker.TokenSet{Access: "implicit-token"}}
	refreshCalls := 0
	ft := &fakeTracker{refreshFn: func(_ context.Context, _ string) (tracker.TokenSet, error) {
		refreshCalls++
		return tracker.TokenSet{}, nil
	}}

	rt := tracker.NewAuthRoundTripper(http.DefaultTransport, ft, source)
	client := &http.Client{Transport: rt}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, upstream.URL, nil)
	_, err := client.Do(req)
	if err == nil {
		t.Fatalf("client.Do: want an error, got nil")
	}
	if !errors.Is(err, tracker.ErrTokenExpired) {
		t.Fatalf("client.Do error = %v, want to wrap tracker.ErrTokenExpired", err)
	}
	if refreshCalls != 0 {
		t.Fatalf("refresh calls = %d, want 0 (no refresh token to try)", refreshCalls)
	}
}

// TestAuthRoundTripper_RetryResendsBody confirms a POST body survives the
// reactive-refresh retry — the second request must carry the SAME payload,
// not an already-drained reader.
func TestAuthRoundTripper_RetryResendsBody(t *testing.T) {
	var bodies []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 64)
		n, _ := r.Body.Read(buf)
		bodies = append(bodies, string(buf[:n]))
		if r.Header.Get("Authorization") == "Bearer old-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	source := &memTokenSource{tok: tracker.TokenSet{Access: "old-token", Refresh: "refresh-me"}}
	ft := &fakeTracker{refreshFn: func(_ context.Context, _ string) (tracker.TokenSet, error) {
		return tracker.TokenSet{Access: "new-token"}, nil
	}}

	rt := tracker.NewAuthRoundTripper(http.DefaultTransport, ft, source)
	client := &http.Client{Transport: rt}

	// strings.NewReader is one of the body types http.NewRequestWithContext
	// auto-populates GetBody for — the exact shape every real tracker
	// client in this codebase uses (form-encoded POST/PUT bodies).
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, upstream.URL, strings.NewReader("hello=world"))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do: %v", err)
	}
	_ = resp.Body.Close()

	if len(bodies) != 2 || bodies[0] != "hello=world" || bodies[1] != "hello=world" {
		t.Fatalf("bodies = %v, want [hello=world hello=world]", bodies)
	}
}
