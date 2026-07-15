// Package engine holds the engine-topology HTTP endpoints. Two kinds live here:
//   - the INTERNAL /internal apk-serving route a future engine-recovery/reconcile
//     pass consumes (NOT part of the public owner API, deliberately absent from
//     the OpenAPI spec) — it streams cached extension .apk bytes so the engine's
//     extensions can be re-installed from Tsundoku's own store even when the
//     upstream repo is offline; and
//   - the owner-facing /api/engine/topology-status readout (IN the OpenAPI spec)
//     that reports, from DB counts alone, how much engine topology Tsundoku has
//     captured — the observable counterpart of the one-shot seed passes.
package engine

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	entpkg "github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/handler/httperr"
)

// apkContentType is the standard MIME type for an Android package.
const apkContentType = "application/vnd.android.package-archive"

// Handler serves the engine-topology endpoints. It holds the APK cache store
// directly (the /internal apk bytes come straight off disk) and the Ent client
// (the /api topology-status readout counts captured topology rows). Neither
// endpoint calls the engine.
type Handler struct {
	cache *apkcache.Store
	db    *entpkg.Client
}

// NewHandler constructs a Handler bound to the APK cache store (serves the
// cached .apk bytes) and the Ent client (computes the topology-status counts).
func NewHandler(cache *apkcache.Store, db *entpkg.Client) *Handler {
	return &Handler{cache: cache, db: db}
}

// ServeAPK handles GET /internal/extensions/apk/:pkg/:file. It streams the cached
// .apk for the (pkg, version) with the Android-package content type.
//
// The LAST path segment MUST be the collision-free filename "<pkg>-<version>.apk"
// — NOT a bare version integer. This is a host-consumption contract: the
// engine-host loader names the file it installs from apkUrl.substringAfterLast('/'),
// so a URL ending in a bare version int would name two same-version extensions
// the same file and load the wrong bytes. The reconcile therefore constructs
// apkUrl ending in "<pkg>-<version>.apk"; this handler parses the version back
// out of :file (validating it matches :pkg) and serves cache.Open(pkg, version).
//
// Returns 400 when :file is not "<pkg>-<version>.apk", 404 when the apk is not
// cached.
func (h *Handler) ServeAPK(c echo.Context) error {
	pkg := c.Param("pkg")
	version, err := versionFromAPKFile(pkg, c.Param("file"))
	if err != nil {
		return httperr.BadRequest(`file must be "<pkg>-<version>.apk"`)
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

// versionFromAPKFile parses the version integer out of a "<pkg>-<version>.apk"
// filename, validating that it belongs to pkg (the same pkg the URL carries as
// its own segment). It is the inverse of the reconcile's URL construction, so a
// malformed or mismatched filename is rejected (→ 400) rather than silently
// serving the wrong extension.
func versionFromAPKFile(pkg, file string) (int, error) {
	rest, ok := strings.CutSuffix(file, ".apk")
	if !ok {
		return 0, errBadAPKFile
	}
	verStr, ok := strings.CutPrefix(rest, pkg+"-")
	if !ok {
		return 0, errBadAPKFile
	}
	version, err := strconv.Atoi(verStr)
	if err != nil {
		return 0, errBadAPKFile
	}
	return version, nil
}

// errBadAPKFile marks a serve-URL filename that is not "<pkg>-<version>.apk".
var errBadAPKFile = errors.New("engine: apk file must be \"<pkg>-<version>.apk\"")
