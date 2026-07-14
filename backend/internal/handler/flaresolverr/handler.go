// Package flaresolverr holds the thin HTTP handlers for Tsundoku's OWN
// FlareSolverr (Cloudflare-bypass) settings (QCAT-238, owner-ratified
// 2026-07-14): a runtime setting on Tsundoku's own settings overlay — never
// an env var, never read from Suwayomi. GET/PATCH read + write that overlay
// via settings.Service; PATCH additionally best-effort MIRRORS the saved
// values down to Suwayomi's own settings (via the existing Suwayomi
// settings-proxy client) so Suwayomi's source-scraping stays in sync while
// Suwayomi still exists — a mirror failure never fails the Tsundoku save.
package flaresolverr

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v4"

	settingssvc "github.com/technobecet/tsundoku/internal/settings"
	suwayomicli "github.com/technobecet/tsundoku/internal/suwayomi"
)

// Handler serves the Tsundoku-owned FlareSolverr settings endpoints.
type Handler struct {
	settings *settingssvc.Service
	sw       suwayomicli.Client
}

// NewHandler constructs a Handler bound to the Tsundoku settings service (the
// source of truth) and a Suwayomi client (the best-effort mirror target).
func NewHandler(settings *settingssvc.Service, sw suwayomicli.Client) *Handler {
	return &Handler{settings: settings, sw: sw}
}

// Get handles GET /api/flaresolverr/settings — returns the six Tsundoku-owned
// FlareSolverr values. Never touches Suwayomi (a pure Tsundoku-settings read).
func (h *Handler) Get(c echo.Context) error {
	return c.JSON(http.StatusOK, currentDTO(c.Request().Context(), h.settings))
}

// Update handles PATCH /api/flaresolverr/settings. It validates + saves a
// partial update to Tsundoku's own settings overlay (all-or-nothing, same
// fail-closed contract as settings.Service.SetMany), THEN best-effort mirrors
// the full resulting state down to Suwayomi via SetServerSettings — a mirror
// failure (Suwayomi down, GraphQL error, ...) is logged and swallowed, NEVER
// fails this request, since the Tsundoku save already succeeded and Tsundoku
// no longer depends on Suwayomi to function. Returns the freshly-saved
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
	h.mirrorToSuwayomi(ctx, dto)
	return c.JSON(http.StatusOK, dto)
}

// mirrorToSuwayomi best-effort pushes the just-saved Tsundoku FlareSolverr
// state down to Suwayomi's own settings, so Suwayomi's source-scraping keeps
// using the same clearance config while Suwayomi still exists (QCAT-238).
// Sends the FULL current state (not just the fields this PATCH touched) so a
// partial Tsundoku update still leaves Suwayomi fully in sync. Never returns
// an error — a Suwayomi-down mirror failure is logged and swallowed.
func (h *Handler) mirrorToSuwayomi(ctx context.Context, dto SettingsDTO) {
	enabled, url, timeout := dto.Enabled, dto.URL, dto.Timeout
	sessionName, sessionTTL, fallback := dto.SessionName, dto.SessionTTL, dto.AsResponseFallback
	patch := suwayomicli.SuwayomiSettingsPatch{
		FlareSolverrEnabled:            &enabled,
		FlareSolverrURL:                &url,
		FlareSolverrTimeout:            &timeout,
		FlareSolverrSessionName:        &sessionName,
		FlareSolverrSessionTTL:         &sessionTTL,
		FlareSolverrAsResponseFallback: &fallback,
	}
	if err := h.sw.SetServerSettings(ctx, patch); err != nil {
		slog.WarnContext(ctx, "flaresolverr: mirror to Suwayomi failed (Tsundoku save already persisted)", "err", err)
	}
}

// mapServiceError translates a settings.Service sentinel into the matching
// HTTP status — mirrors handler/settings's own mapServiceError. Both
// ErrUnknownSetting and ErrInvalidSetting are owner input errors → 400 (the
// message already names the offending key); anything else falls through to
// the central middleware as a 500.
func mapServiceError(err error) error {
	switch {
	case errors.Is(err, settingssvc.ErrUnknownSetting),
		errors.Is(err, settingssvc.ErrInvalidSetting):
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	default:
		return err
	}
}
