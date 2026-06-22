// Package sse implements the SSE (Server-Sent Events) progress hub for
// Tsundoku. It provides a thread-safe subscriber registry with fan-out
// broadcast so that the backend can push download-progress events to all
// connected browser clients over plain HTTP (QCAT-016: SSE is the transport
// for one-way server→client push; WebSocket is reserved for bidirectional
// traffic only).
//
// Slow-subscriber policy: each subscriber channel has a fixed buffer
// (subscriberBufSize). Broadcast is non-blocking: if a subscriber's buffer is
// full the event is dropped for that subscriber rather than stalling the hub
// or blocking delivery to other subscribers. This ensures that a client that
// stops draining its stream (e.g. a tab left in the background) never impedes
// progress updates for healthy clients.
package sse

import "sync"

// subscriberBufSize is the capacity of each subscriber's event channel.
// Events sent to a full channel are dropped (see slow-subscriber policy above).
const subscriberBufSize = 16

// Event is the unit of data pushed from the hub to subscribers.
// Type identifies the SSE event type; Data is any JSON-serialisable payload.
type Event struct {
	// Type is the SSE event name (e.g. "progress", "done", "error").
	// It MUST NOT contain newline (\n) or carriage-return (\r) characters;
	// the SSE wire format is newline-delimited, and a newline in the type field
	// would corrupt the frame. writeSSEFrame strips any such characters
	// defensively, but callers should not rely on that sanitization.
	Type string `json:"type"`
	// Data is the event payload; it is JSON-marshalled into the SSE data line.
	Data any `json:"data"`
}

// Hub is a concurrent-safe SSE subscriber registry.
// Use NewHub to create a zero-value-ready Hub; do not copy after first use.
type Hub struct {
	mu          sync.Mutex
	subscribers map[chan Event]struct{}
}

// NewHub allocates and returns a new, empty Hub ready for use.
func NewHub() *Hub {
	return &Hub{
		subscribers: make(map[chan Event]struct{}),
	}
}

// Subscribe registers a new subscriber and returns:
//   - ch: a read-only channel on which broadcast events will arrive.
//   - unsubscribe: a cleanup function that removes the subscriber and closes ch.
//
// The caller MUST call unsubscribe exactly once (e.g. via defer) to prevent a
// goroutine or channel leak. After unsubscribe returns, ch is closed so any
// pending receive on ch will drain and then yield the zero value.
func (h *Hub) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, subscriberBufSize)

	h.mu.Lock()
	h.subscribers[ch] = struct{}{}
	h.mu.Unlock()

	unsubscribe := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if _, ok := h.subscribers[ch]; ok {
			delete(h.subscribers, ch)
			close(ch)
		}
	}
	return ch, unsubscribe
}

// Broadcast sends event to every current subscriber.
// It is safe to call from multiple goroutines concurrently.
// The send to each subscriber is non-blocking: if a subscriber's channel
// buffer is full the event is dropped for that subscriber (see
// slow-subscriber policy in the package doc).
func (h *Hub) Broadcast(event Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subscribers {
		select {
		case ch <- event:
		default:
			// Subscriber buffer is full — drop the event for this subscriber
			// rather than blocking the hub (slow-subscriber policy).
		}
	}
}
