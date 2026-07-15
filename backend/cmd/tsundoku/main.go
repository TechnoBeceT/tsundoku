// Command tsundoku is the Tsundoku backend server.
//
// Startup sequence:
//  1. config.Load — reads env/yaml and validates all required secrets fail-closed.
//  2. database.Open — opens a pgx pool, runs Ent auto-migration with retry.
//  3. chapter.ResetOrphanedChapters — one-time startup sweep that re-queues
//     chapters a crash/restart stranded mid-cycle (downloading → wanted,
//     upgrading → downloaded), before anything can start a new cycle.
//     Non-fatal: a failed sweep is logged and startup continues.
//  4. auth.NewService — builds the HMAC token service from the validated secret.
//  5. sse.NewHub — allocates the SSE subscriber registry.
//  6. owner.NewHandler — assembles the claim/login handler.
//  7. download.New + job.NewRunner — assembles the dispatcher and chapter job runner.
//     The dispatcher's ChapterFetcher is the engine-host client (internal/
//     sourceengine, Suwayomi-removal P2 slice 2) — every OTHER consumer
//     (ingest/imports/refresh/warmup/cover/handlers) still targets Suwayomi
//     and is repointed in a later slice.
//  8. Suwayomi engine, branched on cfg.Suwayomi.IsExternal():
//     - EXTERNAL mode (TSUNDOKU_SUWAYOMI_EXTERNALURL set): no ProcessManager is
//     constructed; the download + refresh tickers start immediately against
//     the external HTTP target. An unreachable server degrades gracefully.
//     - EMBEDDED mode (default): a background goroutine provisions the Suwayomi
//     JAR, starts the process, then starts the tickers. If provisioning or
//     launch fails, the error is logged and the goroutine exits cleanly — the
//     HTTP server keeps serving the API and reconcile; downloads simply will
//     not run until Suwayomi is available.
//  9. server.New — wires middleware + routes, returns a ready Echo instance.
//  10. Graceful shutdown on SIGINT / SIGTERM with a 15-second drain timeout.
package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/config"
	"github.com/technobecet/tsundoku/internal/database"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/enginetopo"
	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/handler/owner"
	"github.com/technobecet/tsundoku/internal/ingest"
	"github.com/technobecet/tsundoku/internal/job"
	"github.com/technobecet/tsundoku/internal/metadata/providers"
	"github.com/technobecet/tsundoku/internal/metadatasvc"
	"github.com/technobecet/tsundoku/internal/metrics"
	"github.com/technobecet/tsundoku/internal/notify"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/push"
	"github.com/technobecet/tsundoku/internal/refresh"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/server"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/sse"
	"github.com/technobecet/tsundoku/internal/suwayomi"
	"github.com/technobecet/tsundoku/internal/tracker/bind"
	"github.com/technobecet/tsundoku/internal/tracker/connect"
	"github.com/technobecet/tsundoku/internal/tracker/kitsu"
	trackerproviders "github.com/technobecet/tsundoku/internal/tracker/providers"
	"github.com/technobecet/tsundoku/internal/tracker/retry"
	"github.com/technobecet/tsundoku/internal/tracker/syncsvc"
	"github.com/technobecet/tsundoku/internal/warmup"
)

// shutdownTimeout is the maximum time allowed for in-flight requests to complete
// after the shutdown signal is received before the process exits forcefully.
const shutdownTimeout = 15 * time.Second

