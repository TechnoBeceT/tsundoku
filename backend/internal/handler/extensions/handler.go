// Package extensions holds the thin HTTP handlers for the Suwayomi "Sources &
// Extensions management" proxy. It lets the owner list, install, update,
// uninstall, and refresh Suwayomi extensions (the Tachiyomi/Mihon source plugins)
// and manage the extension repo URL list — all from Tsundoku, so they never open
// Suwayomi's own UI.
//
// Like the Suwayomi settings proxy it is a PURE passthrough: no Tsundoku schema,
// no disk, no SSE, no deletion of Tsundoku rows — the extensions live entirely on
// whichever Suwayomi the client targets (embed or external). The handler owns a
// suwayomi.Client directly (cover-proxy / settings-proxy style) and does
// bind → validate → client → DTO; the GraphQL lives in the client's extensions.go.
// Validation is extracted to validate.go; the DTO mapping to dto.go.
package extensions

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/handler/coverproxy"
	"github.com/technobecet/tsundoku/internal/handler/httperr"
	suwayomicli "github.com/technobecet/tsundoku/internal/suwayomi"
)

// Handler serves the Suwayomi extension-management endpoints. It holds the
// Suwayomi client whose BaseURL() targets the active (embedded or external)
// Suwayomi instance.
type Handler struct {
	sw suwayomicli.Client
}

// NewHandler constructs a Handler bound to a Suwayomi client.
func NewHandler(sw suwayomicli.Client) *Handler {
	return &Handler{sw: sw}
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
// returns fetchExtensions's own refreshed list. An upstream failure is a 502.
func (h *Handler) Refresh(c echo.Context) error {
	exts, err := h.sw.FetchExtensions(c.Request().Context())
	if err != nil {
		return httperr.Upstream(err)
	}
	return c.JSON(http.StatusOK, toExtensionDTOs(exts))
}

// Install handles POST /api/suwayomi/extensions/:pkgName/install.
func (h *Handler) Install(c echo.Context) error {
	return h.setState(c, suwayomicli.ExtensionInstall)
}

// Update handles POST /api/suwayomi/extensions/:pkgName/update.
func (h *Handler) Update(c echo.Context) error {
	return h.setState(c, suwayomicli.ExtensionUpdate)
}

// Uninstall handles DELETE /api/suwayomi/extensions/:pkgName.
func (h *Handler) Uninstall(c echo.Context) error {
	return h.setState(c, suwayomicli.ExtensionUninstall)
}

// setState is the shared body of the three mutating extension endpoints: it
// validates :pkgName, applies the state change, then RE-READS the full extension
// list via Extensions and returns it (§16 round-trip) so the caller observes the
// authoritative post-mutation state (e.g. isInstalled flipped) rather than a
// request echo. Returning the whole list cleanly handles uninstall (the entry may
// drop out) and matches the FE's need to re-render. A validation failure is a
// 400; an upstream failure is a 502.
func (h *Handler) setState(c echo.Context, action suwayomicli.ExtensionAction) error {
	pkgName, err := validatePkgName(c.Param("pkgName"))
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	if err := h.sw.SetExtensionState(ctx, pkgName, action); err != nil {
		return httperr.Upstream(err)
	}
	exts, err := h.sw.Extensions(ctx)
	if err != nil {
		return httperr.Upstream(err)
	}
	return c.JSON(http.StatusOK, toExtensionDTOs(exts))
}

// Icon handles GET /api/suwayomi/extensions/:pkgName/icon (M1 bugfix: extension
// icons rendered as colored placeholder squares because Suwayomi's own iconUrl
// is a cross-origin URL the browser can't load). Suwayomi keys its icon REST
// endpoint by the extension's own apk filename, not pkgName — confirmed live
// (build-tagged TestShape6_Extensions) at
// /api/v1/extension/icon/{apkFileName} — and Extensions() already reports that
// exact value as each extension's IconURL, so this looks the extension up by
// pkgName and streams ITS reported IconURL, mirroring the series/imports cover
// proxies. No new suwayomi.Client method: Extensions + the existing PageBytes
// (via coverproxy.Stream) are enough. A blank/unknown pkgName is a 404; any
// Suwayomi failure (the list call or the icon fetch) is a 502.
func (h *Handler) Icon(c echo.Context) error {
	pkgName, err := validatePkgName(c.Param("pkgName"))
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	exts, err := h.sw.Extensions(ctx)
	if err != nil {
		return httperr.Upstream(err)
	}
	for _, e := range exts {
		if e.PkgName == pkgName {
			return coverproxy.Stream(c, h.sw, e.IconURL)
		}
	}
	return echo.NewHTTPError(http.StatusNotFound, "extension not found")
}

// GetRepos handles GET /api/suwayomi/extensions/repos. It returns the configured
// repo URL list. An upstream failure is a 502.
func (h *Handler) GetRepos(c echo.Context) error {
	repos, err := h.sw.ExtensionRepos(c.Request().Context())
	if err != nil {
		return httperr.Upstream(err)
	}
	return c.JSON(http.StatusOK, toReposDTO(repos))
}

// SetRepos handles PUT /api/suwayomi/extensions/repos. It validates the full
// replacement list, applies it (an empty array clears all repos), then RE-READS
// via ExtensionRepos and returns the refreshed list (§16 round-trip). A
// validation failure is a 400; an upstream failure is a 502.
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
	if err := h.sw.SetExtensionRepos(ctx, repos); err != nil {
		return httperr.Upstream(err)
	}
	current, err := h.sw.ExtensionRepos(ctx)
	if err != nil {
		return httperr.Upstream(err)
	}
	return c.JSON(http.StatusOK, toReposDTO(current))
}
