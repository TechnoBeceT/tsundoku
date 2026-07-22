package series

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// cleanupPreview is the shared shape of a GET .../<x>-cleanup preview: parse the
// series id path param, delegate to fetch, and render its DTO as JSON — or the
// mapped service error. Shared by the fractional and sourceless cleanup preview
// handlers (§2 DRY; the two previews differ only in which service method they
// call and what DTO it returns, both captured by fetch's closure).
func cleanupPreview[T any](c echo.Context, fetch func(context.Context, uuid.UUID) (T, error)) error {
	id, err := validateID(c.Param("id"), "series id")
	if err != nil {
		return err
	}
	out, err := fetch(c.Request().Context(), id)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, out)
}

// removeCleanupChapters is the shared shape of a POST .../<x>-cleanup execute:
// parse the series id, bind + validate a {chapterIds:[uuid]} body, and delegate
// to remove. Returns the removed count ready for the caller to wrap in its own
// named JSON result type, or a 400/404 echo.HTTPError from validation or the
// mapped service error. Shared by the fractional and sourceless cleanup execute
// handlers (§2 DRY).
func removeCleanupChapters(c echo.Context, remove func(context.Context, uuid.UUID, []uuid.UUID) (int, error)) (int, error) {
	id, err := validateID(c.Param("id"), "series id")
	if err != nil {
		return 0, err
	}

	var req ChapterIDsRequest
	if err := c.Bind(&req); err != nil {
		return 0, echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	chapterIDs, err := validateChapterIDs(req)
	if err != nil {
		return 0, err
	}

	removed, err := remove(c.Request().Context(), id, chapterIDs)
	if err != nil {
		return 0, mapServiceError(err)
	}
	return removed, nil
}
