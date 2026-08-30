// Package sourcetransport provides the owner-authorized source transport
// mutation endpoint. Policy persistence, resolution, and intent advancement
// remain in the owning sourcetransport service; this package only binds,
// validates, applies the committed revision, composes, and renders.
package sourcetransport

import (
	"context"
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/sourceconfiguration"
	transport "github.com/technobecet/tsundoku/internal/sourcetransport"
)

type updateService interface {
	Update(context.Context, int64, transport.Patch) (transport.UpdateResult, error)
}

type runtimeApplier interface {
	ApplyPending(context.Context, int64) (transport.Intent, error)
}

// ConfigurationReader composes the frozen effective configuration after a
// committed mutation without duplicating configuration resolution here.
type ConfigurationReader[C any] interface {
	Get(context.Context, int64) (C, error)
}

// Handler serves the source-scoped transport policy route.
type Handler[C any] struct {
	updates updateService
	runtime runtimeApplier
	configs ConfigurationReader[C]
}

// NewHandler constructs the thin transport mutation handler.
func NewHandler[C any](updates updateService, runtime runtimeApplier, configs ConfigurationReader[C]) *Handler[C] {
	return &Handler[C]{updates: updates, runtime: runtime, configs: configs}
}

// Update handles PATCH /api/sources/:sourceId/transport.
func (h *Handler[C]) Update(c echo.Context) error {
	sourceID, err := parseSourceID(c.Param("sourceId"))
	if err != nil {
		return err
	}
	patch, err := decodePatch(c.Request().Body)
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	result, err := h.updates.Update(ctx, sourceID, patch)
	// The owning service returns its populated, still-pending intent alongside a
	// synchronous apply error. Persistence has committed in that case, so render
	// the durable pending state and leave its retry to the normal reconciler.
	committedPending := result.Intent.SourceID == sourceID && result.Intent.DesiredRevision > result.Intent.AppliedRevision
	if err != nil && !committedPending {
		return mapServiceError(err)
	}

	intent := result.Intent
	if err == nil {
		applied, _ := h.runtime.ApplyPending(ctx, sourceID)
		if applied.SourceID == sourceID && applied.DesiredRevision == intent.DesiredRevision {
			intent = applied
		}
	}
	configuration, err := h.configs.Get(ctx, sourceID)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, newMutationResponse(configuration, intent))
}

func mapServiceError(err error) error {
	switch {
	case errors.Is(err, transport.ErrInvalidPolicy):
		return echo.NewHTTPError(http.StatusBadRequest, "invalid source transport policy")
	case errors.Is(err, transport.ErrSourceNotFound), errors.Is(err, sourceconfiguration.ErrSourceNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "source not found")
	case errors.Is(err, transport.ErrCatalogUnavailable), errors.Is(err, sourceconfiguration.ErrCatalogUnavailable):
		return echo.NewHTTPError(http.StatusServiceUnavailable, "source catalog unavailable")
	default:
		return err
	}
}
