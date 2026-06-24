// Package server — see server.go for package-level documentation.
package server

import (
	"github.com/labstack/echo/v4"
	"github.com/technobecet/tsundoku/internal/config"
	entpkg "github.com/technobecet/tsundoku/internal/ent"
	importsh "github.com/technobecet/tsundoku/internal/handler/imports"
	"github.com/technobecet/tsundoku/internal/handler/owner"
	seriesh "github.com/technobecet/tsundoku/internal/handler/series"
	"github.com/technobecet/tsundoku/internal/imports"
	mw "github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sse"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// registerRoutes wires all HTTP routes onto the provided Echo instance.
//
// Route groups:
//   - /health                                      — liveness probe (no auth).
//   - /docs, /docs/…                               — Scalar API reference + raw OpenAPI spec (no auth).
//   - /api/owner/claim                             — first-run owner creation (no auth; fail-closed).
//   - /api/owner/login                             — owner login (no auth).
//   - /api/progress                                — SSE stream (RequireOwner).
//   - /api/sources                                 — list Suwayomi sources (RequireOwner).
//   - /api/search                                  — multi-source manga search (RequireOwner).
//   - /api/sources/:sourceId/manga/:mangaId/chapters — chapter preview (RequireOwner).
//   - /api/series (GET)                            — library list (RequireOwner).
//   - /api/series (POST)                           — adopt / import manga (RequireOwner).
//   - /api/series/:id                              — library detail (RequireOwner).
//   - /api/series/:id/category                     — recategorize (RequireOwner).
//   - /api/series/:id/monitored                    — toggle monitoring flag (RequireOwner).
//   - /api/series/:id/providers                    — re-rank provider importances (RequireOwner).
//   - /api/categories                              — per-category counts (RequireOwner).
//   - /api/*                                       — catch-all 404 JSON for unknown API paths.
//   - /*                                           — SPA static fallback for non-API routes (same-origin).
func registerRoutes(
	e *echo.Echo,
	cfg *config.Config,
	client *entpkg.Client,
	authSvc *auth.Service,
	hub *sse.Hub,
	ownerH *owner.Handler,
	suwayomiClient suwayomi.Client,
	trigger func(),
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
	// seriesSvc is shared: reused by both the series handler and the imports
	// handler (to render SeriesDetailDTO after Adopt).
	seriesSvc := series.NewService(client, cfg.Storage.Folder)
	seriesH := seriesh.NewHandler(seriesSvc, trigger)
	authed.GET("/series", seriesH.List)
	authed.GET("/series/:id", seriesH.Detail)
	authed.PATCH("/series/:id/category", seriesH.SetCategory)
	authed.PATCH("/series/:id/monitored", seriesH.SetMonitored)
	authed.PATCH("/series/:id/providers", seriesH.ReorderProviders)
	authed.GET("/categories", seriesH.Categories)

	// Imports (discovery + adoption) API. The ingest is built here so it shares
	// the same Ent client as the rest of the application; a single suwayomiClient
	// value is threaded in from main.
	ingest := suwayomi.NewIngest(suwayomiClient, client)
	importsSvc := imports.NewService(suwayomiClient, ingest, client, cfg.Storage.Folder)
	importsH := importsh.NewHandler(importsSvc, seriesSvc, trigger)
	authed.GET("/sources", importsH.Sources)
	authed.GET("/search", importsH.Search)
	authed.GET("/sources/:sourceId/manga/:mangaId/chapters", importsH.InspectChapters)
	authed.POST("/series", importsH.Adopt)

	// SPA static serving + unknown-route handling (registered last).
	registerStaticSPA(e)
}
