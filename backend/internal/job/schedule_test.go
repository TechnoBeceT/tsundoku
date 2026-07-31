package job_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/fetcher/fake"
	"github.com/technobecet/tsundoku/internal/ingest"
	"github.com/technobecet/tsundoku/internal/job"
	"github.com/technobecet/tsundoku/internal/refresh"
	"github.com/technobecet/tsundoku/internal/settings"
	enginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
	"github.com/technobecet/tsundoku/internal/sse"
)

// newScheduleRunner builds a Runner over a fresh testdb client with the given
// download/refresh intervals, plus the refresh service its sweep loop needs. The
// library is empty, so both a download cycle and a refresh sweep are fast no-ops
// — these tests are about SCHEDULING, not about what a cycle does.
func newScheduleRunner(t *testing.T, download, refreshEvery time.Duration) (*job.Runner, *refresh.Service) {
	t.Helper()
	client := testdb.New(t)
	storage := t.TempDir()
	hub := sse.NewHub()

	intervals := settings.Static{Download: download, Refresh: refreshEvery, Concurrency: 2}
	dispatcher := newScheduleDispatcher(t, client, hub, storage)
	refreshSvc := refresh.NewService(client, ingest.NewIngest(enginefake.New(), client), hub, intervals, nil)
	return job.NewRunner(dispatcher, client, hub, storage, intervals), refreshSvc
}

// newScheduleDispatcher builds the dispatcher the schedule tests drive cycles
// with: the test-only fake fetcher, one retry, a long backoff.
func newScheduleDispatcher(t *testing.T, client *ent.Client, hub *sse.Hub, storage string) *download.Dispatcher {
	t.Helper()
	return download.New(client, fake.New(), hub, download.Config{Storage: storage},
		settings.Static{Retries: 1, Backoff: time.Hour}, nil)
}

// TestRunner_ScheduleSnapshot_UnstartedIsUnscheduled proves a Runner whose loops
// were never started reports the honest "nothing is scheduled" snapshot rather
// than a fabricated next-run instant.
func TestRunner_ScheduleSnapshot_UnstartedIsUnscheduled(t *testing.T) {
	r, _ := newScheduleRunner(t, time.Hour, time.Hour)

	got := r.ScheduleSnapshot()
	if got.Download.Running || !got.Download.NextRunAt.IsZero() {
		t.Errorf("download schedule = %+v, want not running with a zero next-run", got.Download)
	}
	if got.Refresh.Running || !got.Refresh.NextRunAt.IsZero() {
		t.Errorf("refresh schedule = %+v, want not running with a zero next-run", got.Refresh)
	}
}

// TestRunner_ScheduleSnapshot_ReportsNextRunPerLoop proves each loop publishes its
// own next-run instant (one period ahead) as soon as it starts, and that the
// snapshot goes back to unscheduled once the context is cancelled and the loops
// exit.
func TestRunner_ScheduleSnapshot_ReportsNextRunPerLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const period = time.Hour
	r, refreshSvc := newScheduleRunner(t, period, period)

	before := time.Now()
	r.Start(ctx)
	r.StartRefresh(ctx, refreshSvc, func(context.Context) (int, error) { return 0, nil })

	snap := waitForSchedule(t, r, func(s job.Schedule) bool {
		return !s.Download.NextRunAt.IsZero() && !s.Refresh.NextRunAt.IsZero()
	}, "both loops to publish a next-run instant")

	for _, tc := range []struct {
		name string
		loop job.LoopSchedule
	}{
		{"download", snap.Download},
		{"refresh", snap.Refresh},
	} {
		if tc.loop.Running {
			t.Errorf("%s: running = true, want false while waiting out the period", tc.name)
		}
		// The first cycle is due one full period after the loop started.
		if got := tc.loop.NextRunAt; got.Before(before.Add(period)) || got.After(time.Now().Add(period)) {
			t.Errorf("%s: nextRunAt = %v, want ~one %v period from loop start", tc.name, got, period)
		}
	}

	// Cancelling the context must clear the schedule — "no next run is planned" is
	// the truth once the goroutines exit.
	cancel()
	waitForSchedule(t, r, func(s job.Schedule) bool {
		return s.Download.NextRunAt.IsZero() && s.Refresh.NextRunAt.IsZero()
	}, "both loops to report an unscheduled state after cancel")
}

