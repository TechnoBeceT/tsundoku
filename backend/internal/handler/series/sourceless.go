package series

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// LibrarySourceless handles GET /api/library/sourceless — the library-wide
// Sourceless page. It returns every series with at least one DOWNLOADED
// chapter no remaining source carries, each carrying its sourceless count and
// display name/cover, sorted most-actionable first. It DELETES NOTHING — it is
// the read that drives the retroactive bulk-fix surface (mirrors
// LibraryFractionals).
func (h *Handler) LibrarySourceless(c echo.Context) error {
	out, err := h.svc.LibrarySourceless(c.Request().Context())
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, out)
}
