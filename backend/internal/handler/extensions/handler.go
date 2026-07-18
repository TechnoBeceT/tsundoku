// Package extensions holds the thin HTTP handlers for the engine host's
// "Sources & Extensions management" proxy. It lets the owner list, install,
// update, uninstall, and refresh extensions (the Tachiyomi/Mihon source
// plugins) and manage the extension repo URL list — all from Tsundoku, so
// they never need direct access to the engine host.
//
// Like the settings proxy it is a PURE passthrough: no Tsundoku schema, no
// disk, no SSE, no deletion of Tsundoku rows — the extensions live entirely on
// the engine host. The handler owns a sourceengine.Client directly and does
// bind → validate → client → DTO. Validation is extracted to validate.go; the
// DTO mapping to dto.go.
//
// NO POST-MUTATION SOURCE-RELOAD HEAL (deliberate, QCAT-281 Slice C).
// Updating one extension can leave OTHER extensions' sources LISTED in the
// engine's /sources but MISSING from its runtime loaded-source collection —
// they then throw "Collection contains no element matching the predicate" on
// search/fetch (observed live for Rolia Scan + Comick after an Asura update).
// Tsundoku does NOT attempt to heal this after Install/Update/Uninstall, because
// no existing capability CAN reliably heal it:
//   - sourceengine.Client exposes NO runtime source-reload RPC. RefreshExtensions
//     only re-fetches the AVAILABLE-extensions list from the repos ("check for
//     updates") — it does not re-instantiate the runtime loaded-source
//     collection. Install/Update/Uninstall reload only THEIR OWN extension's
//     sources, not the ones collaterally dropped.
//   - enginetopo.Reconcile (DB→engine) is drift-gated and only INSTALLS
//     required-but-MISSING extensions; the dropped sources' extensions are still
//     reported installed, so Reconcile is a guaranteed no-op for this failure
//     mode — wiring it here would add latency (and a ConfigProvider dependency)
//     for zero heal.
//
// A true heal needs a NEW engine-host RPC — e.g. POST /sources/reload that
// re-instantiates the runtime loaded-source collection from all installed
// extensions (or, equivalently, the engine re-instantiating EVERY extension's
// sources after any single-extension update). Until Rensaio exposes that, the
// reliable mitigation is surfacing DEGRADED sources in the picker (the source
// circuit-breaker trips on the resulting fetch failures — internal/sourcegate)
// so a broken source is no longer presented as cleanly selectable.
package extensions

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/enginetopo"
	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/handler/httperr"
	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// SourceToggleStore is the narrow surface the Handler needs for the TSUNDOKU-SIDE
// per-language enable/disable toggle (the Configure dialog's per-source Switch).
// It reads the disabled-source set (for the Preferences group `enabled` field)
// and writes one source's state (SetSourceEnabled). *disabledsource.Service
// satisfies it. A nil store (focused proxy tests) makes every group report
// enabled=true and SetSourceEnabled a no-op-if-unwired path (see its doc).
type SourceToggleStore interface {
	Disabled(ctx context.Context) (map[int64]bool, error)
	SetEnabled(ctx context.Context, sourceID int64, enabled bool) error
}

// Handler serves the extension-management endpoints. It holds the engine-host
// client, plus the durable engine-topology store (Ent client + apk byte cache
// + an httpGet for repo indexes/.apk downloads) that the best-effort
// write-through captures every install/update/uninstall/repo change into, plus
// the Tsundoku-side per-source disabled-flag store for the enable/disable toggle.
type Handler struct {
	sw sourceengine.Client
	// db and cache are the durable topology store the write-through updates. When
	// EITHER is nil the write-through is a no-op and the handler is a pure proxy —
	// used where topology capture is not wired, e.g. focused proxy tests.
	db      *ent.Client
	cache   *apkcache.Store
	httpGet func(url string) (*http.Response, error)
	// disabled is the Tsundoku-side per-source enable/disable store. Nil ⇒ every
	// group reports enabled=true and the enable/disable route is unavailable.
	disabled SourceToggleStore
	// retained resolves the apk-cache rollback-history depth
	// (extensions.retained_versions) at use-time — the prune count for the
	// install/update write-through and the reinstall path. Nil ⇒ the built-in
	// default (enginetopo.defaultRetainedVersions).
	retained func(context.Context) int
}

