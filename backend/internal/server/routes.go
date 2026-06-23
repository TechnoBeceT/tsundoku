// Package server — see server.go for package-level documentation.
package server

import (
	"github.com/labstack/echo/v4"
	"github.com/technobecet/tsundoku/internal/config"
	entpkg "github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/handler/owner"
	seriesh "github.com/technobecet/tsundoku/internal/handler/series"
	mw "github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sse"
)

// registerRoutes wires all HTTP routes onto the provided Echo instance.
//
// Route groups:
//   - /health            — liveness probe (no auth).
//   - /docs, /docs/…     — Scalar API reference + raw OpenAPI spec (no auth).
//   - /api/owner/claim   — first-run owner creation (no auth; fail-closed).
//   - /api/owner/login   — owner login (no auth).
//   - /api/progress      — SSE stream (RequireOwner).
//   - /api/series        — library list (RequireOwner).
//   - /api/series/:id     — library detail (RequireOwner).
//   - /api/series/:id/category — recategorize (RequireOwner).
//   - /api/categories    — per-category counts (RequireOwner).
//   - /api/*             — catch-all 404 JSON for unknown API paths.
//   - /*                 — SPA static fallback for non-API routes (same-origin).
func registerRoutes(
	e *echo.Echo,
	cfg *config.Config,
	client *entpkg.Client,
	authSvc *auth.Service,
	hub *sse.Hub,
	ownerH *owner.Handler,
) {
	// Infrastructure routes — no authentication required.
	e.GET("/health", HealthCheck)
	RegisterDocs(e)

	// Public owner endpoints — auth not required (claim bootstraps it).
	api := e.Group("/api")
	api.POST("/owner/claim", ownerH.Claim)
	api.POST("/owner/login", ownerH.Login)

	// Authenticated API group — all routes require a valid Bearer token.
	authed := e.Group("/api", mw.RequireOwner(authSvc))
	sse.RegisterRoutes(authed, hub)

	// Library (series) API. The service owns the Ent client and the storage root
	// so the recategorize path can move folders on disk in lockstep with the DB.
	seriesH := seriesh.NewHandler(series.NewService(client, cfg.Storage.Folder))
	authed.GET("/series", seriesH.List)
	authed.GET("/series/:id", seriesH.Detail)
	authed.PATCH("/series/:id/category", seriesH.SetCategory)
	authed.GET("/categories", seriesH.Categories)

	// SPA static serving + unknown-route handling (registered last).
	registerStaticSPA(e)
}
