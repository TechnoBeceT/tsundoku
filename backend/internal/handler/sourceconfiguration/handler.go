// Package sourceconfiguration provides owner-authorized source-configuration
// reads and maps the transport-independent composition domain to the frozen API.
package sourceconfiguration

import (
	"context"
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	configuration "github.com/technobecet/tsundoku/internal/sourceconfiguration"
)

type configurationService interface {
	Get(context.Context, int64) (configuration.Configuration, error)
	Exceptions(context.Context) ([]configuration.Summary, error)
}

type configurationGetter interface {
	Get(context.Context, int64) (configuration.Configuration, error)
}

// DTOReader maps the composition domain onto the frozen effective-configuration
// DTO. It is also the typed post-mutation composer used by sibling handlers.
type DTOReader struct{ service configurationGetter }

// NewDTOReader constructs the shared post-mutation configuration reader.
func NewDTOReader(service configurationGetter) *DTOReader { return &DTOReader{service: service} }

// Get composes and maps one source's effective configuration DTO.
func (r *DTOReader) Get(ctx context.Context, sourceID int64) (ConfigurationDTO, error) {
	value, err := r.service.Get(ctx, sourceID)
	if err != nil {
		return ConfigurationDTO{}, err
	}
	return newConfigurationDTO(value), nil
}

// Handler serves the effective-configuration and exception-summary routes.
type Handler struct {
	service configurationService
	reader  *DTOReader
}

// NewHandler constructs the effective-configuration read handler.
func NewHandler(service configurationService) *Handler {
	return &Handler{service: service, reader: NewDTOReader(service)}
}

// Effective handles GET /api/sources/:sourceId/effective-configuration.
func (h *Handler) Effective(c echo.Context) error {
	sourceID, err := parseSourceID(c.Param("sourceId"))
	if err != nil {
		return err
	}
	value, err := h.reader.Get(c.Request().Context(), sourceID)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, value)
}

// Exceptions handles GET /api/sources/exceptions.
func (h *Handler) Exceptions(c echo.Context) error {
	values, err := h.service.Exceptions(c.Request().Context())
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, newSummaryDTOs(values))
}

func mapServiceError(err error) error {
	switch {
	case errors.Is(err, configuration.ErrSourceNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "source not found")
	case errors.Is(err, configuration.ErrCatalogUnavailable):
		return echo.NewHTTPError(http.StatusServiceUnavailable, "source catalog unavailable")
	default:
		return err
	}
}
