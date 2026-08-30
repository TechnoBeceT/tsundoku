package network

import (
	"context"
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	configurationhandler "github.com/technobecet/tsundoku/internal/handler/sourceconfiguration"
	networksvc "github.com/technobecet/tsundoku/internal/network"
	"github.com/technobecet/tsundoku/internal/sourceconfiguration"
	"github.com/technobecet/tsundoku/internal/sourcetransport"
)

type runtimeApplier interface {
	ApplyPending(context.Context, int64) (sourcetransport.Intent, error)
}

type configurationReader interface {
	Get(context.Context, int64) (configurationhandler.ConfigurationDTO, error)
}

// Handler holds the dependencies for the network-routing HTTP handlers. All
// business logic lives in network.Service; the handler is thin.
//
// onChange is the legacy best-effort write-through hook for endpoint changes.
// A fully composed binding handler uses runtime instead, which applies its exact
// committed revision synchronously. The hook remains nil-safe.
type Handler struct {
	svc      *networksvc.Service
	onChange func()
	runtime  runtimeApplier
	configs  configurationReader
}

// NewHandler constructs a Handler bound to a network.Service. onChange may be
// nil when endpoint write-through is not available.
func NewHandler(svc *networksvc.Service, onChange func()) *Handler {
	return &Handler{svc: svc, onChange: onChange}
}

// WithSourceRuntime attaches the shared source-runtime reconciler and the
// canonical effective-configuration DTO reader used by binding mutations.
func (h *Handler) WithSourceRuntime(runtime runtimeApplier, configs configurationReader) *Handler {
	h.runtime = runtime
	h.configs = configs
	return h
}

// notifyChanged fires the write-through hook when one is wired. Best-effort: the
// mutation has already succeeded, so a routing re-derive is a background
// side-effect that must never affect the HTTP response.
func (h *Handler) notifyChanged() {
	if h.onChange != nil {
		h.onChange()
	}
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
	h.notifyChanged()
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
	h.notifyChanged()
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
	h.notifyChanged()
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

// SetBinding handles PUT /api/network/bindings/:sourceId. It commits the
// binding and source runtime intent together, synchronously attempts the exact
// committed revision, then returns the frozen mutation response.
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
	ctx := c.Request().Context()
	result, err := h.svc.SetBinding(ctx, sourceID, in)
	if err != nil {
		return mapServiceError(err)
	}
	if h.runtime == nil || h.configs == nil {
		h.notifyChanged()
		return c.JSON(http.StatusOK, result.BindingDTO)
	}
	intent := h.applyCommitted(ctx, sourceID, result)
	configuration, err := h.configs.Get(ctx, sourceID)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, newMutationResponse(configuration, intent))
}

// ClearBinding handles DELETE /api/network/bindings/:sourceId. An actual
// deletion advances intent and returns the refreshed effective configuration;
// a source with no binding retains the existing 404 no-op contract.
func (h *Handler) ClearBinding(c echo.Context) error {
	sourceID, err := parseSourceID(c.Param("sourceId"))
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	result, err := h.svc.ClearBinding(ctx, sourceID)
	if err != nil {
		return mapServiceError(err)
	}
	if h.runtime == nil || h.configs == nil {
		h.notifyChanged()
		return c.NoContent(http.StatusNoContent)
	}
	intent := h.applyCommitted(ctx, sourceID, result)
	configuration, err := h.configs.Get(ctx, sourceID)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, newMutationResponse(configuration, intent))
}

func (h *Handler) applyCommitted(ctx context.Context, sourceID int64, result networksvc.BindingMutationResult) sourcetransport.Intent {
	intent := result.Intent
	applied, _ := h.runtime.ApplyPending(ctx, sourceID)
	if applied.SourceID != sourceID {
		return intent
	}
	if !result.Changed || applied.DesiredRevision == result.Intent.DesiredRevision {
		return applied
	}
	return intent
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
	case errors.Is(err, sourcetransport.ErrSourceNotFound),
		errors.Is(err, sourceconfiguration.ErrSourceNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "source not found")
	case errors.Is(err, sourcetransport.ErrCatalogUnavailable),
		errors.Is(err, sourceconfiguration.ErrCatalogUnavailable):
		return echo.NewHTTPError(http.StatusServiceUnavailable, "source catalog unavailable")
	case errors.Is(err, networksvc.ErrEndpointInUse):
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	default:
		return err
	}
}
