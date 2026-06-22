package server

import "github.com/labstack/echo/v4"

// registerRoutes wires all HTTP routes onto the provided Echo instance.
// Additional route groups (API, SSE, docs) are registered in subsequent tasks.
func registerRoutes(e *echo.Echo) {
	e.GET("/health", HealthCheck)
}
