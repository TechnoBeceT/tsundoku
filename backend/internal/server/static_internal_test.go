// Package server — whitebox tests for SPA static-file serving.
package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/labstack/echo/v4"
	mw "github.com/technobecet/tsundoku/internal/middleware"
)

// newSPATestEcho builds a minimal Echo instance with the error handler and
// request-id middleware wired (matching the production server.New order) and
// points the SPA static serving at a caller-supplied temp directory.
func newSPATestEcho(t *testing.T, distDir string) *echo.Echo {
	t.Helper()
	e := echo.New()
	e.HTTPErrorHandler = mw.ErrorHandler
	e.Use(mw.RequestID())
	registerStaticSPAFromDir(e, distDir)
	return e
}

// makeTempDist creates a temporary dist directory with index.html and
// assets/app.js, returning the directory path.
func makeTempDist(t *testing.T) (dir, indexBody, jsBody string) {
	t.Helper()
	dir = t.TempDir()
	indexBody = "<!doctype html>INDEX"
	jsBody = "console.log(1)"

	//nolint:gosec // temp files in t.TempDir() — permissions don't matter in tests
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(indexBody), 0o644); err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // temp dir in t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // temp files in t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte(jsBody), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, indexBody, jsBody
}

// TestSPAServesAssetsAndFallsBackToIndex covers the dist-PRESENT path:
//
//	(a) a file that exists under dist/ is served directly (not index.html),
//	(b) a path that has no matching file falls back to index.html,
//	(c) an unknown /api/* path returns 404 JSON even when dist exists,
//	(d) a directory-traversal path does NOT escape the dist directory.
func TestSPAServesAssetsAndFallsBackToIndex(t *testing.T) {
	dir, indexBody, jsBody := makeTempDist(t)
	e := newSPATestEcho(t, dir)

	t.Run("serves_asset_file", func(t *testing.T) {
		spaServesAssetFile(t, e, jsBody)
	})
	t.Run("spa_fallback_to_index", func(t *testing.T) {
		spaFallbackToIndex(t, e, indexBody)
	})
	t.Run("api_unknown_404_json", func(t *testing.T) {
		spaAPIUnknown404JSON(t, e)
	})
	t.Run("traversal_blocked", func(t *testing.T) {
		spaTraversalBlocked(t, e, indexBody)
	})
}

// spaServesAssetFile asserts that GET /assets/app.js returns the JS content.
func spaServesAssetFile(t *testing.T, e *echo.Echo, jsBody string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /assets/app.js: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != jsBody {
		t.Errorf("GET /assets/app.js: body = %q, want %q", body, jsBody)
	}
}

// spaFallbackToIndex asserts that an unknown SPA path returns index.html.
func spaFallbackToIndex(t *testing.T, e *echo.Echo, indexBody string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/some/spa/route", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /some/spa/route: status = %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); body != indexBody {
		t.Errorf("GET /some/spa/route: body = %q, want %q (index.html)", body, indexBody)
	}
}

// spaAPIUnknown404JSON asserts that /api/unknown returns 404 JSON with a
// request-id header, not index.html.
func spaAPIUnknown404JSON(t *testing.T, e *echo.Echo) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/does-not-exist", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/unknown: status = %d, want 404", rec.Code)
	}

	var resp mw.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("GET /api/unknown: response is not JSON ErrorResponse: %v (body: %s)", err, rec.Body.String())
	}
	if resp.Message == "" {
		t.Error("GET /api/unknown: JSON message must not be empty")
	}

	// M-2: request-id header must be present on error responses.
	if rid := rec.Header().Get(mw.RequestIDHeader); rid == "" {
		t.Errorf("GET /api/unknown: %s header missing from 404 response", mw.RequestIDHeader)
	}
}

// spaTraversalBlocked asserts that traversal paths do not escape the dist dir.
func spaTraversalBlocked(t *testing.T, e *echo.Echo, indexBody string) {
	t.Helper()
	paths := []string{
		"/../etc/passwd",
		"/../../etc/passwd",
		"/%2e%2e/etc/passwd",
	}
	for _, path := range paths {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		// Must be 400 (blocked) or safe fallback (200 index / 404) — never
		// a file from outside dist.
		switch rec.Code {
		case http.StatusOK:
			if body := rec.Body.String(); body != indexBody {
				t.Errorf("traversal %q: 200 body %q is not index.html — possible path escape", path, body)
			}
		case http.StatusBadRequest, http.StatusNotFound:
			// safe
		default:
			t.Errorf("traversal %q: status = %d, want 400/404/200(index)", path, rec.Code)
		}
	}
}
