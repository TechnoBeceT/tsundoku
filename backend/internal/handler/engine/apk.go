// Package engine holds the INTERNAL engine-topology HTTP endpoints — routes
// under /internal that Tsundoku itself (and a future engine-recovery/reconcile
// pass) consumes, NOT part of the public owner API and deliberately absent from
// the OpenAPI spec. Its first resident serves cached extension .apk bytes so the
// engine's extensions can be re-installed from Tsundoku's own store even when the
// upstream repo is offline.
package engine

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/handler/httperr"
)

// apkContentType is the standard MIME type for an Android package.
const apkContentType = "application/vnd.android.package-archive"

// Handler serves the internal engine-topology endpoints. It holds the APK cache
// store directly (no Tsundoku service/DB) — the bytes come straight off disk.
type Handler struct {
	cache *apkcache.Store
}

// NewHandler constructs a Handler bound to the APK cache store.
func NewHandler(cache *apkcache.Store) *Handler {
	return &Handler{cache: cache}
}

// ServeAPK handles GET /internal/extensions/apk/:pkg/:version. It streams the
// cached .apk for the (pkg, version) with the Android-package content type.
// Returns 400 on a non-integer version, 404 when the apk is not cached.
func (h *Handler) ServeAPK(c echo.Context) error {
	pkg := c.Param("pkg")
	version, err := strconv.Atoi(c.Param("version"))
	if err != nil {
		return httperr.BadRequest("version must be an integer")
	}

	rc, err := h.cache.Open(pkg, version)
	if errors.Is(err, apkcache.ErrNotCached) {
		return echo.NewHTTPError(http.StatusNotFound, "apk not cached")
	}
	if err != nil {
		// A genuine I/O failure (not a missing file) is surfaced, never masked as a 404.
		return echo.NewHTTPError(http.StatusInternalServerError, "read cached apk failed")
	}
	defer func() { _ = rc.Close() }()

	return c.Stream(http.StatusOK, apkContentType, rc)
}
