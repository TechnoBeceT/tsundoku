package series

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	seriessvc "github.com/technobecet/tsundoku/internal/series"
)

// Handler holds the dependencies for the library (series) HTTP handlers.
// All business logic lives in the series.Service; the handler is thin.
type Handler struct {
	svc     *seriessvc.Service
	trigger func()
}

// NewHandler constructs a Handler bound to a series.Service and an auto-converge
// trigger (called after a successful provider re-rank to re-evaluate upgrades
// immediately — M5; other routes do not use it).
func NewHandler(svc *seriessvc.Service, trigger func()) *Handler {
	return &Handler{svc: svc, trigger: trigger}
}

// List handles GET /api/series.
//
// It parses the optional ?category filter (must be a legal enum value or empty)
// and the optional ?limit/?offset pagination (non-negative; limit defaults to
// 50, capped at 200), then returns a title-ASC page of SeriesSummaryDTO. An
// invalid category or pagination value yields 400.
func (h *Handler) List(c echo.Context) error {
	category, err := validateCategoryFilter(c.QueryParam("category"))
	if err != nil {
		return err
	}
	limit, offset, err := validatePagination(c.QueryParam("limit"), c.QueryParam("offset"))
	if err != nil {
		return err
	}

	out, err := h.svc.ListSeries(c.Request().Context(), seriessvc.ListFilter{
		Category: category,
		Limit:    limit,
		Offset:   offset,
	})
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, out)
}

// Detail handles GET /api/series/:id.
//
// It parses the :id path param as a UUID (malformed → 400) and returns the
// full SeriesDetailDTO for that series. A missing series yields 404.
func (h *Handler) Detail(c echo.Context) error {
	id, err := validateID(c.Param("id"), "series id")
	if err != nil {
		return err
	}

	out, err := h.svc.GetSeries(c.Request().Context(), id)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, out)
}

// SetCategory handles PATCH /api/series/:id/category.
//
// It parses the :id path param and the {category} body, validates the category
// is a legal enum value (else 400), then recategorizes the series via the
// service (which moves the on-disk folder before updating the DB, so DB and disk
// never drift). On success it returns 200 with the UPDATED SeriesSummaryDTO so
// the caller sees the new state without a refetch (§16 full round-trip). A
// missing series yields 404; an invalid category yields 400.
func (h *Handler) SetCategory(c echo.Context) error {
	id, err := validateID(c.Param("id"), "series id")
	if err != nil {
		return err
	}

	var req SetCategoryRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if err := validateSetCategory(req); err != nil {
		return err
	}

	ctx := c.Request().Context()
	if err := h.svc.SetCategory(ctx, id, req.Category); err != nil {
		return mapServiceError(err)
	}

	// Return the updated summary so the response confirms the new category.
	updated, err := h.svc.GetSeries(ctx, id)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, seriessvc.SeriesSummaryDTO{
		ID:            updated.ID,
		Title:         updated.Title,
		Slug:          updated.Slug,
		Category:      updated.Category,
		CoverURL:      updated.CoverURL,
		ChapterCounts: updated.ChapterCounts,
	})
}

// Categories handles GET /api/categories.
//
// It returns one CategoryCountDTO per Series.category enum value (all five,
// including zero-count categories).
func (h *Handler) Categories(c echo.Context) error {
	out, err := h.svc.Categories(c.Request().Context())
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, out)
}

// SetMonitored handles PATCH /api/series/:id/monitored.
//
// It parses the :id path param and the {monitored: bool} body, then sets the
// series' monitored flag via the service. On success it returns 200 with the
// updated SeriesSummaryDTO so the caller sees the new state without a refetch
// (§16 full round-trip). A missing series yields 404; a missing/invalid body
// yields 400.
func (h *Handler) SetMonitored(c echo.Context) error {
	id, err := validateID(c.Param("id"), "series id")
	if err != nil {
		return err
	}

	var req SetMonitoredRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if err := validateSetMonitored(req); err != nil {
		return err
	}

	ctx := c.Request().Context()
	if err := h.svc.SetMonitored(ctx, id, *req.Monitored); err != nil {
		return mapServiceError(err)
	}

	// Return the updated summary so the response confirms the new monitored state.
	updated, err := h.svc.GetSeries(ctx, id)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, seriessvc.SeriesSummaryDTO{
		ID:            updated.ID,
		Title:         updated.Title,
		Slug:          updated.Slug,
		Category:      updated.Category,
		CoverURL:      updated.CoverURL,
		Monitored:     updated.Monitored,
		ChapterCounts: updated.ChapterCounts,
	})
}

// ReorderProviders handles PATCH /api/series/:id/providers.
//
// It parses the :id path param and the {providers: [{id, importance}]} body,
// validates each provider id is a valid UUID and the importance is non-negative,
// then updates provider importances all-or-nothing via the service. On success it
// returns 200 with the updated SeriesDetailDTO so importances are reflected in the
// response without a refetch (§16 full round-trip). A missing series yields 404;
// a provider that doesn't belong to the series yields 400; a bad UUID or empty
// list yields 400.
func (h *Handler) ReorderProviders(c echo.Context) error {
	id, err := validateID(c.Param("id"), "series id")
	if err != nil {
		return err
	}

	var req ReorderProvidersRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	ranks, err := validateReorderProviders(req)
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	if err := h.svc.ReorderProviders(ctx, id, ranks); err != nil {
		return mapServiceError(err)
	}

	// Return the updated detail so the caller sees the new importances without a refetch.
	updated, err := h.svc.GetSeries(ctx, id)
	if err != nil {
		return mapServiceError(err)
	}
	// Auto-converge: re-rank may make a downloaded chapter's new top source a
	// strictly-better version — trigger a cycle so DetectUpgrades runs now (M5).
	h.trigger()
	return c.JSON(http.StatusOK, updated)
}

// RemoveProvider handles DELETE /api/series/:id/providers/:providerId. It
// removes one source from the series (deleting the provider row + its
// availability feed + sync state, clearing satisfied_by on affected chapters),
// keeping every downloaded CBZ, and returns the updated series detail. It does
// NOT trigger an auto-converge cycle — removal creates no wanted chapters (M6).
func (h *Handler) RemoveProvider(c echo.Context) error {
	id, err := validateID(c.Param("id"), "series id")
	if err != nil {
		return err
	}
	providerID, err := validateID(c.Param("providerId"), "provider id")
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	if err := h.svc.RemoveProvider(ctx, id, providerID); err != nil {
		return mapServiceError(err)
	}

	// Return the updated detail so the caller sees the removal without a refetch (§16).
	updated, err := h.svc.GetSeries(ctx, id)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, updated)
}

// LibraryHealth handles GET /api/health — the library-wide source-health scan:
// every series with at least one stale or erroring source.
func (h *Handler) LibraryHealth(c echo.Context) error {
	res, err := h.svc.LibraryHealth(c.Request().Context())
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, res)
}

// mapServiceError translates a series.Service sentinel error into the matching
// HTTP status, leaving any unexpected error to fall through to the central
// middleware as a 500. ErrSeriesNotFound → 404; ErrInvalidCategory → 400;
// ErrProviderNotInSeries → 400.
func mapServiceError(err error) error {
	switch {
	case errors.Is(err, seriessvc.ErrSeriesNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "series not found")
	case errors.Is(err, seriessvc.ErrInvalidCategory):
		return echo.NewHTTPError(http.StatusBadRequest, "invalid category")
	case errors.Is(err, seriessvc.ErrProviderNotInSeries):
		return echo.NewHTTPError(http.StatusBadRequest, "provider does not belong to series")
	default:
		return err
	}
}
