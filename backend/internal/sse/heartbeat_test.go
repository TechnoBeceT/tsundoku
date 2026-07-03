// Package sse_test — heartbeat keepalive behavior (see handler.go).
package sse_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/technobecet/tsundoku/internal/sse"
)

// TestProgressHandlerSendsHeartbeat proves that ProgressHandler writes an idle
// keepalive (a leading-colon SSE comment) on a ticker, even when no real hub
// event is ever broadcast. This is the fix for B1: Cloudflare Tunnel (and
// similar reverse proxies) kill an HTTP stream after ~100s of total silence;
// the heartbeat keeps bytes flowing during idle periods between downloads. A
// leading colon is an SSE comment line per spec — EventSource ignores it, so
// it has no frontend-visible effect.
//
// heartbeatInterval is overridden to a few milliseconds via
// sse.SetHeartbeatInterval (export_test.go) so the test is fast and
// deterministic rather than waiting on the real 20s production interval.
func TestProgressHandlerSendsHeartbeat(t *testing.T) {
	const interval = 20 * time.Millisecond
	restore := sse.SetHeartbeatInterval(interval)
	defer restore()

	hub := sse.NewHub()
	e := echo.New()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/progress", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	done := make(chan error, 1)
	go func() {
		done <- sse.ProgressHandler(hub)(c)
	}()

	waitForSubscribers(t, hub, 1, 2*time.Second)

	// Also prove a real event still flushes alongside the heartbeat — the
	// fix must not interfere with normal event delivery.
	probe, probeUnsub := hub.Subscribe()
	defer probeUnsub()
	waitBroadcastDispatched(t, hub, sse.Event{Type: "progress", Data: map[string]any{"pct": 7}}, probe)

	// Give the ticker several intervals to fire at least once. This is a
	// bounded wait, not a concurrent poll of the ResponseRecorder (which would
	// race with the handler goroutine's writes) — rec.Body is only inspected
	// below, after <-done confirms the handler has fully returned and stopped
	// writing.
	time.Sleep(5 * interval)

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("handler returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after context cancellation")
	}

	body := rec.Body.String()
	if !strings.Contains(body, ": ping\n\n") {
		t.Errorf("body missing heartbeat comment frame; got:\n%s", body)
	}
	assertSSEFrame(t, body, "progress")
}
