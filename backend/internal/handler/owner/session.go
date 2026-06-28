package owner

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	mw "github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/authcookie"
)

// Logout clears the session cookie. POST /api/owner/logout (authed).
func (h *Handler) Logout(c echo.Context) error {
	c.SetCookie(authcookie.Clear(h.cookieSecure))
	return c.NoContent(http.StatusNoContent)
}

// Me returns the authenticated owner id. GET /api/owner/me (authed). It lets
// the SPA learn auth state on load without exposing the token to JS.
func (h *Handler) Me(c echo.Context) error {
	id, ok := c.Get(mw.OwnerIDKey).(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	return c.JSON(http.StatusOK, MeResponse{OwnerID: id.String()})
}
