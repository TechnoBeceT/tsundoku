package library_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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

// TestStartScan_ErrorReleasesLatch proves the error path (the Minor test gap
// from the Task 3 review): a scan that fails outright — here, storage points
// at a regular file so disk.ScanLibrary's os.ReadDir returns a non-directory
// error rather than the "missing root" no-op case — still broadcasts a
// terminal scan.done carrying a non-empty Error, and the single-flight latch
// is released afterward so a subsequent StartScan is not permanently wedged.
func TestStartScan_ErrorReleasesLatch(t *testing.T) {
	dir := t.TempDir()
	notADir := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	client := testdb.New(t)
	hub := sse.NewHub()
	svc := library.NewService(client, nil, nil, nil, func() {}, notADir, hub)

	ch, cancel := hub.Subscribe()
	defer cancel()

	if started := svc.StartScan(context.Background()); !started {
		t.Fatal("StartScan returned false, want true (no scan in flight)")
	}

	events := drainScanEvents(t, ch, 5*time.Second)
	done := events["scan.done"]
	if len(done) != 1 {
		t.Fatalf("scan.done events = %d, want 1", len(done))
	}
	if done[0].Error == "" {
		t.Fatal("scan.done Error = \"\", want non-empty for a failed scan")
	}

	if again := svc.StartScan(context.Background()); !again {
		t.Fatal("StartScan after an errored scan returned false, want true (latch must be released)")
	}
	// Drain the second scan too so its goroutine doesn't outlive the test.
	drainScanEvents(t, ch, 5*time.Second)
}

// TestStartScan_WatchdogTimeoutReleasesLatch proves the Important robustness
// fix: StartScan bounds the single-flight latch with a watchdog so a scan
// that runs longer than scanTimeout still emits a terminal scan.done (with an
// Error naming the timeout) and releases the latch, rather than wedging it
// true forever the way an uninterruptible os.ReadDir hang would without this
// fix.
//
// The watchdog branch of StartScan's select races resultCh against
// time.After(scanTimeout) — select picks pseudo-randomly when both are ready,
// and a real scanWithProgress call (disk read + Postgres round-trip) can
// legitimately finish before even a tiny timeout fires. Shrinking scanTimeout
// alone therefore cannot make the timeout branch win deterministically. The
// only deterministic fix is to keep resultCh UN-ready until after the timeout
// fires: SetScanBlock installs a channel the scan goroutine waits on before
// calling scanWithProgress, so the goroutine can never race the watchdog — the
// select's resultCh case is guaranteed not-ready until this test explicitly
// closes the block, which only happens after the timeout branch has already
// won and been asserted on.
func TestStartScan_WatchdogTimeoutReleasesLatch(t *testing.T) {
	restoreTimeout := library.SetScanTimeout(10 * time.Millisecond)
	block := make(chan struct{})
	restoreBlock := library.SetScanBlock(block)

	storage := t.TempDir()
	writeKaizokuSeries(t, storage, "Manga", "Slow Series", "mangadex", "Alpha", 2)

	client := testdb.New(t)
	hub := sse.NewHub()
	svc := library.NewService(client, nil, nil, nil, func() {}, storage, hub)

	ch, cancel := hub.Subscribe()
	defer cancel()

	if started := svc.StartScan(context.Background()); !started {
		t.Fatal("StartScan returned false, want true (no scan in flight)")
	}

	events := drainScanEvents(t, ch, 5*time.Second)
	done := events["scan.done"]
	if len(done) != 1 {
		t.Fatalf("scan.done events = %d, want 1", len(done))
	}
	if done[0].Error == "" || !containsTimedOut(done[0].Error) {
		t.Fatalf("scan.done Error = %q, want it to mention a timeout", done[0].Error)
	}

	// The first scan's inner goroutine is still blocked on <-block (the
	// watchdog abandoned it, exactly like a real wedged-NFS timeout would).
	// Release it now — after the timeout assertion above, so it can never
	// race the watchdog — and restore both seams BEFORE starting the second
	// scan. Restoring here (synchronously, in program order, ahead of the
	// second StartScan call) rather than via a deferred restore that could
	// still be running as a second scan's goroutine reads scanBlock/
	// scanTimeout is what fixes the second-drain data race: the Go memory
	// model guarantees a `go` statement happens-after everything before it in
	// the launching goroutine, so the second scan's goroutine is guaranteed to
	// observe the restored values, never a torn/concurrent one.
	close(block)
	restoreBlock()
	restoreTimeout()

	if again := svc.StartScan(context.Background()); !again {
		t.Fatal("StartScan after a watchdog timeout returned false, want true (latch must be released)")
	}
	// Drain the second scan too so its goroutine doesn't outlive the test.
	drainScanEvents(t, ch, 5*time.Second)
}

// containsTimedOut reports whether s mentions the watchdog timeout wording
// StartScan broadcasts on the scanTimeout branch.
func containsTimedOut(s string) bool {
	const want = "timed out"
	for i := 0; i+len(want) <= len(s); i++ {
		if s[i:i+len(want)] == want {
			return true
		}
	}
	return false
}
