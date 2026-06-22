// Package middleware provides Echo HTTP middleware for the Tsundoku backend.
package middleware

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
)

// OwnerIDKey is the Echo context key under which the authenticated owner's
// UUID is stored after a successful Bearer token validation.
const OwnerIDKey = "owner_id"

// RequireOwner returns an Echo middleware that validates a Bearer token using
// the given auth.Service. On success it stores the owner UUID on the Echo
// context under OwnerIDKey and calls next. On missing or invalid token it
// returns 401 Unauthorized with a JSON error body.
func RequireOwner(svc *auth.Service) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			header := c.Request().Header.Get("Authorization")
			token, ok := extractBearer(header)
			if !ok {
				return echo.NewHTTPError(http.StatusUnauthorized, "missing or invalid authorization header")
			}
			claims, err := svc.Validate(token)
			if err != nil {
				return echo.NewHTTPError(http.StatusUnauthorized, "invalid or expired token")
			}
			c.Set(OwnerIDKey, claims.OwnerID)
			return next(c)
		}
	}
}

// extractBearer pulls the token string from a "Bearer <token>" Authorization
// header value. Returns the token and true on success, empty string and false
// if the header is absent or not in Bearer format.
func extractBearer(header string) (string, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", false
	}
	tok := strings.TrimPrefix(header, prefix)
	if tok == "" {
		return "", false
	}
	return tok, true
}
