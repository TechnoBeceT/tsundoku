package sourceengine_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
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

// TestSetImpersonate_SendsOnlyProvidedFields mirrors the FlareSolverr
// no-clobber proof for the impersonate-gateway config: only the patch's
// non-nil fields hit the PUT /config/impersonate body.
func TestSetImpersonate_SendsOnlyProvidedFields(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/config/impersonate" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		decodeBody(t, r, &captured)
		writeJSON(t, w, http.StatusOK, map[string]any{
			"enabled": true, "url": "http://impersonate-gateway:8788",
		})
	}))
	defer srv.Close()

	enabled := true
	patch := sourceengine.ImpersonatePatch{Enabled: &enabled}
	got, err := newTestClient(t, srv).SetImpersonate(context.Background(), patch)
	if err != nil {
		t.Fatalf("SetImpersonate: %v", err)
	}
	want := sourceengine.ImpersonateConfig{Enabled: true, URL: "http://impersonate-gateway:8788"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SetImpersonate result = %+v, want %+v", got, want)
	}
	if len(captured) != 1 {
		t.Fatalf("expected exactly 1 key in the request body, got %d: %v", len(captured), captured)
	}
	if _, ok := captured["url"]; ok {
		t.Error("unset field \"url\" leaked into the request body (would clobber)")
	}
	if _, ok := captured["sourceIds"]; ok {
		t.Error("unset field \"sourceIds\" leaked into the request body (would clobber the gating set)")
	}
}

// TestSetImpersonate_SendsSourceIDsAsNumbers pins the exact GAP-131 wire shape
// the engine host's ImpersonateConfigRequest deserialises: the field is named
// "sourceIds" and carries JSON NUMBERS (int64), never names and never strings.
// The owner-facing string form stops at the HTTP handler; a rename or a type
// change on either side would silently un-gate every source (the host would
// deserialise a null list and leave its set untouched), so this is asserted on
// the raw body rather than through a Go round-trip.
func TestSetImpersonate_SendsSourceIDsAsNumbers(t *testing.T) {
	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		writeJSON(t, w, http.StatusOK, map[string]any{
			"enabled": true, "url": "http://gw:8788", "sourceIds": []int64{42, 1998416842837112832},
		})
	}))
	defer srv.Close()

	ids := []int64{42, 1998416842837112832}
	got, err := newTestClient(t, srv).SetImpersonate(context.Background(), sourceengine.ImpersonatePatch{SourceIDs: &ids})
	if err != nil {
		t.Fatalf("SetImpersonate: %v", err)
	}
	if body := string(raw); body != `{"sourceIds":[42,1998416842837112832]}` {
		t.Errorf("request body = %s, want the sourceIds array as bare JSON numbers", body)
	}
	if !reflect.DeepEqual(got.SourceIDs, []int64{42, 1998416842837112832}) {
		t.Errorf("read-back SourceIDs = %v, want the host's set decoded as int64s", got.SourceIDs)
	}
}

// TestSetImpersonate_SendsAnExplicitEmptySourceSet proves an EMPTY (non-nil)
// gating set still reaches the wire, so pushing "no source" actively CLEARS a
// stale engine-side selection instead of being omitted as if untouched.
func TestSetImpersonate_SendsAnExplicitEmptySourceSet(t *testing.T) {
	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		writeJSON(t, w, http.StatusOK, map[string]any{"enabled": false, "url": "", "sourceIds": []int64{}})
	}))
	defer srv.Close()

	empty := []int64{}
	if _, err := newTestClient(t, srv).SetImpersonate(context.Background(), sourceengine.ImpersonatePatch{SourceIDs: &empty}); err != nil {
		t.Fatalf("SetImpersonate: %v", err)
	}
	if body := string(raw); body != `{"sourceIds":[]}` {
		t.Errorf("request body = %s, want an explicit empty sourceIds array", body)
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
