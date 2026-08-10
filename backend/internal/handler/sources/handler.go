// Package sources holds the thin HTTP handlers for source-level concerns: the
// per-source performance metrics screen and the on-demand anti-bot warm-up
// trigger. Business logic lives in internal/metrics + internal/warmup; the
// handler only calls the service and renders the DTO (bind → service → DTO).
package sources

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/handler/httperr"
	"github.com/technobecet/tsundoku/internal/metrics"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/warmup"
)

// errSourceNotFound is returned by resolveSource when a sourceId is not in the
// engine's loaded source registry — mapped to a 404 by ResetBreaker.
var errSourceNotFound = errors.New("source not found")

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

// SourceLister resolves the engine's loaded source registry — used to map a
// numeric source id to the display NAME that keys the circuit-breaker (the reset
// action) and that matches disk-origin provider rows (the dependent-series view).
// The full sourceengine.Client satisfies it.
type SourceLister interface {
	Sources(ctx context.Context) ([]sourceengine.Source, error)
}

// SeriesBySource resolves the series that carry a given source, each with its
// alternative-provider count and take-over source — the read model behind the
// per-source pause impact view (QCAT-513). *series.Service satisfies it; the
// interface keeps the handler dependency narrow (bind → resolve name → service →
// DTO).
type SeriesBySource interface {
	SeriesForSource(ctx context.Context, sourceID int64, sourceName string) ([]series.SourceSeriesDTO, error)
}

// Handler serves the source-level endpoints: GET /api/sources/metrics,
// POST /api/sources/warmup, POST /api/sources/:sourceId/reset-breaker, and
// GET /api/sources/:sourceId/series.
type Handler struct {
	metrics *metrics.Service
	warmup  *warmup.Service
	slow    SlowThreshold
	gate    *sourcegate.Service
	engine  SourceLister
	series  SeriesBySource
}

// NewHandler constructs a Handler over the metrics reader, the warm-up service,
// the slow-threshold provider, the source-politeness gate (for reading + resetting
// per-source breaker state), the source lister (id→name resolution), and the
// dependent-series read model.
func NewHandler(
	metricsSvc *metrics.Service,
	warmupSvc *warmup.Service,
	slow SlowThreshold,
	gate *sourcegate.Service,
	engine SourceLister,
	seriesBySource SeriesBySource,
) *Handler {
	return &Handler{metrics: metricsSvc, warmup: warmupSvc, slow: slow, gate: gate, engine: engine, series: seriesBySource}
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
	// One batch read of every source's breaker state (no per-source lookup —
	// joined in-memory by source name below), so the query count is constant
	// regardless of how many sources exist.
	breakers, err := h.gate.Snapshot(ctx)
	if err != nil {
		return err
	}
	threshold := h.slow.WarmupSlowThresholdMs(ctx)
	return c.JSON(http.StatusOK, toSourceMetricDTOs(rows, threshold, breakers, time.Now()))
}

// ResetBreaker handles POST /api/sources/:sourceId/reset-breaker — the owner
// "reset source" action. It resolves the numeric source id to its display NAME
// (the breaker's key — see sourcegate) against the live source registry, clears
// that one source's tripped breaker, and returns the refreshed breaker state
// (§16 round-trip; Breaker is null once cleared). A non-numeric id is 400; an id
// absent from the loaded source set is 404; an engine failure is 502; a DB error
// falls through to the central middleware as a 500.
func (h *Handler) ResetBreaker(c echo.Context) error {
	ctx := c.Request().Context()

	sourceID, err := strconv.ParseInt(strings.TrimSpace(c.Param("sourceId")), 10, 64)
	if err != nil {
		return httperr.BadRequest("invalid source id")
	}

	src, err := h.resolveSource(ctx, sourceID)
	if errors.Is(err, errSourceNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "source not found")
	}
	if err != nil {
		return httperr.Upstream(err)
	}

	key := strings.TrimSpace(src.Name)
	if err := h.gate.Reset(ctx, key); err != nil {
		return err
	}

	// Re-read the breaker state so the response reflects the post-reset truth
	// (§16): after a clean reset the row is gone, so Breaker is null.
	breakers, err := h.gate.Snapshot(ctx)
	if err != nil {
		return err
	}
	dto := BreakerResetDTO{SourceID: strconv.FormatInt(src.ID, 10), SourceName: src.Name}
	if b, ok := breakers[key]; ok {
		bd := toBreakerDTO(b, time.Now())
		dto.Breaker = &bd
	}
	return c.JSON(http.StatusOK, dto)
}

// resolveSource maps a numeric source id to its loaded Source (Name/Lang), or
// errSourceNotFound when it is absent from the engine's registry. An engine
// failure is returned verbatim so the caller can map it to a 502 (mirrors
// imports.resolveSource: miss → 404, upstream → 502).
func (h *Handler) resolveSource(ctx context.Context, sourceID int64) (sourceengine.Source, error) {
	all, err := h.engine.Sources(ctx)
	if err != nil {
		return sourceengine.Source{}, err
	}
	for _, src := range all {
		if src.ID == sourceID {
			return src, nil
		}
	}
	return sourceengine.Source{}, errSourceNotFound
}

// SeriesForSource handles GET /api/sources/:sourceId/series — the read-only
// "what depends on this source" view (QCAT-513). It resolves the numeric source
// id to its display NAME against the live engine registry (needed to match
// disk-origin provider rows, whose provider IS that name) and returns every
// series carrying the source, each flagged whether it goes dark on pause and
// which provider would take over.
//
// A non-numeric id is 400. An id ABSENT from the loaded source set answers 200
// with an EMPTY list rather than a 404 (the plan's rule): the display name cannot
// be resolved for an uninstalled/unknown source, so no disk-origin rows can be
// matched and "nothing depends on it" is the honest answer. A genuine engine
// failure is a 502; a DB error falls through to the central middleware as a 500.
func (h *Handler) SeriesForSource(c echo.Context) error {
	ctx := c.Request().Context()

	sourceID, err := strconv.ParseInt(strings.TrimSpace(c.Param("sourceId")), 10, 64)
	if err != nil {
		return httperr.BadRequest("invalid source id")
	}

	src, err := h.resolveSource(ctx, sourceID)
	if errors.Is(err, errSourceNotFound) {
		return c.JSON(http.StatusOK, []series.SourceSeriesDTO{})
	}
	if err != nil {
		return httperr.Upstream(err)
	}

	rows, err := h.series.SeriesForSource(ctx, src.ID, src.Name)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, rows)
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
