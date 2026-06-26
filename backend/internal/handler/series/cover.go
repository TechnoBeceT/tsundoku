package series

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// SeriesCover streams the metadata source's cover image for the series. The
// cover_url stored on the metadata provider is fetched from Suwayomi and
// returned as a binary blob — the SPA loads it via the fetch client (object
// URL), not a raw <img src>, so the Authorization header is sent correctly
// (QCAT-018). Returns 404 when the series has no cover, 502 when Suwayomi
// fails to fetch the image.
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
// response. A Suwayomi fetch failure yields 502 Bad Gateway.
func (h *Handler) streamCover(c echo.Context, coverURL string) error {
	data, ext, err := h.sw.PageBytes(c.Request().Context(), coverURL)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadGateway, "cover fetch failed")
	}
	return c.Blob(http.StatusOK, mimeForExt(ext), data)
}

// mimeForExt maps the bare extension returned by suwayomi.Client.PageBytes to
// a MIME content type. Unknown extensions fall back to application/octet-stream.
func mimeForExt(ext string) string {
	switch ext {
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	case "gif":
		return "image/gif"
	case "avif":
		return "image/avif"
	default:
		return "application/octet-stream"
	}
}
