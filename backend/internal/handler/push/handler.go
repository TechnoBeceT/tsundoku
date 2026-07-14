// Package push holds the thin HTTP handlers for Web Push registration: serving
// the server VAPID public key and upserting/removing this device's subscription.
// Business logic (the subscription store) lives in internal/push; the handler
// only binds → validates → calls the service (bind → service → 204).
package push

import (
	"net/http"

	"github.com/labstack/echo/v4"

	pushsvc "github.com/technobecet/tsundoku/internal/push"
)

// VAPIDKeyDTO is the GET /api/push/vapid-key response: the base64url public key
// the browser passes to pushManager.subscribe as its applicationServerKey.
type VAPIDKeyDTO struct {
	Key string `json:"key"`
}

// Handler serves the three push routes. Construct with NewHandler.
type Handler struct {
	subs           *pushsvc.Service
	vapidPublicKey string
}

// NewHandler constructs a push Handler over the subscription store and the
// server's VAPID public key (resolved once at boot in main).
func NewHandler(subs *pushsvc.Service, vapidPublicKey string) *Handler {
	return &Handler{subs: subs, vapidPublicKey: vapidPublicKey}
}

// VAPIDKey handles GET /api/push/vapid-key — returns the server's public key so
// a browser can create a subscription. Empty only if VAPID init failed at boot
// (Web Push then unavailable); the client feature-detects and degrades.
func (h *Handler) VAPIDKey(c echo.Context) error {
	return c.JSON(http.StatusOK, VAPIDKeyDTO{Key: h.vapidPublicKey})
}

// Subscribe handles POST /api/push/subscriptions — upserts this device's
// subscription by endpoint (idempotent). Returns 204 on success; 400 on an
// invalid body.
func (h *Handler) Subscribe(c echo.Context) error {
	req, err := bindSubscription(c)
	if err != nil {
		return err
	}
	if err := h.subs.Upsert(c.Request().Context(), pushsvc.Subscription{
		Endpoint: req.Endpoint,
		P256dh:   req.Keys.P256dh,
		Auth:     req.Keys.Auth,
	}); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// Unsubscribe handles DELETE /api/push/subscriptions — removes this device's
// subscription by endpoint. Returns 204 (deleting an unknown endpoint is a
// no-op); 400 on an invalid body.
func (h *Handler) Unsubscribe(c echo.Context) error {
	req, err := bindUnsubscribe(c)
	if err != nil {
		return err
	}
	if err := h.subs.Delete(c.Request().Context(), req.Endpoint); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}
