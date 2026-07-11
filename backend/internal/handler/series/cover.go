package series

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/handler/coverproxy"
)

// coverImmutable lets the browser keep a cover FOREVER, without ever
// revalidating it.
//
// It is served ONLY to a request whose ?v= matches the cover's CURRENT content
// version (a hash of the image BYTES — see series.coverVersion). That equivalence
// is the entire licence: the URL changes whenever the bytes do, so an immutable
// response can never pin a stale image, and a changed cover shows instantly under
// its new URL.
//
// GOTCHA — the contrast with the reader's page-bytes endpoint, where `immutable`
// was WRONG: there the URL is stable while the bytes legitimately change (a
// convergence upgrade re-renders the CBZ), so an immutable response served stale
// pages. The rule is not "images are immutable", it is "immutable requires the URL
// to change whenever the CONTENT does". Only a content-versioned URL earns it — a
// URL versioned by something that merely correlates with the content (the
// provider's cover_url, which is Suwayomi's id-derived thumbnail path) does NOT:
// `immutable` is a one-way door, and the only lever that can ever show the owner a
// new image is a new URL.
const coverImmutable = "private, max-age=31536000, immutable"

// coverRevalidate is what an UNVERSIONED (or wrongly-versioned) request gets — a
// bookmark, a curl, an <img> preload, the service worker fetching plain
// /api/series/{id}/cover, or a series whose cover is not cached at all.
//
// Marking those immutable would permanently poison a URL that carries NO cache
// buster: nothing could ever change it, so nothing could ever fix it. They stay
// revalidatable; only the DTO's versioned URL is cacheable forever.
const coverRevalidate = "private, no-cache"

// SeriesCover serves the metadata source's cover image for the series.
//
// The bytes come from the LOCAL cache (the series folder) whenever it is current
// — see series.Service.CoverBytes — so a library grid re-render pings no source
// at all, and with the immutable Cache-Control above it usually pings the BACKEND
// zero times either.
//
// A conditional request is answered from the DB alone (CoverVersion) BEFORE the
// image is touched: a 304 that first os.ReadFile'd the whole cover over NFS (or
// worse, re-fetched it from the source) would pay exactly the cost it exists to
// avoid.
//
// Auth is HttpOnly-cookie-based (see pkg/middleware.RequireOwner), so the SPA can
// load this endpoint with a plain same-origin <img src>. Returns 404 when the
// series has no cover, 502 when a cold cover cannot be fetched from Suwayomi.
func (h *Handler) SeriesCover(c echo.Context) error {
	id, err := validateID(c.Param("id"), "series id")
	if err != nil {
		return err
	}
	ctx := c.Request().Context()

	// The client already holds these exact bytes — answer without reading them.
	version, err := h.svc.CoverVersion(ctx, id)
	if err != nil {
		return mapServiceError(err)
	}
	if version != "" && c.Request().Header.Get("If-None-Match") == coverETag(version) {
		writeCoverHeaders(c, version)
		return c.NoContent(http.StatusNotModified)
	}

	cover, err := h.svc.CoverBytes(ctx, id)
	if err != nil {
		// ErrNoCover / ErrSeriesNotFound → 404, ErrCoverFetchFailed → 502.
		return mapServiceError(err)
	}

	writeCoverHeaders(c, cover.Version)
	return c.Blob(http.StatusOK, coverproxy.MimeForExt(cover.Ext), cover.Data)
}

// writeCoverHeaders sets the cover response's validator + freshness headers for
// the version actually being served.
//
// The response is immutable ONLY when the request asked for the version it is
// getting; anything else (no ?v=, a stale ?v=, or an uncached cover that has no
// version at all) is revalidatable. The ETag is derived from the SERVER's version
// — never from the client-supplied ?v= or the body length, which are caller-
// controlled and collidable and could win a spurious 304.
func writeCoverHeaders(c echo.Context, version string) {
	cacheControl := coverRevalidate
	if version != "" {
		c.Response().Header().Set("ETag", coverETag(version))
		if c.QueryParam("v") == version {
			cacheControl = coverImmutable
		}
	}
	c.Response().Header().Set("Cache-Control", cacheControl)
}

// coverETag is the entity tag for a cover of the given content version — the one
// definition both the conditional check and the response header use, so the two
// can never disagree.
func coverETag(version string) string {
	return `"` + version + `"`
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
