// Package sse exports internal symbols needed by the black-box test package.
// This file is compiled only during testing (it lives in package sse, not
// package sse_test, so it can reach unexported identifiers).
package sse

// SubscriberCount returns the number of currently registered subscribers.
// It is intentionally exported only for tests so that tests can perform
// deterministic synchronization (poll until the handler has subscribed) rather
// than relying on time.Sleep.
func SubscriberCount(h *Hub) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subscribers)
}
