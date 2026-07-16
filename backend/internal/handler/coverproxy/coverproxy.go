// Package coverproxy holds the shared cover-image-streaming helpers used by
// every authed cover-proxy endpoint: the series cover, the per-provider cover
// (both in handler/series), and the Discover/Search source-manga cover
// (handler/imports). StreamEngine (sourceengine.Client, the sole surviving
// engine client since the P2 Suwayomi-removal migration) does
// fetch-bytes → write-binary-blob → map-failure-to-502, so that shape lives
// here once instead of being copy-pasted into every handler package (§2 DRY).
//
// StreamEngineCached is StreamEngine's disk-cached, fail-fast sibling (GAP-085)
// — see internal/sourcecover's package doc for why it exists: the engine host
// has no cache of its own for these two proxies (unlike Suwayomi's dropped
// getImageResponse), so a grid render used to burst-refetch every cover LIVE
// on every single request.
package coverproxy

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/sourcecover"
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
//
// This is the RAW, UNCACHED path. Every production caller now goes through
// StreamEngineCached instead (see GAP-085) — StreamEngine stays exported as
// the low-level primitive it wraps, and is exercised directly below.
func StreamEngine(c echo.Context, engine sourceengine.Client, sourceID int64, coverURL string) error {
	data, ext, err := engine.Image(c.Request().Context(), sourceID, "", coverURL)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadGateway, "cover fetch failed")
	}
	return c.Blob(http.StatusOK, MimeForExt(ext), data)
}

// StreamEngineCached resolves coverURL through cache — a disk cache-first,
// fail-fast, bounded-concurrency wrapper around the same engine fetch
// StreamEngine performs directly (see internal/sourcecover.Cache.Get) — and
// writes the result the same way StreamEngine does: a binary blob whose
// Content-Type is resolved from the returned ext via MimeForExt, so a cached
// serve and a live serve are byte-for-byte and header-for-header identical to
// the caller.
//
// A cache MISS that could not be resolved within its fail-fast deadline
// (sourcecover.ErrTimeout — either the bounded concurrency pool never freed a
// slot, or the engine fetch itself did not finish in time) maps to 504
// Gateway Timeout: a deliberate, fast, non-2xx response — never a held
// connection — so the owner's hard no-hang rule holds even under a
// saturating burst of cold covers. Any other engine failure maps to 502,
// exactly like StreamEngine.
func StreamEngineCached(c echo.Context, cache *sourcecover.Cache, sourceID int64, coverURL string) error {
	data, ext, err := cache.Get(c.Request().Context(), sourceID, coverURL)
	if err != nil {
		if errors.Is(err, sourcecover.ErrTimeout) {
			return echo.NewHTTPError(http.StatusGatewayTimeout, "cover fetch timed out")
		}
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
