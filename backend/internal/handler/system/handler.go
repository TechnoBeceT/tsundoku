package system

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/technobecet/tsundoku/internal/config"
)

// Handler handles GET /api/system — read-only env-structural information.
type Handler struct {
	cfg *config.Config
}

// NewHandler returns a new system Handler backed by the given config.
// The handler never reads os.Getenv; all values are sourced from the
// injected *config.Config (the sole env boundary).
func NewHandler(cfg *config.Config) *Handler {
	return &Handler{cfg: cfg}
}

// Get returns a credential-free summary of structural configuration.
// GET /api/system (behind mw.RequireOwner).
//
// The database field is "host:port/name" only — credentials are never
// included so the response is safe to render in the owner UI.
func (h *Handler) Get(c echo.Context) error {
	return c.JSON(http.StatusOK, SystemDTO{
		StorageFolder: h.cfg.Storage.Folder,
		ServerPort:    h.cfg.Server.Port,
		Database:      h.cfg.Database.Host + ":" + h.cfg.Database.Port + "/" + h.cfg.Database.Name,
	})
}
