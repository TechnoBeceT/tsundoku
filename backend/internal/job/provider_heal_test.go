package job_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/fetcher/fake"
	"github.com/technobecet/tsundoku/internal/ingest"
	"github.com/technobecet/tsundoku/internal/job"
	"github.com/technobecet/tsundoku/internal/refresh"
	"github.com/technobecet/tsundoku/internal/settings"
	enginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
	"github.com/technobecet/tsundoku/internal/sse"
)

// fakeHealer is a job.ProviderHealer double: it counts calls and returns a
// caller-chosen result, so the sweep's use of the healer can be asserted without
// a real library service, storage root, or CBZ fixture.
type fakeHealer struct {
	calls  atomic.Int64
	merged int
	err    error
}

// HealDriftedProviders records the call and replays the configured outcome.
func (f *fakeHealer) HealDriftedProviders(context.Context) (int, int, error) {
	f.calls.Add(1)
	return f.merged, 0, f.err
}

// newSweepRunner builds a Runner whose refresh loop ticks fast and whose sweep
// has nothing to discover (an unconfigured engine fake) — enough to drive
// runRefreshSweep end to end. Returns the runner plus the SSE stream and the
// refresh service to hand to StartRefresh.
func newSweepRunner(t *testing.T) (*job.Runner, *refresh.Service, <-chan sse.Event) {
	t.Helper()
	client := testdb.New(t)
	storage := t.TempDir()
	hub := sse.NewHub()
	events, unsub := hub.Subscribe()
	t.Cleanup(unsub)

	refreshSvc := refresh.NewService(client, ingest.NewIngest(enginefake.New(), client), hub, settings.Static{Concurrency: 2}, nil)
	d := download.New(client, fake.New(), hub, download.Config{Storage: storage}, settings.Static{Retries: 3, Backoff: time.Hour}, nil)
	r := job.NewRunner(d, client, hub, storage, settings.Static{Refresh: 50 * time.Millisecond})
	return r, refreshSvc, events
}

// awaitHealthSummary drains the SSE stream until a health.summary event arrives,
// returning its unhealthy count. health.summary is the LAST thing a sweep emits
// before the sources summary, so seeing it proves the sweep ran to completion.
func awaitHealthSummary(t *testing.T, events <-chan sse.Event) int {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-events:
			if ev.Type != "health.summary" {
				continue
			}
			raw, _ := ev.Data.(json.RawMessage)
			var p struct {
				Unhealthy int `json:"unhealthy"`
			}
			if err := json.Unmarshal([]byte(raw), &p); err != nil {
				t.Fatalf("unmarshal health.summary: %v", err)
			}
			return p.Unhealthy
		case <-deadline:
			t.Fatal("timed out waiting for health.summary — the sweep never completed")
			return 0
		}
	}
}

// TestRefreshSweep_RunsTheProviderHeal is the wiring proof: a registered healer
// is invoked by every discovery sweep. This is the whole point of GAP-120 — the
// merge machinery already existed but NOTHING called it in the background, so a
// declined merge-at-attach was never retried.
//
// FAILS on the unfixed code: `grep edup internal/job/*.go` returns nothing there,
// so no seam exists and the healer is never called.
func TestRefreshSweep_RunsTheProviderHeal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r, refreshSvc, _ := newSweepRunner(t)
	healer := &fakeHealer{merged: 2}
	r.SetProviderHealer(healer)
	r.StartRefresh(ctx, refreshSvc, func(context.Context) (int, error) { return 0, nil })

	deadline := time.Now().Add(5 * time.Second)
	for healer.calls.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the refresh sweep never invoked the provider healer")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestRefreshSweep_HealErrorDoesNotAbortTheSweep pins the log-and-swallow
// contract: a healer that fails hard must not kill the refresh loop or stop the
// steps after it. health.summary is broadcast at the END of the sweep, so
// receiving it after a failing heal proves the sweep continued.
//
// FAILS on any implementation that propagates the heal error out of
// runRefreshSweep (an early return would skip the health summary entirely, and a
// returned error would eventually stop the loop).
func TestRefreshSweep_HealErrorDoesNotAbortTheSweep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r, refreshSvc, events := newSweepRunner(t)
	healer := &fakeHealer{err: errors.New("heal exploded")}
	r.SetProviderHealer(healer)
	r.StartRefresh(ctx, refreshSvc, func(context.Context) (int, error) { return 4, nil })

	if got := awaitHealthSummary(t, events); got != 4 {
		t.Fatalf("health.summary unhealthy = %d, want 4", got)
	}
	if healer.calls.Load() == 0 {
		t.Fatal("the healer was never called, so the swallow path was not exercised")
	}
}

// TestRefreshSweep_NoHealerIsANoOp proves the seam is nil-safe: with no healer
// registered (every existing call site, and the window before the route layer
// wires one) the sweep runs exactly as before, health summary included.
//
// This one is a GUARD — it passes on the unfixed code too, because there the
// heal call does not exist at all. It exists to stop a future change turning the
// unwired case into a nil-pointer panic inside the background loop.
func TestRefreshSweep_NoHealerIsANoOp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r, refreshSvc, events := newSweepRunner(t)
	r.StartRefresh(ctx, refreshSvc, func(context.Context) (int, error) { return 1, nil })

	if got := awaitHealthSummary(t, events); got != 1 {
		t.Fatalf("health.summary unhealthy = %d, want 1", got)
	}
}
