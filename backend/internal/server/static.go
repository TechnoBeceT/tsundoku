// Package server — see server.go for package-level documentation.
package server

import (
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
)

// staticDistDir is the directory name where the built Nuxt SPA is written by
// `bun run build`. It is resolved relative to the working directory at startup.
// Same-origin deployment (QCAT-020): the backend binary serves the frontend
// assets directly; no separate static file server is needed.
const staticDistDir = "dist"

// registerStaticSPA wires the SPA static-file serving and fallback onto e
// using the default dist/ directory.
//
// Behaviour:
//   - If dist/ does not exist at startup a warning is logged and no static
//     routes are registered; the API continues to work normally. This lets
//     developers run the backend without a frontend build.
//   - Requests for known static assets (files that exist under dist/) are
//     served directly.
//   - Any other non-/api, non-/docs, non-/health path that does not match a
//     registered route receives dist/index.html so the Nuxt router can handle
//     client-side navigation.
//   - Unknown /api/* paths return 404 JSON so that API consumers get a
//     machine-readable error rather than the SPA HTML.
func registerStaticSPA(e *echo.Echo) {
	registerStaticSPAFromDir(e, staticDistDir)
}

// registerStaticSPAFromDir is the implementation behind registerStaticSPA.
// It accepts an explicit distDir so that tests can point it at a temp directory
// instead of the real dist/ location.
func registerStaticSPAFromDir(e *echo.Echo, distDir string) {
	indexPath := filepath.Join(distDir, "index.html")
	if _, err := os.Stat(indexPath); errors.Is(err, os.ErrNotExist) {
		log.Printf("server: static dist not found at %q — SPA serving disabled; API still operational", indexPath)
		// Register a fallback that returns 404 JSON for unknown routes so the
		// server stays well-behaved even without a frontend build.
		registerAPINotFound(e)
		return
	}

	// Resolve distDir to an absolute path once so the handler closure can use
	// it for traversal checking without repeated Abs calls.
	absDistDir, err := filepath.Abs(distDir)
	if err != nil {
		log.Printf("server: cannot resolve dist dir: %v — SPA serving disabled", err)
		registerAPINotFound(e)
		return
	}

	// Single catch-all GET handler: serves static assets when the file exists
	// inside distDir, falls back to index.html for SPA client-side routes.
	//
	// Traversal guard: filepath.Clean("/" + path) always produces an absolute
	// path beginning with "/" — it eliminates ".." sequences before the Join,
	// so the resolved full path is guaranteed to remain under absDistDir.
	// A prefix check after the join provides defence-in-depth.
	e.GET("/*", func(c echo.Context) error {
		rel := filepath.Clean("/" + c.Param("*"))

		// Reject any path whose cleaned form still contains "..".
		if strings.Contains(rel, "..") {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid path")
		}

		full := filepath.Join(absDistDir, rel)

		// Ensure the resolved path is actually inside the dist directory.
		if !strings.HasPrefix(full, absDistDir+string(filepath.Separator)) && full != absDistDir {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid path")
		}

		if st, err := os.Stat(full); err == nil && !st.IsDir() {
			return c.File(full)
		}
		return c.File(indexPath)
	})

	// Unknown /api/* routes must return 404 JSON, NOT the SPA HTML, so that
	// API clients can detect bad paths programmatically.
	registerAPINotFound(e)
}

// registerAPINotFound adds a catch-all for /api/* that returns 404 JSON.
// It is registered last so that specific /api/... routes defined earlier take
// precedence.
func registerAPINotFound(e *echo.Echo) {
	e.GET("/api/*", apiNotFound)
	e.POST("/api/*", apiNotFound)
	e.PUT("/api/*", apiNotFound)
	e.DELETE("/api/*", apiNotFound)
	e.PATCH("/api/*", apiNotFound)
}

// apiNotFound returns a 404 JSON ErrorResponse for unrecognised API paths.
// It delegates to the central ErrorHandler via echo.NewHTTPError so that the
// response shape always matches the OpenAPI ErrorResponse schema.
func apiNotFound(c echo.Context) error {
	return echo.NewHTTPError(http.StatusNotFound, "api endpoint not found")
}