// vapidSubject is the VAPID "sub" claim sent with every Web Push — a contact URI
// identifying this server to push services. Single-owner homelab: a fixed
// project-scoped mailto is sufficient (push services only require a valid
// mailto:/https: URI, not a reachable address).
const vapidSubject = "mailto:tsundoku@localhost"

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("tsundoku: config: %v", err)
	}

	// Cancellable root context — cancelled on SIGINT / SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	entClient, err := database.Open(ctx, cfg.Database)
	if err != nil {
		stop()
		log.Fatalf("tsundoku: database: %v", err)
	}
	defer stop()
	defer func() {
		if err := entClient.Close(); err != nil {
			log.Printf("tsundoku: database close: %v", err)
		}
	}()

	// Startup orphan-recovery sweep: a crash/restart mid-cycle can strand
	// chapters in a process-owned state (downloading/upgrading) that the
	// dispatcher's WantedChapters never selects and SetState can't reach — they
	// would otherwise be stuck forever. Run this exactly once, before any
	// download/refresh ticker starts (both embed and external Suwayomi modes go
	// through startSuwayomiEngine below), so it can never race a live cycle.
	// Non-fatal: a failed sweep is logged and startup continues — the API must
	// keep serving even if this best-effort recovery step fails.
	if result, err := chapter.ResetOrphanedChapters(ctx, entClient); err != nil {
		slog.Error("startup: reset orphaned chapters failed", "error", err)
	} else {
		slog.Info("startup: reset orphaned chapters", "requeued", result.Requeued, "upgrades_reset", result.UpgradesReset)
	}

	authSvc := auth.NewService(cfg.Auth.Secret)
	hub := sse.NewHub()
	ownerH := owner.NewHandler(entClient, authSvc, cfg.Auth.CookieSecure)

	// Runtime-tunable settings overlay: env-config defaults (single boundary)
	// overlaid by the Settings DB table for the allowlisted keys. Threaded into
	// every consumer that reads a tunable at use-time (dispatcher retry policy,
	// job tickers, refresh concurrency, series stale-grace) so an owner's change
	// via the settings API applies on the next cycle without a restart.
	settingsSvc := settings.NewService(entClient, defaultsFromConfig(cfg))

	// Source-performance metrics store (best-effort recorder + reader). The
	// imports search fan-out records per-source timings into it; the warm-up job
	// reads it to target slow sources.
	metricsSvc := metrics.NewService(entClient)

	// Phase-1 native metadata engine (spec/metadata-engine-phase1): the
	// composed registry of the 5 public-read metadata providers (AniList,
	// MangaDex, MangaUpdates, MAL, Kitsu — internal/metadata/providers is the
	// ONE place that depends on every concrete provider package) plus the
	// orchestration service over it (search / identify / cover pick / the
	// background auto-identify pass). MAL is the only credentialed provider
	// (cfg.Metadata.MALClientID, optional — see MetadataConfig's doc comment);
	// the other four carry the engine end-to-end without it.
	metaRegistry := providers.NewRegistry(providers.Config{MALClientID: cfg.Metadata.MALClientID})
	// WithAutoIdentifyGate wires the metadata.auto_identify runtime tunable
	// (settingsSvc is already constructed above) so an owner can pause the
	// background auto-identify pass without a restart, hot-reloadable —
	// mirrors settingsSvc.AutoUpdateTrack's own gate wiring in syncsvc.
	metaSvc := metadatasvc.NewService(entClient, metaRegistry, cfg.Storage.Folder).
		WithAutoIdentifyGate(settingsSvc.MetadataAutoIdentify)

	// Phase-3 tracker subsystem (spec/trackers-oauth-phase3): the composed
	// registry of the four native trackers (AniList, MAL, Kitsu,
	// MangaUpdates — internal/tracker/providers is the ONE place that
	// depends on every concrete tracker package, mirroring
	// internal/metadata/providers) plus the connect (per-ACCOUNT: OAuth/
	// credential login, token storage) and bind (per-SERIES: search, bind,
	// unbind, fetch) services over it. A blank AniList/MAL client-id, or a
	// blank PublicURL, leaves the affected tracker(s)/the whole OAuth path
	// dormant (AuthURL fails closed with tracker.ErrClientIDNotConfigured /
	// connect.ErrPublicURLNotConfigured) rather than a startup failure —
	// the same "blank disables" pattern as SuwayomiConfig.ExternalURL;
	// Kitsu/MangaUpdates need no client-id at all (credential login).
	trackerRegistry := trackerproviders.NewRegistry(trackerproviders.Config{
		AniListClientID: cfg.Tracker.AniListClientID,
		MALClientID:     cfg.Tracker.MALClientID,
		MALClientSecret: cfg.Tracker.MALClientSecret,
		// FlareSolverrGate resolves Kitsu's Cloudflare-clearing config from the
		// Tsundoku-owned settings overlay AT REQUEST TIME (settingsSvc is
		// already constructed above) — never an env var, never read from
		// Suwayomi (QCAT-238). A Settings-screen change hot-reloads on the very
		// next Kitsu request.
		FlareSolverrGate: func(ctx context.Context) kitsu.FlareSolverrConfig {
			return kitsu.FlareSolverrConfig{
				Enabled:     settingsSvc.FlareSolverrEnabled(ctx),
				URL:         settingsSvc.FlareSolverrURL(ctx),
				Timeout:     time.Duration(settingsSvc.FlareSolverrTimeout(ctx)) * time.Second,
				SessionName: settingsSvc.FlareSolverrSessionName(ctx),
				SessionTTL:  time.Duration(settingsSvc.FlareSolverrSessionTTL(ctx)) * time.Minute,
			}
		},
	})
	trackerConnectSvc := connect.NewService(entClient, trackerRegistry, cfg.Tracker.PublicURL)
	trackerBindSvc := bind.NewService(entClient, trackerRegistry, cfg.Storage.Folder)

	// Phase-4c tracker SYNC subsystem (spec/trackers-sync-phase4): push/pull/
	// update over the rule kernel (internal/tracker/sync) + the durable,
	// coalescing retry queue (internal/tracker/retry) a failed push lands in.
	// trackerBindSvc doubles as the SidecarSyncer (it already owns the
	// TrackBinding↔sidecar mirror for Bind/Unbind/FetchTrack — see
	// bind.Service.SyncSidecar's doc comment); settingsSvc doubles as the
	// AutoUpdateTracker (trackers.auto_update_track, hot-reloadable).
	trackerRetryQueue := retry.NewQueue(entClient)
	syncSvc := syncsvc.NewService(entClient, trackerRegistry, trackerRetryQueue, trackerBindSvc, settingsSvc)

	// Source-politeness gate: a per-physical-source circuit-breaker (persisted
	// in SourceCircuitState) + in-memory politeness delay, shared by every
	// background source-access path below (download, refresh, warm-up) so a
	// source Cloudflare starts blocking is never hammered further. Thresholds
	// are the same settingsSvc overlay, resolved at use-time (hot reload).
	gateSvc := sourcegate.NewService(entClient, settingsSvc)

	// Shared chapter-fetch cache: memoizes the raw all-scanlators chapter list per
	// source-manga so the INTERACTIVE coverage→configure→adopt discovery flow stops
	// re-triggering a live source Chapters fetch for the same manga (anti-ban
	// de-amplification). ONE instance is shared across the registerRoutes
	// ingest/imports service so those fetches collapse. Its TTL is read PER-Get from
	// the settings overlay (jobs.chapter_cache_ttl, hot reload); 0 disables it live.
	// The refresh sweep deliberately does NOT route through this cache (it fetches
	// fresh via FetchChaptersUncached), so this TTL can be long without staling-out
	// discovery. P2 Suwayomi-removal (slice 3b): this is now the engine-agnostic
	// internal/ingest.ChapterCache (imports/library no longer talk to Suwayomi).
	chapterCache := ingest.NewChapterCache(func(ctx context.Context) time.Duration {
		return settingsSvc.ChapterCacheTTL(ctx)
	})

	// Build the Suwayomi HTTP client now — the suwayomi-settings/extensions/
	// flaresolverr proxy handlers (and enginetopo) still target Suwayomi
	// directly and are repointed in a later Suwayomi-removal slice. P2 slice 4
	// repointed the cover-fetch chain (series/handler/series cover proxy,
	// cmd/tsundoku.sourceCoverAdapter) and the warm-up job onto engineClient
	// below — they no longer use suwayomiClient.
	httpc := &http.Client{Timeout: cfg.Suwayomi.HTTPTimeout}
	suwayomiClient := suwayomi.NewClient(cfg.Suwayomi, httpc)

	// Build the engine-host client + ChapterFetcher — these are just typed
	// values and do not require the engine host to be running yet. This is
	// the first real use of internal/sourceengine (Suwayomi-removal P2 slice
	// 2): the download dispatcher's fetcher now targets the engine-host
	// instead of Suwayomi.
	engineClient := sourceengine.New(cfg.Engine.URL, httpc)
	engineFetcher := sourceengine.NewFetcher(engineClient)

	// Shared extension-.apk byte cache (rooted under the Suwayomi runtime dir).
	// Constructed ONCE here and handed to BOTH the boot-time engine-topology seed
	// (which caches extension apks into it) and server.New's engine handler (which
	// serves those bytes back for offline recovery) — construct-once, one store.
	apkStore := apkcache.New(filepath.Join(cfg.Suwayomi.RuntimeDir, "apkcache"))

	// Anti-bot session warm-up job: keeps slow (Cloudflare-protected) sources
	// warm with a cheap Popular call so interactive search stays fast. Works in
	// BOTH embedded + external modes — it only needs the engine-host client
	// (P2 slice 4: repointed off suwayomiClient) and the metrics store.
	warmupSvc := warmup.NewService(engineClient, metricsSvc, settingsSvc, gateSvc)

	dispatcher := download.New(entClient, engineFetcher, hub, download.Config{
		Storage: cfg.Storage.Folder,
	}, settingsSvc, gateSvc)
	runner := job.NewRunner(dispatcher, entClient, hub, cfg.Storage.Folder, settingsSvc)

	// Web Push + new-chapter notifier (see buildNotifier). VAPID failure degrades
	// gracefully — the notifier still broadcasts over SSE; only Web Push is off.
	// The returned public key + subscription store are threaded into the push
	// handler (server.New) so a browser can subscribe.
	pushSubsSvc := push.NewService(entClient)
	vapidPublic := buildNotifier(ctx, entClient, hub, settingsSvc, runner)

	// Tracker-push retry worker: independent of the Suwayomi engine (it only
	// ever talks to the native trackers, never Suwayomi), so it starts
	// immediately rather than waiting on startSuwayomiEngine's tickers —
	// dormant-safe when no trackers are connected (RunOnce simply finds zero
	// due rows every pass).
	runner.StartTrackerRetry(ctx, trackerRetryQueue, syncSvc)

	// Discovery sweep service (M5): re-fetches every monitored series' chapter
	// list to find new releases. Suwayomi-removal P2 slice 3a: refresh's ingest
	// is now the engine-agnostic internal/ingest.Ingest, targeting the
	// engine-host client built above — refresh no longer talks to Suwayomi at
	// all. It gets its OWN PRIVATE ChapterCache (not the suwayomi one shared by
	// registerRoutes' ingest/imports wiring): refresh fetches via
	// FetchChaptersUncached (fresh every sweep, so a long interactive-cache TTL
	// can never stale-out discovery) + ingests via AddSeriesWithChapters (never
	// the gated/cached AddSeries), so it never actually reads this cache — a
	// private instance just keeps this slice from touching the shared one.
	// It shares gateSvc with every other background source-access path.
	refreshChapterCache := ingest.NewChapterCache(func(ctx context.Context) time.Duration {
		return settingsSvc.ChapterCacheTTL(ctx)
	})
	refreshSvc := refresh.NewService(
		entClient,
		ingest.NewIngestWithGate(engineClient, entClient, refreshChapterCache, gateSvc),
		hub,
		settingsSvc,
		gateSvc,
	)

	// healthSvc is a stateless series.Service instance used only to supply the
	// UnhealthyCount function to StartRefresh. A second stateless instance is
	// safe — it shares no mutable state with the one constructed by
	// registerRoutes; this follows the M5 precedent for a second
	// suwayomi.NewIngest.
	healthSvc := series.NewServiceWithStaleGrace(entClient, cfg.Storage.Folder, settingsSvc.StaleGraceDays)

	// Wire the metadata engine's "set a library source's own cover" pick
	// (metadatasvc.Service.SetCover, kind=="source"): it needs the engine-host
	// client (P2 slice 4: repointed off suwayomiClient) plus the series
	// domain's own provider-cover resolution, so this can't be attached at
	// metaSvc's own construction site earlier in this function — see
	// sourceCoverAdapter's doc comment for why the adapter itself lives
	// outside internal/metadatasvc.
	metaSvc = metaSvc.WithSourceCoverFetcher(sourceCoverAdapter{series: healthSvc, sw: engineClient})

	// Start the Suwayomi engine. pm is the embedded process manager (nil in
	// external mode) — the shutdown path guards on pm != nil so Stop() is only
	// called when tsundoku owns the process. This also launches the one-shot
	// engine-topology seed (BackfillProviderURLs → SeedExtensions →
	// SeedSourcePreferences → SeedEngineConfig) in a detached background goroutine
	// once the engine is reachable — see startSuwayomiEngine.
	pm := startSuwayomiEngine(ctx, cfg, settingsSvc, runner, refreshSvc, healthSvc.UnhealthyCount, suwayomiClient, engineClient, warmupSvc, entClient, apkStore)

	e := server.New(cfg, entClient, authSvc, hub, ownerH, suwayomiClient, engineClient, settingsSvc, metricsSvc, warmupSvc, gateSvc, chapterCache, metaSvc, trackerRegistry, trackerConnectSvc, trackerBindSvc, syncSvc, pushSubsSvc, vapidPublic, runner.Trigger, apkStore)

	addr := ":" + cfg.Server.Port

	// Start the HTTP server in a background goroutine so we can wait for the
	// shutdown signal on the main goroutine.
	go func() {
		log.Printf("tsundoku: listening on %s", addr)
		if err := e.Start(addr); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("tsundoku: server: %v", err)
		}
	}()

	// Block until a shutdown signal arrives.
	<-ctx.Done()
	log.Println("tsundoku: shutdown signal received — draining requests")

	// Stop the embedded Suwayomi process before draining HTTP. pm is nil in
	// external mode (tsundoku owns no process), so guard the call.
	if pm != nil {
		pm.Stop()
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := e.Shutdown(shutCtx); err != nil {
		log.Printf("tsundoku: graceful shutdown: %v", err)
	}
}

