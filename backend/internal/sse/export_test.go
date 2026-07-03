// Package sse exports internal symbols needed by the black-box test package.
// This file is compiled only during testing (it lives in package sse, not
// package sse_test, so it can reach unexported identifiers).
package sse

import "time"

// SubscriberCount returns the number of currently registered subscribers.
// It is intentionally exported only for tests so that tests can perform
// deterministic synchronization (poll until the handler has subscribed) rather
// than relying on time.Sleep.
func SubscriberCount(h *Hub) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subscribers)
}

// SetHeartbeatInterval overrides the package-level heartbeatInterval (the
// idle SSE keepalive period, production default 20s) and returns a restore
// func that puts the previous value back. Tests use this to shrink the
// interval to a few milliseconds so the heartbeat fires deterministically
// within a short timeout instead of waiting on the real production interval.
func SetHeartbeatInterval(d time.Duration) (restore func()) {
	prev := heartbeatInterval
	heartbeatInterval = d
	return func() { heartbeatInterval = prev }
}
