package sourceengine_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// TestSetFlareSolverr_SendsOnlyProvidedFields is the no-clobber proof: only
// the patch's non-nil fields must appear in the outgoing PUT body, and the
// read-back FlareSolverrConfig is decoded from the response.
func TestSetFlareSolverr_SendsOnlyProvidedFields(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/config/flaresolverr" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		decodeBody(t, r, &captured)
		writeJSON(t, w, http.StatusOK, map[string]any{
			"enabled": true, "url": "http://flare:8191", "session": "sess",
			"sessionTtl": 15, "timeout": 60, "asResponseFallback": true,
		})
	}))
	defer srv.Close()

	enabled := true
	url := "http://flare:8191"
	patch := sourceengine.FlareSolverrPatch{Enabled: &enabled, URL: &url}
	got, err := newTestClient(t, srv).SetFlareSolverr(context.Background(), patch)
	if err != nil {
		t.Fatalf("SetFlareSolverr: %v", err)
	}
	want := sourceengine.FlareSolverrConfig{
		Enabled: true, URL: "http://flare:8191", Session: "sess",
		SessionTTL: 15, Timeout: 60, AsResponseFallback: true,
	}
	if got != want {
		t.Errorf("SetFlareSolverr result = %+v, want %+v", got, want)
	}
	if len(captured) != 2 {
		t.Fatalf("expected exactly 2 keys in the request body, got %d: %v", len(captured), captured)
	}
	for _, unset := range []string{"session", "sessionTtl", "timeout", "asResponseFallback"} {
		if _, ok := captured[unset]; ok {
			t.Errorf("unset field %q leaked into the request body (would clobber)", unset)
		}
	}
}

// TestSetSocks_SendsOnlyProvidedFields mirrors the FlareSolverr no-clobber
// proof for the SOCKS-proxy config.
func TestSetSocks_SendsOnlyProvidedFields(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/config/socks" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		decodeBody(t, r, &captured)
		writeJSON(t, w, http.StatusOK, map[string]any{
			"enabled": true, "version": 5, "host": "127.0.0.1", "port": "1080", "username": "user",
			// password is deliberately absent — the host never echoes it back.
		})
	}))
	defer srv.Close()

	version := 5
	port := "1080"
	patch := sourceengine.SocksPatch{Version: &version, Port: &port}
	got, err := newTestClient(t, srv).SetSocks(context.Background(), patch)
	if err != nil {
		t.Fatalf("SetSocks: %v", err)
	}
	want := sourceengine.SocksConfig{Enabled: true, Version: 5, Host: "127.0.0.1", Port: "1080", Username: "user", Password: ""}
	if got != want {
		t.Errorf("SetSocks result = %+v, want %+v", got, want)
	}
	if len(captured) != 2 {
		t.Fatalf("expected exactly 2 keys in the request body, got %d: %v", len(captured), captured)
	}
}

// TestSetFlareSolverr_BadRequest proves a 400 maps to *BadRequestError.
func TestSetFlareSolverr_BadRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).SetFlareSolverr(context.Background(), sourceengine.FlareSolverrPatch{})
	assertBadRequestError(t, err)
}

// TestSetSocks_UpstreamFailure proves a 502 maps to *UpstreamError.
func TestSetSocks_UpstreamFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadGateway, map[string]string{"error": "boom"})
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).SetSocks(context.Background(), sourceengine.SocksPatch{})
	assertUpstreamError(t, err, http.StatusBadGateway)
}
