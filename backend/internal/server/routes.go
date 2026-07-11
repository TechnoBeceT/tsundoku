// Package server — see server.go for package-level documentation.
package server

import (
	"context"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/config"
	"github.com/technobecet/tsundoku/internal/downloads"
	entpkg "github.com/technobecet/tsundoku/internal/ent"
	categoryh "github.com/technobecet/tsundoku/internal/handler/category"
	downloadsh "github.com/technobecet/tsundoku/internal/handler/downloads"
	extensionsh "github.com/technobecet/tsundoku/internal/handler/extensions"
	importsh "github.com/technobecet/tsundoku/internal/handler/imports"
	libraryh "github.com/technobecet/tsundoku/internal/handler/library"
	"github.com/technobecet/tsundoku/internal/handler/owner"
	seriesh "github.com/technobecet/tsundoku/internal/handler/series"
	settingsh "github.com/technobecet/tsundoku/internal/handler/settings"
	sourcesh "github.com/technobecet/tsundoku/internal/handler/sources"
	suwayomih "github.com/technobecet/tsundoku/internal/handler/suwayomi"
	systemh "github.com/technobecet/tsundoku/internal/handler/system"
	"github.com/technobecet/tsundoku/internal/imports"
	"github.com/technobecet/tsundoku/internal/library"
	"github.com/technobecet/tsundoku/internal/metrics"
	mw "github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/sse"
	"github.com/technobecet/tsundoku/internal/suwayomi"
	"github.com/technobecet/tsundoku/internal/warmup"
)

