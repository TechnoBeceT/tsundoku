package series

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// SourcelessCleanupResult is the JSON response for
// POST /api/series/:id/sourceless-cleanup: how many sourceless chapters (CBZ +
// Chapter row) were removed.
type SourcelessCleanupResult struct {
	// Removed is the count of sourceless chapters deleted by the cleanup.
	Removed int `json:"removed"`
}

// SourcelessCleanupPreview handles GET /api/series/:id/sourceless-cleanup. It
// returns the series' REMOVABLE sourceless chapters — downloaded CBZs left
// behind when every source that supplied them was removed (a source removal
// keeps downloaded chapters by the keep-CBZs invariant, so they persist with no
// remaining carrier). It DELETES NOTHING. A missing series yields 404.
func (h *Handler) SourcelessCleanupPreview(c echo.Context) error {
	return cleanupPreview(c, h.svc.SourcelessCleanupPreview)
}

// RemoveSourcelessChapters handles POST /api/series/:id/sourceless-cleanup with a
// {chapterIds: [uuid]} body. It removes each selected chapter's CBZ file and its
// Chapter row and returns {removed: N}.
//
// The removable set is RE-COMPUTED by the service: an id outside it (a chapter a
// live source still carries, a chapter of another series) is rejected with 400
// and nothing is deleted. A missing series yields 404; an empty or malformed
// chapterIds list yields 400.
func (h *Handler) RemoveSourcelessChapters(c echo.Context) error {
	removed, err := removeCleanupChapters(c, h.svc.RemoveSourcelessChapters)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, SourcelessCleanupResult{Removed: removed})
}
