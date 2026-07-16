package extensions

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/handler/httperr"
	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// This file (preferences.go) holds the per-source preference endpoints of the
// extension-management proxy — the "Configure" gear for an installed extension.
// Like the rest of the package it is a PURE passthrough: no Tsundoku state; the
// preferences live on the engine host.
//
// KEY-ADDRESSED (QCAT-253, P2 Suwayomi-removal slice 5): a preference write is
// now addressed by its stable KEY, not a 0-based array position — the retired
// Suwayomi shape required a fresh read before every write to resolve the
// current position (the array order could shift server-side); the engine
// host's SetPreferences has no such caveat.

// Preferences handles GET /api/suwayomi/extensions/:pkgName/preferences. It
// resolves the extension's sources (embedded on Extensions()'s own response —
// no separate lookup call, unlike the retired Suwayomi shape) and returns each
// source's configurable preferences grouped by source. A blank pkgName is a
// 400; an unknown pkgName is a 404; any upstream engine failure is a 502.
func (h *Handler) Preferences(c echo.Context) error {
	pkgName, err := validatePkgName(c.Param("pkgName"))
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	exts, err := h.sw.Extensions(ctx)
	if err != nil {
		return httperr.Upstream(err)
	}
	ext, ok := findExtension(exts, pkgName)
	if !ok {
		return echo.NewHTTPError(http.StatusNotFound, "extension not found")
	}

	groups := make([]SourcePreferencesGroupDTO, 0, len(ext.Sources))
	for _, s := range ext.Sources {
		prefs, err := h.sw.Preferences(ctx, s.ID)
		if err != nil {
			return httperr.Upstream(err)
		}
		groups = append(groups, SourcePreferencesGroupDTO{
			SourceID:    formatSourceID(s.ID),
			SourceName:  s.Name,
			Lang:        s.Lang,
			Preferences: toSourcePreferenceDTOs(prefs),
		})
	}
	return c.JSON(http.StatusOK, SourcePreferencesBySourceDTO{Sources: groups})
}

// SetPreference handles PATCH /api/suwayomi/extensions/:pkgName/preferences with a
// {sourceId, key, value} body. It re-reads the source's preferences to resolve
// the variant at that key (so the value is coerced to the correct JSON-native
// type), writes the one preference via a single-key SetPreferences batch, and
// returns the FULL refreshed list from the write's payload (§16 round-trip). A
// validation failure (blank/unparseable sourceId, blank key, an unknown key, or
// a value whose type doesn't match the variant) is a 400; any upstream engine
// failure is a 502.
func (h *Handler) SetPreference(c echo.Context) error {
	if _, err := validatePkgName(c.Param("pkgName")); err != nil {
		return err
	}
	var req PreferenceUpdateRequest
	if err := c.Bind(&req); err != nil {
		return httperr.BadRequest("invalid request body")
	}
	sourceID, key, err := validatePreferenceUpdate(req)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()

	// Read the current list to resolve the variant at this key — the write must
	// send the correctly-typed value. This also turns an unknown key into a
	// clean 400 instead of the engine host's raw "unknown preference key" 502.
	prefs, err := h.sw.Preferences(ctx, sourceID)
	if err != nil {
		return httperr.Upstream(err)
	}
	pref, ok := findPreferenceByKey(prefs, key)
	if !ok {
		return httperr.BadRequest("unknown preference key")
	}
	value, err := coercePreferenceValue(pref.Type, req.Value)
	if err != nil {
		return err
	}

	refreshed, err := h.sw.SetPreferences(ctx, sourceID, map[string]any{key: value})
	if err != nil {
		return httperr.Upstream(err)
	}
	return c.JSON(http.StatusOK, toSourcePreferenceDTOs(refreshed))
}

// findPreferenceByKey returns the preference in prefs whose Key matches key.
func findPreferenceByKey(prefs []sourceengine.Preference, key string) (sourceengine.Preference, bool) {
	for _, p := range prefs {
		if p.Key == key {
			return p, true
		}
	}
	return sourceengine.Preference{}, false
}
