package job_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/job"
)

// cycleRecorder records when each cycle of a period loop STARTED, how long the
// loop ever ran concurrently, and whether each cycle was trigger-driven. It is the
// instrument every period-loop test measures with, so the timing assertions read
// as plain arithmetic on start instants.
type cycleRecorder struct {
	mu        sync.Mutex
	starts    []time.Time
	triggered []bool
	inFlight  int
	maxFlight int
	// work is how long each cycle body takes (simulating a real cycle's duration).
	work time.Duration
}

// run is the loop's cycle body: it stamps the start, holds for the configured
// work duration, and tracks peak concurrency so an overlap is provable.
func (c *cycleRecorder) run(_ context.Context, triggered bool) {
	c.mu.Lock()
	c.starts = append(c.starts, time.Now())
	c.triggered = append(c.triggered, triggered)
	c.inFlight++
	if c.inFlight > c.maxFlight {
		c.maxFlight = c.inFlight
	}
	work := c.work
	c.mu.Unlock()

	time.Sleep(work)

	c.mu.Lock()
	c.inFlight--
	c.mu.Unlock()
}

// snapshot returns a copy of the recorded cycle starts, their triggered flags, and
// the peak number of concurrently-running cycles.
func (c *cycleRecorder) snapshot() ([]time.Time, []bool, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Time(nil), c.starts...), append([]bool(nil), c.triggered...), c.maxFlight
}

// count returns how many cycles have started so far.
func (c *cycleRecorder) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.starts)
}

// waitForCycles blocks until the recorder has seen at least n cycle starts, or
// fails the test after the deadline.
func (c *cycleRecorder) waitForCycles(t *testing.T, n int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for c.count() < n {
		if time.Now().After(deadline) {
			t.Fatalf("only %d cycle(s) started within %v, want >= %d", c.count(), within, n)
		}
		time.Sleep(time.Millisecond)
	}
}

// fixedInterval returns an interval function that always reports d.
func fixedInterval(d time.Duration) func(context.Context) time.Duration {
	return func(context.Context) time.Duration { return d }
}

// TestPeriodLoop_PeriodMeasuredFromCycleStart is the teeth behind GAP-115: the
// interval is a PERIOD anchored on the previous cycle's START, not a gap appended
// after it ends. With a 200ms period and a 120ms cycle, starts must be ~200ms
// apart; the old post-cycle-gap shape produced 320ms (interval + cycle duration).
func TestPeriodLoop_PeriodMeasuredFromCycleStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const period = 200 * time.Millisecond
	rec := &cycleRecorder{work: 120 * time.Millisecond}
	job.StartPeriodLoopForTest(ctx, fixedInterval(period), nil, rec.run, func(bool, time.Time) {})

	rec.waitForCycles(t, 3, 5*time.Second)
	starts, _, maxFlight := rec.snapshot()

	if maxFlight > 1 {
		t.Errorf("cycles overlapped (peak in-flight %d, want 1)", maxFlight)
	}
	for i := 1; i < 3; i++ {
		delta := starts[i].Sub(starts[i-1])
		// A Go timer never fires early, so the lower bound is the period itself.
		if delta < period {
			t.Errorf("start %d→%d gap = %v, want >= the %v period", i-1, i, delta, period)
		}
		// The regression direction: period + cycle duration (320ms) means the
		// interval was treated as a post-cycle gap again.
		if delta > period+60*time.Millisecond {
			t.Errorf("start %d→%d gap = %v, want ~%v — the interval is being applied AFTER the cycle instead of as a period", i-1, i, delta, period)
		}
	}
}

// TestPeriodLoop_MissedTicksCollapseAndNeverOverlap proves the skip-if-running
// half of the contract: with a period far shorter than the cycle duration, ticks
// that land mid-cycle are DROPPED, never queued. Cycles then run back-to-back
// (the intended behaviour when the period is outpaced) — so no two cycles may
// start closer together than one cycle duration, and no burst of instant catch-up
// runs may appear.
func TestPeriodLoop_MissedTicksCollapseAndNeverOverlap(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const (
		period = 10 * time.Millisecond
		work   = 80 * time.Millisecond
	)
	rec := &cycleRecorder{work: work}
	job.StartPeriodLoopForTest(ctx, fixedInterval(period), nil, rec.run, func(bool, time.Time) {})

	rec.waitForCycles(t, 4, 5*time.Second)
	cancel()

	starts, _, maxFlight := rec.snapshot()
	if maxFlight > 1 {
		t.Fatalf("cycles overlapped (peak in-flight %d, want 1) — a tick started while a cycle was running", maxFlight)
	}
	for i := 1; i < len(starts); i++ {
		// Every missed tick collapsed into ONE catch-up run: a queued burst would
		// show consecutive starts far closer together than a cycle duration.
		if delta := starts[i].Sub(starts[i-1]); delta < work {
			t.Errorf("start %d→%d gap = %v, want >= the %v cycle duration — missed ticks accumulated into a burst", i-1, i, delta, work)
		}
	}
}

