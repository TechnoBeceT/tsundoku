// Package settings holds the thin HTTP handlers for the runtime-tunable settings
// API. All business logic (the allowlist, validation, persistence) lives in the
// settings.Service; the handler only binds, validates the request shape, calls
// the service, and renders the DTO.
package settings

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	settingssvc "github.com/technobecet/tsundoku/internal/settings"
)

// Handler holds the dependencies for the settings HTTP handlers.
type Handler struct {
	svc *settingssvc.Service
}

// NewHandler constructs a Handler bound to a settings.Service.
func NewHandler(svc *settingssvc.Service) *Handler {
	return &Handler{svc: svc}
}

// List handles GET /api/settings. It returns the whole tunable allowlist (each
// key with its current resolved value, default, type, and unit) in stable order.
func (h *Handler) List(c echo.Context) error {
	return c.JSON(http.StatusOK, h.svc.List(c.Request().Context()))
}

// Update handles PATCH /api/settings with body {"settings":[{"key","value"}]}.
// Validation is all-or-nothing: an unknown key or an out-of-bounds value rejects
// the WHOLE batch with 400 naming the offending key (no partial write lands). On
// success it returns 200 with the updated settings list so the caller sees the
// persisted values without a refetch (§16 round-trip).
func (h *Handler) Update(c echo.Context) error {
	var req UpdateRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	updates, err := validateUpdate(req)
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	if err := h.svc.SetMany(ctx, updates); err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, h.svc.List(ctx))
}

// mapServiceError translates a settings.Service sentinel into the matching HTTP
// status. Both ErrUnknownSetting and ErrInvalidSetting are owner input errors →
// 400; the error message already names the offending key, so it is surfaced
// verbatim. Anything else falls through to the central middleware as a 500.
func mapServiceError(err error) error {
	switch {
	case errors.Is(err, settingssvc.ErrUnknownSetting),
		errors.Is(err, settingssvc.ErrInvalidSetting):
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	default:
		return err
	}
}
