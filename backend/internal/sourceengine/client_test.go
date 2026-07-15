package sourceengine_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// newTestClient builds a sourceengine.Client pointed at srv, using srv's own
// http.Client as the HTTPDoer. Shared by every test file in this package —
// mirrors the newTestClient idiom in internal/suwayomi/*_test.go.
func newTestClient(t *testing.T, srv *httptest.Server) sourceengine.Client {
	t.Helper()
	return sourceengine.New(srv.URL, srv.Client())
}

// writeJSON is a small test helper that marshals body and writes it as the
// HTTP response with the given status code.
func writeJSON(t *testing.T, w http.ResponseWriter, status int, body any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}
}

// decodeBody is a small test helper that decodes an incoming request body
// into out, used by tests that assert exactly what the client sent.
func decodeBody(t *testing.T, r *http.Request, out any) {
	t.Helper()
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		t.Fatalf("decodeBody: %v", err)
	}
}

// TestHealth_Success proves a GET /health response decodes into Health.
func TestHealth_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/health" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{"status": "ok", "sources": 3})
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	want := sourceengine.Health{Status: "ok", Sources: 3}
	if got != want {
		t.Errorf("Health = %+v, want %+v", got, want)
	}
}

// TestClient_400_MapsToBadRequestError proves any 400 response body {"error":
// "..."} is surfaced as a *sourceengine.BadRequestError carrying the host's
// message.
func TestClient_400_MapsToBadRequestError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadRequest, map[string]string{"error": "unknown sourceId 99"})
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Health(context.Background())
	assertBadRequestError(t, err)
	var badReq *sourceengine.BadRequestError
	errors.As(err, &badReq)
	if badReq.Msg != "unknown sourceId 99" {
		t.Errorf("BadRequestError.Msg = %q, want %q", badReq.Msg, "unknown sourceId 99")
	}
}

// TestClient_502_MapsToUpstreamError proves a 502 response is surfaced as a
// *sourceengine.UpstreamError carrying both the status and the host's message.
func TestClient_502_MapsToUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadGateway, map[string]string{"error": "IOException: connection reset"})
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Health(context.Background())
	assertUpstreamError(t, err, http.StatusBadGateway)
	var upstream *sourceengine.UpstreamError
	errors.As(err, &upstream)
	if upstream.Msg != "IOException: connection reset" {
		t.Errorf("UpstreamError.Msg = %q, want %q", upstream.Msg, "IOException: connection reset")
	}
}

// TestClient_UnexpectedStatus_MapsToUpstreamError proves that a non-400,
// non-2xx status OTHER than 502 (e.g. a stray 404) still maps to
// *UpstreamError, never a bare unwrapped error — the brief's error model is
// exactly two typed errors, not one per status code.
func TestClient_UnexpectedStatus_MapsToUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusNotFound, map[string]string{"error": "no route for /bogus"})
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Health(context.Background())
	assertUpstreamError(t, err, http.StatusNotFound)
}

// TestBadRequestError_ErrorMessage proves the Error() string carries the
// host's message, not just a generic placeholder.
func TestBadRequestError_ErrorMessage(t *testing.T) {
	err := &sourceengine.BadRequestError{Msg: "bad body"}
	if err.Error() == "" {
		t.Fatal("BadRequestError.Error() must not be empty")
	}
}

// TestUpstreamError_ErrorMessage proves the Error() string carries both the
// status and the host's message.
func TestUpstreamError_ErrorMessage(t *testing.T) {
	err := &sourceengine.UpstreamError{Status: 502, Msg: "boom"}
	if err.Error() == "" {
		t.Fatal("UpstreamError.Error() must not be empty")
	}
}

// assertBadRequestError is a shared test helper: it fails t unless err
// unwraps to a *sourceengine.BadRequestError. Used by every endpoint's 400
// test so the assertion logic lives in one place (§2 DRY).
func assertBadRequestError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("want error, got nil")
	}
	var badReq *sourceengine.BadRequestError
	if !errors.As(err, &badReq) {
		t.Fatalf("error = %v (%T), want *BadRequestError", err, err)
	}
}

// assertUpstreamError is a shared test helper: it fails t unless err unwraps
// to a *sourceengine.UpstreamError carrying wantStatus.
func assertUpstreamError(t *testing.T, err error, wantStatus int) {
	t.Helper()
	if err == nil {
		t.Fatal("want error, got nil")
	}
	var upstream *sourceengine.UpstreamError
	if !errors.As(err, &upstream) {
		t.Fatalf("error = %v (%T), want *UpstreamError", err, err)
	}
	if upstream.Status != wantStatus {
		t.Errorf("UpstreamError.Status = %d, want %d", upstream.Status, wantStatus)
	}
}

// TestNew_TrimsTrailingBaseURLSlash proves New tolerates a baseURL that ends
// in "/" (a common copy-paste mistake) without producing a doubled slash in
// the request path.
func TestNew_TrimsTrailingBaseURLSlash(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("request path = %q, want /health (no doubled slash)", r.URL.Path)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{"status": "ok", "sources": 0})
	}))
	defer srv.Close()

	c := sourceengine.New(srv.URL+"/", srv.Client())
	if _, err := c.Health(context.Background()); err != nil {
		t.Fatalf("Health: %v", err)
	}
}

// failingDoer is an HTTPDoer stand-in that always fails the transport call.
// It exercises the network-error path of send() that a real httptest.Server
// cannot trigger (a live server always answers something).
type failingDoer struct{}

// Do always returns a transport-level error, simulating an unreachable host.
func (failingDoer) Do(_ *http.Request) (*http.Response, error) {
	return nil, errors.New("simulated network failure")
}

// TestClient_NetworkFailure_IsWrapped proves a transport-level failure (the
// doer itself erroring, e.g. a DNS failure or refused connection) is
// wrapped and returned, never panics or hangs.
func TestClient_NetworkFailure_IsWrapped(t *testing.T) {
	c := sourceengine.New("http://engine-host.invalid", failingDoer{})
	if _, err := c.Health(context.Background()); err == nil {
		t.Fatal("Health: want error from a failing doer, got nil")
	}
}

// TestClient_InvalidJSONResponse_IsWrapped proves a 200 response with a
// malformed JSON body surfaces a decode error rather than silently zeroing
// the result.
func TestClient_InvalidJSONResponse_IsWrapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{not valid json"))
	}))
	defer srv.Close()

	if _, err := newTestClient(t, srv).Health(context.Background()); err == nil {
		t.Fatal("Health: want a decode error for a malformed JSON body, got nil")
	}
}