// TestPeriodLoop_TriggerRunsImmediatelyAndRebases proves a trigger both forces an
// immediate cycle AND re-bases the schedule onto that cycle's start: the next
// timed cycle is due one full period after the TRIGGERED cycle began, not after
// whatever anchor preceded it.
func TestPeriodLoop_TriggerRunsImmediatelyAndRebases(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const (
		period    = 200 * time.Millisecond
		work      = 100 * time.Millisecond
		triggerAt = 50 * time.Millisecond
	)
	rec := &cycleRecorder{work: work}
	trigger := make(chan struct{}, 1)
	loopStart := time.Now()
	job.StartPeriodLoopForTest(ctx, fixedInterval(period), trigger, rec.run, func(bool, time.Time) {})

	time.Sleep(triggerAt)
	trigger <- struct{}{}

	rec.waitForCycles(t, 2, 5*time.Second)
	starts, triggered, _ := rec.snapshot()

	// The trigger must not have waited out the period.
	if elapsed := starts[0].Sub(loopStart); elapsed >= period {
		t.Errorf("triggered cycle started %v after the loop began, want promptly after the %v trigger", elapsed, triggerAt)
	}
	if !triggered[0] || triggered[1] {
		t.Errorf("triggered flags = %v, want the first cycle trigger-driven and the second timer-driven", triggered[:2])
	}
	// Re-based: the second cycle is due one period after the TRIGGERED cycle's
	// start. Had the trigger left the old anchor in place, it would have started
	// about (period - triggerAt) = 150ms after it instead.
	if delta := starts[1].Sub(starts[0]); delta < period {
		t.Errorf("triggered→next start gap = %v, want >= the %v period — the trigger did not re-base the schedule", delta, period)
	}
}

// TestPeriodLoop_ReReadsIntervalEveryIteration proves the hot-reload contract at
// the primitive level: the period is re-read at the top of every iteration, so a
// settings change applies to the very next wait without a restart.
func TestPeriodLoop_ReReadsIntervalEveryIteration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	current := 10 * time.Millisecond
	reads := 0
	interval := func(context.Context) time.Duration {
		mu.Lock()
		defer mu.Unlock()
		reads++
		return current
	}
	readCount := func() int {
		mu.Lock()
		defer mu.Unlock()
		return reads
	}

	rec := &cycleRecorder{}
	job.StartPeriodLoopForTest(ctx, interval, nil, rec.run, func(bool, time.Time) {})

	rec.waitForCycles(t, 3, 5*time.Second)
	if got := readCount(); got < 3 {
		t.Fatalf("interval read %d time(s) across 3 cycles, want one read per iteration", got)
	}

	// A longer period must slow the loop down immediately: after the change, the
	// next cycle may not start sooner than the NEW period.
	mu.Lock()
	current = 300 * time.Millisecond
	mu.Unlock()
	before := rec.count()
	changedAt := time.Now()
	rec.waitForCycles(t, before+2, 5*time.Second)

	starts, _, _ := rec.snapshot()
	last := starts[before+1]
	if elapsed := last.Sub(changedAt); elapsed < 300*time.Millisecond {
		t.Errorf("two more cycles ran %v after the period was raised to 300ms — the loop kept the old interval", elapsed)
	}
}

// scheduleMark is one schedule-state publication the loop made.
type scheduleMark struct {
	running bool
	next    time.Time
}

// markRecorder collects every schedule-state publication a period loop makes, so
// the transitions (wait → run → stop) can be asserted after the fact.
type markRecorder struct {
	mu    sync.Mutex
	marks []scheduleMark
}

// record is the loop's mark callback.
func (m *markRecorder) record(running bool, next time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.marks = append(m.marks, scheduleMark{running: running, next: next})
}

// all returns a copy of every publication recorded so far.
func (m *markRecorder) all() []scheduleMark {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]scheduleMark(nil), m.marks...)
}

// waitForUnscheduled blocks until the most recent publication carries a zero
// next-run instant (the loop's shutdown write), failing the test on timeout.
func (m *markRecorder) waitForUnscheduled(t *testing.T, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		marks := m.all()
		if len(marks) > 0 && marks[len(marks)-1].next.IsZero() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("loop never published the unscheduled (zero next-run) state within %v after cancel", within)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestPeriodLoop_PublishesScheduleState proves the loop publishes its schedule
// state at every transition: not-running with the next-run instant while waiting,
// running while a cycle executes, and UNSCHEDULED (zero instant) once the context
// is cancelled and the goroutine exits.
func TestPeriodLoop_PublishesScheduleState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const period = 40 * time.Millisecond
	marks := &markRecorder{}
	rec := &cycleRecorder{work: 20 * time.Millisecond}
	job.StartPeriodLoopForTest(ctx, fixedInterval(period), nil, rec.run, marks.record)

	rec.waitForCycles(t, 2, 5*time.Second)
	cancel()
	marks.waitForUnscheduled(t, 2*time.Second)

	got := marks.all()
	if len(got) < 3 {
		t.Fatalf("only %d schedule publications, want at least wait/run/stop", len(got))
	}
	// The first publication is the pre-wait one: not running, next run scheduled.
	if got[0].running || got[0].next.IsZero() {
		t.Errorf("first publication = %+v, want not-running with a scheduled next run", got[0])
	}
	// The second is the cycle start: running, with the next run one period on.
	if !got[1].running || got[1].next.IsZero() {
		t.Errorf("second publication = %+v, want running with a scheduled next run", got[1])
	}
	if gap := got[1].next.Sub(got[0].next); gap < period-time.Millisecond {
		t.Errorf("next-run advanced by %v across a cycle, want ~the %v period", gap, period)
	}
}
