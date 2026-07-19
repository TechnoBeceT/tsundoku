package job

import "context"

// BroadcastSourcesSummaryForTest exposes the unexported periodic/transition
// summary emitter (broadcastSourcesSummary — the method runRefreshSweep calls on
// each tick) to the black-box job_test package, so the sync broadcast path can be
// asserted directly without driving a whole refresh sweep.
func (r *Runner) BroadcastSourcesSummaryForTest(ctx context.Context) {
	r.broadcastSourcesSummary(ctx)
}
