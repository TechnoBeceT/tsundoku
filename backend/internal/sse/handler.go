// Package sse — see hub.go for package-level documentation.
package sse

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"
)

// ProgressHandler returns an Echo HandlerFunc that streams SSE events from hub
// to the connected client.
//
// The handler:
//   - Sets Content-Type: text/event-stream, Cache-Control: no-cache,
//     Connection: keep-alive so that browsers and proxies treat the response
//     as a live stream.
//   - Subscribes to hub, then writes each received event as an SSE frame:
//     "event: <type>\ndata: <json>\n\n", flushing after each frame.
//   - Exits cleanly when the client disconnects (request context Done), calling
//     the unsubscribe function to release the subscriber channel.
//
// Authentication is enforced by the RequireOwner middleware wired in
// RegisterRoutes; the handler itself does not re-validate the token.
//
// Frontend note (QCAT-018): the browser SSE client MUST use fetch (not native
// EventSource) to send the Authorization: Bearer header. Native EventSource
// cannot set custom headers, so it is not used.
func ProgressHandler(hub *Hub) echo.HandlerFunc {
	return func(c echo.Context) error {
		w := c.Response()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		// Disable buffering at the response writer level so frames are sent
		// immediately. WriteHeader flushes headers to the client.
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.Writer.(http.Flusher)
		if !ok {
			// This should not happen with a real HTTP/1.1 connection, but if the
			// underlying ResponseWriter does not support flushing we cannot stream.
			return echo.NewHTTPError(http.StatusInternalServerError, "streaming unsupported")
		}

		ch, unsubscribe := hub.Subscribe()
		defer unsubscribe()

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
			}
		}
	}
}

// writeSSEFrame serialises event into the SSE wire format and writes it to w:
//
//	event: <type>
//	data: <json>
//	<blank line>
func writeSSEFrame(w interface{ Write([]byte) (int, error) }, event Event) error {
	data, err := json.Marshal(event.Data)
	if err != nil {
		return fmt.Errorf("marshal SSE event data: %w", err)
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data)
	return err
}

// RegisterRoutes registers the SSE progress endpoint on the provided Echo
// group behind the RequireOwner middleware.
//
// Callers (typically internal/server/routes.go, Task 9) should pass the
// authenticated API group so the route inherits the Bearer-auth middleware:
//
//	api := e.Group("/api", middleware.RequireOwner(authSvc))
//	sse.RegisterRoutes(api, hub)
func RegisterRoutes(g *echo.Group, hub *Hub) {
	g.GET("/progress", ProgressHandler(hub))
}
