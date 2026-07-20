package downloads

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	downloadssvc "github.com/technobecet/tsundoku/internal/downloads"
)

// Handler holds the dependencies for the download-activity HTTP handlers. All
// business logic lives in the downloads.Service; the handler is thin.
type Handler struct {
	svc     *downloadssvc.Service
	trigger func()
}

// NewHandler constructs a Handler bound to a downloads.Service and an
// auto-converge trigger (identical wiring to handler/series — bound to
// runner.Trigger in main.go). RetryChapter and RetryAll call it after a
// SUCCESSFUL reset so an owner retry starts an immediate download cycle
// instead of waiting for the next timer tick; Run calls it directly for the
// explicit "Download now" action.
func NewHandler(svc *downloadssvc.Service, trigger func()) *Handler {
	return &Handler{svc: svc, trigger: trigger}
}

// List handles GET /api/downloads.
//
// It parses the REQUIRED ?state CSV (each token a legal chapter state; empty or
// unknown → 400), the optional ?limit/?offset pagination (non-negative; limit
// defaults to 50, capped at 200), and the optional ?q series-title filter, then
// returns a DownloadListDTO: the total matching count plus the requested page of
// enriched chapters.
func (h *Handler) List(c echo.Context) error {
	states, err := parseStates(c.QueryParam("state"))
	if err != nil {
		return err
	}
	limit, offset, err := validatePagination(c.QueryParam("limit"), c.QueryParam("offset"))
	if err != nil {
		return err
	}
	includeSourceFailures, err := parseOptionalBool(c.QueryParam("include_source_failures"), "include_source_failures")
	if err != nil {
		return err
	}

	out, err := h.svc.List(c.Request().Context(), downloadssvc.ListFilter{
		States:                states,
		Limit:                 limit,
		Offset:                offset,
		Query:                 c.QueryParam("q"),
		IncludeSourceFailures: includeSourceFailures,
	})
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, out)
}

// Summary handles GET /api/downloads/summary — the global nav-badge counts
// (downloading / queued / failed) for a persistent badge. No params; one grouped
// aggregate. Returns 200 with a DownloadSummaryDTO.
func (h *Handler) Summary(c echo.Context) error {
	out, err := h.svc.Summary(c.Request().Context())
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, out)
}

// RetryChapter handles POST /api/chapters/:id/retry.
//
// It parses the :id path param as a UUID (malformed → 400) and resets that
// chapter back to wanted (clearing the failure bookkeeping). Returns 204 on
// success; a missing chapter yields 404; a non-retryable state yields 409. On
// success ONLY (never on 404/409) it calls the injected trigger so the reset
// chapter downloads immediately instead of waiting for the next cycle.
func (h *Handler) RetryChapter(c echo.Context) error {
	id, err := validateID(c.Param("id"), "chapter id")
	if err != nil {
		return err
	}
	if err := h.svc.RetryChapter(c.Request().Context(), id); err != nil {
		return mapServiceError(err)
	}
	h.trigger()
	return c.NoContent(http.StatusNoContent)
}

// RetryAll handles POST /api/downloads/retry-all.
//
// It parses the optional ?state CSV (retryable states only — a non-retryable
// state → 400; absent defaults to failed + permanently_failed) and the optional
// ?series_id scope (malformed → 400), bulk-resets every matching chapter to
// wanted, and returns 200 with {"retried": N} — the number of chapters reset.
// On success ONLY it calls the injected trigger so the reset chapters download
// immediately instead of waiting for the next cycle.
func (h *Handler) RetryAll(c echo.Context) error {
	states, err := parseRetryStates(c.QueryParam("state"))
	if err != nil {
		return err
	}
	seriesID, err := parseOptionalID(c.QueryParam("series_id"), "series_id")
	if err != nil {
		return err
	}
	includeSourceFailures, err := parseOptionalBool(c.QueryParam("include_source_failures"), "include_source_failures")
	if err != nil {
		return err
	}

	n, err := h.svc.RetryAll(c.Request().Context(), downloadssvc.RetryAllFilter{
		States:                states,
		SeriesID:              seriesID,
		IncludeSourceFailures: includeSourceFailures,
	})
	if err != nil {
		return mapServiceError(err)
	}
	h.trigger()
	return c.JSON(http.StatusOK, downloadssvc.RetryAllResultDTO{Retried: n})
}

// Run handles POST /api/downloads/run — the explicit "Download now" action.
//
// It calls the injected trigger and returns 202 Accepted immediately (mirrors
// POST /api/sources/warmup's fire-and-forget shape). runner.Trigger is already
// non-blocking and cap-1 coalescing, so a double-click is a no-op; the
// triggered cycle runs downloads AND upgrades exactly as the timer-driven cycle
// does — it does NOT bypass the per-source circuit breaker, so a
// cooled-down/blocked source's chapters still wait out their backoff.
func (h *Handler) Run(c echo.Context) error {
	h.trigger()
	return c.JSON(http.StatusAccepted, RunResultDTO{Started: true})
}

// mapServiceError translates a downloads.Service sentinel error into the matching
// HTTP status, leaving any unexpected error to fall through to the central
// middleware as a 500. ErrChapterNotFound → 404; ErrNotRetryable → 409.
func mapServiceError(err error) error {
	switch {
	case errors.Is(err, downloadssvc.ErrChapterNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "chapter not found")
	case errors.Is(err, downloadssvc.ErrNotRetryable):
		return echo.NewHTTPError(http.StatusConflict, "chapter is not in a retryable state")
	default:
		return err
	}
}
