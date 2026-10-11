package job

import (
	"context"
	"log/slog"
	"time"

	"github.com/technobecet/tsundoku/internal/download"
)

// sourceCoordinator owns discovery cadence, window completion reporting, and
// batched library housekeeping. The dispatcher owns chapter admission/work.
type sourceCoordinator struct {
	r                              *Runner
	ctx                            context.Context
	periodic                       bool
	scheduler                      *download.SourceScheduler
	lastOpportunity                time.Time
	wait, interval                 time.Duration
	active, cycleOpen, pendingWork bool
	upgraded                       int
	sourceWake                     time.Time
}

// runSourceSchedule admits one coordinator lifetime, including manual drains.
func (r *Runner) runSourceSchedule(ctx context.Context, periodic bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// A replacement Start observes its own epoch settings before waiting for
	// chapter ownership; runtime convergence registration stays independent.
	if periodic {
		_ = r.intervals.DownloadInterval(ctx)
	}
	select {
	case r.downloadOwner <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-r.downloadOwner }()
	if err := ctx.Err(); err != nil {
		return err
	}
	r.downloadMu.Lock()
	if r.downloadStopping {
		r.downloadMu.Unlock()
		return context.Canceled
	}
	owned, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	r.downloadCancel = cancel
	r.downloadDone = done
	r.downloadMu.Unlock()
	defer func() { cancel(); close(done) }()
	c := sourceCoordinator{r: r, ctx: owned, periodic: periodic, lastOpportunity: time.Now()}
	defer func() {
		if c.scheduler != nil {
			c.scheduler.Close()
		}
		if periodic {
			r.schedule.markDownload(false, time.Time{})
		}
	}()
	return c.run()
}

func (c *sourceCoordinator) run() (err error) {
	defer func() {
		if err != nil && c.cycleOpen {
			c.r.broadcastCycle("cycle.done", CycleEvent{Upgraded: c.upgraded, Error: err.Error()})
		}
	}()
	for c.ctx.Err() == nil {
		triggered, err := c.waitForDiscovery()
		if err != nil {
			return err
		}
		drained, err := c.discover(triggered)
		if err != nil {
			if !c.retry(err) {
				return err
			}
			continue
		}
		if !c.periodic && drained {
			return nil
		}
	}
	return c.ctx.Err()
}

func (c *sourceCoordinator) discover(triggered bool) (bool, error) {
	now := time.Now()
	opportunity := c.scheduler == nil || triggered || (c.periodic && !now.Before(c.lastOpportunity.Add(c.interval)))
	if err := c.prepare(opportunity, now); err != nil {
		return false, err
	}
	if triggered {
		c.scheduler.RestartDiscovery()
	}
	step, err := c.nextStep(now, opportunity)
	if err != nil {
		return false, err
	}
	if err := c.apply(step, opportunity); err != nil {
		return false, err
	}
	return !step.Running && !step.More, nil
}

// nextStep keeps frequent completion polls separate from candidate discovery.
func (c *sourceCoordinator) nextStep(now time.Time, opportunity bool) (download.SourceStep, error) {
	step, err := c.scheduler.Poll(now, c.interval)
	if err != nil || !c.discoveryDue(step, opportunity, now) {
		return step, err
	}
	completed, upgraded := step.Completed, step.Upgraded
	step, err = c.scheduler.Step(now, c.interval)
	step.Completed = step.Completed || completed
	step.Upgraded += upgraded
	return step, err
}

func (c *sourceCoordinator) discoveryDue(step download.SourceStep, opportunity bool, now time.Time) bool {
	// Consume the previously reported deadline once. Poll only reports future
	// deadlines, so a drained source cannot spin on an expired allowance window.
	renewalDue := !c.sourceWake.IsZero() && !now.Before(c.sourceWake)
	return opportunity || step.More || step.Completed || renewalDue
}

func (c *sourceCoordinator) waitForDiscovery() (bool, error) {
	c.updateWait()
	timer := time.NewTimer(max(0, c.wait))
	defer timer.Stop()
	triggered := false
	select {
	case <-c.ctx.Done():
		return false, c.ctx.Err()
	case <-timer.C:
	case <-c.completionSignal():
	case <-c.r.trigger:
		triggered = true
	}
	if err := c.ctx.Err(); err != nil {
		if triggered {
			c.r.Trigger()
		}
		return false, err
	}
	return triggered, nil
}

func (c *sourceCoordinator) completionSignal() <-chan struct{} {
	if c.scheduler == nil {
		return nil
	}
	return c.scheduler.CompletionSignal()
}

