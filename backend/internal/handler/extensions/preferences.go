package extensions

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/handler/httperr"
)

// This file (preferences.go) holds the per-source preference endpoints of the
// extension-management proxy — the "Configure" gear for an installed extension.
// Like the rest of the package it is a PURE passthrough: no Tsundoku state; the
// preferences live on whichever Suwayomi the client targets.
//
// POSITION-INDEXED CAUTION: a preference is written by its 0-based array position
// (Suwayomi offers no key-indexed write). The array order can shift server-side,
// so the FE must ALWAYS use the freshly-returned list after each write and never
// cache positions across renders. The PATCH handler re-reads the source's
// preferences immediately before the write to resolve the variant at the target
// position (needed to send the correct typed value), and returns the refreshed
// list from the write's own payload (§16 round-trip).

// Preferences handles GET /api/suwayomi/extensions/:pkgName/preferences. It
// resolves the extension's sources (one per language) and returns each source's
// configurable preferences grouped by source. A blank pkgName is a 400; any
// upstream Suwayomi failure (resolving sources or reading a source's preferences)
// is a 502.
func (h *Handler) Preferences(c echo.Context) error {
	pkgName, err := validatePkgName(c.Param("pkgName"))
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	sources, err := h.sw.ExtensionSources(ctx, pkgName)
	if err != nil {
		return httperr.Upstream(err)
	}
	groups := make([]SourcePreferencesGroupDTO, 0, len(sources))
	for _, s := range sources {
		prefs, err := h.sw.SourcePreferences(ctx, s.ID)
		if err != nil {
			return httperr.Upstream(err)
		}
		groups = append(groups, SourcePreferencesGroupDTO{
			SourceID:    s.ID,
			SourceName:  s.Name,
			Lang:        s.Lang,
			Preferences: toSourcePreferenceDTOs(prefs),
		})
	}
	return c.JSON(http.StatusOK, SourcePreferencesBySourceDTO{Sources: groups})
}

// SetPreference handles PATCH /api/suwayomi/extensions/:pkgName/preferences with a
// {sourceId, position, value} body. It re-reads the source's preferences to
// resolve the variant at position (so the value is sent as the correct type),
// writes the one preference, and returns the FULL refreshed list from the write's
// payload (§16 round-trip — the FE re-derives fresh positions from it). A
// validation failure (blank sourceId, missing/negative position, out-of-range
// position, or a value whose type doesn't match the variant) is a 400; any
// upstream Suwayomi failure is a 502.
func (h *Handler) SetPreference(c echo.Context) error {
	if _, err := validatePkgName(c.Param("pkgName")); err != nil {
		return err
	}
	var req PreferenceUpdateRequest
	if err := c.Bind(&req); err != nil {
		return httperr.BadRequest("invalid request body")
	}
	sourceID, position, err := validatePreferenceUpdate(req)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()

	// Read the current list to resolve the variant at this position — the write
	// must send the one typed field matching that variant. This also turns an
	// out-of-range position into a clean 400 instead of Suwayomi's raw
	// "Index: N, Size: M" 502.
	prefs, err := h.sw.SourcePreferences(ctx, sourceID)
	if err != nil {
		return httperr.Upstream(err)
	}
	if position >= len(prefs) {
		return httperr.BadRequest("position out of range")
	}
	value, err := coercePreferenceValue(prefs[position].Type, req.Value)
	if err != nil {
		return err
	}

	refreshed, err := h.sw.SetSourcePreference(ctx, sourceID, position, value)
	if err != nil {
		return httperr.Upstream(err)
	}
	return c.JSON(http.StatusOK, toSourcePreferenceDTOs(refreshed))
}
