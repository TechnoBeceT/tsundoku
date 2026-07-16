// Package server — see server.go for package-level documentation.
package server

import (
	"context"
	"net/http"
	"path/filepath"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/config"
	"github.com/technobecet/tsundoku/internal/downloads"
	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	entpkg "github.com/technobecet/tsundoku/internal/ent"
	categoryh "github.com/technobecet/tsundoku/internal/handler/category"
	downloadsh "github.com/technobecet/tsundoku/internal/handler/downloads"
	engineh "github.com/technobecet/tsundoku/internal/handler/engine"
	extensionsh "github.com/technobecet/tsundoku/internal/handler/extensions"
	flaresolverrh "github.com/technobecet/tsundoku/internal/handler/flaresolverr"
	importsh "github.com/technobecet/tsundoku/internal/handler/imports"
	libraryh "github.com/technobecet/tsundoku/internal/handler/library"
	metadatah "github.com/technobecet/tsundoku/internal/handler/metadata"
	"github.com/technobecet/tsundoku/internal/handler/owner"
	pushh "github.com/technobecet/tsundoku/internal/handler/push"
	seriesh "github.com/technobecet/tsundoku/internal/handler/series"
	settingsh "github.com/technobecet/tsundoku/internal/handler/settings"
	sourcesh "github.com/technobecet/tsundoku/internal/handler/sources"
	systemh "github.com/technobecet/tsundoku/internal/handler/system"
	trackersh "github.com/technobecet/tsundoku/internal/handler/trackers"
	"github.com/technobecet/tsundoku/internal/imports"
	"github.com/technobecet/tsundoku/internal/ingest"
	"github.com/technobecet/tsundoku/internal/library"
	"github.com/technobecet/tsundoku/internal/metadatasvc"
	"github.com/technobecet/tsundoku/internal/metrics"
	mw "github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	pushsvc "github.com/technobecet/tsundoku/internal/push"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourcecover"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/sse"
	"github.com/technobecet/tsundoku/internal/tracker"
	"github.com/technobecet/tsundoku/internal/tracker/bind"
	"github.com/technobecet/tsundoku/internal/tracker/connect"
	"github.com/technobecet/tsundoku/internal/tracker/syncsvc"
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
//   - /api/sources/:sourceId/manga/:mangaId/details  — on-demand rich manga details (RequireOwner).
//   - /api/sources/:sourceId/manga/:mangaId/breakdown — per-scanlator chapter coverage breakdown (RequireOwner).
//   - /api/sources/:sourceId/cover                 — source-manga cover proxy, re-fetched via the engine host so protected sources render (RequireOwner).
//   - /api/series (GET)                            — library list (RequireOwner).
//   - /api/series (POST)                           — adopt / import manga (RequireOwner).
//   - /api/series/:id (GET)                        — library detail (RequireOwner).
//   - /api/series/:id (DELETE)                     — delete a whole series (?deleteFiles=) (RequireOwner).
//   - /api/series/:id/category                     — recategorize (RequireOwner).
//   - /api/series/:id/monitored                    — toggle monitoring flag (RequireOwner).
//   - /api/series/:id/completed                    — toggle completed (finished) flag (RequireOwner).
//   - /api/series/:id/providers                    — re-rank provider importances (RequireOwner).
//   - /api/series/:id/providers/:providerId        — remove a source (RequireOwner).
//   - /api/series/:id/providers/:providerId/ignore-fractional — flag a source as a fractional re-uploader (RequireOwner).
//   - /api/series/:id/cover                        — metadata-source cover proxy (RequireOwner).
//   - /api/series/:id/providers/:providerId/cover  — per-provider cover proxy (RequireOwner).
//   - /api/series/:id/metadata-source              — pin metadata source (RequireOwner).
//   - /api/metadata/search                          — cross-provider metadata candidate search (RequireOwner).
//   - /api/series/:id/metadata/identify (POST)      — anchor-then-aggregate metadata identify (RequireOwner).
//   - /api/series/:id/metadata/covers (GET)         — aggregated metadata cover-candidate gallery (RequireOwner).
//   - /api/series/:id/cover (POST)                  — owner's explicit cover pick (RequireOwner).
//   - /api/series/:id/chapters/:chapterId/pages/:n — in-app reader CBZ page bytes (RequireOwner).
//   - /api/chapters/:id/progress (PATCH)           — set chapter reading progress (RequireOwner).
//   - /api/series/:id/reading-progress (POST)      — set series reading progress to chapter N: resets local
//     chapters + force-sets every bound tracker (QCAT-242) (RequireOwner).
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
//   - /api/push/vapid-key (GET)                     — server VAPID public key for Web Push subscribe (RequireOwner).
//   - /api/push/subscriptions (POST)               — upsert this device's Web Push subscription (RequireOwner).
//   - /api/push/subscriptions (DELETE)             — remove this device's Web Push subscription (RequireOwner).
//   - /api/system (GET)                             — read-only env-structural info (RequireOwner).
//   - /api/flaresolverr/settings (GET)              — read Tsundoku-owned FlareSolverr settings (RequireOwner).
//   - /api/flaresolverr/settings (PATCH)            — partial-update + best-effort mirror to Suwayomi (RequireOwner).
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
//   - /api/trackers (GET)                          — list tracker connect status (RequireOwner).
//   - /api/trackers/:id/auth-url (GET)             — build a fresh OAuth authorize URL on demand (RequireOwner).
//   - /api/trackers/:id/login/oauth (POST)         — complete an OAuth login callback (RequireOwner).
//   - /api/trackers/:id/login/credentials (POST)   — direct username/password tracker login (RequireOwner).
//   - /api/trackers/:id/logout (POST)              — disconnect a tracker account (RequireOwner).
//   - /api/trackers/:id/search (GET)                — authed tracker search (RequireOwner).
//   - /api/series/:id/tracking (GET)               — a series' tracker bindings (RequireOwner).
//   - /api/series/:id/tracking (POST)              — bind a series to a tracker entry (RequireOwner).
//   - /api/series/:id/tracking/:recordId (DELETE)  — unbind (?deleteRemote=) (RequireOwner).
//   - /api/series/:id/tracking/:recordId/refresh (POST) — re-pull a binding's remote entry (RequireOwner).
//   - /api/series/:id/tracking/:recordId/update (POST) — owner's manual tracking-sheet edit (RequireOwner).
//   - /api/series/:id/tracking/sync (POST)         — pull + converge every binding for a series (RequireOwner).
//   - /api/engine/topology-status (GET)          — read-only captured-engine-topology status (RequireOwner).
//   - /internal/extensions/apk/:pkg/:file (GET) — cached extension .apk bytes for engine recovery; :file = "<pkg>-<version>.apk" (RequireOwner; NOT in the OpenAPI spec).
//   - /api/*                                       — catch-all 404 JSON for unknown API paths.
//   - /*                                           — SPA static fallback for non-API routes (same-origin).
func registerRoutes(
	e *echo.Echo,
	cfg *config.Config,
	client *entpkg.Client,
	authSvc *auth.Service,
	hub *sse.Hub,
	ownerH *owner.Handler,
	engineClient sourceengine.Client,
	settingsSvc *settings.Service,
	metricsSvc *metrics.Service,
	warmupSvc *warmup.Service,
	gate *sourcegate.Service,
	chapterCache *ingest.ChapterCache,
	metaSvc *metadatasvc.Service,
	trackerRegistry *tracker.Registry,
	trackerConnectSvc *connect.Service,
	trackerBindSvc *bind.Service,
	trackerSyncSvc *syncsvc.Service,
	pushSubsSvc *pushsvc.Service,
	vapidPublicKey string,
	trigger func(),
	apkStore *apkcache.Store,
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

	// coverCache (GAP-085) is the shared disk-cache-first, fail-fast wrapper
	// around an engine-host cover fetch — restores the disk-cache-first
	// behaviour Suwayomi's own thumbnail cache had, which the P2 engine swap
	// dropped for these two proxies (see internal/sourcecover's package doc).
	// It is shared between BOTH engine-fed cover proxies (the per-provider
	// metadata cover below, and the Discover/Search source cover further down)
	// so a burst across either surface queues through the SAME bounded pool.
	// Rooted under a "source-covers" subdirectory of Engine.RuntimeDir,
	// mirroring apkStore's own "apkcache" subdirectory of the same root (both
	// are Tsundoku-owned durable caches of engine-fed artifacts, never an
	// engine-host data directory — Tsundoku launches no engine-host process of
	// its own). Deliberately NOT the library series cover cache
	// (series.Service.CoverBytes, WithCoverFetcher below) — that endpoint was
	// already disk-cached and is untouched.
	coverCache := sourcecover.NewCache(
		sourcecover.New(filepath.Join(cfg.Engine.RuntimeDir, "source-covers")),
		engineClient,
		sourcecover.DefaultConcurrency,
		sourcecover.DefaultDeadline,
	)

	// Library (series) API. The service owns the Ent client and the storage root
	// so the recategorize path can move folders on disk in lockstep with the DB.
	// seriesSvc is shared: reused by both the series handler and the imports
	// handler (to render SeriesDetailDTO after Adopt).
	// WithCoverFetcher lets the series cover endpoint fall back to the engine
	// host (P2 slice 4: repointed off suwayomiClient) when a series' cover is
	// not yet cached in its library folder (it caches it there on that first
	// fetch, and never pings the source for it again).
	// WithProgressPusher wires the reading-triggered tracker push: marking a
	// chapter read in the reader (series.SetProgress) fires a detached,
	// best-effort syncsvc push, gated by the auto_update_track setting. This is
	// the "live on read" half of the trigger model (QCAT-234); the detail-open
	// reconcile below is the other. trackerSyncSvc satisfies both hooks.
	seriesSvc := series.NewService(client, cfg.Storage.Folder, cfg.Health.StaleGraceDays).
		WithCoverFetcher(engineClient).
		WithSourceLister(sourceengine.NewSourceLister(engineClient)).
		WithProgressPusher(trackerSyncSvc)
	// WithViewSyncer wires the detail-open tracker reconcile: opening a series'
	// detail page fires a detached, best-effort syncsvc.Service.SyncOnView IN
	// ADDITION to the existing reading-triggered push (series.ProgressPusher —
	// a DIFFERENT hook, attached to seriesSvc itself, not the handler; see
	// handler/series.ViewSyncer's doc comment for why detail-open is ungated
	// where the reading push is toggle-gated). trackerSyncSvc satisfies both
	// hooks' narrow interfaces — one service, two independent trigger points.
	// WithTrackerProgressSetter wires the QCAT-242 "set reading progress to N"
	// tracker force-set (SetReadingProgress) — a THIRD, independent hook the
	// same trackerSyncSvc instance also satisfies.
	seriesH := seriesh.NewHandler(seriesSvc, trigger, coverCache).
		WithViewSyncer(trackerSyncSvc).
		WithTrackerProgressSetter(trackerSyncSvc)
	authed.GET("/series", seriesH.List)
	authed.GET("/series/:id", seriesH.Detail)
	authed.PATCH("/series/:id/category", seriesH.SetCategory)
	authed.PATCH("/series/:id/monitored", seriesH.SetMonitored)
	authed.PATCH("/series/:id/completed", seriesH.SetCompleted)
	authed.PATCH("/series/:id/providers", seriesH.ReorderProviders)
	authed.DELETE("/series/:id/providers/:providerId", seriesH.RemoveProvider)
	authed.PATCH("/series/:id/providers/:providerId/ignore-fractional", seriesH.SetIgnoreFractional)
	authed.DELETE("/series/:id", seriesH.DeleteSeries)
	authed.POST("/series/:id/dedupe-files", seriesH.DedupeFiles)
	authed.GET("/series/:id/fractional-cleanup", seriesH.FractionalCleanupPreview)
	authed.POST("/series/:id/fractional-cleanup", seriesH.RemoveFractionalChapters)
	authed.GET("/series/:id/cover", seriesH.SeriesCover)
	authed.GET("/series/:id/providers/:providerId/cover", seriesH.ProviderCover)
	authed.PATCH("/series/:id/metadata-source", seriesH.SetMetadataSource)
	authed.GET("/series/:id/chapters/:chapterId/pages/:n", seriesH.ChapterPage)
	authed.PATCH("/chapters/:id/progress", seriesH.SetProgress)
	authed.POST("/series/:id/reading-progress", seriesH.SetReadingProgress)
	authed.GET("/health", seriesH.LibraryHealth)

	// Phase-1 native metadata engine (spec/metadata-engine-phase1): cross-
	// provider search, per-series identify, cover-candidate gallery, and cover
	// pick. metaSvc is built in main.go (it owns the composed provider
	// registry) and shares seriesSvc so a mutating call returns the refreshed
	// SeriesDetailDTO (§16 round-trip).
	metadataH := metadatah.NewHandler(metaSvc, seriesSvc)
	authed.GET("/metadata/search", metadataH.Search)
	authed.POST("/series/:id/metadata/identify", metadataH.Identify)
	authed.GET("/series/:id/metadata/covers", metadataH.Covers)
	authed.POST("/series/:id/cover", metadataH.SetCover)

	// Phase-3 tracker subsystem (spec/trackers-oauth-phase3): per-account
	// connect (OAuth/credential login, status, logout) + per-series bind
	// (search, bind, unbind, refresh). trackerRegistry/trackerConnectSvc/
	// trackerBindSvc are built in main.go over the four native trackers
	// (AniList, MAL, Kitsu, MangaUpdates); a blank client-id/public-URL
	// leaves the affected OAuth path dormant rather than failing startup.
	// trackerSyncSvc is the Phase-4c sync service (spec/trackers-sync-phase4)
	// built over the SAME registry — it serves the owner's manual
	// tracking-sheet edit + the pull-and-converge sync-now action, and is
	// independently injected as the series.ProgressPusher (see main.go) so a
	// reader-marked chapter also fires a background push.
	trackersH := trackersh.NewHandler(client, trackerRegistry, trackerConnectSvc, trackerBindSvc, trackerSyncSvc)
	authed.GET("/trackers", trackersH.List)
	authed.GET("/trackers/:id/auth-url", trackersH.AuthURL)
	authed.POST("/trackers/:id/login/oauth", trackersH.LoginOAuth)
	authed.POST("/trackers/:id/login/credentials", trackersH.LoginCredentials)
	authed.POST("/trackers/:id/logout", trackersH.Logout)
	authed.GET("/trackers/:id/search", trackersH.Search)
	authed.GET("/series/:id/tracking", trackersH.ListBindings)
	authed.POST("/series/:id/tracking", trackersH.CreateBinding)
	authed.DELETE("/series/:id/tracking/:recordId", trackersH.DeleteBinding)
	authed.POST("/series/:id/tracking/:recordId/refresh", trackersH.RefreshBinding)
	authed.POST("/series/:id/tracking/:recordId/update", trackersH.UpdateTrack)
	authed.POST("/series/:id/tracking/sync", trackersH.SyncTracking)

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

	// Web Push registration API. The handler serves the server VAPID public key
	// and upserts/removes a device's subscription (internal/push store); the
	// notifier (internal/notify) fans new-chapter notifications to every stored
	// subscription.
	pushH := pushh.NewHandler(pushSubsSvc, vapidPublicKey)
	authed.GET("/push/vapid-key", pushH.VAPIDKey)
	authed.POST("/push/subscriptions", pushH.Subscribe)
	authed.DELETE("/push/subscriptions", pushH.Unsubscribe)

	// System info — read-only credential-free structural config (storage path,
	// server port, DB host:port/name). The handler needs only the config struct;
	// no service or Ent client is required.
	systemH := systemh.NewHandler(cfg)
	authed.GET("/system", systemH.Get)

	// Tsundoku-owned FlareSolverr settings (QCAT-238): a runtime setting on
	// settingsSvc, NOT read from Suwayomi/the engine. PATCH best-effort mirrors
	// down to the engine host via engineClient.SetFlareSolverr (P2 slice 6: the
	// obsolete Suwayomi settings-proxy this used to mirror through is deleted —
	// the engine host has no readable config, so its GET half was already
	// impossible; SOCKS runtime-push stays deferred to reconcile-on-boot, a
	// later slice).
	flareSolverrH := flaresolverrh.NewHandler(settingsSvc, engineClient)
	authed.GET("/flaresolverr/settings", flareSolverrH.Get)
	authed.PATCH("/flaresolverr/settings", flareSolverrH.Update)

	// Sources & Extensions management (P2 Suwayomi-removal slice 5: repointed
	// onto the engine host). Like the settings proxy, the handler holds the
	// engine client directly and proxies its extension surface; no Tsundoku
	// state is involved. db/cache/http.Get are the durable engine-topology
	// store: an install/update/uninstall or repo change is written through to
	// the HarvestedExtension/HarvestedRepo rows + the shared apk cache
	// immediately (best-effort), so the store never lags a live owner change.
	// apkStore is the SAME cache the boot seed writes and the /internal
	// apk-serving route reads. The extension icon proxy + the per-language
	// source enable/disable toggle are RETIRED: sourceengine has no
	// PageBytes-shaped fetch (the FE renders iconUrl directly) and no
	// server-side "disabled source" concept to proxy.
	extensionsH := extensionsh.NewHandler(engineClient, client, apkStore, http.Get)
	authed.GET("/suwayomi/extensions", extensionsH.List)
	authed.POST("/suwayomi/extensions/refresh", extensionsH.Refresh)
	authed.GET("/suwayomi/extensions/repos", extensionsH.GetRepos)
	authed.PUT("/suwayomi/extensions/repos", extensionsH.SetRepos)
	authed.POST("/suwayomi/extensions/:pkgName/install", extensionsH.Install)
	authed.POST("/suwayomi/extensions/:pkgName/update", extensionsH.Update)
	authed.DELETE("/suwayomi/extensions/:pkgName", extensionsH.Uninstall)
	authed.GET("/suwayomi/extensions/:pkgName/preferences", extensionsH.Preferences)
	authed.PATCH("/suwayomi/extensions/:pkgName/preferences", extensionsH.SetPreference)

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
	// the same Ent client as the rest of the application. P2 Suwayomi-removal
	// (slice 3b): imports + library now share the engine-agnostic
	// internal/ingest.Ingest, targeting engineClient (internal/sourceengine) —
	// neither package imports internal/suwayomi any more (slice 8 deleted the
	// package entirely).
	// Anti-ban de-amplification: the ingest routes its adopt/attach fetch through
	// the shared source-politeness gate (Task B) and the shared chapter cache
	// (Task C2 — the SAME instance the imports coverage paths use, so a
	// coverage→configure→adopt session fetches a source-manga once). imports uses
	// NewServiceWithCaches so its coverage + Search paths are cached too (C1/C2).
	ingestSvc := ingest.NewIngestWithGate(engineClient, client, chapterCache, gate)
	importsSvc := imports.NewServiceWithCaches(
		engineClient, ingestSvc, client, cfg.Storage.Folder, cfg.Engine.SearchTimeout, metricsSvc, chapterCache,
		// Search-cache TTL read per Get from the settings overlay (jobs.search_cache_ttl,
		// hot reload); 0 disables the search cache at runtime.
		func(ctx context.Context) time.Duration { return settingsSvc.SearchCacheTTL(ctx) },
	).WithAutoIdentifier(metaSvc) // fires a detached background rich-metadata pass after Adopt (spec/metadata-engine-phase1 §4)
	importsH := importsh.NewHandler(importsSvc, seriesSvc, trigger, coverCache)
	authed.GET("/sources", importsH.Sources)
	authed.GET("/search", importsH.Search)
	authed.GET("/sources/:sourceId/browse", importsH.Browse)
	authed.GET("/sources/:sourceId/manga/:mangaId/chapters", importsH.InspectChapters)
	authed.GET("/sources/:sourceId/manga/:mangaId/details", importsH.Details)
	authed.GET("/sources/:sourceId/manga/:mangaId/breakdown", importsH.Breakdown)
	authed.GET("/sources/:sourceId/cover", importsH.SourceCover)
	authed.POST("/series", importsH.Adopt)

	// Library-import (on-disk scan + adopt-without-redownload) API. Reuses the
	// SAME ingest/importsSvc/seriesSvc instances constructed above — no double
	// construction — plus the shared trigger, storage root, and SSE hub (the
	// async scan streams scan.start/scan.progress/scan.done over it).
	librarySvc := library.NewService(client, ingestSvc, importsSvc, seriesSvc, trigger, cfg.Storage.Folder, hub).
		WithAutoIdentifier(metaSvc) // fires a detached background rich-metadata pass after Import (spec/metadata-engine-phase1 §4)
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

	// Engine-topology endpoints. apkStore is the SHARED apk byte cache constructed
	// once in main.go (rooted under the engine runtime dir) and also handed to
	// the boot-time topology seed goroutine — construct-once, so the seed writes
	// to and this handler serves from the same bytes.
	//   - The owner-facing GET /api/engine/topology-status readout (IN the OpenAPI
	//     spec) reports how much engine topology Tsundoku has captured, from DB
	//     counts alone (no engine call). It reuses the same authed group as the
	//     rest of the owner API.
	//   - The /internal apk-serving route (NOT in the OpenAPI spec) lets a future
	//     engine-recovery/reconcile pass re-install extensions from Tsundoku's own
	//     APK byte cache even when the upstream repo is offline, via an apkUrl whose
	//     last segment is the collision-free filename "<pkg>-<version>.apk" (the
	//     engine-host loader names the installed file from it); the handler parses
	//     (pkg, version) back out.
	engineH := engineh.NewHandler(apkStore, client)
	authed.GET("/engine/topology-status", engineH.TopologyStatus)
	internalAPI := e.Group("/internal", mw.RequireOwner(authSvc, cfg.Auth.CookieSecure))
	internalAPI.GET("/extensions/apk/:pkg/:file", engineH.ServeAPK)

	// SPA static serving + unknown-route handling (registered last).
	registerStaticSPA(e)
}
