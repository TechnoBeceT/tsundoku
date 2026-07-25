package job

import (
	"sync"
	"time"
)

// Schedule is a point-in-time snapshot of the runner's two periodic loops — the
// download cycle (Start) and the discovery sweep (StartRefresh). It is a plain
// value copy, safe to hand to an HTTP handler goroutine while the loops keep
// running (GAP-115).
type Schedule struct {
	// Download is the download-cycle loop's state (Runner.Start).
	Download LoopSchedule
	// Refresh is the discovery-sweep loop's state (Runner.StartRefresh).
	Refresh LoopSchedule
}

// LoopSchedule is one periodic loop's schedule state.
//
// # The NextRunAt contract (read this before rendering a countdown)
//
// NextRunAt is the EARLIEST instant the next cycle may start: the start of the
// most recent cycle plus the interval that was in force when it started. Because
// the interval is a TRUE PERIOD measured from the previous cycle's START, this
// instant can already be IN THE PAST while Running is true — that is what an
// overrunning cycle looks like (a 90s period with a 113s cycle), and it is
// reported honestly rather than papered over with a fabricated future timestamp.
// A past NextRunAt means "due now": the next cycle starts the moment the current
// one returns (the tick is collapsed, never queued).
//
// NextRunAt is the zero Time when the loop is not scheduled at all — it was never
// started, or its context has been cancelled and the goroutine has exited.
type LoopSchedule struct {
	// Running reports whether a cycle is executing RIGHT NOW. Cycles never
	// overlap: each loop runs its cycles on a single goroutine, so a tick that
	// lands while Running is true is skipped rather than run concurrently.
	Running bool
	// NextRunAt is the earliest instant the next cycle may start. See the type
	// doc — it may be in the past (due/overdue) and is zero when unscheduled.
	NextRunAt time.Time
}

// schedule is the Runner's concurrency-safe schedule store: the two ticker
// goroutines WRITE it (once per state change: before waiting, at cycle start, and
// on shutdown) while HTTP handler goroutines READ it via Runner.ScheduleSnapshot.
// Every access goes through mu, so the snapshot is race-free by construction.
type schedule struct {
	mu       sync.RWMutex
	download LoopSchedule
	refresh  LoopSchedule
}

// markDownload publishes the download loop's current state.
func (s *schedule) markDownload(running bool, nextRunAt time.Time) {
	s.set(&s.download, running, nextRunAt)
}

// markRefresh publishes the refresh loop's current state.
func (s *schedule) markRefresh(running bool, nextRunAt time.Time) {
	s.set(&s.refresh, running, nextRunAt)
}

// set writes one loop's state under the write lock. dst must point at a field of
// this schedule (the lock guards both fields).
func (s *schedule) set(dst *LoopSchedule, running bool, nextRunAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	*dst = LoopSchedule{Running: running, NextRunAt: nextRunAt}
}

// snapshot returns a consistent copy of both loops' state under the read lock.
func (s *schedule) snapshot() Schedule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Schedule{Download: s.download, Refresh: s.refresh}
}

// ScheduleSnapshot returns the current schedule of the download and refresh
// loops: whether each is running and when each may next start. It is a pure
// in-memory read — no DB query, no engine call — and is safe to call from any
// goroutine while the loops run, which is what lets an HTTP handler serve it
// (GET /api/engine/schedule).
//
// A Runner whose loops were never started reports the zero LoopSchedule (not
// running, zero NextRunAt); see LoopSchedule for the full NextRunAt contract.
func (r *Runner) ScheduleSnapshot() Schedule {
	return r.schedule.snapshot()
}
