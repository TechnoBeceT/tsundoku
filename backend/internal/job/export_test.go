package job

import (
	"context"
	"time"
)

// StartPeriodLoopForTest drives the unexported period-loop primitive (the shared
// timing engine behind Start and StartRefresh) with a caller-supplied cycle body,
// so its contract — period measured from the previous cycle's START, collapsed
// catch-up runs, never-overlapping cycles, trigger re-basing — can be asserted
// from the black-box job_test package without a real download cycle or a database.
//
// run receives triggered=true when a trigger (not the timer) started the cycle;
// mark receives every schedule-state publication the loop makes.
func StartPeriodLoopForTest(
	ctx context.Context,
	interval func(context.Context) time.Duration,
	trigger <-chan struct{},
	run func(context.Context, bool),
	mark func(bool, time.Time),
) {
	periodLoop{
		name:     "test",
		interval: interval,
		trigger:  trigger,
		run:      run,
		mark:     mark,
	}.start(ctx)
}

// BroadcastSourcesSummaryForTest exposes the unexported periodic/transition
// summary emitter (broadcastSourcesSummary — the method runRefreshSweep calls on
// each tick) to the black-box job_test package, so the sync broadcast path can be
// asserted directly without driving a whole refresh sweep.
func (r *Runner) BroadcastSourcesSummaryForTest(ctx context.Context) {
	r.broadcastSourcesSummary(ctx)
}
