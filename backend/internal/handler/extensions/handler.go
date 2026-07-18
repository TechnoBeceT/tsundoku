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
) *Handler {
	return &Handler{sw: sw, db: db, cache: cache, httpGet: httpGet, disabled: disabled}
}

// List handles GET /api/suwayomi/extensions. It returns every extension
// (installed + available). An upstream failure is a 502.
func (h *Handler) List(c echo.Context) error {
	exts, err := h.sw.Extensions(c.Request().Context())
	if err != nil {
		return httperr.Upstream(err)
	}
	return c.JSON(http.StatusOK, toExtensionDTOs(exts))
}

// Refresh handles POST /api/suwayomi/extensions/refresh. It re-fetches the
// available-extensions list from the configured repos ("check for updates") and
// returns the refreshed list. An upstream failure is a 502.
func (h *Handler) Refresh(c echo.Context) error {
	exts, err := h.sw.RefreshExtensions(c.Request().Context())
	if err != nil {
		return httperr.Upstream(err)
	}
	return c.JSON(http.StatusOK, toExtensionDTOs(exts))
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
	return c.JSON(http.StatusOK, toExtensionDTOs(exts))
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
	return c.JSON(http.StatusOK, toExtensionDTOs(exts))
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
	enginetopo.OnExtensionInstalled(ctx, h.db, h.cache, h.httpGet, ext)
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
