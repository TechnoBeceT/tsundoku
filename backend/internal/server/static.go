// Package server — see server.go for package-level documentation.
package server

import (
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
)

// staticDistDir is the directory name where the built Nuxt SPA is written by
// `bun run build`. It is resolved relative to the working directory at startup.
// Same-origin deployment (QCAT-020): the backend binary serves the frontend
// assets directly; no separate static file server is needed.
const staticDistDir = "dist"

// registerStaticSPA wires the SPA static-file serving and fallback onto e.
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
	indexPath := filepath.Join(staticDistDir, "index.html")
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		log.Printf("server: static dist not found at %q — SPA serving disabled; API still operational", indexPath)
		// Register a fallback that returns 404 JSON for unknown routes so the
		// server stays well-behaved even without a frontend build.
		registerAPINotFound(e)
		return
	}

	// Serve static assets (JS, CSS, images …) from dist/.
	e.Static("/", staticDistDir)

	// SPA fallback: any path not matched by a more-specific route receives
	// index.html so that client-side routing works after a hard refresh.
	e.GET("/*", func(c echo.Context) error {
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
