// Package middleware provides Echo HTTP middleware for the Tsundoku backend.
package middleware

import (
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/pkg/authcookie"
)

// OwnerIDKey is the Echo context key under which the authenticated owner's
// UUID is stored after a successful token validation.
const OwnerIDKey = "owner_id"

// RequireOwner returns an Echo middleware that validates the session token, then
// calls next. The token is read from the tsundoku_session cookie first; if
// absent or empty the Authorization: Bearer header is used as a fallback (so
// scripts and curl keep working). On success the owner UUID is stored on the
// Echo context under OwnerIDKey. If the validated token has passed its
// half-life a fresh token is issued and a renewed Set-Cookie is added to the
// response (sliding session). cookieSecure controls the Secure flag on any
// renewed cookie (pass cfg.Auth.CookieSecure in production).
func RequireOwner(svc *auth.Service, cookieSecure bool) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			token, ok := tokenFromRequest(c)
			if !ok {
				return echo.NewHTTPError(http.StatusUnauthorized, "missing or invalid authorization")
			}
			claims, err := svc.Validate(token)
			if err != nil {
				return echo.NewHTTPError(http.StatusUnauthorized, "invalid or expired token")
			}
			c.Set(OwnerIDKey, claims.OwnerID)

			// Sliding renewal: re-issue + re-set the cookie past half-life.
			// Failure is best-effort: log and continue — the existing session
			// remains valid for the rest of its lifetime.
			if svc.ShouldRenew(claims, time.Now()) {
				if fresh, err := svc.Issue(claims.OwnerID); err == nil {
					c.SetCookie(authcookie.New(fresh, cookieSecure))
				} else {
					log.Printf("owner session renewal failed (continuing): %v", err)
				}
			}
			return next(c)
		}
	}
}

// tokenFromRequest reads the session token from the cookie first, then falls
// back to the Authorization: Bearer header (so scripts/curl keep working).
func tokenFromRequest(c echo.Context) (string, bool) {
	if ck, err := c.Cookie(authcookie.Name); err == nil && ck.Value != "" {
		return ck.Value, true
	}
	return extractBearer(c.Request().Header.Get("Authorization"))
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
