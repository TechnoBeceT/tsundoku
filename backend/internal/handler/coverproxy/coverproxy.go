// Package coverproxy holds the shared cover-image-streaming helper used by
// every authed cover-proxy endpoint: the series cover, the per-provider cover
// (both in handler/series), and the Discover/Search source-manga cover
// (handler/imports). StreamEngine (sourceengine.Client, the sole surviving
// engine client since the P2 Suwayomi-removal migration) does
// fetch-bytes → write-binary-blob → map-failure-to-502, so that shape lives
// here once instead of being copy-pasted into every handler package (§2 DRY).
package coverproxy

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// StreamEngine fetches coverURL from the engine host via
// engine.Image(ctx, sourceID, "", coverURL) — pageURL is deliberately EMPTY
// and the cover URL goes in the imageURL slot (see internal/series/cover.go's
// fetchAndCacheCover doc comment for why: HttpSource.getImage uses
// page.imageUrl directly and skips getImageUrl, which throws for most
// sources) — and writes the bytes as a binary blob HTTP response, mirroring
// Stream's shape for the sourceengine-backed callers. A fetch failure yields
// 502 Bad Gateway — the upstream is a separate service, so its failure is a
// gateway error, never a false 200.
func StreamEngine(c echo.Context, engine sourceengine.Client, sourceID int64, coverURL string) error {
	data, ext, err := engine.Image(c.Request().Context(), sourceID, "", coverURL)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadGateway, "cover fetch failed")
	}
	return c.Blob(http.StatusOK, MimeForExt(ext), data)
}

// MimeForExt maps the bare image extension reported by Suwayomi (or read back
// from a locally cached cover) to a MIME content type. Unknown extensions fall
// back to application/octet-stream. Exported so the cached-cover endpoint —
// which serves bytes from disk instead of proxying — resolves the content type
// through the same table (§2 DRY).
func MimeForExt(ext string) string {
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
