package job_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/fetcher/fake"
	"github.com/technobecet/tsundoku/internal/job"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/sse"
)

// fakeSnapshotter is a job.BreakerSnapshotter test double returning a fixed
// snapshot (or a fixed error) so the summary broadcast can be exercised without a
// live circuit-breaker.
type fakeSnapshotter struct {
	snap map[string]sourcegate.BreakerState
	err  error
}

func (f fakeSnapshotter) Snapshot(_ context.Context) (map[string]sourcegate.BreakerState, error) {
	return f.snap, f.err
}

// newSummaryRunner builds a minimal Runner over an ephemeral DB + the given
// snapshotter, sharing hub so a subscriber can capture the broadcast.
func newSummaryRunner(t *testing.T, hub *sse.Hub, snap job.BreakerSnapshotter) *job.Runner {
	t.Helper()
	client := testdb.New(t)
	storage := t.TempDir()
	d := download.New(client, fake.New(), hub, download.Config{Storage: storage},
		settings.Static{Retries: 3, Backoff: time.Hour}, nil)
	r := job.NewRunner(d, client, hub, storage, settings.Static{})
	if snap != nil {
		r.SetBreakerSnapshotter(snap)
	}
	return r
}

// awaitSourcesSummary drains events until a sources.summary is seen or timeout;
// returns the decoded payload and whether one arrived.
func awaitSourcesSummary(events <-chan sse.Event, timeout time.Duration) (job.SourcesSummaryEvent, bool) {
	timer := time.After(timeout)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return job.SourcesSummaryEvent{}, false
			}
			if ev.Type == "sources.summary" {
				var payload job.SourcesSummaryEvent
				_ = json.Unmarshal(ev.Data.(json.RawMessage), &payload)
				return payload, true
			}
		case <-timer:
			return job.SourcesSummaryEvent{}, false
		}
	}
}

// TestBroadcastSourcesSummary_EmitsCounts proves the periodic/transition emitter
// pushes a sources.summary carrying the folded erroring/coolingDown counts.
func TestBroadcastSourcesSummary_EmitsCounts(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Minute)
	future := now.Add(time.Hour)
	snap := map[string]sourcegate.BreakerState{
		"Comix": {FailingSince: &past, CooldownUntil: &future}, // erroring + coolingDown
		"Asura": {FailingSince: &past},                         // erroring only
		"Weeb":  {},                                            // healthy
	}
	hub := sse.NewHub()
	events, unsub := hub.Subscribe()
	defer unsub()
	r := newSummaryRunner(t, hub, fakeSnapshotter{snap: snap})

	r.BroadcastSourcesSummaryForTest(context.Background())

	got, ok := awaitSourcesSummary(events, 2*time.Second)
	if !ok {
		t.Fatal("expected a sources.summary event, got none")
	}
	if got.Erroring != 2 || got.CoolingDown != 1 {
		t.Fatalf("payload = %+v, want {Erroring:2 CoolingDown:1}", got)
	}
}

// TestBroadcastSourcesSummary_NilSnapshotterIsNoOp proves the summary is a no-op
// (no event, no panic) when no snapshotter is wired.
func TestBroadcastSourcesSummary_NilSnapshotterIsNoOp(t *testing.T) {
	hub := sse.NewHub()
	events, unsub := hub.Subscribe()
	defer unsub()
	r := newSummaryRunner(t, hub, nil)

	r.BroadcastSourcesSummaryForTest(context.Background())

	if _, ok := awaitSourcesSummary(events, 200*time.Millisecond); ok {
		t.Fatal("no sources.summary expected when no snapshotter is wired")
	}
}

// TestBroadcastSourcesSummary_SnapshotErrorSwallowed proves a snapshot read
// failure is best-effort: no event is emitted and nothing propagates/panics.
func TestBroadcastSourcesSummary_SnapshotErrorSwallowed(t *testing.T) {
	hub := sse.NewHub()
	events, unsub := hub.Subscribe()
	defer unsub()
	r := newSummaryRunner(t, hub, fakeSnapshotter{err: errors.New("db down")})

	r.BroadcastSourcesSummaryForTest(context.Background())

	if _, ok := awaitSourcesSummary(events, 200*time.Millisecond); ok {
		t.Fatal("no sources.summary expected when the snapshot read fails")
	}
}

// TestSourcesSummaryHook_BroadcastsAsync proves the breaker-transition hook (fired
// by sourcegate on a trip/clear) pushes a sources.summary on its detached
// goroutine and returns immediately.
func TestSourcesSummaryHook_BroadcastsAsync(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Minute)
	snap := map[string]sourcegate.BreakerState{"Comix": {FailingSince: &past}}
	hub := sse.NewHub()
	events, unsub := hub.Subscribe()
	defer unsub()
	r := newSummaryRunner(t, hub, fakeSnapshotter{snap: snap})

	r.SourcesSummaryHook() // returns instantly; work happens on a goroutine

	got, ok := awaitSourcesSummary(events, 2*time.Second)
	if !ok {
		t.Fatal("expected the hook to broadcast a sources.summary event")
	}
	if got.Erroring != 1 {
		t.Fatalf("payload = %+v, want Erroring:1", got)
	}
}
