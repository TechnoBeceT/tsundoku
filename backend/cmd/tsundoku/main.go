// Command tsundoku is the Tsundoku backend server.
//
// Startup sequence:
//  1. config.Load — reads env/yaml and validates all required secrets fail-closed.
//  2. database.Open — opens a pgx pool, runs Ent auto-migration with retry.
//  3. auth.NewService — builds the HMAC token service from the validated secret.
//  4. sse.NewHub — allocates the SSE subscriber registry.
//  5. owner.NewHandler — assembles the claim/login handler.
//  6. job.NewRunner — assembles the chapter job runner (download/upgrade/reconcile).
//     M1 status: the reconcile trigger is wired live. The download/upgrade ticker
//     (runner.Start) is NOT started yet — it requires the Suwayomi ChapterFetcher
//     which ships in M2. Once M2 supplies the fetcher, construct download.New with
//     it and call runner.Start(ctx, cfg.Jobs.DownloadInterval).
//  7. server.New — wires middleware + routes, returns a ready Echo instance.
//  8. Graceful shutdown on SIGINT / SIGTERM with a 15-second drain timeout.
package main

import (
	"context"
	"errors"
	"log"
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

	client, err := database.Open(ctx, cfg.Database)
	if err != nil {
		stop()
		log.Fatalf("tsundoku: database: %v", err)
	}
	defer stop()
	defer func() {
		if err := client.Close(); err != nil {
			log.Printf("tsundoku: database close: %v", err)
		}
	}()

	authSvc := auth.NewService(cfg.Auth.Secret)
	hub := sse.NewHub()
	ownerH := owner.NewHandler(client, authSvc)

	// M1: The chapter job runner is assembled here.
	//
	// Reconcile is available immediately — it requires no fetcher and can be
	// triggered on-demand (HTTP surface in M3/M5).
	//
	// The download/upgrade ticker (runner.Start) is NOT started in M1 because
	// the production Suwayomi ChapterFetcher does not yet exist (it lands in M2).
	// Once M2 ships the fetcher, replace the nil stub below with:
	//
	//   suwayomiFetcher := suwayomi.NewFetcher(cfg.Suwayomi)
	//   dispatcher := download.New(client, suwayomiFetcher, hub, download.Config{
	//       PerProviderConcurrency: 4,
	//       MaxRetries:             5,
	//       Storage:                cfg.Storage.Folder,
	//   })
	//   runner.Start(ctx, 15*time.Minute)
	//
	// M2 seam: the zero-value dispatcher below is never used for download/upgrade
	// in M1 production (Start is not called). It satisfies the type system.
	dispatcher := download.New(client, nil, hub, download.Config{ //nolint:staticcheck // nil fetcher intentional: M2 seam, Start not called in M1
		PerProviderConcurrency: 4,
		MaxRetries:             5,
		Storage:                cfg.Storage.Folder,
	})
	runner := job.NewRunner(dispatcher, client, hub, cfg.Storage.Folder)
	_ = runner // reconcile available; used by future HTTP handler layer (M3/M5)
	log.Println("tsundoku: job runner ready (reconcile live; download/upgrade ticker awaits M2 Suwayomi fetcher)")

	e := server.New(cfg, client, authSvc, hub, ownerH)

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

	shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := e.Shutdown(shutCtx); err != nil {
		log.Printf("tsundoku: graceful shutdown: %v", err)
	}
}
