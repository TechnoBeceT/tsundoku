package series

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// LibraryFractionals handles GET /api/library/fractionals — the library-wide
// Fractionals page. It returns every series with at least one DOWNLOADED
// fractional chapter (regardless of ignore state), each carrying its fractional
// vs removable counts, the whole-series ignore-policy state, and its display
// name/cover, sorted most-actionable first. It DELETES NOTHING — it is the
// read that drives the retroactive bulk-fix surface (mirrors LibraryHealth).
func (h *Handler) LibraryFractionals(c echo.Context) error {
	out, err := h.svc.LibraryFractionals(c.Request().Context())
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, out)
}

// SetIgnoreFractionalForSeries handles PATCH /api/series/:id/ignore-fractional —
// the whole-series ignore-fractional policy toggle: it sets (or clears)
// ignore_fractional on ALL the series' sources in one call, then reconciles the
// series' undownloaded fractionals. It DELETES NOTHING (the already-downloaded
// files are cleaned by the separate, previewed fractional-cleanup POST).
//
// It reuses SetIgnoreFractionalRequest / validateSetIgnoreFractional — the body is
// the same {ignoreFractional: bool} as the per-source toggle (§2 DRY). On success
// it returns 200 with the full SeriesDetailDTO so the caller sees every source's
// new flag without a refetch (§16 round-trip). A missing series yields 404; a
// missing ignoreFractional field yields 400.
func (h *Handler) SetIgnoreFractionalForSeries(c echo.Context) error {
	id, err := validateID(c.Param("id"), "series id")
	if err != nil {
		return err
	}

	var req SetIgnoreFractionalRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if err := validateSetIgnoreFractional(req); err != nil {
		return err
	}

	ctx := c.Request().Context()
	if err := h.svc.SetIgnoreFractionalForSeries(ctx, id, *req.IgnoreFractional); err != nil {
		return mapServiceError(err)
	}

	// Return the updated detail so the caller sees every source's new flag (§16).
	updated, err := h.svc.GetSeries(ctx, id)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, updated)
}