// TestRunner_ScheduleSnapshot_TriggeredCycleRebasesNextRun proves an owner-forced
// cycle (Trigger, e.g. "Download now") re-bases the published schedule onto that
// cycle's start, and that the loop reports itself not-running again once the cycle
// returns.
func TestRunner_ScheduleSnapshot_TriggeredCycleRebasesNextRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// A long period so only the trigger drives the cycle; the cycle itself is a
	// no-op on an empty library, so "running" is observed by polling fast.
	const period = time.Hour
	r, _ := newScheduleRunner(t, period, period)
	r.Start(ctx)
	waitForSchedule(t, r, func(s job.Schedule) bool { return !s.Download.NextRunAt.IsZero() },
		"the download loop to publish its first next-run instant")

	triggeredAt := time.Now()
	r.Trigger()

	// The triggered cycle re-bases the schedule: the next run is due one period
	// after the TRIGGERED cycle's start, which is strictly later than the instant
	// published when the loop started.
	snap := waitForSchedule(t, r, func(s job.Schedule) bool {
		return s.Download.NextRunAt.After(triggeredAt.Add(period - time.Second))
	}, "the triggered cycle to re-base the next-run instant")

	if snap.Download.NextRunAt.Before(triggeredAt.Add(period - time.Second)) {
		t.Errorf("nextRunAt = %v, want ~%v (the triggered cycle's start + the period)",
			snap.Download.NextRunAt, triggeredAt.Add(period))
	}
	// Once the (no-op) cycle returns, the loop is waiting again.
	waitForSchedule(t, r, func(s job.Schedule) bool { return !s.Download.Running },
		"the download loop to report not-running after the triggered cycle")
}

// TestRunner_ScheduleSnapshot_ConcurrentReadsDuringCycles is the race proof: HTTP
// handler goroutines read the snapshot while BOTH ticker goroutines write it on
// every state change. Run under `go test -race ./internal/job/` it fails on any
// unsynchronized access; the sanity assertions also catch a torn read (a next-run
// instant that belongs to no loop).
func TestRunner_ScheduleSnapshot_ConcurrentReadsDuringCycles(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Very short periods so both loops are constantly starting cycles (and thus
	// constantly writing the snapshot) for the whole reader window.
	r, refreshSvc := newScheduleRunner(t, 2*time.Millisecond, 2*time.Millisecond)
	r.Start(ctx)
	r.StartRefresh(ctx, refreshSvc, func(context.Context) (int, error) { return 0, nil })

	const readers = 8
	deadline := time.Now().Add(500 * time.Millisecond)
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				if !snapshotIsIntact(t, r.ScheduleSnapshot()) {
					return
				}
			}
		}()
	}
	// Trigger concurrently too — a third writer path into the download loop.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for time.Now().Before(deadline) {
			r.Trigger()
			time.Sleep(time.Millisecond)
		}
	}()
	wg.Wait()
}

// snapshotIsIntact reports whether both published next-run instants are
// plausible wall-clock times. A published instant is never before the epoch-ish
// past, so a garbage year is the signature of a TORN read — the sanity check
// that catches unsynchronized access even without the race detector. It uses
// t.Errorf (never t.Fatal) because it runs on the reader goroutines, and
// returns false so the caller stops reading once it has reported.
func snapshotIsIntact(t *testing.T, snap job.Schedule) bool {
	t.Helper()
	if !snap.Download.NextRunAt.IsZero() && snap.Download.NextRunAt.Year() < 2000 {
		t.Errorf("torn download snapshot: %+v", snap.Download)
		return false
	}
	if !snap.Refresh.NextRunAt.IsZero() && snap.Refresh.NextRunAt.Year() < 2000 {
		t.Errorf("torn refresh snapshot: %+v", snap.Refresh)
		return false
	}
	return true
}

// waitForSchedule polls the runner's schedule snapshot until cond holds (or fails
// the test after a fixed deadline), returning the snapshot that satisfied it.
// what describes the awaited condition for the failure message.
func waitForSchedule(t *testing.T, r *job.Runner, cond func(job.Schedule) bool, what string) job.Schedule {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		snap := r.ScheduleSnapshot()
		if cond(snap) {
			return snap
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s (last snapshot: %+v)", what, snap)
		}
		time.Sleep(time.Millisecond)
	}
}
