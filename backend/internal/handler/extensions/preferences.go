package extensions

import (
	"context"
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/handler/httperr"
	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// errNoDisabledStore is surfaced (as a 502) if SetSourceEnabled runs without a
// disabled-flag store wired — an impossible state in production (the route is
// only registered with a store), guarded so a write never silently no-ops.
var errNoDisabledStore = errors.New("disabled-source store not configured")

// errNoIgnoreScanlatorStore is surfaced (as a 502) if SetSourceIgnoreScanlator
// runs without an ignore-scanlator store wired — an impossible state in
// production (the route is only registered with a store), guarded so a write
// never silently no-ops.
var errNoIgnoreScanlatorStore = errors.New("ignore-scanlator store not configured")

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

	// The Tsundoku-side disabled set drives each group's `enabled` flag (the FE
	// hides a disabled group's preference block). A nil store ⇒ nothing disabled.
	disabled, err := h.disabledSet(ctx)
	if err != nil {
		return err
	}
	// The ignore-scanlator set drives each group's `ignoreScanlator` flag (the FE
	// seeds its per-source toggle from it). A nil store ⇒ nothing flagged.
	ignoreScanlator, err := h.ignoreScanlatorSet(ctx)
	if err != nil {
		return err
	}

	groups := make([]SourcePreferencesGroupDTO, 0, len(ext.Sources))
	for _, s := range ext.Sources {
		prefs, err := h.sw.Preferences(ctx, s.ID)
		if err != nil {
			return httperr.Upstream(err)
		}
		groups = append(groups, SourcePreferencesGroupDTO{
			SourceID:        formatSourceID(s.ID),
			SourceName:      s.Name,
			Lang:            s.Lang,
			Enabled:         !disabled[s.ID],
			IgnoreScanlator: ignoreScanlator[s.ID],
			Preferences:     toSourcePreferenceDTOs(prefs),
		})
	}
	return c.JSON(http.StatusOK, SourcePreferencesBySourceDTO{Sources: groups})
}

// disabledSet returns the Tsundoku-side set of disabled source ids, or an empty
// (non-nil) set when no disabled-flag store is wired. A store read failure is
// wrapped as a 502 (the disabled flag lives in Tsundoku's DB, but a read failure
// there is an internal upstream just like an engine failure — the group's
// enabled state can't be resolved).
func (h *Handler) disabledSet(ctx context.Context) (map[int64]bool, error) {
	if h.disabled == nil {
		return map[int64]bool{}, nil
	}
	set, err := h.disabled.Disabled(ctx)
	if err != nil {
		return nil, httperr.Upstream(err)
	}
	return set, nil
}

// SetSourceEnabled handles PATCH /api/sources/:sourceId/enabled with an
// {enabled} body — the TSUNDOKU-SIDE per-language enable/disable toggle behind
// the Configure dialog's per-source Switch. Disabling hides the source from
// Tsundoku's Discover/Search/Browse pickers (internal/imports); it does NOT stop
// refreshing a series already adopted from it, and it is NEVER pushed to the
// engine (internal/enginetopo does not read this store). Applies the write then
// RE-READS the store for the authoritative post-write state (never the request
// echo). A blank/non-numeric :sourceId or a missing `enabled` field is a 400; a
// store read/write failure is a 502.
func (h *Handler) SetSourceEnabled(c echo.Context) error {
	sourceID, err := validateSourceID(c.Param("sourceId"))
	if err != nil {
		return err
	}
	var req SourceEnabledUpdateRequest
	if err := c.Bind(&req); err != nil {
		return httperr.BadRequest("invalid request body")
	}
	enabled, err := validateSourceEnabledUpdate(req)
	if err != nil {
		return err
	}
	if h.disabled == nil {
		// The route is only registered with a wired store in production; a nil
		// store here means the deployment cannot persist the flag. Fail loud
		// rather than silently accept a write that goes nowhere.
		return httperr.Upstream(errNoDisabledStore)
	}
	ctx := c.Request().Context()

	if err := h.disabled.SetEnabled(ctx, sourceID, enabled); err != nil {
		return httperr.Upstream(err)
	}
	// Re-read the authoritative state from the store (§16 round-trip).
	set, err := h.disabled.Disabled(ctx)
	if err != nil {
		return httperr.Upstream(err)
	}
	return c.JSON(http.StatusOK, SourceEnabledDTO{
		SourceID: formatSourceID(sourceID),
		Enabled:  !set[sourceID],
	})
}

