package series

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// LibraryDuplicateFiles handles GET /api/library/duplicate-files — the
// library-wide Duplicates page. It returns every series whose folder carries CBZs
// the per-series "Remove duplicate files" action would delete, each with its file
// count and reclaimable bytes plus its display name/cover, sorted most-actionable
// first, and the library totals.
//
// It DELETES NOTHING and has NO execute counterpart: this is DISCOVERY only, so
// the owner can find which series need cleaning instead of opening each one. The
// removal itself stays where it already is — the owner-triggered per-series
// endpoint (mirrors LibraryFractionals / LibrarySourceless, which are also
// read-only rollups over a per-series action).
func (h *Handler) LibraryDuplicateFiles(c echo.Context) error {
	out, err := h.svc.LibraryDuplicateFiles(c.Request().Context())
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, out)
}
