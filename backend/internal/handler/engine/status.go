package engine

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/enginetopo"
)

// TopologyStatus handles GET /api/engine/topology-status. It returns a read-only
// snapshot of how much of the engine topology Tsundoku has captured into its own
// durable store (repos, extensions cached, sources with preferences, provider
// urls resolved) plus human-readable gap notes. Every number comes from a DB
// count — no engine call is made — so an empty database is a valid, zeroed 200
// response rather than an error.
func (h *Handler) TopologyStatus(c echo.Context) error {
	status, err := enginetopo.TopologyStatus(c.Request().Context(), h.db)
	if err != nil {
		// A DB count failure is a genuine server error → central middleware 500.
		return err
	}
	return c.JSON(http.StatusOK, toTopologyStatusDTO(status))
}
