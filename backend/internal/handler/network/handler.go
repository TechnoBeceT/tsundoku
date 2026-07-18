package network

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	networksvc "github.com/technobecet/tsundoku/internal/network"
)

// Handler holds the dependencies for the network-routing HTTP handlers. All
// business logic lives in network.Service; the handler is thin. This slice is
// DB-truth only — no engine client is involved.
type Handler struct {
	svc *networksvc.Service
}

// NewHandler constructs a Handler bound to a network.Service.
func NewHandler(svc *networksvc.Service) *Handler {
	return &Handler{svc: svc}
}

// ListEndpoints handles GET /api/network/endpoints — every endpoint (passwords
// omitted), ordered by name.
func (h *Handler) ListEndpoints(c echo.Context) error {
	out, err := h.svc.ListEndpoints(c.Request().Context())
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, out)
}

// CreateEndpoint handles POST /api/network/endpoints. It creates an endpoint
// from the request body and returns 201 with the persisted DTO (§16). An
// invalid endpoint yields 400.
func (h *Handler) CreateEndpoint(c echo.Context) error {
	var req CreateEndpointRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	out, err := h.svc.CreateEndpoint(c.Request().Context(), req.toInput())
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusCreated, out)
}

// UpdateEndpoint handles PATCH /api/network/endpoints/:id — a partial update.
// On success it returns 200 with the updated DTO (§16). A missing id yields
// 404; an invalid merged endpoint yields 400.
func (h *Handler) UpdateEndpoint(c echo.Context) error {
	id, err := validateID(c.Param("id"))
	if err != nil {
		return err
	}
	var req UpdateEndpointRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	out, err := h.svc.UpdateEndpoint(c.Request().Context(), id, req.toPatch())
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, out)
}

// DeleteEndpoint handles DELETE /api/network/endpoints/:id. It removes an
// endpoint only when no binding references it (else 409, listing the
// referencing sources). Returns 204 on success; a missing id yields 404.
func (h *Handler) DeleteEndpoint(c echo.Context) error {
	id, err := validateID(c.Param("id"))
	if err != nil {
		return err
	}
	if err := h.svc.DeleteEndpoint(c.Request().Context(), id); err != nil {
		return mapServiceError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// ListBindings handles GET /api/network/bindings — every per-source binding
// (drives the assignment table). Unbound sources simply have no row.
func (h *Handler) ListBindings(c echo.Context) error {
	out, err := h.svc.ListBindings(c.Request().Context())
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, out)
}

// SetBinding handles PUT /api/network/sources/:sourceId/binding — upsert the
// source's binding. On success it returns 200 with the persisted DTO (§16). A
// malformed sourceId/UUID or an invalid binding yields 400.
func (h *Handler) SetBinding(c echo.Context) error {
	sourceID, err := parseSourceID(c.Param("sourceId"))
	if err != nil {
		return err
	}
	var req SetBindingRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	in, err := req.toInput()
	if err != nil {
		return err
	}
	out, err := h.svc.SetBinding(c.Request().Context(), sourceID, in)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, out)
}

// ClearBinding handles DELETE /api/network/sources/:sourceId/binding — revert
// the source to the global default. Returns 204 on success; a source with no
// binding yields 404.
func (h *Handler) ClearBinding(c echo.Context) error {
	sourceID, err := parseSourceID(c.Param("sourceId"))
	if err != nil {
		return err
	}
	if err := h.svc.ClearBinding(c.Request().Context(), sourceID); err != nil {
		return mapServiceError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// mapServiceError translates a network.Service sentinel into the matching HTTP
// status, leaving any unexpected error to the central middleware as a 500.
// ErrEndpointNotFound / ErrBindingNotFound → 404; ErrInvalidEndpoint /
// ErrInvalidBinding → 400 (the wrapped message names the offending field);
// ErrEndpointInUse → 409 (the message lists the referencing sources).
func mapServiceError(err error) error {
	switch {
	case errors.Is(err, networksvc.ErrEndpointNotFound),
		errors.Is(err, networksvc.ErrBindingNotFound):
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	case errors.Is(err, networksvc.ErrInvalidEndpoint),
		errors.Is(err, networksvc.ErrInvalidBinding):
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	case errors.Is(err, networksvc.ErrEndpointInUse):
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	default:
		return err
	}
}