// buildNotifier wires the Web Push sender + new-chapter notifier into the runner
// and returns the server's VAPID public key (for the push handler). EnsureVAPID
// generates the key pair once (persisted); the sender fans notifications to every
// subscription; the notifier pass runs at the end of each download cycle, gated
// by the notifications.enabled tunable. BackfillArm arms every existing series
// once + seeds the watermark to now so a caught-up library never re-announces its
// back-catalogue. Every failure is logged and swallowed — a notifier/push problem
// must never abort startup.
func buildNotifier(ctx context.Context, entClient *ent.Client, hub *sse.Hub, settingsSvc *settings.Service, runner *job.Runner) string {
	pub, priv, err := push.EnsureVAPID(ctx, entClient)
	if err != nil {
		slog.WarnContext(ctx, "push: VAPID key init failed; Web Push disabled", "err", err)
	}
	sender := push.NewSender(entClient, pub, priv, vapidSubject)
	notifySvc := notify.NewService(entClient, hub, sender, settingsSvc)
	runner.SetNotifier(notifySvc)
	if err := notifySvc.BackfillArm(ctx); err != nil {
		slog.WarnContext(ctx, "notify: backfill-arm failed", "err", err)
	}
	return pub
}

// defaultsFromConfig maps the env-resolved *config.Config into the settings
// overlay's Defaults. This is the ONLY bridge between config and settings: the
// settings layer never reads env, it receives these typed defaults, so the
// single env boundary (internal/config) is preserved.
func defaultsFromConfig(cfg *config.Config) settings.Defaults {
	return settings.Defaults{
		DownloadInterval:        cfg.Jobs.DownloadInterval,
		DownloadConcurrency:     cfg.Jobs.DownloadConcurrency,
		RefreshInterval:         cfg.Jobs.RefreshInterval,
		RefreshConcurrency:      cfg.Jobs.RefreshConcurrency,
		MaxRetries:              cfg.Jobs.MaxRetries,
		RetryBackoff:            cfg.Jobs.RetryBackoff,
		StaleGraceDays:          cfg.Health.StaleGraceDays,
		ExtensionCheckInterval:  cfg.Jobs.ExtensionCheckInterval,
		WarmupInterval:          cfg.Jobs.WarmupInterval,
		WarmupSlowThresholdMs:   cfg.Jobs.WarmupSlowThresholdMs,
		SearchCacheTTL:          cfg.Jobs.SearchCacheTTL,
		ChapterCacheTTL:         cfg.Jobs.ChapterCacheTTL,
		SourcesFailureThreshold: cfg.Sources.FailureThreshold,
		SourcesCooldown:         cfg.Sources.Cooldown,
		SourcesMinRequestDelay:  cfg.Sources.MinRequestDelay,
		SuppressSplitParts:      cfg.Jobs.SuppressSplitParts,
		TrackRetryInterval:      cfg.Jobs.TrackRetryInterval,
		AutoUpdateTrack:         cfg.Jobs.AutoUpdateTrack,
		MetadataAutoIdentify:    cfg.Metadata.AutoIdentify,
		// FlareSolverrEnabled..FlareSolverrResponseFallback are deliberately
		// LITERAL, not cfg.*-sourced (QCAT-238): FlareSolverr config is
		// Tsundoku-owned runtime settings, never an env var. These are just the
		// fixed factory defaults an owner overrides via the Settings UI.
		FlareSolverrEnabled:          false,
		FlareSolverrURL:              "",
		FlareSolverrTimeout:          60,
		FlareSolverrSessionName:      "",
		FlareSolverrSessionTTL:       15,
		FlareSolverrResponseFallback: false,
		// NotificationsEnabled has no env var: new-chapter notifications are on by
		// default (the owner disables via the Settings UI).
		NotificationsEnabled: true,
		// EngineSocksEnabled..EngineSocksVersion mirror the FlareSolverr group:
		// fixed factory defaults, no env var — enginetopo.SeedEngineConfig
		// overwrites them from the live engine's own SOCKS settings on its
		// one-shot seed pass, and the owner can further edit via the Settings UI.
		EngineSocksEnabled: false,
		EngineSocksHost:    "",
		EngineSocksPort:    1080,
		EngineSocksVersion: 5,
	}
}