// NewHandler constructs a Handler bound to an engine client, the durable
// engine-topology store (Ent client, apk cache, and the httpGet used to fetch
// repo indexes + .apk bytes — http.Get in production), and the Tsundoku-side
// per-source disabled-flag store. db/cache/httpGet may be nil, which turns the
// write-through into a no-op (pure passthrough); disabled may be nil, which
// makes every group enabled and disables the enable/disable route.
func NewHandler(
	sw sourceengine.Client,
	db *ent.Client,
	cache *apkcache.Store,
	httpGet func(url string) (*http.Response, error),
	disabled SourceToggleStore,
	retained func(context.Context) int,
) *Handler {
	return &Handler{sw: sw, db: db, cache: cache, httpGet: httpGet, disabled: disabled, retained: retained}
}

// retainedCount resolves the rollback-history depth at use-time, falling back to
// the shared default when no resolver is wired (focused proxy tests).
func (h *Handler) retainedCount(ctx context.Context) int {
	if h.retained == nil {
		return defaultRetainedVersions
	}
	if n := h.retained(ctx); n >= 1 {
		return n
	}
	return defaultRetainedVersions
}

// defaultRetainedVersions mirrors settings' extensions.retained_versions default
// (3) for the unwired-resolver path — kept in sync with config's default.
const defaultRetainedVersions = 3

// respondExtensions writes the extension list as JSON, attaching each package's
// held-version set from ONE batched read of the durable store (no N+1). The map
// is built from a single HarvestedExtension query regardless of list size; when
// the store is not wired it is empty (every extension reports cachedVersions:[]).
func (h *Handler) respondExtensions(c echo.Context, exts []sourceengine.Extension) error {
	held := h.heldVersionsByPkg(c.Request().Context())
	return c.JSON(http.StatusOK, toExtensionDTOs(exts, held))
}

// heldVersionsByPkg loads the held (retained) .apk versions for every extension
// in ONE query (pkg_name → cached_versions), so respondExtensions never issues a
// per-extension lookup. A nil store or a read failure yields an empty map — the
// held-version list is a display enrichment, never a reason to fail the list.
func (h *Handler) heldVersionsByPkg(ctx context.Context) map[string][]apkcache.CachedVersion {
	if h.db == nil {
		return map[string][]apkcache.CachedVersion{}
	}
	rows, err := h.db.HarvestedExtension.Query().All(ctx)
	if err != nil {
		slog.WarnContext(ctx, "extensions: could not load held versions, omitting", "err", err)
		return map[string][]apkcache.CachedVersion{}
	}
	byPkg := make(map[string][]apkcache.CachedVersion, len(rows))
	for _, r := range rows {
		byPkg[r.PkgName] = r.CachedVersions
	}
	return byPkg
}

// List handles GET /api/suwayomi/extensions. It returns every extension
// (installed + available). An upstream failure is a 502.
func (h *Handler) List(c echo.Context) error {
	exts, err := h.sw.Extensions(c.Request().Context())
	if err != nil {
		return httperr.Upstream(err)
	}
	return h.respondExtensions(c, exts)
}

// Refresh handles POST /api/suwayomi/extensions/refresh. It re-fetches the
// available-extensions list from the configured repos ("check for updates") and
// returns the refreshed list. An upstream failure is a 502.
func (h *Handler) Refresh(c echo.Context) error {
	exts, err := h.sw.RefreshExtensions(c.Request().Context())
	if err != nil {
		return httperr.Upstream(err)
	}
	return h.respondExtensions(c, exts)
}

