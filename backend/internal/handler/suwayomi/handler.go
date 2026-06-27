// Package suwayomi holds the thin HTTP handlers for the Suwayomi server-settings
// proxy. It exposes the FlareSolverr (Cloudflare-bypass) + SOCKS-proxy subset of
// Suwayomi's own server-global settings so the owner never has to open
// Suwayomi's UI. It is a PURE passthrough: no Tsundoku schema, no disk, no SSE —
// the values live on whichever Suwayomi the client targets (embed or external).
//
// The handler owns a suwayomi.Client directly (like the series cover proxy) and
// does bind → validate → client → DTO; the GraphQL logic lives in the client's
// settings.go. Validation is extracted to validate.go; the DTO mapping to dto.go.
package suwayomi

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/handler/httperr"
	suwayomicli "github.com/technobecet/tsundoku/internal/suwayomi"
)

// Handler serves the Suwayomi server-settings proxy endpoints. It holds the
// Suwayomi client whose BaseURL() targets the active (embedded or external)
// Suwayomi instance.
type Handler struct {
	sw suwayomicli.Client
}

// NewHandler constructs a Handler bound to a Suwayomi client.
func NewHandler(sw suwayomicli.Client) *Handler {
	return &Handler{sw: sw}
}

// Get handles GET /api/suwayomi/settings. It reads the current FlareSolverr +
// SOCKS subset from Suwayomi and returns it as a SuwayomiSettingsDTO. An upstream
// failure (Suwayomi unreachable or a GraphQL error) is a 502 Bad Gateway.
func (h *Handler) Get(c echo.Context) error {
	settings, err := h.sw.ServerSettings(c.Request().Context())
	if err != nil {
		return httperr.Upstream(err)
	}
	return c.JSON(http.StatusOK, toDTO(settings))
}

// Update handles PATCH /api/suwayomi/settings. It validates a partial update,
// applies it via Suwayomi's setSettings mutation (only the provided fields are
// sent, so no unset setting is clobbered), then RE-READS the settings and
// returns the refreshed DTO so the caller observes the authoritative persisted
// state (§16 round-trip). A validation failure is a 400; an upstream failure is
// a 502.
func (h *Handler) Update(c echo.Context) error {
	var req UpdateRequest
	if err := c.Bind(&req); err != nil {
		return httperr.BadRequest("invalid request body")
	}
	patch, err := validateUpdate(req)
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	if err := h.sw.SetServerSettings(ctx, patch); err != nil {
		return httperr.Upstream(err)
	}
	settings, err := h.sw.ServerSettings(ctx)
	if err != nil {
		return httperr.Upstream(err)
	}
	return c.JSON(http.StatusOK, toDTO(settings))
}
