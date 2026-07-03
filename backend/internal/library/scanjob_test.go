package library_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/library"
	"github.com/technobecet/tsundoku/internal/sse"
)

// drainScanEvents reads off ch, decoding each event's Data into a ScanEvent,
// until a scan.done event arrives or timeout elapses. Returns every decoded
// event grouped by SSE type so a test can assert on scan.start/scan.progress/
// scan.done independently.
func drainScanEvents(t *testing.T, ch <-chan sse.Event, timeout time.Duration) map[string][]library.ScanEvent {
	t.Helper()
	got := map[string][]library.ScanEvent{}
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-ch:
			raw, ok := ev.Data.(json.RawMessage)
			if !ok {
				t.Fatalf("event %q: Data is %T, want json.RawMessage", ev.Type, ev.Data)
			}
			var payload library.ScanEvent
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Fatalf("decode %q event: %v", ev.Type, err)
			}
			got[ev.Type] = append(got[ev.Type], payload)
			if ev.Type == "scan.done" {
				return got
			}
		case <-deadline:
			t.Fatalf("timed out waiting for scan.done; got so far: %+v", got)
		}
	}
}

// TestStartScan_BroadcastsStartAndDone proves the async happy path: StartScan
// returns true immediately and the background scan streams a scan.start
// followed by a scan.done carrying the final tally over the SSE hub — the
// owner never blocks on the HTTP request for a long NFS walk.
func TestStartScan_BroadcastsStartAndDone(t *testing.T) {
	storage := t.TempDir()
	writeKaizokuSeries(t, storage, "Manga", "Series One", "mangadex", "Alpha", 1)
	writeKaizokuSeries(t, storage, "Manga", "Series Two", "mangadex", "Alpha", 1)

	client := testdb.New(t)
	hub := sse.NewHub()
	svc := library.NewService(client, nil, nil, nil, func() {}, storage, hub)

	ch, cancel := hub.Subscribe()
	defer cancel()

	if started := svc.StartScan(context.Background()); !started {
		t.Fatal("StartScan returned false, want true (no scan in flight)")
	}

	events := drainScanEvents(t, ch, 5*time.Second)
	if len(events["scan.start"]) < 1 {
		t.Fatalf("scan.start events = %d, want >=1", len(events["scan.start"]))
	}
	done := events["scan.done"]
	if len(done) != 1 {
		t.Fatalf("scan.done events = %d, want 1", len(done))
	}
	if done[0].Total <= 0 {
		t.Fatalf("scan.done Total = %d, want >0 for a 2-series fixture", done[0].Total)
	}
	if done[0].Error != "" {
		t.Fatalf("scan.done Error = %q, want empty on success", done[0].Error)
	}
}

// TestStartScan_SingleFlight proves the single-flight guard: a second
// StartScan issued immediately after the first (no synchronization point in
// between, so the first call's goroutine cannot have completed and released
// the guard yet) is rejected outright rather than launching a second
// concurrent NFS walk.
func TestStartScan_SingleFlight(t *testing.T) {
	storage := t.TempDir()
	writeKaizokuSeries(t, storage, "Manga", "Series One", "mangadex", "Alpha", 1)
	writeKaizokuSeries(t, storage, "Manga", "Series Two", "mangadex", "Alpha", 1)

	client := testdb.New(t)
	hub := sse.NewHub()
	svc := library.NewService(client, nil, nil, nil, func() {}, storage, hub)

	ch, cancel := hub.Subscribe()
	defer cancel()

	first := svc.StartScan(context.Background())
	second := svc.StartScan(context.Background())
	if !first {
		t.Fatal("first StartScan returned false, want true")
	}
	if second {
		t.Fatal("second StartScan returned true while the first was still in flight, want false")
	}

	// Drain the in-flight scan to completion so its goroutine (and the temp
	// storage dir it reads) doesn't outlive the test.
	drainScanEvents(t, ch, 5*time.Second)
}