// Install handles POST /api/suwayomi/extensions/:pkgName/install. It installs
// REPO-based (apkURL ""; the apk-cache fallback + sideload install is
// DEFERRED — see enginetopo.Reconcile's doc comment on the same deferral).
func (h *Handler) Install(c echo.Context) error {
	return h.mutate(c, func(ctx context.Context, pkgName string) ([]sourceengine.Extension, error) {
		return h.sw.InstallExtension(ctx, pkgName, "")
	})
}

// Update handles POST /api/suwayomi/extensions/:pkgName/update.
func (h *Handler) Update(c echo.Context) error {
	return h.mutate(c, h.sw.UpdateExtension)
}

// Uninstall handles DELETE /api/suwayomi/extensions/:pkgName. It skips the
// shared install/update write-through capture (captureInstallOrUpdate) —
// uninstall removes the durable row + cached apk instead (OnExtensionUninstalled).
func (h *Handler) Uninstall(c echo.Context) error {
	pkgName, err := validatePkgName(c.Param("pkgName"))
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	exts, err := h.sw.UninstallExtension(ctx, pkgName)
	if err != nil {
		return httperr.Upstream(err)
	}
	if h.db != nil {
		enginetopo.OnExtensionUninstalled(ctx, h.db, h.cache, pkgName)
	}
	return h.respondExtensions(c, exts)
}

// Reinstall handles POST /api/suwayomi/extensions/:pkgName/reinstall — the
// reversible-update rollback path. It reinstalls a HELD (older) .apk version
// from Tsundoku's own apk cache, addressed by (pkgName, versionCode) in the body.
//
// The engine host installs it BY LOCAL FILESYSTEM PATH: the engine host and the
// Go server run in the SAME container sharing the /config volume the apk cache
// lives on, and the engine host's install(apkUrl) treats a NON-http apkUrl as a
// local file it copies onto its own volume — an EXISTING engine capability
// (ExtensionManager.install / ExtensionLoader.resolveApk), so no HTTP fetch and
// no auth are involved. (The /internal apk HTTP route stays owner-authed and is
// deliberately NOT used here: the engine fetches an http URL with no auth headers
// and would 401 against it — see the DISCOVERY note in the reversible-updates
// feature. The local-path install is the correct wiring for the bundled
// single-container topology; a remote/external engine that does not share the
// filesystem is a documented follow-up.)
//
// Flow: validate → the version must be HELD (in cached_versions) AND its bytes
// present on disk (else 404) → InstallExtension(ctx, "", <cache path>) →
// best-effort durable write-through pinning installed_version_code → return the
// refreshed list (§16). A validation failure is a 400; an upstream failure a 502.
func (h *Handler) Reinstall(c echo.Context) error {
	pkgName, err := validatePkgName(c.Param("pkgName"))
	if err != nil {
		return err
	}
	if h.db == nil || h.cache == nil {
		// The reinstall path needs the durable store + apk cache; without them there
		// is no held-version history to reinstall from.
		return echo.NewHTTPError(http.StatusServiceUnavailable, "extension version cache not available")
	}
	var req ReinstallRequest
	if err := c.Bind(&req); err != nil {
		return httperr.BadRequest("invalid request body")
	}
	versionCode, err := validateReinstall(req)
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	if !h.heldVersionOnDisk(ctx, pkgName, versionCode) {
		return echo.NewHTTPError(http.StatusNotFound, "no cached apk for that extension version")
	}

	exts, err := h.sw.InstallExtension(ctx, "", h.cache.Path(pkgName, versionCode))
	if err != nil {
		return httperr.Upstream(err)
	}
	// Best-effort durable write-through: pin installed_version_code to the
	// reinstalled version + re-prune (logged-and-swallowed inside the helper).
	enginetopo.OnExtensionReinstalled(ctx, h.db, h.cache, pkgName, versionCode, h.retainedCount(ctx))
	return h.respondExtensions(c, exts)
}

