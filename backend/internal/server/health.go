// Package server provides the Echo HTTP server construction and route wiring
// for the Tsundoku backend.
package server

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// HealthCheck handles GET /health and returns HTTP 200 with {"status":"ok"}.
// It is intentionally dependency-free so it can be tested without a full
// server stack.
func HealthCheck(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}