// startSuwayomiEngine starts the download + refresh tickers under the configured
// Suwayomi lifecycle mode and returns the embedded process manager (nil in
// external mode). In EXTERNAL mode (cfg.Suwayomi.IsExternal()) a standalone
// Suwayomi is assumed already running at BaseURL(): no process is owned and the
// tickers start immediately — an unreachable server degrades gracefully (per-
// cycle errors are logged, downloads just don't progress). In EMBEDDED mode the
// Suwayomi JAR is provisioned + launched in a background goroutine so the HTTP
// server stays available during JVM startup; the tickers start once the process
// is ready, and a launch failure is logged without taking the API down.
// The returned *ProcessManager is nil in external mode, so callers must guard
// Stop() with a nil check.
//
// In BOTH modes, once the engine is reachable it also launches the one-shot
// engine-topology reconcile + seed (enginetopo.Reconcile then
// enginetopo.RunSeed) in a detached, non-blocking background goroutine — see
// startEngineTopo. Neither can ever delay the HTTP server or the tickers (it
// is a fire-and-forget goroutine, reachability-gated, panic-safe, and
// idempotent).
func startSuwayomiEngine(
	ctx context.Context,
	cfg *config.Config,
	settingsSvc *settings.Service,
	runner *job.Runner,
	refreshSvc *refresh.Service,
	unhealthyCount func(context.Context) (int, error),
	swClient suwayomi.Client,
	engineClient sourceengine.Client,
	warmupSvc *warmup.Service,
	entClient *ent.Client,
	apkStore *apkcache.Store,
) *suwayomi.ProcessManager {
	startTickers := func() {
		// Log the currently-resolved cadence (the loops re-read it each cycle, so
		// these are the values in force right now, not a fixed schedule).
		slog.Info("tsundoku: starting download + refresh + extension-check + warm-up tickers",
			"download_interval", settingsSvc.DownloadInterval(ctx),
			"refresh_interval", settingsSvc.RefreshInterval(ctx),
			"extension_check_interval", settingsSvc.ExtensionCheckInterval(ctx),
			"warmup_interval", settingsSvc.WarmupInterval(ctx),
		)
		runner.Start(ctx)
		runner.StartRefresh(ctx, refreshSvc, unhealthyCount)
		runner.StartExtensionCheck(ctx, swClient)
		runner.StartWarmup(ctx, warmupSvc)
		startEngineTopo(ctx, engineClient, entClient, apkStore, settingsSvc)
	}

	if cfg.Suwayomi.IsExternal() {
		slog.Info("tsundoku: using external Suwayomi", "url", cfg.Suwayomi.BaseURL())
		startTickers()
		return nil
	}

	pm := suwayomi.NewProcessManager(cfg.Suwayomi)
	go func() {
		slog.Info("tsundoku: starting embedded Suwayomi")
		if err := pm.Start(ctx); err != nil {
			// Suwayomi failed to start (no JVM, bad JAR, provisioning network
			// error, etc.). Log and exit the goroutine — the API keeps serving;
			// downloads won't proceed until the process is available. A context
			// cancellation during startup is expected (ctx.Err()), not alarming.
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				slog.Info("tsundoku: Suwayomi start cancelled", "reason", err)
			} else {
				slog.Error("tsundoku: Suwayomi failed to start — downloads disabled", "err", err)
			}
			return
		}
		slog.Info("tsundoku: embedded Suwayomi ready")
		startTickers()
	}()
	return pm
}