// heldVersionOnDisk reports whether versionCode is recorded in pkgName's held set
// AND its .apk bytes are present in the cache — BOTH are required for a reinstall
// (the DB row is the durable claim; the file is the actual bytes the engine
// installs). Reuses the single-query held-versions load (no per-request N+1).
func (h *Handler) heldVersionOnDisk(ctx context.Context, pkgName string, versionCode int) bool {
	found := false
	for _, cv := range h.heldVersionsByPkg(ctx)[pkgName] {
		if cv.VersionCode == versionCode {
			found = true
			break
		}
	}
	return found && h.cache.Exists(pkgName, versionCode)
}

// mutate is the shared body of Install/Update: it validates :pkgName, applies
// the mutation (which the engine host ALREADY returns the refreshed extension
// list from — unlike the retired Suwayomi shape, no separate re-read call is
// needed), captures the just-affected extension into the durable topology
// store (best-effort write-through), and returns the refreshed list. A
// validation failure is a 400; an upstream failure is a 502.
func (h *Handler) mutate(
	c echo.Context,
	apply func(ctx context.Context, pkgName string) ([]sourceengine.Extension, error),
) error {
	pkgName, err := validatePkgName(c.Param("pkgName"))
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	exts, err := apply(ctx, pkgName)
	if err != nil {
		return httperr.Upstream(err)
	}
	h.captureInstallOrUpdate(ctx, pkgName, exts)
	return h.respondExtensions(c, exts)
}

// captureInstallOrUpdate runs the best-effort durable write-through after a
// successful install/update engine mutation: it finds the just-affected
// extension by pkgName in the handler's post-mutation exts (no redundant
// Extensions() call), then captures it via OnExtensionInstalled. A no-op when
// the durable store is not wired, or when pkgName is absent from the refreshed
// list (logged and skipped). Any capture failure is logged inside the
// enginetopo helpers and never affects the handler's success response.
func (h *Handler) captureInstallOrUpdate(ctx context.Context, pkgName string, exts []sourceengine.Extension) {
	if h.db == nil || h.cache == nil {
		return
	}
	ext, ok := findExtension(exts, pkgName)
	if !ok {
		slog.WarnContext(ctx, "extensions: installed extension not in post-mutation list, skipping topology capture",
			"pkg_name", pkgName)
		return
	}
	enginetopo.OnExtensionInstalled(ctx, h.db, h.cache, h.httpGet, ext, h.retainedCount(ctx))
}

// findExtension returns the extension with the given pkgName from exts.
func findExtension(exts []sourceengine.Extension, pkgName string) (sourceengine.Extension, bool) {
	for _, e := range exts {
		if e.PkgName == pkgName {
			return e, true
		}
	}
	return sourceengine.Extension{}, false
}

// GetRepos handles GET /api/suwayomi/extensions/repos. It returns the configured
// repo URL list. An upstream failure is a 502.
func (h *Handler) GetRepos(c echo.Context) error {
	repos, err := h.sw.Repos(c.Request().Context())
	if err != nil {
		return httperr.Upstream(err)
	}
	return c.JSON(http.StatusOK, toReposDTO(repos))
}

// SetRepos handles PUT /api/suwayomi/extensions/repos. It validates the full
// replacement list, applies it (an empty array clears all repos, and the engine
// host echoes the written list back), then writes it through to the durable
// store and returns it (§16 round-trip). A validation failure is a 400; an
// upstream failure is a 502.
func (h *Handler) SetRepos(c echo.Context) error {
	var req ReposUpdateRequest
	if err := c.Bind(&req); err != nil {
		return httperr.BadRequest("invalid request body")
	}
	repos, err := validateRepos(req)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	current, err := h.sw.SetRepos(ctx, repos)
	if err != nil {
		return httperr.Upstream(err)
	}
	// Best-effort durable write-through: replace the HarvestedRepo set with the
	// authoritative echoed-back list (rows for removed repos are pruned). Logged-
	// and-swallowed inside the helper; never affects this response.
	if h.db != nil {
		enginetopo.OnReposSet(ctx, h.db, current)
	}
	return c.JSON(http.StatusOK, toReposDTO(current))
}
