// Package coverproxy holds the shared cover-image-streaming helper used by
// every authed cover-proxy endpoint: the series cover, the per-provider cover
// (both in handler/series), and the Discover/Search source-manga cover
// (handler/imports). All three need the identical fetch-bytes-from-Suwayomi →
// write-binary-blob → map-failure-to-502 behavior, so it lives here once
// instead of being copy-pasted into each handler package (§2 DRY).
package coverproxy

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// Stream fetches coverURL from Suwayomi via sw.PageBytes and writes the bytes
// as a binary blob HTTP response, with a MIME type resolved from the bare file
// extension PageBytes reports. A Suwayomi fetch failure yields a 502 Bad
// Gateway — the upstream is a separate service, so its failure is a gateway
// error, never a false 200.
func Stream(c echo.Context, sw suwayomi.Client, coverURL string) error {
	data, ext, err := sw.PageBytes(c.Request().Context(), coverURL)
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