func (c *sourceCoordinator) updateWait() {
	if c.periodic {
		c.interval = max(time.Millisecond, c.r.intervals.DownloadInterval(c.ctx))
		if !c.active && !c.cycleOpen {
			next := c.lastOpportunity.Add(c.interval)
			if !c.sourceWake.IsZero() && c.sourceWake.Before(next) {
				next = c.sourceWake
			}
			c.wait = waitUntil(next)
			c.r.schedule.markDownload(false, next)
		}
	}
}

func (c *sourceCoordinator) prepare(opportunity bool, now time.Time) error {
	if opportunity {
		c.lastOpportunity = now
		if c.periodic {
			c.r.requestRuntimeRetry()
		}
		if !c.cycleOpen {
			c.r.broadcastCycle("cycle.start", CycleEvent{})
			c.cycleOpen = true
		}
	}
	if c.scheduler == nil {
		var err error
		c.scheduler, err = c.r.dispatcher.NewSourceScheduler(c.ctx)
		return err
	}
	if c.periodic && opportunity {
		return c.scheduler.RefreshPolicy()
	}
	return nil
}

func (c *sourceCoordinator) apply(step download.SourceStep, opportunity bool) error {
	c.active = step.Running
	c.sourceWake = step.WakeAt
	c.upgraded += step.Upgraded
	c.pendingWork = c.pendingWork || step.Completed
	if step.Started && !c.cycleOpen {
		c.r.broadcastCycle("cycle.start", CycleEvent{})
		c.cycleOpen = true
	}
	drained := !step.Running && !step.More
	// Suppression and notifications are library-wide: batch them on the configured
	// cadence or after a drain, instead of running them for each completed chapter.
	if c.housekeepingDue(drained, opportunity) {
		if err := c.r.finishDownloadWork(c.ctx); err != nil {
			return err
		}
		c.pendingWork = false
	}
	if drained && c.cycleOpen {
		c.r.broadcastCycle("cycle.done", CycleEvent{Upgraded: c.upgraded})
		c.cycleOpen = false
		c.upgraded = 0
	}
	c.finishStep(step)
	return nil
}

func (c *sourceCoordinator) housekeepingDue(drained, opportunity bool) bool {
	return (c.pendingWork && opportunity) || (drained && c.cycleOpen)
}

func (c *sourceCoordinator) finishStep(step download.SourceStep) {
	c.markSchedule(step)
	c.wait = min(time.Second, c.interval)
	if c.periodic {
		c.wait = min(c.wait, waitUntil(c.lastOpportunity.Add(c.interval)))
		if !step.WakeAt.IsZero() {
			c.wait = min(c.wait, waitUntil(step.WakeAt))
		}
	}
	if !c.periodic {
		c.wait = 100 * time.Millisecond
	}
	if step.More {
		c.wait = 0
	}
}

func (c *sourceCoordinator) markSchedule(step download.SourceStep) {
	if !c.periodic {
		return
	}
	next := step.NextRunAt
	if next.IsZero() {
		next = c.lastOpportunity.Add(c.interval)
	}
	c.r.schedule.markDownload(step.Running, next)
}

func (c *sourceCoordinator) retry(err error) bool {
	if !c.periodic || c.ctx.Err() != nil {
		return false
	}
	c.r.broadcastCycle("cycle.done", CycleEvent{Upgraded: c.upgraded, Error: err.Error()})
	c.cycleOpen = false
	c.upgraded = 0
	slog.ErrorContext(c.ctx, "job.Runner: source scheduling failed; retrying on the next interval", "err", err)
	c.wait = c.interval
	c.sourceWake = time.Time{}
	return true
}

// ShutdownDownloads closes chapter admission and joins all owned window tails.
// A timeout leaves dependencies open; another call can continue the same join.
func (r *Runner) ShutdownDownloads(ctx context.Context) error {
	r.downloadMu.Lock()
	r.downloadStopping = true
	cancel, done := r.downloadCancel, r.downloadDone
	if cancel != nil {
		cancel()
	}
	r.downloadMu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Runner) startSourceSchedule(ctx context.Context) {
	for ctx.Err() == nil {
		if err := r.runSourceSchedule(ctx, true); err != nil && ctx.Err() == nil {
			if err == context.Canceled {
				return
			}
			slog.ErrorContext(ctx, "job.Runner: source scheduler startup failed; retrying on the next interval", "err", err)
		}
		timer := time.NewTimer(max(time.Millisecond, r.intervals.DownloadInterval(ctx)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		case <-r.trigger:
			timer.Stop()
			r.Trigger()
		}
	}
}
