package series

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/handler/coverproxy"
)

// coverCacheControl lets the browser keep a cover FOREVER, without ever
// revalidating it.
//
// This is safe ONLY because the cover URL is content-versioned: the DTO emits
// "…/cover?v=<hash of the provider's cover_url>" (series.SeriesDisplay), and the
// bytes can only change when that cover_url changes — which mints a different
// URL. So a re-render of the library grid costs ZERO requests, and a
// metadata-source switch shows the new cover INSTANTLY (new URL, cache miss).
//
// GOTCHA — the contrast with the reader's page-bytes endpoint, where `immutable`
// was WRONG: there the URL is stable while the bytes legitimately change (a
// convergence upgrade re-renders the CBZ), so an immutable response served stale
// pages. The rule is not "images are immutable", it is "immutable requires the
// URL to change whenever the content does". Only a versioned URL earns it.
const coverCacheControl = "private, max-age=31536000, immutable"

// SeriesCover serves the metadata source's cover image for the series.
//
// The bytes come from the LOCAL cache (the series folder) whenever it is current
// — see series.Service.CoverBytes — so a library grid re-render pings no source
// at all, and with the immutable Cache-Control above it usually pings the BACKEND
// zero times either.
//
// The "v" query param is a pure cache buster and is deliberately IGNORED here:
// the series id alone identifies the image, so a request without ?v= serves the
// same cover (only its browser-side cacheability differs).
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
	return serveCachedImage(c, data, ext, c.QueryParam("v"))
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

// serveCachedImage writes image bytes with the immutable Cache-Control and a
// CHEAP ETag, honouring If-None-Match with a bodyless 304.
//
// GOTCHA: the ETag is built from the URL's version + the byte length — NEVER by
// hashing the body. Hashing a ~200 KB image on every request was pure waste: it
// bought a 304 the versioned URL now makes unnecessary anyway (the browser does
// not even ask). The ETag is kept only as a correctness belt for clients that
// ignore Cache-Control (curl, a proxy, a request with no ?v=), and it still
// changes whenever the version does.
func serveCachedImage(c echo.Context, data []byte, ext, version string) error {
	etag := `"` + version + "-" + strconv.Itoa(len(data)) + `"`

	c.Response().Header().Set("ETag", etag)
	c.Response().Header().Set("Cache-Control", coverCacheControl)

	if c.Request().Header.Get("If-None-Match") == etag {
		return c.NoContent(http.StatusNotModified)
	}
	return c.Blob(http.StatusOK, coverproxy.MimeForExt(ext), data)
}
