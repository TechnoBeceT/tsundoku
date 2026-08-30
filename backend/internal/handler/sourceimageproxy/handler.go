// Package sourceimageproxy provides the owner-authorized source-scoped image
// proxy mutation endpoint. Persistence, live-catalog validation, and atomic
// intent advancement remain in internal/sourceimageproxy; this package only
// binds, applies the committed revision, composes, and renders.
package sourceimageproxy

import (
	"context"
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/sourceconfiguration"
	proxy "github.com/technobecet/tsundoku/internal/sourceimageproxy"
	"github.com/technobecet/tsundoku/internal/sourcetransport"
)

type updateService interface {
	Update(context.Context, int64, bool) (proxy.UpdateResult, error)
}

type runtimeApplier interface {
	ApplyPending(context.Context, int64) (sourcetransport.Intent, error)
}

// ConfigurationReader loads the frozen effective-configuration response after
// a mutation. Its concrete type is supplied by the composition domain, keeping
// this handler typed without duplicating any resolution logic.
type ConfigurationReader[C any] interface {
	Get(context.Context, int64) (C, error)
}

// Handler serves one source-scoped image-proxy membership route.
type Handler[C any] struct {
	updates updateService
	runtime runtimeApplier
	configs ConfigurationReader[C]
}

// NewHandler constructs the thin handler from the owning mutation service,
// source runtime applier, and effective-configuration reader.
func NewHandler[C any](updates updateService, runtime runtimeApplier, configs ConfigurationReader[C]) *Handler[C] {
	return &Handler[C]{updates: updates, runtime: runtime, configs: configs}
}

// Update handles PUT /api/sources/:sourceId/image-proxy.
func (h *Handler[C]) Update(c echo.Context) error {
	sourceID, err := parseSourceID(c.Param("sourceId"))
	if err != nil {
		return err
	}
	request, err := decodeUpdateRequest(c.Request().Body)
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	result, err := h.updates.Update(ctx, sourceID, request.Enabled)
	if err != nil {
		return mapServiceError(err)
	}

	intent := result.Intent
	applied, _ := h.runtime.ApplyPending(ctx, sourceID)
	if applied.SourceID == sourceID && applied.DesiredRevision >= intent.DesiredRevision {
		intent = applied
	}
	configuration, err := h.configs.Get(ctx, sourceID)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, newMutationResponse(configuration, intent))
}

func mapServiceError(err error) error {
	switch {
	case errors.Is(err, proxy.ErrSourceNotFound), errors.Is(err, sourceconfiguration.ErrSourceNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "source not found")
	case errors.Is(err, proxy.ErrCatalogUnavailable), errors.Is(err, sourceconfiguration.ErrCatalogUnavailable):
		return echo.NewHTTPError(http.StatusServiceUnavailable, "source catalog unavailable")
	default:
		return err
	}
}
