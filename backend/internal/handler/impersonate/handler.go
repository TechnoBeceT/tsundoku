// Package impersonate holds the thin HTTP handlers for Tsundoku's OWN
// impersonate-gateway settings (GAP-111): the Chrome-fingerprint image-fetch
// gateway toggle + URL + its per-source gating set (GAP-131), runtime settings
// on Tsundoku's own settings overlay — never an env var, never read from the
// download engine. GET/PUT read + write that overlay via settings.Service; PUT
// additionally best-effort MIRRORS the saved values down to the engine host's
// own impersonate config (via sourceengine.Client.SetImpersonate) so the
// engine's image fetches use the gateway — a mirror failure never fails the
// Tsundoku save.
//
// It mirrors handler/flaresolverr one-for-one (GET reads the overlay, the
// mutating verb saves + best-effort mirrors + returns the persisted state), just
// over the three-field impersonate group instead of the six-field FlareSolverr
// group.
//
// The gating set crosses this boundary as STRINGIFIED numeric source ids
// (matching the Source schema's own 64-bit-int-as-string convention) and is
// converted to int64 here; a source NAME never appears on any wire — see
// settings.KeyImpersonateSources for why.
package impersonate

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v4"

	settingssvc "github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// Handler serves the Tsundoku-owned impersonate-gateway settings endpoints.
type Handler struct {
	settings *settingssvc.Service
	engine   sourceengine.Client
}

// NewHandler constructs a Handler bound to the Tsundoku settings service (the
// source of truth) and the engine-host client (the best-effort mirror target).
func NewHandler(settings *settingssvc.Service, engine sourceengine.Client) *Handler {
	return &Handler{settings: settings, engine: engine}
}

// Get handles GET /api/impersonate — returns the Tsundoku-owned
// impersonate-gateway values (toggle, URL, per-source gating set). Never touches the engine host (a pure
// Tsundoku-settings read).
func (h *Handler) Get(c echo.Context) error {
	return c.JSON(http.StatusOK, currentDTO(c.Request().Context(), h.settings))
}

// Update handles PUT /api/impersonate. It validates + saves a partial update to
// Tsundoku's own settings overlay (all-or-nothing, same fail-closed contract as
// settings.Service.SetMany), THEN best-effort mirrors the full resulting state
// down to the engine host via sourceengine.Client.SetImpersonate — a mirror
// failure (engine down, RPC error, ...) is logged and swallowed, NEVER fails
// this request, since the Tsundoku save already succeeded and Tsundoku owns this
// setting regardless of the engine's reachability. Returns the freshly-saved
// Tsundoku settings (§16 round-trip).
func (h *Handler) Update(c echo.Context) error {
	var req UpdateRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	updates, err := buildUpdates(req)
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	if err := h.settings.SetMany(ctx, updates); err != nil {
		return mapServiceError(err)
	}

	dto := currentDTO(ctx, h.settings)
	h.mirrorToEngine(ctx, dto, h.settings.ImpersonateSources(ctx))
	return c.JSON(http.StatusOK, dto)
}

// mirrorToEngine best-effort pushes the just-saved Tsundoku impersonate state
// down to the engine host's own impersonate config, so the engine's image
// fetches use the same gateway. Sends the FULL current state (not just the
// fields this PUT touched) so a partial Tsundoku update still leaves the engine
// fully in sync — including an EMPTY gating set, which is a meaningful value
// ("no source uses the gateway") and must actively clear a stale engine-side
// selection. Never returns an error — an engine-down mirror failure is logged
// and swallowed; reconcile-on-boot re-pushes it anyway (the durable settings are
// the truth).
func (h *Handler) mirrorToEngine(ctx context.Context, dto SettingsDTO, sourceIDs []int64) {
	enabled, url := dto.Enabled, dto.URL
	if sourceIDs == nil {
		sourceIDs = []int64{}
	}
	patch := sourceengine.ImpersonatePatch{
		Enabled:   &enabled,
		URL:       &url,
		SourceIDs: &sourceIDs,
	}
	if _, err := h.engine.SetImpersonate(ctx, patch); err != nil {
		slog.WarnContext(ctx, "impersonate: mirror to engine host failed (Tsundoku save already persisted)", "err", err)
	}
}

// mapServiceError translates a settings.Service sentinel into the matching HTTP
// status — mirrors handler/flaresolverr's own mapServiceError. Both
// ErrUnknownSetting and ErrInvalidSetting are owner input errors → 400 (the
// message already names the offending key); anything else falls through to the
// central middleware as a 500.
func mapServiceError(err error) error {
	switch {
	case errors.Is(err, settingssvc.ErrUnknownSetting),
		errors.Is(err, settingssvc.ErrInvalidSetting):
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	default:
		return err
	}
}
