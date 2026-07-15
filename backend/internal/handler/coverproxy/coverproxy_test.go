package coverproxy_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/handler/coverproxy"
	sourceenginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
)

// newTestContext builds a bare echo.Context over a GET request/response pair.
func newTestContext() (echo.Context, *httptest.ResponseRecorder) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	return e.NewContext(req, rec), rec
}

// TestStreamEngine_OK verifies StreamEngine calls Image with pageURL EMPTY and
// the cover URL in the imageURL slot, and writes the fetched bytes with a
// Content-Type resolved from the reported extension.
func TestStreamEngine_OK(t *testing.T) {
	pngBytes := []byte{0x89, 0x50, 0x4E, 0x47}
	const sourceID int64 = 7
	const coverURL = "https://source.example/covers/1.jpg"
	engine := sourceenginefake.New(sourceenginefake.WithCoverImage(sourceID, coverURL, pngBytes, "png"))
	c, rec := newTestContext()

	if err := coverproxy.StreamEngine(c, engine, sourceID, coverURL); err != nil {
		t.Fatalf("StreamEngine: unexpected error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("StreamEngine: status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("StreamEngine: Content-Type = %q, want image/png", ct)
	}
	if rec.Body.String() != string(pngBytes) {
		t.Errorf("StreamEngine: body mismatch")
	}
	if got := engine.CallCount("Image"); got != 1 {
		t.Errorf("StreamEngine: Image call count = %d, want 1", got)
	}
}

// TestStreamEngine_UnknownExtFallsBackToOctetStream verifies an unrecognised
// extension maps to application/octet-stream rather than an empty type.
func TestStreamEngine_UnknownExtFallsBackToOctetStream(t *testing.T) {
	const sourceID int64 = 1
	const coverURL = "/x"
	engine := sourceenginefake.New(sourceenginefake.WithCoverImage(sourceID, coverURL, []byte("data"), "bin"))
	c, rec := newTestContext()

	if err := coverproxy.StreamEngine(c, engine, sourceID, coverURL); err != nil {
		t.Fatalf("StreamEngine: unexpected error: %v", err)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("StreamEngine: Content-Type = %q, want application/octet-stream", ct)
	}
}

// TestStreamEngine_ImageFail verifies an engine-host fetch failure maps to
// 502, not a false 200 (the upstream is a separate service — never lie about
// success).
func TestStreamEngine_ImageFail(t *testing.T) {
	engine := sourceenginefake.New(sourceenginefake.WithError("Image", errors.New("engine down")))
	c, _ := newTestContext()

	err := coverproxy.StreamEngine(c, engine, 1, "/x")
	var he *echo.HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("StreamEngine: want *echo.HTTPError, got %T (%v)", err, err)
	}
	if he.Code != http.StatusBadGateway {
		t.Errorf("StreamEngine: code = %d, want 502", he.Code)
	}
}

// TestStreamEngine_PageURLAlwaysEmpty proves StreamEngine calls Image with
// pageURL="" — the cover-fetch shape (imageURL carries the address, mirroring
// series.fetchAndCacheCover) — by seeding a page-keyed (non-cover) entry under
// the same sourceID/URL and confirming it is NEVER returned for a cover fetch.
func TestStreamEngine_PageURLAlwaysEmpty(t *testing.T) {
	const sourceID int64 = 3
	const url = "https://source.example/covers/2.jpg"
	// WithImage keys on (sourceID, pageURL) — seeding it here must NOT satisfy a
	// StreamEngine (cover) call, which always passes pageURL="".
	engine := sourceenginefake.New(
		sourceenginefake.WithImage(sourceID, url, []byte("PAGE-BYTES"), "jpg"),
		sourceenginefake.WithCoverImage(sourceID, url, []byte("COVER-BYTES"), "png"),
	)
	c, rec := newTestContext()

	if err := coverproxy.StreamEngine(c, engine, sourceID, url); err != nil {
		t.Fatalf("StreamEngine: unexpected error: %v", err)
	}
	if rec.Body.String() != "COVER-BYTES" {
		t.Fatalf("StreamEngine: body = %q, want COVER-BYTES (must not collide with the page-keyed entry)", rec.Body.String())
	}
}