// ignoreScanlatorSet returns the Tsundoku-side set of ignore-scanlator-flagged
// source ids, or an empty (non-nil) set when no store is wired. A store read
// failure is wrapped as a 502 (the flag lives in Tsundoku's DB, but a read
// failure there is an internal upstream just like an engine failure — the
// group's flag state can't be resolved). Mirrors disabledSet.
func (h *Handler) ignoreScanlatorSet(ctx context.Context) (map[int64]bool, error) {
	if h.ignoreScanlator == nil {
		return map[int64]bool{}, nil
	}
	set, err := h.ignoreScanlator.IgnoreScanlatorSet(ctx)
	if err != nil {
		return nil, httperr.Upstream(err)
	}
	return set, nil
}

// SetSourceIgnoreScanlator handles PATCH /api/sources/:sourceId/ignore-scanlator
// with an {ignoreScanlator} body — the TSUNDOKU-SIDE per-source flag behind the
// Configure dialog's per-source toggle (sibling of the enable/disable Switch).
// Flagging a source ON collapses its per-uploader providers into one [Source]
// provider on FUTURE adopts (an uploader-in-scanlator source, e.g. Hive Scans);
// it is APPLY-FORWARD ONLY — it never migrates an already-adopted per-uploader
// series or its CBZs, and it is NEVER pushed to the engine. Applies the write
// then RE-READS the store for the authoritative post-write state (never the
// request echo). A blank/non-numeric :sourceId or a missing `ignoreScanlator`
// field is a 400; a store read/write failure is a 502.
func (h *Handler) SetSourceIgnoreScanlator(c echo.Context) error {
	sourceID, err := validateSourceID(c.Param("sourceId"))
	if err != nil {
		return err
	}
	var req SourceIgnoreScanlatorUpdateRequest
	if err := c.Bind(&req); err != nil {
		return httperr.BadRequest("invalid request body")
	}
	ignore, err := validateSourceIgnoreScanlatorUpdate(req)
	if err != nil {
		return err
	}
	if h.ignoreScanlator == nil {
		// The route is only registered with a wired store in production; a nil
		// store here means the deployment cannot persist the flag. Fail loud
		// rather than silently accept a write that goes nowhere.
		return httperr.Upstream(errNoIgnoreScanlatorStore)
	}
	ctx := c.Request().Context()

	if err := h.ignoreScanlator.SetIgnore(ctx, sourceID, ignore); err != nil {
		return httperr.Upstream(err)
	}
	// Re-read the authoritative state from the store (§16 round-trip).
	set, err := h.ignoreScanlator.IgnoreScanlatorSet(ctx)
	if err != nil {
		return httperr.Upstream(err)
	}
	dto := SourceIgnoreScanlatorDTO{
		SourceID:        formatSourceID(sourceID),
		IgnoreScanlator: set[sourceID],
	}
	// Slice B: flipping the flag ON also migrates already-adopted series (see
	// maybeCollapse). A migration failure is a 502.
	if err := h.maybeCollapse(ctx, sourceID, ignore && dto.IgnoreScanlator, &dto); err != nil {
		return httperr.Upstream(err)
	}
	return c.JSON(http.StatusOK, dto)
}

// maybeCollapse runs the Slice-B on-enable migration when the flag was flipped ON
// (flagOn) and a collapser is wired, attaching its (seriesProcessed, merged,
// skipped) summary to dto — so the owner sees what the toggle did (§16). It folds
// this source's already-adopted per-uploader providers into one [Source] provider
// and relabels their CBZs. A nil collapser keeps the apply-forward Slice-A
// behaviour (flag persisted, no migration). ONE-WAY: flipping OFF never un-merges,
// so flagOn is false there and this is a no-op.
func (h *Handler) maybeCollapse(ctx context.Context, sourceID int64, flagOn bool, dto *SourceIgnoreScanlatorDTO) error {
	if !flagOn || h.collapser == nil {
		return nil
	}
	seriesProcessed, merged, skipped, err := h.collapser.CollapseIgnoredScanlatorSource(ctx, sourceID)
	if err != nil {
		return err
	}
	dto.Migration = &ScanlatorMigrationDTO{
		SeriesProcessed: seriesProcessed,
		Merged:          merged,
		Skipped:         skipped,
	}
	return nil
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
