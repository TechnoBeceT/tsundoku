package series

import (
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/handler/coverproxy"
)

// SeriesCover streams the metadata source's cover image for the series. The
// cover_url stored on the metadata provider is fetched from Suwayomi and
// returned as a binary blob. Auth is HttpOnly-cookie-based (see
// pkg/middleware.RequireOwner), so the SPA can load this endpoint with a plain
// same-origin <img src> — the browser attaches the session cookie
// automatically, no Authorization header needed. Returns 404 when the series
// has no cover, 502 when Suwayomi fails to fetch the image.
func (h *Handler) SeriesCover(c echo.Context) error {
	id, err := validateID(c.Param("id"), "series id")
	if err != nil {
		return err
	}
	coverURL, err := h.svc.CoverURL(c.Request().Context(), id)
	if err != nil {
		return mapServiceError(err) // ErrNoCover → 404, ErrSeriesNotFound → 404
	}
	return h.streamCover(c, coverURL)
}

// ProviderCover streams the cover image for a specific provider. The cover_url
// stored on that SeriesProvider is fetched from Suwayomi. Returns 404 when the
// provider has no cover or does not belong to the series, 502 on a Suwayomi
// fetch failure.
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
	return h.streamCover(c, coverURL)
}

// streamCover fetches coverURL from Suwayomi and writes it as a binary blob
// response. A Suwayomi fetch failure yields 502 Bad Gateway. Delegates to the
// shared coverproxy.Stream helper — handler/imports' source-manga cover proxy
// needs the identical fetch→blob→502 behavior, so it lives in one place (§2 DRY).
func (h *Handler) streamCover(c echo.Context, coverURL string) error {
	return coverproxy.Stream(c, h.sw, coverURL)
}
