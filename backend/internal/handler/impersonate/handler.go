// Package impersonate holds the thin HTTP handlers for Tsundoku's OWN
// impersonate-gateway settings (GAP-111): the Chrome-fingerprint image-fetch
// gateway toggle + URL + its per-source gating set (GAP-131), runtime settings
// on Tsundoku's own settings overlay — never an env var, never read from the
// download engine. GET/PUT read + write that overlay via settings.Service; the
// service converges runtime-setting commits through the shared engine lifecycle
// so this handler never writes engine config directly.
//
// It mirrors handler/flaresolverr one-for-one (GET reads the overlay; the
// mutating verb saves, requests service-owned convergence, and returns the
// persisted state), just over the three-field impersonate group instead of the
// six-field FlareSolverr group.
//
// The gating set crosses this boundary as STRINGIFIED numeric source ids
// (matching the Source schema's own 64-bit-int-as-string convention) and is
// converted to int64 here; a source NAME never appears on any wire — see
// settings.KeyImpersonateSources for why.
package impersonate

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	settingssvc "github.com/technobecet/tsundoku/internal/settings"
)

// Handler serves the Tsundoku-owned impersonate-gateway settings endpoints.
type Handler struct {
	settings *settingssvc.Service
}

// NewHandler constructs a Handler bound to the Tsundoku settings service.
func NewHandler(settings *settingssvc.Service) *Handler {
	return &Handler{settings: settings}
}

// Get handles GET /api/impersonate — returns the Tsundoku-owned
// impersonate-gateway values (toggle, URL, per-source gating set). Never touches the engine host (a pure
// Tsundoku-settings read).
func (h *Handler) Get(c echo.Context) error {
	return c.JSON(http.StatusOK, currentDTO(c.Request().Context(), h.settings))
}

// Update handles PUT /api/impersonate. It validates + saves a partial update to
// Tsundoku's own settings overlay (all-or-nothing, same fail-closed contract as
// settings.Service.SetMany). Runtime convergence is the service's post-commit
// responsibility and remains best-effort, preserving the endpoint's persisted-
// success behavior when the engine is unavailable. Returns the freshly-saved
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
	return c.JSON(http.StatusOK, dto)
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
