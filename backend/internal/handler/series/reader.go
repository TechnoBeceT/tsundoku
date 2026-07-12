package series

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/disk"
	seriessvc "github.com/technobecet/tsundoku/internal/series"
)

// pageCacheable is what a request whose ?v= matches the chapter's CURRENT
// page version (seriessvc.PageVersion) earns: a full day of private caching.
// This is what makes the client-side whole-chapter prefetcher worth doing — the
// prefetched pages actually survive until the owner finishes reading, instead of
// expiring mid-chapter.
//
// GOTCHA — do NOT widen this to `immutable`, and do not copy the cover
// endpoint's policy (handler/series/cover.go coverImmutable). The cover version is a hash
// of the actual cover BYTES; PageVersion is a PROXY derived from the Chapter
// row's filename + download_date, not the CBZ's bytes — an owner who replaces a
// CBZ file out of band (bypassing download/upgrade) would leave those DB fields
// unchanged, so the version would NOT bump. `immutable` is a ONE-WAY DOOR: it
// would pin stale pages in the browser for a year with no server-side remedy. A
// bounded max-age instead self-heals — the worst case is a same-day staleness
// window, not a permanent one.
const pageCacheable = "private, max-age=86400"

// pageRevalidate is what an absent or STALE ?v= gets — the request carries no
// (or an out-of-date) cache buster, so it must always revalidate rather than
// risk serving a page a convergence upgrade has since replaced.
const pageRevalidate = "private, no-cache"

// ChapterPage handles GET /api/series/:id/chapters/:chapterId/pages/:n?v=<version>
// — the in-app reader's page-bytes endpoint. It validates the ids and the page
// index (non-int → 400), then answers a conditional request (If-None-Match)
// from the chapter's CURRENT version ALONE — see seriessvc.ChapterPageVersion —
// so a 304 never opens the CBZ. Otherwise it streams the n-th image as a binary
// blob and sets Cache-Control per pageCacheable/pageRevalidate above.
//
// ?v= never changes what bytes are served — ChapterPage always returns the
// CURRENT page regardless of the query value; v only selects the caching
// policy on the response (see writePageHeaders).
//
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
	ctx := c.Request().Context()

	// The cheap half: a single narrow column read, no disk I/O. If the client
	// already holds exactly these bytes, answer without ever opening the archive.
	version, err := h.svc.ChapterPageVersion(ctx, seriesID, chapterID)
	if err != nil {
		return mapReaderError(err)
	}
	if version != "" && c.Request().Header.Get("If-None-Match") == pageETag(version, n) {
		writePageHeaders(c, version, n, c.QueryParam("v"))
		return c.NoContent(http.StatusNotModified)
	}

	data, contentType, err := h.svc.ChapterPage(ctx, seriesID, chapterID, n)
	if err != nil {
		return mapReaderError(err)
	}

	writePageHeaders(c, version, n, c.QueryParam("v"))
	return c.Blob(http.StatusOK, contentType, data)
}

// writePageHeaders sets the page response's validator + freshness headers.
// version=="" (chapter has no rendered CBZ's identity to key on — should not
// normally reach here since ChapterPage would already have 404'd) skips both
// headers rather than emit a bogus ETag. Otherwise: the ETag identifies exactly
// these bytes (version + page index), and the response is only long-cached when
// the request's ?v= matches the CURRENT version — a stale or absent v always
// gets pageRevalidate, never pageCacheable (see pageCacheable's doc for why that
// asymmetry, not `immutable`, is correct here).
func writePageHeaders(c echo.Context, version string, n int, requestedV string) {
	if version == "" {
		c.Response().Header().Set("Cache-Control", pageRevalidate)
		return
	}
	c.Response().Header().Set("ETag", pageETag(version, n))
	cacheControl := pageRevalidate
	if requestedV == version {
		cacheControl = pageCacheable
	}
	c.Response().Header().Set("Cache-Control", cacheControl)
}

// pageETag is the entity tag for page n of a chapter at the given content
// version — the one definition both the conditional check and the response
// header use, so the two can never disagree. The page index is included
// because one chapter version spans many distinct page byte-strings.
func pageETag(version string, n int) string {
	return `"` + version + "-" + strconv.Itoa(n) + `"`
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
