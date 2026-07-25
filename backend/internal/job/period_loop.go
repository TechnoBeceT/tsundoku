package job

import (
	"context"
	"log/slog"
	"time"
)

// periodLoop drives one recurring background job on a TRUE PERIOD: a cycle starts
// every interval measured from the PREVIOUS CYCLE'S START — never from its end
// (GAP-115). It is the shared engine behind Runner.Start (download cycles) and
// Runner.StartRefresh (discovery sweeps), so the timing rules live in exactly one
// place.
//
// # Why the period is measured from the start
//
// The earlier shape armed the timer at the top of the iteration and ran the cycle
// INSIDE the select case, so the next wait only began once the cycle returned:
// the configured interval was a post-cycle GAP, and the real cadence was
// cycle_duration + interval (measured on a live library: a 90s setting produced a
// cycle roughly every 203s because cycles averaged 113s). Anchoring on the start
// makes the knob mean what it says — "start one every N".
//
// # The four properties this loop guarantees
//
//   - PERIOD FROM START: the next cycle is due at lastStart+interval.
//   - COLLAPSED CATCH-UP: a due instant already in the past waits zero and runs
//     ONCE. Missed ticks never accumulate into a burst.
//   - NO OVERLAP: cycles run on this one goroutine, so a tick that would land
//     while a cycle is still running simply cannot start — skip-if-running comes
//     for free, with no extra state.
//   - BACK-TO-BACK WHEN OUTPACED: when a cycle takes longer than the period, the
//     next wait is zero and cycles run continuously. That is intended — the period
//     is a ceiling on how often a cycle STARTS, not a promise of idle time.
//
// The interval is re-read at the top of every iteration, so an owner's settings
// change takes effect on the next wait without a restart (hot reload).
//
// # Cancellation outranks a due cycle
//
// A cycle never STARTS on an already-cancelled context. That guarantee needs
// explicit checks rather than falling out of the select, and the reason is the
// zero wait: once a cycle has overrun its period the next deadline is already in
// the past, so timer.C is ready at the very same instant as ctx.Done() — and Go
// chooses among ready select cases at RANDOM. Without the checks roughly half of
// all shutdowns that land mid-cycle would start one more doomed cycle (at
// SIGTERM: a spurious cycle.start/cycle.done pair plus a database pass on a dead
// context). The checks only order two simultaneously-ready cases; while ctx is
// live they do nothing, so BACK-TO-BACK WHEN OUTPACED is untouched (GAP-115).
type periodLoop struct {
	// name labels the loop in its stop log line ("download", "refresh").
	name string
	// interval returns the CURRENT period, re-read once per iteration.
	interval func(context.Context) time.Duration
	// trigger is an optional immediate-run signal (Runner.trigger for the download
	// loop). A trigger RE-BASES the schedule: the triggered cycle's start becomes
	// the anchor the next period is measured from. Leave it nil for a loop with no
	// trigger — a nil channel blocks forever in a select, which is exactly the
	// "this case never fires" behaviour wanted.
	trigger <-chan struct{}
	// run executes one cycle on the loop goroutine. triggered reports whether a
	// trigger (rather than the timer) started it, so the callee can label its logs.
	// It must not panic: a panic kills the loop.
	run func(ctx context.Context, triggered bool)
	// mark publishes the loop's schedule state (running?, next-run instant) into
	// the Runner's concurrency-safe snapshot. A zero nextRunAt means "unscheduled".
	mark func(running bool, nextRunAt time.Time)
}

// start launches the loop on its own goroutine and returns immediately. The
// goroutine runs until ctx is cancelled.
func (l periodLoop) start(ctx context.Context) {
	go l.loop(ctx)
}

// loop is the period engine described in the type doc. The first cycle is due one
// full interval after start (the loop anchors on its own start instant), matching
// the long-standing "wait, then run" boot behaviour.
func (l periodLoop) loop(ctx context.Context) {
	lastStart := time.Now()
	for {
		// Don't even set an iteration up on a dead context: a cycle that overran
		// its period returns here with the next deadline already past, which is
		// exactly the zero-wait case the select cannot resolve on its own.
		if ctx.Err() != nil {
			l.stop(ctx)
			return
		}

		interval := l.interval(ctx)
		next := lastStart.Add(interval)
		l.mark(false, next)

		timer := time.NewTimer(waitUntil(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			l.stop(ctx)
			return
		case <-timer.C:
			// The check above cannot be the last word: reading the interval is a
			// live settings read, so cancellation can still arrive between it and
			// this select — and if the wait was zero, both cases are then ready
			// and the choice is random. This is where the ordering is actually
			// enforced, immediately before the only place a timed cycle starts.
			if ctx.Err() != nil {
				l.stop(ctx)
				return
			}
			lastStart = l.runCycle(ctx, interval, false)
		case <-l.trigger:
			timer.Stop()
			lastStart = l.runCycle(ctx, interval, true)
		}
	}
}

// stop publishes the loop's terminal schedule state (unscheduled) and logs the
// exit. Every return from loop goes through it, so a shutdown looks identical
// whichever of the three cancellation checks observed it first.
func (l periodLoop) stop(ctx context.Context) {
	l.mark(false, time.Time{})
	slog.InfoContext(ctx, "job.Runner: "+l.name+" loop stopped (context cancelled)")
}

// runCycle marks the loop running, executes one cycle, and returns the instant the
// cycle STARTED — the anchor the next period is measured from. The published
// next-run instant is that start plus the interval in force, which is what makes
// an overrunning cycle report an already-past (due) next run instead of a
// fabricated future one.
func (l periodLoop) runCycle(ctx context.Context, interval time.Duration, triggered bool) time.Time {
	start := time.Now()
	l.mark(true, start.Add(interval))
	l.run(ctx, triggered)
	return start
}

// waitUntil returns how long to wait for the instant next, floored at zero: a
// deadline already in the past means "run now, once" — never a negative duration,
// and never a queue of missed ticks.
func waitUntil(next time.Time) time.Duration {
	if wait := time.Until(next); wait > 0 {
		return wait
	}
	return 0
}
