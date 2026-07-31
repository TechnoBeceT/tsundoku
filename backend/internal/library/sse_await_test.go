package library_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/sse"
)

// The library service reports every ASYNC outcome over SSE — a detached merge
// and the detached library-wide dedup sweep both answer a bare 202, so their
// event is the ONLY channel their result reaches the owner on. Several tests
// therefore need the same shape: drain the hub until the one event that matters
// shows up, decode it, and fail loudly if it never does. These two helpers own
// that shape so each test is left holding only the contract it actually pins
// (which event, which payload, which acceptance rule, how long it may take).

// awaitEvent drains the SSE stream until an event of type want arrives whose
// decoded payload satisfies accept, and returns that payload. A nil accept
// takes the first event of that type; a non-nil one that rejects a payload
// keeps draining (used where several series broadcast the same event type and
// the test cares about one of them). missing is the failure message used when
// timeout expires — write it as the CONTRACT that was broken, not "timeout",
// because a missing event is a real defect and not a timing quirk.
func awaitEvent[T any](t *testing.T, events <-chan sse.Event, want string, timeout time.Duration, missing string, accept func(T) bool) T {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-events:
			if ev.Type != want {
				continue
			}
			payload := decodeEventPayload[T](t, ev)
			if accept == nil || accept(payload) {
				return payload
			}
		case <-deadline:
			var zero T
			t.Fatal(missing)
			return zero
		}
	}
}

// decodeEventPayload unmarshals an SSE event's data into T. The hub carries the
// payload as an already-encoded json.RawMessage, so a wrong dynamic type here
// means the emitter changed shape — worth failing on rather than skipping.
func decodeEventPayload[T any](t *testing.T, ev sse.Event) T {
	t.Helper()
	var payload T
	raw, ok := ev.Data.(json.RawMessage)
	if !ok {
		t.Fatalf("%s payload is %T, want json.RawMessage", ev.Type, ev.Data)
		return payload
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal %s: %v", ev.Type, err)
	}
	return payload
}
