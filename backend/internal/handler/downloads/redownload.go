package downloads

import (
	"net/http"

	"github.com/labstack/echo/v4"

	downloadssvc "github.com/technobecet/tsundoku/internal/downloads"
)

// RedownloadChapter handles POST /api/chapters/:id/redownload — the per-chapter
// re-download the Series-Detail chapter row triggers.
//
// It parses the :id path param as a UUID (malformed → 400) and re-queues that
// chapter so the engine fetches it again over the existing CBZ. Returns 204 on
// success; a missing chapter yields 404; a chapter that is not downloaded (nothing
// to replace) yields 409. On success ONLY it calls the injected trigger so the
// re-download starts immediately instead of waiting for the next cycle.
//
// This is NOT the retry route. A retry gives a chapter with no file another go; a
// re-download deliberately replaces a file that already exists — and per QCAT-343
// it deletes nothing, so a failed attempt leaves the old CBZ in place.
func (h *Handler) RedownloadChapter(c echo.Context) error {
	id, err := validateID(c.Param("id"), "chapter id")
	if err != nil {
		return err
	}
	if err := h.svc.RedownloadChapter(c.Request().Context(), id); err != nil {
		return mapServiceError(err)
	}
	h.trigger()
	return c.NoContent(http.StatusNoContent)
}

// RedownloadPreview handles GET /api/downloads/redownload — the preview half of the
// bulk re-download.
//
// It parses the same filter the POST applies (?source, ?since, optional
// ?scanlator — see parseRedownloadFilter) and reports how many downloaded chapters
// it WOULD re-queue plus the honest throughput cost. It mutates nothing.
func (h *Handler) RedownloadPreview(c echo.Context) error {
	filter, err := parseRedownloadFilter(c)
	if err != nil {
		return err
	}
	out, err := h.svc.RedownloadPreview(c.Request().Context(), filter)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, out)
}

// RedownloadAll handles POST /api/downloads/redownload — the owner-confirmed bulk
// re-download.
//
// It re-computes the filter's matching set (never trusting ids the preview handed
// out), re-queues every match, and returns 200 with {"requeued": N}. On success
// ONLY it calls the injected trigger. Nothing is deleted: every CBZ stays on disk
// until its replacement lands.
func (h *Handler) RedownloadAll(c echo.Context) error {
	filter, err := parseRedownloadFilter(c)
	if err != nil {
		return err
	}
	n, err := h.svc.RedownloadAll(c.Request().Context(), filter)
	if err != nil {
		return mapServiceError(err)
	}
	h.trigger()
	return c.JSON(http.StatusOK, downloadssvc.RedownloadResultDTO{Requeued: n})
}
