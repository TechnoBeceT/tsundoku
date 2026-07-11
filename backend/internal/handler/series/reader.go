package series

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/disk"
	seriessvc "github.com/technobecet/tsundoku/internal/series"
)

// ChapterPage handles GET /api/series/:id/chapters/:chapterId/pages/:n — the
// in-app reader's page-bytes endpoint. It validates the ids and the page index
// (non-int → 400), streams the n-th image of the chapter's CBZ as a binary blob,
// and sets a short owner-private Cache-Control header (a convergence upgrade can
// replace a chapter's bytes at the same id, so the pages are NOT immutable).
// Errors: unknown chapter / wrong series / missing CBZ / page out of range →
// 404; a corrupt-archive read failure → 502 (see mapReaderError).
func (h *Handler) ChapterPage(c echo.Context) error {
	seriesID, err := validateID(c.Param("id"), "series id")
	if err != nil {
		return err
	}
	chapterID, err := validateID(c.Param("chapterId"), "chapter id")
	if err != nil {
		return err
	}
	n, err := validatePageIndex(c.Param("n"))
	if err != nil {
		return err
	}

	data, contentType, err := h.svc.ChapterPage(c.Request().Context(), seriesID, chapterID, n)
	if err != nil {
		return mapReaderError(err)
	}

	// Owner-private, briefly cached. NOT immutable/public: Library-Convergence
	// (download.tryDeleteOldCBZ + finishDownload) can REPLACE a chapter's CBZ bytes
	// at the SAME chapterId when a higher-importance source is attached, so a long
	// immutable TTL would serve stale pages; and the pages are owner-private, so a
	// shared cache must never store them. 5 minutes bounds the staleness window.
	c.Response().Header().Set("Cache-Control", "private, max-age=300")
	return c.Blob(http.StatusOK, contentType, data)
}

// SetProgress handles PATCH /api/chapters/:id/progress. It validates the body
// ({lastReadPage: int>=0, read: bool} — both required; negative page → 400),
// records the owner's reading progress, and returns 200 with the updated
// ChapterProgressDTO so the caller sees read_at without a refetch (§16). A
// missing chapter yields 404. This route lives in handler/series (not
// handler/downloads) so it sits beside the reader's page-bytes endpoint and the
// ChapterDTO it feeds — the whole reader surface is in one package.
func (h *Handler) SetProgress(c echo.Context) error {
	id, err := validateID(c.Param("id"), "chapter id")
	if err != nil {
		return err
	}

	var req ProgressRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	lastReadPage, read, err := validateProgress(req)
	if err != nil {
		return err
	}

	out, err := h.svc.SetProgress(c.Request().Context(), id, lastReadPage, read)
	if err != nil {
		return mapReaderError(err)
	}
	return c.JSON(http.StatusOK, out)
}

// mapReaderError maps the reader service sentinels to HTTP statuses:
// ErrChapterNotFound / ErrChapterFileMissing / disk.ErrPageOutOfRange → 404
// (there is nothing to serve); ErrPageRead → 502 (the archive exists but a page
// could not be decoded — a genuine gateway-style failure). Any unexpected error
// falls through to the central middleware as a 500.
func mapReaderError(err error) error {
	switch {
	case errors.Is(err, seriessvc.ErrChapterNotFound),
		errors.Is(err, seriessvc.ErrChapterFileMissing),
		errors.Is(err, disk.ErrPageOutOfRange):
		return echo.NewHTTPError(http.StatusNotFound, "page not found")
	case errors.Is(err, seriessvc.ErrPageRead):
		return echo.NewHTTPError(http.StatusBadGateway, "page read failed")
	default:
		return err
	}
}
