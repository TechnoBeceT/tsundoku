// Command tsundoku is the Tsundoku backend server.
//
// Startup sequence:
//  1. config.Load — reads env/yaml and validates all required secrets fail-closed.
//  2. database.Open — opens a pgx pool, runs Ent auto-migration with retry.
//  3. auth.NewService — builds the HMAC token service from the validated secret.
//  4. sse.NewHub — allocates the SSE subscriber registry.
//  5. owner.NewHandler — assembles the claim/login handler.
//  6. download.New + job.NewRunner — assembles the dispatcher and chapter job runner
//     with the real Suwayomi ChapterFetcher (M2).
//  7. Background goroutine: provisions the Suwayomi JAR, starts the Suwayomi
//     process, then calls runner.Start. If provisioning or launch fails, the
//     error is logged and the goroutine exits cleanly — the HTTP server continues
//     serving the API and reconcile; downloads simply will not run until Suwayomi
//     is available.
//  8. server.New — wires middleware + routes, returns a ready Echo instance.
//  9. Graceful shutdown on SIGINT / SIGTERM with a 15-second drain timeout.
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

	"github.com/technobecet/tsundoku/internal/config"
	"github.com/technobecet/tsundoku/internal/database"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/handler/owner"
	"github.com/technobecet/tsundoku/internal/job"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/server"
	"github.com/technobecet/tsundoku/internal/sse"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// shutdownTimeout is the maximum time allowed for in-flight requests to complete
// after the shutdown signal is received before the process exits forcefully.
const shutdownTimeout = 15 * time.Second

// suwayomiHTTPTimeout is the deadline applied to individual page-download requests
// made by the Suwayomi HTTP client. Page images can be several megabytes; 60 s
// gives slow hosts time to respond without blocking the dispatcher indefinitely.
const suwayomiHTTPTimeout = 60 * time.Second

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

	authSvc := auth.NewService(cfg.Auth.Secret)
	hub := sse.NewHub()
	ownerH := owner.NewHandler(entClient, authSvc)

	// Build the Suwayomi HTTP client and real ChapterFetcher now — these are
	// just typed values and do not require Suwayomi to be running yet. They are
	// passed to download.New immediately so the dispatcher is fully wired.
	httpc := &http.Client{Timeout: suwayomiHTTPTimeout}
	suwayomiClient := suwayomi.NewClient(cfg.Suwayomi, httpc)
	suwayomiFetcher := suwayomi.NewFetcher(suwayomiClient)

	dispatcher := download.New(entClient, suwayomiFetcher, hub, download.Config{
		PerProviderConcurrency: 4,
		MaxRetries:             5,
		Storage:                cfg.Storage.Folder,
	})
	runner := job.NewRunner(dispatcher, entClient, hub, cfg.Storage.Folder)

	// pm is the embedded Suwayomi process manager. It is initialised here so
	// that the shutdown path can call pm.Stop() unconditionally — Stop is
	// idempotent and a no-op when the process was never started.
	pm := suwayomi.NewProcessManager(cfg.Suwayomi)

	// Launch Suwayomi and start the download ticker in a background goroutine so
	// the HTTP server (and reconcile) become available immediately, even when the
	// JVM startup takes minutes or when Suwayomi is unavailable entirely.
	go func() {
		slog.Info("tsundoku: starting embedded Suwayomi")
		if err := pm.Start(ctx); err != nil {
			// Suwayomi failed to start (no JVM, bad JAR, network error during
			// provisioning, etc.). Log clearly and exit the goroutine — the API
			// server keeps running; downloads will not proceed until the process
			// is available. On context cancellation during startup the error is
			// ctx.Err() which is not alarming.
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				slog.Info("tsundoku: Suwayomi start cancelled", "reason", err)
			} else {
				slog.Error("tsundoku: Suwayomi failed to start — downloads disabled", "err", err)
			}
			return
		}
		slog.Info("tsundoku: Suwayomi ready — starting download ticker", "interval", cfg.Jobs.DownloadInterval)
		runner.Start(ctx, cfg.Jobs.DownloadInterval)
	}()

	e := server.New(cfg, entClient, authSvc, hub, ownerH, suwayomiClient, runner.Trigger)

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

	// Stop the Suwayomi process before draining HTTP — idempotent if it never started.
	pm.Stop()

	shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := e.Shutdown(shutCtx); err != nil {
		log.Printf("tsundoku: graceful shutdown: %v", err)
	}
}
