// Package sources holds the thin HTTP handlers for source-level concerns: the
// per-source performance metrics screen and the on-demand anti-bot warm-up
// trigger. Business logic lives in internal/metrics + internal/warmup; the
// handler only calls the service and renders the DTO (bind → service → DTO).
package sources

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/handler/httperr"
	"github.com/technobecet/tsundoku/internal/metrics"
	"github.com/technobecet/tsundoku/internal/warmup"
)

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

// Warmup handles POST /api/sources/warmup. It runs a full WarmAll pass (the
// manual "warm everything now" the owner triggers while testing) and returns the
// number of sources warmed. A Suwayomi failure (e.g. the source list is
// unreachable) is a 502 via the shared upstream mapper.
func (h *Handler) Warmup(c echo.Context) error {
	n, err := h.warmup.WarmAll(c.Request().Context())
	if err != nil {
		return httperr.Upstream(err)
	}
	return c.JSON(http.StatusOK, WarmResultDTO{Warmed: n})
}
