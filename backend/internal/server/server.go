package server

import (
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

// New constructs and returns a configured Echo instance with the Tsundoku
// route set registered.
//
// TODO(task-9): inject cfg *config.Config and client *ent.Client once those
// packages exist; replace the literal port in main.go with cfg.Server.Port.
func New() *echo.Echo {
	e := echo.New()
	e.HideBanner = true

	// Standard middleware applied to every request.
	e.Use(middleware.Recover())
	e.Use(middleware.Logger())

	registerRoutes(e)
	return e
}
