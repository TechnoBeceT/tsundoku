// Package server provides Echo HTTP server construction and route wiring for
// the Tsundoku backend. server.New is the single assembly point: it accepts all
// runtime dependencies, applies middleware in the correct order, and registers
// every route group before returning a ready-to-start Echo instance.
package server

import (
	"github.com/labstack/echo/v4"
	echomiddleware "github.com/labstack/echo/v4/middleware"
	"github.com/technobecet/tsundoku/internal/config"
	entpkg "github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/handler/owner"
	mw "github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/sse"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// New constructs and returns a configured Echo instance with all Tsundoku
// routes registered and middleware applied in the correct order.
//
// Middleware order (outer → inner):
//  1. RequestID — attaches/propagates X-Request-Id before anything else so that
//     all downstream log lines and error responses carry the correlation ID.
//  2. Recover — converts panics into 500 responses handled by ErrorHandler.
//  3. Gzip — compresses responses where the client advertises support.
//  4. Logger — logs after the response is committed so the status code is known.
//
// The central ErrorHandler (mw.ErrorHandler) is wired as Echo's HTTPErrorHandler
// so that every returned error — from handlers, middleware, or Recover — is
// rendered as a JSON ErrorResponse matching the OpenAPI contract.
//
// suwayomiClient is the typed Suwayomi interface used by the imports handler and
// the ingest service. It is constructed in main.go before server.New is called.
func New(
	cfg *config.Config,
	client *entpkg.Client,
	authSvc *auth.Service,
	hub *sse.Hub,
	ownerH *owner.Handler,
	suwayomiClient suwayomi.Client,
) *echo.Echo {
	e := echo.New()
	e.HideBanner = true

	// Central error handler: renders all errors as JSON {"message":...}.
	// Must be set before middleware is applied so panics surfaced by Recover
	// are also routed through it.
	e.HTTPErrorHandler = mw.ErrorHandler

	// Middleware applied to every request in outer-to-inner order.
	e.Use(mw.RequestID())
	e.Use(echomiddleware.Recover())
	// Gzip is skipped for SSE routes: gzip buffers the response writer and
	// breaks event-by-event flushing that SSE requires. Text/event-stream
	// responses are tiny and already newline-framed; compression adds no value.
	e.Use(echomiddleware.GzipWithConfig(echomiddleware.GzipConfig{
		Skipper: func(c echo.Context) bool {
			return c.Request().URL.Path == "/api/progress"
		},
	}))
	e.Use(echomiddleware.Logger())

	registerRoutes(e, cfg, client, authSvc, hub, ownerH, suwayomiClient)
	return e
}
