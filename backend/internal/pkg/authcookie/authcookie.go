// Package authcookie builds the single-owner session cookie used by the owner
// login/logout handlers and the RequireOwner middleware's sliding renewal.
package authcookie

import (
	"net/http"

	"github.com/technobecet/tsundoku/internal/pkg/auth"
)

// Name is the session cookie name.
const Name = "tsundoku_session"

// New builds a session cookie carrying token. secure comes from config
// (AuthConfig.CookieSecure) so plain-HTTP LAN deploys can disable it.
func New(token string, secure bool) *http.Cookie {
	//nolint:gosec // Secure is configurable for plain-HTTP LAN deploys.
	return &http.Cookie{
		Name:     Name,
		Value:    token,
		Path:     "/",
		MaxAge:   int(auth.TokenTTL.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	}
}

// Clear builds a cookie that immediately expires the session cookie.
func Clear(secure bool) *http.Cookie {
	//nolint:gosec // Secure is configurable for plain-HTTP LAN deploys.
	return &http.Cookie{
		Name:     Name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	}
}
