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
//  7. download.New + job.NewRunner — assembles the dispatcher and chapter job runner
//     with the real Suwayomi ChapterFetcher (M2).
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
	"syscall"
	"time"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/config"
	"github.com/technobecet/tsundoku/internal/database"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/handler/owner"
	"github.com/technobecet/tsundoku/internal/job"
	"github.com/technobecet/tsundoku/internal/metrics"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/refresh"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/server"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/sse"
	"github.com/technobecet/tsundoku/internal/suwayomi"
	"github.com/technobecet/tsundoku/internal/warmup"
)

// shutdownTimeout is the maximum time allowed for in-flight requests to complete
// after the shutdown signal is received before the process exits forcefully.
const shutdownTimeout = 15 * time.Second

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

	// Source-politeness gate: a per-physical-source circuit-breaker (persisted
	// in SourceCircuitState) + in-memory politeness delay, shared by every
	// background source-access path below (download, refresh, warm-up) so a
	// source Cloudflare starts blocking is never hammered further. Thresholds
	// are the same settingsSvc overlay, resolved at use-time (hot reload).
	gateSvc := sourcegate.NewService(entClient, settingsSvc)

	// Shared chapter-fetch cache: memoizes the raw all-scanlators chapter list per
	// source-manga so the INTERACTIVE coverage→configure→adopt discovery flow stops
	// re-triggering a live source FetchChapters for the same manga (anti-ban
	// de-amplification). ONE instance is shared across the registerRoutes
	// ingest/imports service so those fetches collapse. Its TTL is read PER-Get from
	// the settings overlay (jobs.chapter_cache_ttl, hot reload); 0 disables it live.
	// The refresh sweep deliberately does NOT route through this cache (it fetches
	// fresh via FetchChaptersUncached), so this TTL can be long without staling-out
	// discovery.
	chapterCache := suwayomi.NewChapterCache(func(ctx context.Context) time.Duration {
		return settingsSvc.ChapterCacheTTL(ctx)
	})

	// Build the Suwayomi HTTP client and real ChapterFetcher now — these are
	// just typed values and do not require Suwayomi to be running yet. They are
	// passed to download.New immediately so the dispatcher is fully wired.
	httpc := &http.Client{Timeout: cfg.Suwayomi.HTTPTimeout}
	suwayomiClient := suwayomi.NewClient(cfg.Suwayomi, httpc)
	suwayomiFetcher := suwayomi.NewFetcher(suwayomiClient)

	// Anti-bot session warm-up job: keeps slow (Cloudflare-protected) sources
	// warm with a cheap Browse call so interactive search stays fast. Works in
	// BOTH embedded + external modes — it only needs the Suwayomi client (which
	// targets BaseURL() either way) and the metrics store.
	warmupSvc := warmup.NewService(suwayomiClient, metricsSvc, settingsSvc, gateSvc)

	dispatcher := download.New(entClient, suwayomiFetcher, hub, download.Config{
		Storage: cfg.Storage.Folder,
	}, settingsSvc, gateSvc)
	runner := job.NewRunner(dispatcher, entClient, hub, cfg.Storage.Folder, settingsSvc)

	// Discovery sweep service (M5): re-fetches every monitored series' chapter
	// list to find new releases. Its own ingest shares the same Ent client +
	// Suwayomi client; NewIngest is a stateless constructor so a second instance
	// alongside the one built in registerRoutes is fine.
	refreshSvc := refresh.NewService(
		entClient,
		// Refresh's ingest shares the gate but NOT the chapter cache in effect:
		// refresh fetches via FetchChaptersUncached (fresh every sweep, so a long
		// interactive-cache TTL can never stale-out discovery) + ingests via
		// AddSeriesWithChapters (never the gated/cached AddSeries). It applies its
		// OWN gate around the single per-source-manga pre-fetch, so no double-Wait.
		// The cache is still passed for the constructor's shape; refresh just
		// doesn't route its pre-fetch through it.
		suwayomi.NewIngestWithGate(suwayomiClient, entClient, chapterCache, gateSvc),
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

	// Start the Suwayomi engine. pm is the embedded process manager (nil in
	// external mode) — the shutdown path guards on pm != nil so Stop() is only
	// called when tsundoku owns the process.
	pm := startSuwayomiEngine(ctx, cfg, settingsSvc, runner, refreshSvc, healthSvc.UnhealthyCount, suwayomiClient, warmupSvc)

	e := server.New(cfg, entClient, authSvc, hub, ownerH, suwayomiClient, settingsSvc, metricsSvc, warmupSvc, gateSvc, chapterCache, runner.Trigger)

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
func startSuwayomiEngine(
	ctx context.Context,
	cfg *config.Config,
	settingsSvc *settings.Service,
	runner *job.Runner,
	refreshSvc *refresh.Service,
	unhealthyCount func(context.Context) (int, error),
	swClient suwayomi.Client,
	warmupSvc *warmup.Service,
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
