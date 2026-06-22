// Package sse_test provides black-box tests for the SSE hub and handler.
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

// Uncovered defensive branches (provably unreachable in normal use):
//   - ProgressHandler: "streaming unsupported" branch — httptest.ResponseRecorder
//     always implements http.Flusher; real HTTP/1.1 connections always do too.
//     We document it rather than injecting a fake ResponseWriter just to
//     move the counter (engineering standard §11: document, don't fake).
//   - writeSSEFrame error path — fmt.Fprintf to an in-memory recorder never
//     returns an error; a real network write failure races with ctx.Done so
//     the client-disconnect path fires first. Document, not fake-covered.

// TestHubBroadcastReachesSubscriber verifies that an event sent via Broadcast
// arrives on a subscriber's channel within a reasonable timeout.
func TestHubBroadcastReachesSubscriber(t *testing.T) {
	hub := sse.NewHub()

	ch, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	want := sse.Event{Type: "progress", Data: map[string]any{"pct": 42}}
	hub.Broadcast(want)

	select {
	case got := <-ch:
		if got.Type != want.Type {
			t.Fatalf("got event type %q, want %q", got.Type, want.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broadcast event")
	}
}

// TestUnsubscribeStopsDelivery verifies that after calling the unsubscribe
// function no further events are delivered and the channel is closed (so a
// receive returns the zero value immediately rather than blocking).
func TestUnsubscribeStopsDelivery(t *testing.T) {
	hub := sse.NewHub()

	ch, unsubscribe := hub.Subscribe()
	unsubscribe() // immediately unsubscribe before any broadcast

	hub.Broadcast(sse.Event{Type: "ping", Data: nil})

	select {
	case evt, ok := <-ch:
		if ok {
			// A value arrived after unsubscribe — that is acceptable only if it
			// was already in the buffer before unsubscribe was called, but since
			// we unsubscribed BEFORE broadcasting this must not happen.
			t.Fatalf("received event after unsubscribe: %+v", evt)
		}
		// Channel was closed — correct cleanup path.
	case <-time.After(200 * time.Millisecond):
		t.Fatal("channel was neither closed nor delivered an event within timeout")
	}
}

// TestSlowSubscriberDoesNotBlockHub proves that a subscriber whose channel is
// full (never draining) does not block Broadcast for other subscribers.
// If Broadcast blocked on a full channel this test would hang/timeout, making
// the test non-vacuous.
func TestSlowSubscriberDoesNotBlockHub(t *testing.T) {
	hub := sse.NewHub()

	// Slow subscriber — never read from.
	_, unsub1 := hub.Subscribe()
	defer unsub1()

	// Healthy subscriber.
	ch2, unsub2 := hub.Subscribe()
	defer unsub2()

	evt := sse.Event{Type: "progress", Data: "hello"}

	// Fill the hub with enough events to exceed any internal buffer of the slow
	// subscriber so that a blocking Broadcast would stall here.
	for i := 0; i < 20; i++ {
		hub.Broadcast(evt)
	}

	// The healthy subscriber must still receive at least one event.
	select {
	case got := <-ch2:
		if got.Type != evt.Type {
			t.Fatalf("unexpected event type %q", got.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("healthy subscriber timed out — slow subscriber may be blocking Broadcast")
	}
}

// TestHandlerSetsEventStreamHeaders verifies that the SSE handler sets the
// correct HTTP headers and that a broadcast event appears in the response body
// as a properly formatted SSE frame. A cancelled context ends the stream so
// the test terminates.
func TestHandlerSetsEventStreamHeaders(t *testing.T) {
	hub := sse.NewHub()
	e := echo.New()

	ctx, cancel := context.WithCancel(context.Background())

	req := httptest.NewRequest(http.MethodGet, "/api/progress", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Run the handler in a goroutine; cancel context after a short delay.
	done := make(chan error, 1)
	go func() {
		done <- sse.ProgressHandler(hub)(c)
	}()

	// Give the handler time to subscribe and write headers.
	time.Sleep(50 * time.Millisecond)

	hub.Broadcast(sse.Event{Type: "progress", Data: map[string]any{"pct": 99}})

	// Give the handler time to write the event frame.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("handler returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after context cancellation")
	}

	// Verify headers.
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if rec.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", rec.Header().Get("Cache-Control"))
	}
	if rec.Header().Get("Connection") != "keep-alive" {
		t.Errorf("Connection = %q, want keep-alive", rec.Header().Get("Connection"))
	}

	// Verify SSE frame format: must contain "event: progress" and "data: ".
	body := rec.Body.String()
	if !strings.Contains(body, "event: progress\n") {
		t.Errorf("body missing SSE event line; got:\n%s", body)
	}
	if !strings.Contains(body, "data: ") {
		t.Errorf("body missing SSE data line; got:\n%s", body)
	}
	if !strings.Contains(body, "\n\n") {
		t.Errorf("body missing SSE frame terminator; got:\n%s", body)
	}
}

// TestRegisterRoutes verifies that RegisterRoutes wires GET /progress on the
// provided Echo group and the handler responds with Content-Type:
// text/event-stream (proving the route is registered and the right handler
// is mounted).
func TestRegisterRoutes(t *testing.T) {
	hub := sse.NewHub()
	e := echo.New()

	ctx, cancel := context.WithCancel(context.Background())

	api := e.Group("/api")
	sse.RegisterRoutes(api, hub)

	req := httptest.NewRequest(http.MethodGet, "/api/progress", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		e.ServeHTTP(rec, req)
	}()

	time.Sleep(30 * time.Millisecond)
	cancel()

	<-done

	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
}

// TestUnsubscribeIdempotent verifies that calling unsubscribe more than once
// does not panic (double-close protection).
func TestUnsubscribeIdempotent(t *testing.T) {
	hub := sse.NewHub()
	_, unsubscribe := hub.Subscribe()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("double unsubscribe panicked: %v", r)
		}
	}()
	unsubscribe()
	unsubscribe() // must not panic
}
