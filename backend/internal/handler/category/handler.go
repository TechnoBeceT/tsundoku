package category

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	categorysvc "github.com/technobecet/tsundoku/internal/category"
)

// Handler holds the dependencies for the category HTTP handlers. All business
// logic lives in category.Service; the handler is thin.
type Handler struct {
	svc *categorysvc.Service
}

// NewHandler constructs a Handler bound to a category.Service.
func NewHandler(svc *categorysvc.Service) *Handler {
	return &Handler{svc: svc}
}

// List handles GET /api/categories. It returns every category ordered by sort
// order then name, each with its current series count.
func (h *Handler) List(c echo.Context) error {
	out, err := h.svc.List(c.Request().Context())
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, out)
}

// Create handles POST /api/categories. It creates a new empty category from the
// {name, sortOrder?} body and returns 201 with the created CategoryDTO so the
// caller sees the persisted state without a refetch (§16). A duplicate name
// yields 409; an invalid name yields 400.
func (h *Handler) Create(c echo.Context) error {
	var req CreateCategoryRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	out, err := h.svc.Create(c.Request().Context(), req.Name, req.SortOrder)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusCreated, out)
}

// Update handles PATCH /api/categories/:id — rename and/or reorder. A name moves
// the on-disk category folder (rename); a sortOrder is a DB-only reorder; both
// may be present. On success it returns 200 with the updated CategoryDTO (§16).
// A missing id yields 404; a duplicate name yields 409; renaming/deleting the
// protected default yields 400; an invalid name yields 400.
func (h *Handler) Update(c echo.Context) error {
	id, err := validateID(c.Param("id"))
	if err != nil {
		return err
	}
	var req UpdateCategoryRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if err := validateUpdate(req); err != nil {
		return err
	}

	ctx := c.Request().Context()
	// Rename first (it can fail on a folder move); only then reorder, so a failed
	// rename never leaves a half-applied sort change.
	if req.Name != nil {
		if err := h.svc.Rename(ctx, id, *req.Name); err != nil {
			return mapServiceError(err)
		}
	}
	if req.SortOrder != nil {
		if err := h.svc.Reorder(ctx, id, *req.SortOrder); err != nil {
			return mapServiceError(err)
		}
	}

	out, err := h.svc.Get(ctx, id)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, out)
}

// SetDefault handles PATCH /api/categories/:id/default. It promotes the category
// to be the single default landing for new / uncategorized series, demoting the
// previous default in the same transaction. On success it returns 200 with the
// updated CategoryDTO (§16). A missing id yields 404.
func (h *Handler) SetDefault(c echo.Context) error {
	id, err := validateID(c.Param("id"))
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	if err := h.svc.SetDefault(ctx, id); err != nil {
		return mapServiceError(err)
	}
	out, err := h.svc.Get(ctx, id)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, out)
}

// Delete handles DELETE /api/categories/:id. It removes a category only when no
// series is filed under it (else 409) and never the current default (else 400).
// Returns 204 No Content on success; a missing id yields 404.
func (h *Handler) Delete(c echo.Context) error {
	id, err := validateID(c.Param("id"))
	if err != nil {
		return err
	}
	if err := h.svc.Delete(c.Request().Context(), id); err != nil {
		return mapServiceError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// mapServiceError translates a category.Service sentinel into the matching HTTP
// status, leaving any unexpected error to the central middleware as a 500.
// ErrCategoryNotFound → 404; ErrInvalidCategoryName → 400; ErrCategoryProtected
// → 400; ErrCategoryIsDefault → 400; ErrCategoryNameTaken → 409;
// ErrCategoryNotEmpty → 409.
func mapServiceError(err error) error {
	switch {
	case errors.Is(err, categorysvc.ErrCategoryNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "category not found")
	case errors.Is(err, categorysvc.ErrInvalidCategoryName):
		return echo.NewHTTPError(http.StatusBadRequest, "invalid category name")
	case errors.Is(err, categorysvc.ErrCategoryProtected):
		return echo.NewHTTPError(http.StatusBadRequest, "category is protected")
	case errors.Is(err, categorysvc.ErrCategoryIsDefault):
		return echo.NewHTTPError(http.StatusBadRequest, "category is the default")
	case errors.Is(err, categorysvc.ErrCategoryNameTaken):
		return echo.NewHTTPError(http.StatusConflict, "category name already in use")
	case errors.Is(err, categorysvc.ErrCategoryNotEmpty):
		return echo.NewHTTPError(http.StatusConflict, "category is not empty")
	default:
		return err
	}
}
