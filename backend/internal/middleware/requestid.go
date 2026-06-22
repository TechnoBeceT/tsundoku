// Package middleware — see auth.go for package-level documentation.
package middleware

import (
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// RequestIDHeader is the HTTP header used to propagate request identifiers
// both inbound (client-supplied) and outbound (server-assigned).
const RequestIDHeader = "X-Request-Id"

// RequestIDKey is the Echo context key under which the request ID string is
// stored so that handlers and other middleware can retrieve it without
// re-parsing the response header.
const RequestIDKey = "request_id"

// RequestID returns an Echo middleware that attaches a unique request identifier
// to every request/response cycle.
//
// If the incoming request already carries an X-Request-Id header its value is
// reused; otherwise a new UUID v4 is generated. The identifier is:
//   - stored on the Echo context under RequestIDKey for downstream handlers,
//   - echoed back in the X-Request-Id response header so clients can correlate
//     log lines with their own request records.
func RequestID() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			rid := c.Request().Header.Get(RequestIDHeader)
			if rid == "" {
				rid = uuid.New().String()
			}
			c.Set(RequestIDKey, rid)
			c.Response().Header().Set(RequestIDHeader, rid)
			return next(c)
		}
	}
}
