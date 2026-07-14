package push

import (
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/handler/httperr"
	"github.com/technobecet/tsundoku/internal/pkg/urlx"
)

// subscriptionRequest is the POST /api/push/subscriptions body — the shape a
// browser's PushSubscription.toJSON() produces (endpoint + the two encryption
// keys nested under "keys").
type subscriptionRequest struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// unsubscribeRequest is the DELETE /api/push/subscriptions body.
type unsubscribeRequest struct {
	Endpoint string `json:"endpoint"`
}

// bindSubscription binds + validates a subscribe body: the endpoint must be an
// absolute http(s) URL and both encryption keys must be non-empty. Any failure
// is a 400 (fail-closed) — the store never holds a malformed subscription.
func bindSubscription(c echo.Context) (subscriptionRequest, error) {
	var req subscriptionRequest
	if err := c.Bind(&req); err != nil {
		return req, httperr.BadRequest("invalid request body")
	}
	req.Endpoint = strings.TrimSpace(req.Endpoint)
	req.Keys.P256dh = strings.TrimSpace(req.Keys.P256dh)
	req.Keys.Auth = strings.TrimSpace(req.Keys.Auth)
	if !urlx.IsAbsoluteHTTP(req.Endpoint) {
		return req, httperr.BadRequest("endpoint must be an absolute http(s) URL")
	}
	if req.Keys.P256dh == "" || req.Keys.Auth == "" {
		return req, httperr.BadRequest("p256dh and auth are required")
	}
	return req, nil
}

// bindUnsubscribe binds + validates a delete body: the endpoint must be an
// absolute http(s) URL (the same identity a subscribe used). A blank/invalid
// endpoint is a 400.
func bindUnsubscribe(c echo.Context) (unsubscribeRequest, error) {
	var req unsubscribeRequest
	if err := c.Bind(&req); err != nil {
		return req, httperr.BadRequest("invalid request body")
	}
	req.Endpoint = strings.TrimSpace(req.Endpoint)
	if !urlx.IsAbsoluteHTTP(req.Endpoint) {
		return req, httperr.BadRequest("endpoint must be an absolute http(s) URL")
	}
	return req, nil
}
