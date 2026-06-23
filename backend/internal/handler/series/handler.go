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
	svc *seriessvc.Service
}

// NewHandler constructs a Handler bound to a series.Service.
func NewHandler(svc *seriessvc.Service) *Handler {
	return &Handler{svc: svc}
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
	id, err := validateID(c.Param("id"))
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
	id, err := validateID(c.Param("id"))
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

// mapServiceError translates a series.Service sentinel error into the matching
// HTTP status, leaving any unexpected error to fall through to the central
// middleware as a 500. ErrSeriesNotFound → 404; ErrInvalidCategory → 400.
func mapServiceError(err error) error {
	switch {
	case errors.Is(err, seriessvc.ErrSeriesNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "series not found")
	case errors.Is(err, seriessvc.ErrInvalidCategory):
		return echo.NewHTTPError(http.StatusBadRequest, "invalid category")
	default:
		return err
	}
}