// registerRoutes wires all HTTP routes onto the provided Echo instance.
//
// Route groups:
//   - /health                                      — liveness probe (no auth).
//   - /docs, /docs/…                               — Scalar API reference + raw OpenAPI spec (no auth).
//   - /api/owner/claim                             — first-run owner creation (no auth; fail-closed).
//   - /api/owner/login                             — owner login (no auth).
//   - /api/owner/logout                            — revoke session cookie (RequireOwner).
//   - /api/owner/me                                — return current owner identity (RequireOwner).
//   - /api/progress                                — SSE stream (RequireOwner).
//   - /api/sources                                 — list Suwayomi sources (RequireOwner).
//   - /api/search                                  — multi-source manga search (RequireOwner).
//   - /api/sources/:sourceId/browse                — per-source Popular/Latest catalog browse (RequireOwner).
//   - /api/sources/:sourceId/manga/:mangaId/chapters — chapter preview (RequireOwner).
//   - /api/sources/:sourceId/manga/:mangaId/cover    — source-manga cover proxy (RequireOwner).
//   - /api/sources/:sourceId/manga/:mangaId/details  — on-demand rich manga details (RequireOwner).
//   - /api/sources/:sourceId/manga/:mangaId/breakdown — per-scanlator chapter coverage breakdown (RequireOwner).
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
//   - /api/series/:id/chapters/:chapterId/pages/:n — in-app reader CBZ page bytes (RequireOwner).
//   - /api/chapters/:id/progress (PATCH)           — set chapter reading progress (RequireOwner).
//   - /api/categories (GET)                        — list categories with counts (RequireOwner).
//   - /api/categories (POST)                       — create a category (RequireOwner).
//   - /api/categories/:id (PATCH)                  — rename and/or reorder a category (RequireOwner).
//   - /api/categories/:id/default (PATCH)          — set the default landing category (RequireOwner).
//   - /api/categories/:id (DELETE)                 — delete an empty category (RequireOwner).
//   - /api/health                                  — library source-health scan (RequireOwner).
//   - /api/settings (GET)                          — list runtime tunables (RequireOwner).
//   - /api/settings (PATCH)                         — batch-update runtime tunables (RequireOwner).
//   - /api/sources/metrics (GET)                   — per-source performance metrics + isSlow (RequireOwner).
//   - /api/sources/warmup (POST)                   — trigger a full anti-bot warm-up pass (RequireOwner).
//   - /api/system (GET)                             — read-only env-structural info (RequireOwner).
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
//   - /api/suwayomi/extensions/:pkgName/icon (GET)  — extension icon proxy (RequireOwner).
//   - /api/suwayomi/extensions/:pkgName/preferences (GET)   — per-source preferences, grouped by source (RequireOwner).
//   - /api/suwayomi/extensions/:pkgName/preferences (PATCH) — write one preference by position (RequireOwner).
//   - /api/suwayomi/sources/:sourceId/enabled (PATCH)        — per-language enable/disable toggle (RequireOwner).
//   - /api/downloads (GET)                         — cross-library chapter activity by state (RequireOwner).
//   - /api/downloads/retry-all (POST)              — bulk-reset failed chapters to wanted (RequireOwner).
//   - /api/chapters/:id/retry (POST)               — reset one failed chapter to wanted (RequireOwner).
//   - /api/downloads/run (POST)                    — trigger an immediate download cycle ("Download now") (RequireOwner).
//   - /api/library/scan (POST)                     — scan on-disk storage, stage found series (RequireOwner).
//   - /api/library/imports (GET)                   — list staged imports (?status=) (RequireOwner).
//   - /api/library/imports/match (GET)             — search sources for a staged entry's title (?path=) (RequireOwner).
//   - /api/library/import (POST)                   — import a staged entry without re-downloading (RequireOwner).
//   - /api/library/import/batch (POST)              — bulk disk-only import of many staged entries (RequireOwner).
//   - /api/series/:id/providers (POST)             — attach an additional source to an existing series (RequireOwner).
//   - /api/series/:id/providers/batch (POST)       — attach several sources to an existing series in one call (RequireOwner).
//   - /api/series/:id/providers/:providerId/match (POST) — attribute existing on-disk chapters to a real source without re-downloading (RequireOwner).
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
	metricsSvc *metrics.Service,
	warmupSvc *warmup.Service,
	gate *sourcegate.Service,
	chapterCache *suwayomi.ChapterCache,
	trigger func(),
) {
	// Infrastructure routes — no authentication required.
	e.GET("/health", HealthCheck)
	RegisterDocs(e)

	// Public owner endpoints — auth not required (claim bootstraps it).
	api := e.Group("/api")
	api.POST("/owner/claim", ownerH.Claim)
	api.POST("/owner/login", ownerH.Login)

	// Authenticated API group — all routes require a valid owner session.
	authed := e.Group("/api", mw.RequireOwner(authSvc, cfg.Auth.CookieSecure))
	authed.POST("/owner/logout", ownerH.Logout)
	authed.GET("/owner/me", ownerH.Me)
	sse.RegisterRoutes(authed, hub)

	// Library (series) API. The service owns the Ent client and the storage root
	// so the recategorize path can move folders on disk in lockstep with the DB.
	// seriesSvc is shared: reused by both the series handler and the imports
	// handler (to render SeriesDetailDTO after Adopt).
	// WithCoverFetcher lets the series cover endpoint fall back to Suwayomi when a
	// series' cover is not yet cached in its library folder (it caches it there on
	// that first fetch, and never pings the source for it again).
	seriesSvc := series.NewService(client, cfg.Storage.Folder, cfg.Health.StaleGraceDays).
		WithCoverFetcher(suwayomiClient)
	seriesH := seriesh.NewHandler(seriesSvc, trigger, suwayomiClient)
	authed.GET("/series", seriesH.List)
	authed.GET("/series/:id", seriesH.Detail)
	authed.PATCH("/series/:id/category", seriesH.SetCategory)
	authed.PATCH("/series/:id/monitored", seriesH.SetMonitored)
	authed.PATCH("/series/:id/completed", seriesH.SetCompleted)
	authed.PATCH("/series/:id/providers", seriesH.ReorderProviders)
	authed.DELETE("/series/:id/providers/:providerId", seriesH.RemoveProvider)
	authed.DELETE("/series/:id", seriesH.DeleteSeries)
	authed.POST("/series/:id/dedupe-files", seriesH.DedupeFiles)
	authed.GET("/series/:id/cover", seriesH.SeriesCover)
	authed.GET("/series/:id/providers/:providerId/cover", seriesH.ProviderCover)
	authed.PATCH("/series/:id/metadata-source", seriesH.SetMetadataSource)
	authed.GET("/series/:id/chapters/:chapterId/pages/:n", seriesH.ChapterPage)
	authed.PATCH("/chapters/:id/progress", seriesH.SetProgress)
	authed.GET("/health", seriesH.LibraryHealth)

	// Settings (runtime tunables) API. The service is built in main.go (it shares
	// the Ent client + carries the config-resolved defaults) and threaded in here.
	settingsH := settingsh.NewHandler(settingsSvc)
	authed.GET("/settings", settingsH.List)
	authed.PATCH("/settings", settingsH.Update)

	// Source metrics + anti-bot warm-up API. The handler reads the rolling
	// per-source performance snapshot (metricsSvc) and triggers a manual warm
	// pass (warmupSvc); the slow threshold is resolved from the settings overlay
	// at read time (settingsSvc).
	sourcesH := sourcesh.NewHandler(metricsSvc, warmupSvc, settingsSvc)
	authed.GET("/sources/metrics", sourcesH.Metrics)
	authed.POST("/sources/warmup", sourcesH.Warmup)

	// System info — read-only credential-free structural config (storage path,
	// server port, DB host:port/name). The handler needs only the config struct;
	// no service or Ent client is required.
	systemH := systemh.NewHandler(cfg)
	authed.GET("/system", systemH.Get)

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
	authed.GET("/suwayomi/extensions/:pkgName/icon", extensionsH.Icon)
	authed.GET("/suwayomi/extensions/:pkgName/preferences", extensionsH.Preferences)
	authed.PATCH("/suwayomi/extensions/:pkgName/preferences", extensionsH.SetPreference)
	authed.PATCH("/suwayomi/sources/:sourceId/enabled", extensionsH.SetSourceEnabled)

	// Category CRUD API. The service owns the Ent client + storage root so a
	// rename moves the on-disk category folder in lockstep with the DB.
	categorySvc := category.NewService(client, cfg.Storage.Folder)
	categoryH := categoryh.NewHandler(categorySvc)
	authed.GET("/categories", categoryH.List)
	authed.POST("/categories", categoryH.Create)
	authed.PATCH("/categories/:id", categoryH.Update)
	authed.PATCH("/categories/:id/default", categoryH.SetDefault)
	authed.DELETE("/categories/:id", categoryH.Delete)

	// Downloads (cross-library chapter activity) API. The service reuses the
	// exported series resolvers for name/display/cover enrichment, so it needs
	// only the Ent client.
	downloadsSvc := downloads.NewService(client)
	downloadsH := downloadsh.NewHandler(downloadsSvc, trigger)
	authed.GET("/downloads", downloadsH.List)
	authed.POST("/downloads/retry-all", downloadsH.RetryAll)
	authed.POST("/chapters/:id/retry", downloadsH.RetryChapter)
	authed.POST("/downloads/run", downloadsH.Run)

	// Imports (discovery + adoption) API. The ingest is built here so it shares
	// the same Ent client as the rest of the application; a single suwayomiClient
	// value is threaded in from main.
	// Anti-ban de-amplification: the ingest routes its adopt/attach fetch through
	// the shared source-politeness gate (Task B) and the shared chapter cache
	// (Task C2 — the SAME instance the imports coverage paths use, so a
	// coverage→configure→adopt session fetches a source-manga once). imports uses
	// NewServiceWithCaches so its coverage + Search paths are cached too (C1/C2).
	ingest := suwayomi.NewIngestWithGate(suwayomiClient, client, chapterCache, gate)
	importsSvc := imports.NewServiceWithCaches(
		suwayomiClient, ingest, client, cfg.Storage.Folder, cfg.Suwayomi.SearchTimeout, metricsSvc, chapterCache,
		// Search-cache TTL read per Get from the settings overlay (jobs.search_cache_ttl,
		// hot reload); 0 disables the search cache at runtime.
		func(ctx context.Context) time.Duration { return settingsSvc.SearchCacheTTL(ctx) },
	)
	importsH := importsh.NewHandler(importsSvc, seriesSvc, trigger, suwayomiClient)
	authed.GET("/sources", importsH.Sources)
	authed.GET("/search", importsH.Search)
	authed.GET("/sources/:sourceId/browse", importsH.Browse)
	authed.GET("/sources/:sourceId/manga/:mangaId/chapters", importsH.InspectChapters)
	authed.GET("/sources/:sourceId/manga/:mangaId/cover", importsH.MangaCover)
	authed.GET("/sources/:sourceId/manga/:mangaId/details", importsH.Details)
	authed.GET("/sources/:sourceId/manga/:mangaId/breakdown", importsH.Breakdown)
	authed.POST("/series", importsH.Adopt)

	// Library-import (on-disk scan + adopt-without-redownload) API. Reuses the
	// SAME ingest/importsSvc/seriesSvc instances constructed above — no double
	// construction — plus the shared trigger, storage root, and SSE hub (the
	// async scan streams scan.start/scan.progress/scan.done over it).
	librarySvc := library.NewService(client, ingest, importsSvc, seriesSvc, trigger, cfg.Storage.Folder, hub)
	libraryH := libraryh.NewHandler(librarySvc)
	authed.POST("/library/scan", libraryH.Scan)
	authed.GET("/library/imports", libraryH.ListImports)
	authed.GET("/library/imports/match", libraryH.Match)
	authed.POST("/library/import", libraryH.Import)
	authed.POST("/library/import/batch", libraryH.Batch)
	authed.POST("/library/imports/skip", libraryH.Skip)
	authed.POST("/series/:id/providers", libraryH.AddProvider)
	authed.POST("/series/:id/providers/batch", libraryH.AddProviders)
	authed.POST("/series/:id/providers/:providerId/match", libraryH.MatchDiskProvider)
	authed.POST("/series/:id/providers/dedup", libraryH.DedupProviders)
	authed.POST("/library/dedup-providers", libraryH.DedupAllProviders)

	// SPA static serving + unknown-route handling (registered last).
	registerStaticSPA(e)
}
