// Package server — see server.go for package-level documentation.
package server

import (
	"github.com/labstack/echo/v4"
	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/config"
	"github.com/technobecet/tsundoku/internal/downloads"
	entpkg "github.com/technobecet/tsundoku/internal/ent"
	categoryh "github.com/technobecet/tsundoku/internal/handler/category"
	downloadsh "github.com/technobecet/tsundoku/internal/handler/downloads"
	extensionsh "github.com/technobecet/tsundoku/internal/handler/extensions"
	importsh "github.com/technobecet/tsundoku/internal/handler/imports"
	"github.com/technobecet/tsundoku/internal/handler/owner"
	seriesh "github.com/technobecet/tsundoku/internal/handler/series"
	settingsh "github.com/technobecet/tsundoku/internal/handler/settings"
	suwayomih "github.com/technobecet/tsundoku/internal/handler/suwayomi"
	"github.com/technobecet/tsundoku/internal/imports"
	mw "github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/settings"
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
//   - /api/sources/:sourceId/browse                — per-source Popular/Latest catalog browse (RequireOwner).
//   - /api/sources/:sourceId/manga/:mangaId/chapters — chapter preview (RequireOwner).
//   - /api/series (GET)                            — library list (RequireOwner).
//   - /api/series (POST)                           — adopt / import manga (RequireOwner).
//   - /api/series/:id (GET)                        — library detail (RequireOwner).
//   - /api/series/:id (DELETE)                     — delete a whole series (?deleteFiles=) (RequireOwner).
//   - /api/series/:id/category                     — recategorize (RequireOwner).
//   - /api/series/:id/monitored                    — toggle monitoring flag (RequireOwner).
//   - /api/series/:id/completed                    — toggle completed (finished) flag (RequireOwner).
//   - /api/series/:id/providers                    — re-rank provider importances (RequireOwner).
//   - /api/series/:id/providers/:providerId        — remove a source (RequireOwner).
//   - /api/series/:id/cover                        — metadata-source cover proxy (RequireOwner).
//   - /api/series/:id/providers/:providerId/cover  — per-provider cover proxy (RequireOwner).
//   - /api/series/:id/metadata-source              — pin metadata source (RequireOwner).
//   - /api/categories (GET)                        — list categories with counts (RequireOwner).
//   - /api/categories (POST)                       — create a category (RequireOwner).
//   - /api/categories/:id (PATCH)                  — rename and/or reorder a category (RequireOwner).
//   - /api/categories/:id (DELETE)                 — delete an empty category (RequireOwner).
//   - /api/health                                  — library source-health scan (RequireOwner).
//   - /api/settings (GET)                          — list runtime tunables (RequireOwner).
//   - /api/settings (PATCH)                         — batch-update runtime tunables (RequireOwner).
//   - /api/suwayomi/settings (GET)                  — read Suwayomi FlareSolverr/SOCKS settings (RequireOwner).
//   - /api/suwayomi/settings (PATCH)                — partial-update Suwayomi FlareSolverr/SOCKS settings (RequireOwner).
//   - /api/suwayomi/extensions (GET)                — list Suwayomi extensions (RequireOwner).
//   - /api/suwayomi/extensions/refresh (POST)       — refresh available extensions from repos (RequireOwner).
//     The static /repos routes are registered BEFORE the dynamic /:pkgName routes
//     (Echo matches static before param, so /repos never collides with :pkgName).
//   - /api/suwayomi/extensions/repos (GET)          — read extension repo URLs (RequireOwner).
//   - /api/suwayomi/extensions/repos (PUT)          — replace extension repo URLs (RequireOwner).
//   - /api/suwayomi/extensions/:pkgName/install (POST) — install an extension (RequireOwner).
//   - /api/suwayomi/extensions/:pkgName/update (POST)  — update an extension (RequireOwner).
//   - /api/suwayomi/extensions/:pkgName (DELETE)    — uninstall an extension (RequireOwner).
//   - /api/downloads (GET)                         — cross-library chapter activity by state (RequireOwner).
//   - /api/downloads/retry-all (POST)              — bulk-reset failed chapters to wanted (RequireOwner).
//   - /api/chapters/:id/retry (POST)               — reset one failed chapter to wanted (RequireOwner).
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
	settingsSvc *settings.Service,
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
	seriesSvc := series.NewService(client, cfg.Storage.Folder, cfg.Health.StaleGraceDays)
	seriesH := seriesh.NewHandler(seriesSvc, trigger, suwayomiClient)
	authed.GET("/series", seriesH.List)
	authed.GET("/series/:id", seriesH.Detail)
	authed.PATCH("/series/:id/category", seriesH.SetCategory)
	authed.PATCH("/series/:id/monitored", seriesH.SetMonitored)
	authed.PATCH("/series/:id/completed", seriesH.SetCompleted)
	authed.PATCH("/series/:id/providers", seriesH.ReorderProviders)
	authed.DELETE("/series/:id/providers/:providerId", seriesH.RemoveProvider)
	authed.DELETE("/series/:id", seriesH.DeleteSeries)
	authed.GET("/series/:id/cover", seriesH.SeriesCover)
	authed.GET("/series/:id/providers/:providerId/cover", seriesH.ProviderCover)
	authed.PATCH("/series/:id/metadata-source", seriesH.SetMetadataSource)
	authed.GET("/health", seriesH.LibraryHealth)

	// Settings (runtime tunables) API. The service is built in main.go (it shares
	// the Ent client + carries the config-resolved defaults) and threaded in here.
	settingsH := settingsh.NewHandler(settingsSvc)
	authed.GET("/settings", settingsH.List)
	authed.PATCH("/settings", settingsH.Update)

	// Suwayomi server-settings proxy (FlareSolverr + SOCKS). The handler holds
	// the Suwayomi client directly and proxies its server-global settings; no
	// Tsundoku state is involved.
	suwayomiSettingsH := suwayomih.NewHandler(suwayomiClient)
	authed.GET("/suwayomi/settings", suwayomiSettingsH.Get)
	authed.PATCH("/suwayomi/settings", suwayomiSettingsH.Update)

	// Suwayomi extension (Sources & Extensions) management. Like the settings
	// proxy, the handler holds the Suwayomi client directly and proxies its
	// extension GraphQL surface; no Tsundoku state is involved.
	extensionsH := extensionsh.NewHandler(suwayomiClient)
	authed.GET("/suwayomi/extensions", extensionsH.List)
	authed.POST("/suwayomi/extensions/refresh", extensionsH.Refresh)
	authed.GET("/suwayomi/extensions/repos", extensionsH.GetRepos)
	authed.PUT("/suwayomi/extensions/repos", extensionsH.SetRepos)
	authed.POST("/suwayomi/extensions/:pkgName/install", extensionsH.Install)
	authed.POST("/suwayomi/extensions/:pkgName/update", extensionsH.Update)
	authed.DELETE("/suwayomi/extensions/:pkgName", extensionsH.Uninstall)

	// Category CRUD API. The service owns the Ent client + storage root so a
	// rename moves the on-disk category folder in lockstep with the DB.
	categorySvc := category.NewService(client, cfg.Storage.Folder)
	categoryH := categoryh.NewHandler(categorySvc)
	authed.GET("/categories", categoryH.List)
	authed.POST("/categories", categoryH.Create)
	authed.PATCH("/categories/:id", categoryH.Update)
	authed.DELETE("/categories/:id", categoryH.Delete)

	// Downloads (cross-library chapter activity) API. The service reuses the
	// exported series resolvers for name/display/cover enrichment, so it needs
	// only the Ent client.
	downloadsSvc := downloads.NewService(client)
	downloadsH := downloadsh.NewHandler(downloadsSvc)
	authed.GET("/downloads", downloadsH.List)
	authed.POST("/downloads/retry-all", downloadsH.RetryAll)
	authed.POST("/chapters/:id/retry", downloadsH.RetryChapter)

	// Imports (discovery + adoption) API. The ingest is built here so it shares
	// the same Ent client as the rest of the application; a single suwayomiClient
	// value is threaded in from main.
	ingest := suwayomi.NewIngest(suwayomiClient, client)
	importsSvc := imports.NewService(suwayomiClient, ingest, client, cfg.Storage.Folder)
	importsH := importsh.NewHandler(importsSvc, seriesSvc, trigger)
	authed.GET("/sources", importsH.Sources)
	authed.GET("/search", importsH.Search)
	authed.GET("/sources/:sourceId/browse", importsH.Browse)
	authed.GET("/sources/:sourceId/manga/:mangaId/chapters", importsH.InspectChapters)
	authed.POST("/series", importsH.Adopt)

	// SPA static serving + unknown-route handling (registered last).
	registerStaticSPA(e)
}
