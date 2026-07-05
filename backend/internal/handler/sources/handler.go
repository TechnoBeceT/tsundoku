// Package sources holds the thin HTTP handlers for source-level concerns: the
// per-source performance metrics screen and the on-demand anti-bot warm-up
// trigger. Business logic lives in internal/metrics + internal/warmup; the
// handler only calls the service and renders the DTO (bind → service → DTO).
package sources

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/metrics"
	"github.com/technobecet/tsundoku/internal/warmup"
)

// warmAllTimeout bounds the detached background WarmAll a Warmup request kicks
// off. A full warm pass warms sources serially and — over ~40 sources with
// several slow anti-bot ones (each a 20-85s cold challenge solve) — legitimately
// runs for minutes, so the cap is generous. It exists only to guarantee the
// background goroutine + its context can never leak forever.
const warmAllTimeout = 10 * time.Minute

// SlowThreshold supplies the EWMA-latency threshold (ms) used to derive each
// metric row's isSlow flag. *settings.Service satisfies it; it is read at
// use-time so a re-tuned threshold reflects immediately without a migration.
type SlowThreshold interface {
	WarmupSlowThresholdMs(ctx context.Context) int
}

// Handler serves GET /api/sources/metrics and POST /api/sources/warmup.
type Handler struct {
	metrics *metrics.Service
	warmup  *warmup.Service
	slow    SlowThreshold
}

// NewHandler constructs a Handler over the metrics reader, the warm-up service,
// and the slow-threshold provider.
func NewHandler(metricsSvc *metrics.Service, warmupSvc *warmup.Service, slow SlowThreshold) *Handler {
	return &Handler{metrics: metricsSvc, warmup: warmupSvc, slow: slow}
}

// Metrics handles GET /api/sources/metrics. It returns every source's rolling
// performance snapshot (slowest first), each with a derived isSlow flag computed
// against the current threshold. A DB read error falls through to the central
// middleware as a 500.
func (h *Handler) Metrics(c echo.Context) error {
	ctx := c.Request().Context()
	rows, err := h.metrics.List(ctx)
	if err != nil {
		return err
	}
	threshold := h.slow.WarmupSlowThresholdMs(ctx)
	return c.JSON(http.StatusOK, toSourceMetricDTOs(rows, threshold))
}

// Warmup handles POST /api/sources/warmup. The full WarmAll pass warms sources
// SERIALLY and — with slow anti-bot sources — runs for MINUTES, far longer than
// any CDN/proxy request budget (it would 524 behind the edge), so it CANNOT run
// inline. Warmup fires WarmAll on a detached, time-bounded background goroutine
// and returns 202 Accepted immediately; the owner watches per-source progress via
// GET /api/sources/metrics (each source's lastWarmedAt / lastError updates as the
// pass proceeds). A background failure is logged, never returned — there is no
// request left to fail once the 202 is sent.
func (h *Handler) Warmup(c echo.Context) error {
	// Detach from the request context (which ends when we return 202) but keep a
	// hard timeout so the goroutine + context can never leak.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request().Context()), warmAllTimeout)
	go func() {
		defer cancel()
		if _, err := h.warmup.WarmAll(ctx); err != nil {
			slog.WarnContext(ctx, "sources: background warm-up pass failed", "err", err)
		}
	}()
	return c.JSON(http.StatusAccepted, WarmStartedDTO{Started: true})
}
