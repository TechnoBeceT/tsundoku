// Package httperr holds the small set of HTTP-error constructors shared by the
// thin passthrough handlers (the Suwayomi settings + extensions proxies). Both
// proxies need the same two error shapes — a 400 for a bad request body and a 502
// for an upstream-dependency failure — so the constructors live here in one place
// instead of each handler package re-declaring an identical copy (§2 DRY). The
// central error middleware renders whatever message these carry as {"message": …}.
package httperr

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// BadRequest builds a 400 Bad Request echo.HTTPError carrying msg. The central
// error middleware surfaces msg verbatim to the client, so msg should be a
// caller-facing validation reason (e.g. "pkgName required"), never internal text.
func BadRequest(msg string) error {
	return echo.NewHTTPError(http.StatusBadRequest, msg)
}

// Upstream maps a failure from a proxied upstream dependency — the Suwayomi
// client, whether a transport error or a GraphQL rejection — to a 502 Bad
// Gateway. The upstream is a separate service, so its failure is a gateway error
// rather than an internal one, and surfacing it as a 502 means a proxy handler
// never returns a false 200. The message is prefixed with "suwayomi: " so the
// rendered error identifies which upstream failed.
func Upstream(err error) error {
	return echo.NewHTTPError(http.StatusBadGateway, "suwayomi: "+err.Error())
}
