// Package sse_test provides black-box tests for the SSE hub and handler.
package sse_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
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

// waitForSubscribers polls hub's subscriber count until it reaches n or the
// deadline elapses. It is used instead of time.Sleep to provide deterministic
// synchronization between the test goroutine and the handler goroutine.
func waitForSubscribers(t *testing.T, hub *sse.Hub, n int, deadline time.Duration) {
	t.Helper()
	timeout := time.After(deadline)
	for {
		if sse.SubscriberCount(hub) >= n {
			return
		}
		select {
		case <-timeout:
			t.Fatalf("timed out waiting for %d subscriber(s); current count: %d", n, sse.SubscriberCount(hub))
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

// assertSSEHeaders checks that rec contains the required SSE response headers.
func assertSSEHeaders(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
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
}

// assertSSEFrame checks that body contains a well-formed SSE frame for the
// given event type.
func assertSSEFrame(t *testing.T, body, eventType string) {
	t.Helper()
	if !strings.Contains(body, "event: "+eventType+"\n") {
		t.Errorf("body missing SSE event line for %q; got:\n%s", eventType, body)
	}
	if !strings.Contains(body, "data: ") {
		t.Errorf("body missing SSE data line; got:\n%s", body)
	}
	if !strings.Contains(body, "\n\n") {
		t.Errorf("body missing SSE frame terminator; got:\n%s", body)
	}
}

// waitBroadcastDispatched broadcasts event to hub, waits for the probe channel
// to confirm delivery (proving the hub dispatched to all concurrent
// subscribers), then returns. It is safe to cancel the handler and read
// rec.Body once this function returns.
func waitBroadcastDispatched(t *testing.T, hub *sse.Hub, event sse.Event, probe <-chan sse.Event) {
	t.Helper()
	hub.Broadcast(event)
	select {
	case <-probe:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for probe to receive broadcast event")
	}
}

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
//
// Synchronization is deterministic: we poll sse.SubscriberCount (via
// export_test.go) rather than sleeping, so the test is not sensitive to
// scheduling delays.
func TestHandlerSetsEventStreamHeaders(t *testing.T) {
	hub := sse.NewHub()
	e := echo.New()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/progress", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Run the handler in a goroutine.
	done := make(chan error, 1)
	go func() {
		done <- sse.ProgressHandler(hub)(c)
	}()

	// Wait deterministically until the handler has subscribed.
	waitForSubscribers(t, hub, 1, 2*time.Second)

	// Subscribe a probe BEFORE broadcasting; wait for probe receipt to confirm
	// the hub dispatched to all subscribers. Reading rec.Body before <-done
	// would race with the handler's concurrent Write calls.
	probe, probeUnsub := hub.Subscribe()
	defer probeUnsub()

	waitBroadcastDispatched(t, hub, sse.Event{Type: "progress", Data: map[string]any{"pct": 99}}, probe)

	// Cancel and wait for the handler to exit before reading the response.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("handler returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after context cancellation")
	}

	assertSSEHeaders(t, rec)
	assertSSEFrame(t, rec.Body.String(), "progress")
}

// TestRegisterRoutes verifies that RegisterRoutes wires GET /progress on the
// provided Echo group and the handler responds with Content-Type:
// text/event-stream (proving the route is registered and the right handler
// is mounted).
func TestRegisterRoutes(t *testing.T) {
	hub := sse.NewHub()
	e := echo.New()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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

	// Wait deterministically until the handler has subscribed, then cancel.
	waitForSubscribers(t, hub, 1, 2*time.Second)
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

// TestProgressRouteRequiresOwner proves that the /api/progress endpoint is
// protected by the RequireOwner middleware: an unauthenticated request must
// receive 401, and an authenticated request must reach the stream (not 401).
func TestProgressRouteRequiresOwner(t *testing.T) {
	const secret = "test-secret"
	authSvc := auth.NewService(secret)

	hub := sse.NewHub()
	e := echo.New()
	// Hide Echo's default error handler output in tests.
	e.HideBanner = true

	api := e.Group("/api", middleware.RequireOwner(authSvc, false))
	sse.RegisterRoutes(api, hub)

	t.Run("unauthenticated returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/progress", nil)
		// No Authorization header — must be rejected.
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("unauthenticated request: got status %d, want 401", rec.Code)
		}
	})

	t.Run("authenticated reaches stream", func(t *testing.T) {
		ownerID := uuid.New()
		token, err := authSvc.Issue(ownerID)
		if err != nil {
			t.Fatalf("failed to issue token: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		req := httptest.NewRequest(http.MethodGet, "/api/progress", nil)
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()

		done := make(chan struct{})
		go func() {
			defer close(done)
			e.ServeHTTP(rec, req)
		}()

		// Wait until the handler has subscribed (proves it got past auth).
		waitForSubscribers(t, hub, 1, 2*time.Second)
		cancel()
		<-done

		if rec.Code == http.StatusUnauthorized {
			t.Error("authenticated request was rejected with 401")
		}
		ct := rec.Header().Get("Content-Type")
		if !strings.HasPrefix(ct, "text/event-stream") {
			t.Errorf("authenticated request: Content-Type = %q, want text/event-stream", ct)
		}
	})
}