// startEngineTopo launches the one-shot engine-topology boot pass — RECONCILE
// (enginetopo.Reconcile, DB->engine PROVISION) then SEED (enginetopo.RunSeed,
// engine->DB CAPTURE) — in that order, in a single detached, non-blocking
// background goroutine. It is called from startTickers, so it runs at the
// same "engine is now reachable" point the recurring tickers do — after
// pm.Start in embedded mode, immediately in external mode. http.Get is the
// production repo-index/apk fetcher.
//
// ORDER MATTERS: Reconcile runs FIRST because it is what makes a freshly-
// started/wiped/swapped engine-host USABLE — it installs the library's
// required extensions (from the reachable repos), pushes the durable source
// preferences, and pushes Tsundoku's own FlareSolverr/SOCKS config, all read
// from Tsundoku's DB. Only after that does RunSeed's capture passes make
// sense: on a genuinely fresh engine, running RunSeed alone first would
// capture nothing (the engine is empty) and never provision it — Reconcile is
// the recovery step a DEPLOYED update (prod-on-old-engine -> a new,
// empty engine-host image) needs to end up matching the existing library.
// Reconcile is IDEMPOTENT (an in-sync engine gets zero drift-driven
// mutations — see ReconcileResult.InSync), so unlike a migration it is safe
// to run this way on EVERY boot, not just a first/fresh one — this is the
// self-healing recovery model (QCAT-245/250), not a one-time bootstrap.
//
// Both passes are detached onto ONE goroutine (not two) so Reconcile
// deterministically finishes provisioning before RunSeed starts capturing —
// running them concurrently could race a RunSeed capture against an
// in-flight Reconcile install/push. Neither call can delay the HTTP server or
// the tickers, which have already started by the time this goroutine is
// launched.
//
// (QCAT-253, P2 Suwayomi-removal slice 5): targets engineClient
// (internal/sourceengine) now, not the Suwayomi client — the seed's repo/
// extension/preference passes are engine-agnostic capture. The retired
// SeriesProvider.url backfill and engine-config gap-fill seed are gone — see
// enginetopo.RunSeed's doc comment. (P2 slice 7): settingsSvc is threaded
// back in here — RunSeed itself still doesn't need it, but Reconcile does (it
// satisfies enginetopo.ConfigProvider, the FlareSolverr/SOCKS push source).
func startEngineTopo(
	ctx context.Context,
	engineClient sourceengine.Client,
	entClient *ent.Client,
	apkStore *apkcache.Store,
	settingsSvc *settings.Service,
) {
	go func() {
		runEngineTopoReconcile(ctx, engineClient, entClient, apkStore, settingsSvc)
		enginetopo.RunSeed(ctx, enginetopo.SeedDeps{
			Client:  engineClient,
			DB:      entClient,
			Cache:   apkStore,
			HTTPGet: http.Get,
		})
	}()
}

