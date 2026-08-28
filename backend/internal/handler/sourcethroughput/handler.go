package sourcethroughput

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v4"
	policy "github.com/technobecet/tsundoku/internal/sourcethroughput"
)

type service interface {
	Snapshot(context.Context) (map[int64]policy.Override, error)
	Defaults(context.Context) policy.Effective
	Update(context.Context, int64, policy.Patch) (policy.Override, error)
}

// Handler exposes owner-controlled per-source throughput policy.
type Handler struct{ svc service }

// NewHandler constructs a source-throughput policy handler.
func NewHandler(svc service) *Handler { return &Handler{svc: svc} }

// List handles GET /api/sources/throughput.
func (h *Handler) List(c echo.Context) error {
	ctx := c.Request().Context()
	snapshot, err := h.svc.Snapshot(ctx)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, newListResponse(h.svc.Defaults(ctx), snapshot))
}

// Update handles PATCH /api/sources/:sourceId/throughput.
func (h *Handler) Update(c echo.Context) error {
	id, err := parseSourceID(c.Param("sourceId"))
	if err != nil {
		return err
	}
	var request updateRequest
	if err := c.Bind(&request); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	patch, err := request.toPatch()
	if err != nil {
		return err
	}
	stored, err := h.svc.Update(c.Request().Context(), id, patch)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, newSourcePolicyDTO(id, h.svc.Defaults(c.Request().Context()), stored))
}
