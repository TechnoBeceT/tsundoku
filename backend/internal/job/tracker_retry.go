package job

import (
	"context"
	"log/slog"
	"time"

	"github.com/technobecet/tsundoku/internal/tracker/retry"
)

// trackRetryMaxAttempts is the hard per-push attempt cap enforced by every
// tracker-retry pass (Komikku parity — spec/trackers-sync-phase4 §3). A
// pending push that has failed this many times is left in the queue as a
// tracking-health signal and is never retried again; see
// retry.Queue.RunOnce's own doc comment for the full due/terminal rule.
const trackRetryMaxAttempts = 3

// StartTrackerRetry launches a background goroutine that drains the durable,
// coalescing tracker-push retry queue (internal/tracker/retry) on a dynamic
// timer until ctx is cancelled — mirrors StartRefresh/StartWarmup's
// re-read-the-interval-every-pass shape (a runtime change to
// jobs.track_retry_interval takes effect on the very next pass, no
// restart). Each pass calls retrySvc.RunOnce against the current wall-clock
// time and the fixed trackRetryMaxAttempts cap.
//
// pusher is the actual "send this chapter number to the bound tracker" seam
// (retry.Pusher) — Phase 4b (this slice) only builds the queue + worker;
// Phase 4c implements Pusher against the sync rule kernel + the tracker
// port and wires a real instance in main.go. Until then, callers that do not
// yet have a real Pusher simply do not call StartTrackerRetry (the worker
// existing and being callable is the Phase 4b deliverable — see the package
// doc comment on internal/tracker/retry).
//
// A hard RunOnce error (e.g. a DB failure loading due rows) is logged and
// does not stop the loop — a transient failure must not permanently kill
// the retry worker. Returns immediately.
func (r *Runner) StartTrackerRetry(ctx context.Context, retrySvc *retry.Queue, pusher retry.Pusher) {
	go func() {
		for {
			timer := time.NewTimer(r.intervals.TrackRetryInterval(ctx))
			select {
			case <-ctx.Done():
				timer.Stop()
				slog.InfoContext(ctx, "job.Runner: tracker-retry loop stopped (context cancelled)")
				return
			case <-timer.C:
				r.runTrackerRetryPass(ctx, retrySvc, pusher)
			}
		}
	}()
}

// runTrackerRetryPass runs one bounded retry.Queue.RunOnce pass and logs
// (without stopping the loop) any hard error.
func (r *Runner) runTrackerRetryPass(ctx context.Context, retrySvc *retry.Queue, pusher retry.Pusher) {
	processed, err := retrySvc.RunOnce(ctx, pusher, time.Now(), trackRetryMaxAttempts)
	if err != nil {
		slog.ErrorContext(ctx, "job.Runner: tracker-retry pass error", "err", err)
		return
	}
	if processed > 0 {
		slog.InfoContext(ctx, "job.Runner: tracker-retry pass finished", "processed", processed)
	}
}