// runEngineTopoReconcile runs ONE enginetopo.Reconcile pass, mirroring
// RunSeed's own safety contract (see its doc comment) since this is the other
// half of the same detached boot goroutine:
//   - REACHABILITY-GATED: probes the engine (Sources) first; an unreachable
//     engine skips the pass entirely (Reconcile itself performs no such probe
//     — it trusts the caller to gate it, exactly like RunSeed gates its own
//     passes) rather than emitting a wall of per-call errors against a dead
//     engine. RunSeed performs its own, separate probe right after, so a
//     transient reachability flap between the two calls only skips whichever
//     half it hit — both retry on the next boot.
//   - PANIC-SAFE: a deferred recover turns any bug in Reconcile (or in this
//     wrapper) into a logged error, never a crashed process.
//   - LOGGED: the ReconcileResult is logged at Info (repos_set,
//     extensions_installed, prefs_applied, config_applied, gaps_count) so an
//     operator can see exactly what a boot provisioned; each individual gap
//     (an isolated per-item failure — see ReconcileResult.Gaps) is ALSO
//     logged at Warn so none is buried inside a count.
func runEngineTopoReconcile(
	ctx context.Context,
	engineClient sourceengine.Client,
	entClient *ent.Client,
	apkStore *apkcache.Store,
	settingsSvc *settings.Service,
) {
	defer func() {
		if r := recover(); r != nil {
			slog.ErrorContext(ctx, "enginetopo: reconcile panicked — recovered", "panic", r)
		}
	}()

	if _, err := engineClient.Sources(ctx); err != nil {
		slog.WarnContext(ctx, "enginetopo: engine unreachable, skipping reconcile (a later boot retries)", "err", err)
		return
	}

	res, err := enginetopo.Reconcile(ctx, engineClient, entClient, apkStore, settingsSvc)
	if err != nil {
		slog.ErrorContext(ctx, "enginetopo: reconcile failed", "err", err)
		return
	}
	slog.InfoContext(ctx, "enginetopo: reconcile complete",
		"in_sync", res.InSync,
		"repos_set", res.ReposSet,
		"extensions_installed", res.ExtensionsInstalled,
		"prefs_applied", res.PrefsApplied,
		"config_applied", res.ConfigApplied,
		"gaps_count", len(res.Gaps),
	)
	for _, gap := range res.Gaps {
		slog.WarnContext(ctx, "enginetopo: reconcile gap", "err", gap)
	}
}
