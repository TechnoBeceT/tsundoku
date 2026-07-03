// Package sse — see hub.go for package-level documentation.
package sse

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

// heartbeatInterval is how often ProgressHandler writes an idle SSE keepalive
// comment when no real hub event is flowing. Cloudflare Tunnel (and similar
// reverse proxies) kill an HTTP stream after ~100s of total silence (observed
// as an HTTP 524 on /api/progress); 20s comfortably beats that. It is a
// package-level var, not a const, so tests can shrink it via
// export_test.go's SetHeartbeatInterval.
var heartbeatInterval = 20 * time.Second

// ProgressHandler returns an Echo HandlerFunc that streams SSE events from hub
// to the connected client.
//
// The handler:
//   - Sets Content-Type: text/event-stream, Cache-Control: no-cache,
//     Connection: keep-alive so that browsers and proxies treat the response
//     as a live stream.
//   - Subscribes to hub, then writes each received event as an SSE frame:
//     "event: <type>\ndata: <json>\n\n", flushing after each frame.
//   - Writes an idle keepalive (": ping\n\n", an SSE comment line) every
//     heartbeatInterval so a reverse proxy never sees a silent connection
//     between real events; EventSource ignores comment lines, so this has no
//     effect visible to the frontend.
//   - Exits cleanly when the client disconnects (request context Done), calling
//     the unsubscribe function to release the subscriber channel.
//
// Authentication is enforced by the RequireOwner middleware wired in
// RegisterRoutes; the handler itself does not re-validate the token.
//
// Frontend note (QCAT-018): the browser SSE client uses native EventSource
// ('/api/progress'). Authentication is carried by the HttpOnly tsundoku_session
// cookie, which the browser attaches automatically — no custom header is needed.
// The Authorization: Bearer fallback is still accepted by RequireOwner (for
// scripts/curl) but the frontend no longer sends it for the SSE stream.
func ProgressHandler(hub *Hub) echo.HandlerFunc {
	return func(c echo.Context) error {
		w := c.Response()

		// Check for flushing support BEFORE committing any response headers or
		// status code, so that we can still return a 500 if streaming is
		// unsupported. Once WriteHeader is called the status code is locked.
		flusher, ok := w.Writer.(http.Flusher)
		if !ok {
			// This should not happen with a real HTTP/1.1 connection, but if the
			// underlying ResponseWriter does not support flushing we cannot stream.
			return echo.NewHTTPError(http.StatusInternalServerError, "streaming unsupported")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		// Disable buffering at the response writer level so frames are sent
		// immediately. WriteHeader flushes headers to the client.
		w.WriteHeader(http.StatusOK)

		ch, unsubscribe := hub.Subscribe()
		defer unsubscribe()

		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()

		ctx := c.Request().Context()
		for {
			select {
			case <-ctx.Done():
				// Client disconnected — cleanup is handled by defer unsubscribe.
				return nil
			case event, ok := <-ch:
				if !ok {
					// Channel closed (hub shutting down) — end the stream.
					return nil
				}
				if err := writeSSEFrame(w, event); err != nil {
					// Write failure usually means the client disconnected.
					return nil //nolint:nilerr // intentional: stream end is not an error
				}
				flusher.Flush()
			case <-ticker.C:
				// Idle heartbeat: a leading colon is an SSE comment per spec,
				// so EventSource ignores it entirely — this is transport-only
				// keepalive, invisible to the frontend.
				if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
					// Write failure usually means the client disconnected.
					return nil //nolint:nilerr // intentional: stream end is not an error
				}
				flusher.Flush()
			}
		}
	}
}

// sseNewlineReplacer strips CR and LF from strings that are written verbatim
// into SSE field values. A newline in an SSE field name or event type would
// break the framing; we remove it defensively rather than rejecting the event.
var sseNewlineReplacer = strings.NewReplacer("\n", "", "\r", "")

// writeSSEFrame serialises event into the SSE wire format and writes it to w:
//
//	event: <type>
//	data: <json>
//	<blank line>
//
// event.Type is sanitized to strip any CR/LF before being written (SSE framing
// is newline-delimited; a newline in the type would corrupt the frame). Data is
// JSON-encoded, so it is already safe.
func writeSSEFrame(w interface{ Write([]byte) (int, error) }, event Event) error {
	data, err := json.Marshal(event.Data)
	if err != nil {
		return fmt.Errorf("marshal SSE event data: %w", err)
	}
	safeType := sseNewlineReplacer.Replace(event.Type)
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", safeType, data)
	return err
}

// RegisterRoutes registers the SSE progress endpoint on the provided Echo
// group behind the RequireOwner middleware.
//
// Callers (typically internal/server/routes.go, Task 9) should pass the
// authenticated API group so the route inherits the auth middleware:
//
//	api := e.Group("/api", middleware.RequireOwner(authSvc, cfg.Auth.CookieSecure))
//	sse.RegisterRoutes(api, hub)
func RegisterRoutes(g *echo.Group, hub *Hub) {
	g.GET("/progress", ProgressHandler(hub))
}
