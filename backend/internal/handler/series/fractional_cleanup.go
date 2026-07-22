package series

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// FractionalCleanupResult is the JSON response for
// POST /api/series/:id/fractional-cleanup: how many fractional chapters (CBZ +
// Chapter row) were removed.
type FractionalCleanupResult struct {
	// Removed is the count of fractional chapters deleted by the cleanup.
	Removed int `json:"removed"`
}

// FractionalCleanupPreview handles GET /api/series/:id/fractional-cleanup. It
// returns the series' REMOVABLE fractional chapters — the already-downloaded
// fractional CBZs left behind when the owner ticked ignore_fractional on a source
// (the toggle stops new fractional downloads and deletes nothing) — each carrying
// the evidence he judges from (page count, satisfying source, filename), plus the
// series' typical (median) whole-chapter page count as the yardstick. It DELETES
// NOTHING. A missing series yields 404.
func (h *Handler) FractionalCleanupPreview(c echo.Context) error {
	return cleanupPreview(c, h.svc.FractionalCleanupPreview)
}

// RemoveFractionalChapters handles POST /api/series/:id/fractional-cleanup with a
// {chapterIds: [uuid]} body. It removes each selected chapter's CBZ file and its
// Chapter row, keeping every ProviderChapter feed row (so un-ticking the toggle
// restores the chapter), and returns {removed: N}.
//
// The removable set is RE-COMPUTED by the service: an id outside it (a whole
// chapter, a fractional a live source still carries, a chapter of another series)
// is rejected with 400 and nothing is deleted. A missing series yields 404; an
// empty or malformed chapterIds list yields 400.
func (h *Handler) RemoveFractionalChapters(c echo.Context) error {
	removed, err := removeCleanupChapters(c, h.svc.RemoveFractionalChapters)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, FractionalCleanupResult{Removed: removed})
}
