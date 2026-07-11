package series

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/handler/coverproxy"
)

// coverCacheControl keeps the cached cover REVALIDATABLE.
//
// GOTCHA: a positive max-age would defeat the feature's own acceptance criterion
// — a "fresh" response means the browser never sends If-None-Match, so after the
// owner switches metadata source (the URL is stable, there is no cache-buster)
// the OLD cover would keep rendering until the freshness window expired. With
// no-cache the browser always revalidates, the ETag turns that into a bodyless
// 304, and the backend serves it from disk: still ZERO source-ward calls.
const coverCacheControl = "private, no-cache"

// SeriesCover serves the metadata source's cover image for the series.
//
// The bytes come from the LOCAL cache (the series folder) whenever it is current
// — see series.Service.CoverBytes — so a library grid re-render pings no source
// at all. The response carries an ETag + a revalidatable Cache-Control, so a
// re-render usually costs a 304 instead of a body: without them we would still
// pay 52 round-trips per render, just to disk.
//
// Auth is HttpOnly-cookie-based (see pkg/middleware.RequireOwner), so the SPA can
// load this endpoint with a plain same-origin <img src>. Returns 404 when the
// series has no cover, 502 when a cold cover cannot be fetched from Suwayomi.
func (h *Handler) SeriesCover(c echo.Context) error {
	id, err := validateID(c.Param("id"), "series id")
	if err != nil {
		return err
	}
	data, ext, err := h.svc.CoverBytes(c.Request().Context(), id)
	if err != nil {
		// ErrNoCover / ErrSeriesNotFound → 404, ErrCoverFetchFailed → 502.
		return mapServiceError(err)
	}
	return serveCachedImage(c, data, ext)
}

// ProviderCover streams the cover image for a specific provider. The cover_url
// stored on that SeriesProvider is fetched from Suwayomi. Returns 404 when the
// provider has no cover or does not belong to the series, 502 on a Suwayomi
// fetch failure.
//
// GOTCHA: this one is NOT cached on disk (only the SERIES cover is). It is the
// metadata-source picker's thumbnail — a handful per detail page, not 52 per
// grid render — and a provider the series does not display has no obvious file
// to own in the series folder.
func (h *Handler) ProviderCover(c echo.Context) error {
	id, err := validateID(c.Param("id"), "series id")
	if err != nil {
		return err
	}
	providerID, err := validateID(c.Param("providerId"), "provider id")
	if err != nil {
		return err
	}
	coverURL, err := h.svc.ProviderCoverURL(c.Request().Context(), id, providerID)
	if err != nil {
		return mapServiceError(err)
	}
	return coverproxy.Stream(c, h.sw, coverURL)
}

// serveCachedImage writes image bytes with a content-derived ETag and a
// revalidatable Cache-Control, honouring If-None-Match with a 304 so a repeat
// view transfers no body at all.
func serveCachedImage(c echo.Context, data []byte, ext string) error {
	sum := sha256.Sum256(data)
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`

	c.Response().Header().Set("ETag", etag)
	c.Response().Header().Set("Cache-Control", coverCacheControl)

	if c.Request().Header.Get("If-None-Match") == etag {
		return c.NoContent(http.StatusNotModified)
	}
	return c.Blob(http.StatusOK, coverproxy.MimeForExt(ext), data)
}
