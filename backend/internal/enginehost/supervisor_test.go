package enginehost_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/enginehost"
)

// fixedInterval is a supervise-interval accessor returning a constant (the
// superviseOnce-driven tests never consult it, but NewSupervisor requires one).
func fixedInterval(d time.Duration) func(context.Context) time.Duration {
	return func(context.Context) time.Duration { return d }
}

// TestSupervise_RestartsDeadInstanceAndRestores proves the core supervision loop:
// a managed instance that has died is degraded to the default engine, restarted
// on its existing port via the launcher's spawn path, and — once the restart is
// healthy — its sources are restored to their own instance. Recovery happens in
// the same pass as the successful restart.
func TestSupervise_RestartsDeadInstanceAndRestores(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	rr := newFakeRerouter()
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithRerouter(rr))
	sup := enginehost.NewSupervisor(l, fixedInterval(30*time.Second))

	if _, err := l.EnsureProfile(context.Background(), profileWithSources("k1", 10, 11)); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}
	// The JVM dies.
	starter.proc(0).exit()

	enginehost.SuperviseOnce(sup, context.Background(), time.Now())

	if starter.callCount() != 2 {
		t.Fatalf("starter callCount = %d, want 2 (initial spawn + one restart)", starter.callCount())
	}
	if rr.isDegraded(10) || rr.isDegraded(11) {
		t.Error("sources still degraded after a successful restart, want restored")
	}
	degraded, failures, ok := enginehost.InstanceSupervisionState(l, "k1")
	if !ok {
		t.Fatal("instance k1 not managed after restart")
	}
	if degraded || failures != 0 {
		t.Errorf("post-recovery state degraded=%v failures=%d, want false/0", degraded, failures)
	}
	// The restart reused the SAME port + data dir (so the base route stays valid).
	if got := starter.lastCall().port; got != 41001 {
		t.Errorf("restart port = %d, want 41001 (reused, not reallocated)", got)
	}
}

// TestSupervise_RestartFailsToCapThenStaysDegraded proves a persistently-crashing
// instance is retried up to the cap (bounded attempts), its sources stay degraded
// to the default engine, and after the cap the supervisor stops attempting
// (enters cooldown) rather than hammering in a tight loop.
func TestSupervise_RestartFailsToCapThenStaysDegraded(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	rr := newFakeRerouter()
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithRerouter(rr))
	sup := enginehost.NewSupervisor(l, fixedInterval(30*time.Second),
		enginehost.WithSuperviseMaxRestarts(3),
		enginehost.WithSuperviseBackoff(time.Millisecond, time.Millisecond),
		enginehost.WithSuperviseCooldown(time.Hour))

	if _, err := l.EnsureProfile(context.Background(), profileWithSources("k1", 10)); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}
	// Make every subsequent restart fail, then kill the instance.
	starter.setErr(errors.New("crash on boot"))
	starter.proc(0).exit()

	now := time.Now()
	// Drive enough passes to exhaust the cap and enter cooldown. Advance now past
	// the (tiny) backoff each pass so backoff never masks an attempt.
	for i := 0; i < 5; i++ {
		enginehost.SuperviseOnce(sup, context.Background(), now)
		now = now.Add(time.Second)
	}

	// Exactly maxRestarts (3) restart attempts on top of the initial spawn: passes
	// 4 and 5 hit the cap/cooldown and attempt nothing (no tight loop).
	if got := starter.attemptCount(); got != 1+3 {
		t.Errorf("Start attempts = %d, want 4 (initial + 3 capped restarts)", got)
	}
	if !rr.isDegraded(10) {
		t.Error("source not degraded while its instance is persistently down")
	}
	degraded, failures, ok := enginehost.InstanceSupervisionState(l, "k1")
	if !ok || !degraded {
		t.Errorf("state ok=%v degraded=%v, want managed + degraded", ok, degraded)
	}
	// After the cap the failure count is reset for the cooldown episode.
	if failures != 0 {
		t.Errorf("post-cap failures = %d, want 0 (reset into cooldown)", failures)
	}
}

// TestSupervise_RecoversAfterAFailedRestart proves a failed restart arms backoff
// and keeps the sources degraded, and that a LATER pass (once the environment
// recovers) restarts the instance and restores routing.
func TestSupervise_RecoversAfterAFailedRestart(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	rr := newFakeRerouter()
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithRerouter(rr))
	sup := enginehost.NewSupervisor(l, fixedInterval(30*time.Second),
		enginehost.WithSuperviseBackoff(time.Millisecond, time.Millisecond))

	if _, err := l.EnsureProfile(context.Background(), profileWithSources("k1", 10)); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}
	starter.setErr(errors.New("transient"))
	starter.proc(0).exit()

	now := time.Now()
	enginehost.SuperviseOnce(sup, context.Background(), now) // fails → degraded, backoff armed
	if !rr.isDegraded(10) {
		t.Fatal("source not degraded after the first failed restart")
	}
	if _, failures, _ := enginehost.InstanceSupervisionState(l, "k1"); failures != 1 {
		t.Errorf("failures after one failed restart = %d, want 1", failures)
	}

	// Environment recovers; a later pass (past the backoff) restarts + restores.
	starter.setErr(nil)
	enginehost.SuperviseOnce(sup, context.Background(), now.Add(time.Second))

	if rr.isDegraded(10) {
		t.Error("source still degraded after a successful recovery restart")
	}
	if degraded, failures, _ := enginehost.InstanceSupervisionState(l, "k1"); degraded || failures != 0 {
		t.Errorf("post-recovery state degraded=%v failures=%d, want false/0", degraded, failures)
	}
}

// TestSupervise_HealthyInstanceUntouched proves a healthy managed instance is
// left completely alone — never restarted, never degraded — and that a launcher
// managing nothing (the default-instance-only deployment: the default 7777 is
// owned by the entrypoint and never enters the launcher's map) is a pure no-op.
func TestSupervise_HealthyInstanceUntouched(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	rr := newFakeRerouter()
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithRerouter(rr))
	sup := enginehost.NewSupervisor(l, fixedInterval(30*time.Second))

	// No instances yet: a pass over an empty launcher does nothing.
	enginehost.SuperviseOnce(sup, context.Background(), time.Now())
	if starter.attemptCount() != 0 {
		t.Fatalf("empty-launcher pass started %d processes, want 0", starter.attemptCount())
	}

	if _, err := l.EnsureProfile(context.Background(), profileWithSources("k1", 10)); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}
	enginehost.SuperviseOnce(sup, context.Background(), time.Now())

	if starter.callCount() != 1 {
		t.Errorf("healthy instance was restarted (%d spawns), want 1 (untouched)", starter.callCount())
	}
	if d, _ := rr.counts(); d != 0 {
		t.Errorf("healthy instance was degraded %d times, want 0", d)
	}
	if rr.isDegraded(10) {
		t.Error("healthy instance's source is degraded, want routed to its own instance")
	}
}

// TestSupervise_BackoffPreventsTightLoop proves a down instance is retried at most
// ONCE per pass while its backoff is unexpired: repeated passes at the same wall
// clock (within the backoff window) make no further restart attempts, so a
// persistently-failing instance is never hammered in a tight loop.
func TestSupervise_BackoffPreventsTightLoop(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	rr := newFakeRerouter()
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithRerouter(rr))
	sup := enginehost.NewSupervisor(l, fixedInterval(30*time.Second),
		// A large backoff so the second+ passes at the same clock are all gated.
		enginehost.WithSuperviseBackoff(time.Hour, time.Hour))

	if _, err := l.EnsureProfile(context.Background(), profileWithSources("k1", 10)); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}
	starter.setErr(errors.New("still crashing"))
	starter.proc(0).exit()

	now := time.Now()
	for i := 0; i < 4; i++ {
		enginehost.SuperviseOnce(sup, context.Background(), now) // same clock every pass
	}

	// Initial spawn + exactly ONE restart attempt; the other three passes were
	// gated by the unexpired backoff.
	if got := starter.attemptCount(); got != 1+1 {
		t.Errorf("Start attempts = %d, want 2 (initial + one; backoff gated the rest)", got)
	}
	if !rr.isDegraded(10) {
		t.Error("source not degraded while its instance stays down")
	}
}

// TestSupervise_StartLoopRunsPasses proves the background Start loop actually
// drives supervision passes on its interval: a dead instance is auto-restarted
// without any manual SuperviseOnce, and the loop exits on ctx cancel.
func TestSupervise_StartLoopRunsPasses(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	rr := newFakeRerouter()
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithRerouter(rr))
	sup := enginehost.NewSupervisor(l, fixedInterval(2*time.Millisecond))

	if _, err := l.EnsureProfile(context.Background(), profileWithSources("k1", 10)); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}
	starter.proc(0).exit() // the instance dies before the loop starts

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sup.Start(ctx)

	// The loop should restart it (a second spawn) within a short window.
	deadline := time.After(2 * time.Second)
	for starter.callCount() < 2 {
		select {
		case <-deadline:
			t.Fatalf("Start loop did not restart the dead instance (callCount=%d)", starter.callCount())
		case <-time.After(2 * time.Millisecond):
		}
	}
	cancel() // loop must return on ctx cancel
}

// TestSupervise_RestartsWedgedInstanceStopsOldProcessFirst proves the restart
// lifecycle handles a WEDGED-but-alive instance (its process is still running but
// /health fails): the supervisor stops the old process BEFORE respawning on the
// same port, so the fresh process is not blocked by the old one still holding the
// port (which would otherwise fail every restart forever and leak the wedged JVM).
func TestSupervise_RestartsWedgedInstanceStopsOldProcessFirst(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true} // the wedged proc exits on SIGTERM
	rr := newFakeRerouter()
	// Healthy for the initial spawn, wedged (proc alive + /health failing) for the
	// supervise probe, then healthy again so the restart's own readiness gate passes.
	prober := sequenceProber(nil, errors.New("wedged: /health not answering"), nil)
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, prober,
		enginehost.WithRerouter(rr))
	sup := enginehost.NewSupervisor(l, fixedInterval(30*time.Second))

	if _, err := l.EnsureProfile(context.Background(), profileWithSources("k1", 10)); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}
	// The instance is now wedged: its process stays ALIVE but /health fails.
	enginehost.SuperviseOnce(sup, context.Background(), time.Now())

	// The old (wedged) process was stopped before the respawn — without that, its
	// still-held port would block the fresh process every time.
	if !starter.proc(0).wasSignalled() {
		t.Error("wedged instance's old process was not stopped before restart")
	}
	// A fresh process was started, reusing the SAME port (no bind-failure loop).
	if starter.callCount() != 2 {
		t.Fatalf("starter callCount = %d, want 2 (initial spawn + one restart)", starter.callCount())
	}
	if got := starter.lastCall().port; got != 41001 {
		t.Errorf("restart port = %d, want 41001 (reused, not reallocated)", got)
	}
	// The restart brought it back: routing restored, counters reset.
	if rr.isDegraded(10) {
		t.Error("source still degraded after the wedged instance was restarted healthy")
	}
	if degraded, failures, ok := enginehost.InstanceSupervisionState(l, "k1"); !ok || degraded || failures != 0 {
		t.Errorf("post-restart state ok=%v degraded=%v failures=%d, want managed/false/0", ok, degraded, failures)
	}
}

// TestSupervise_SlowCrasherBoundedToCapThenDegraded proves the max-restart cap
// bounds a slow-crasher: an instance that passes /health on every restart and then
// dies shortly after must NOT be restarted every interval forever. Because the cap
// is measured over a rolling window counting every restart ATTEMPT (not a
// consecutive-failure count reset by each brief health-pass), the profile is
// bounded to ~the cap within the window and then stays degraded for the cooldown.
func TestSupervise_SlowCrasherBoundedToCapThenDegraded(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	rr := newFakeRerouter()
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithRerouter(rr))
	sup := enginehost.NewSupervisor(l, fixedInterval(30*time.Second),
		enginehost.WithSuperviseMaxRestarts(3),
		enginehost.WithSuperviseBackoff(time.Millisecond, time.Millisecond),
		enginehost.WithSuperviseCooldown(time.Hour))

	if _, err := l.EnsureProfile(context.Background(), profileWithSources("k1", 10)); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}

	// A slow-crasher: every restart's startProcess SUCCEEDS (passes /health) and the
	// instance then dies shortly after. Model that by exiting the current (newest)
	// process right before each pass, so the pass observes it down and restarts it —
	// a restart that itself succeeds yet dies before the next pass. Advance the clock
	// past the (tiny) backoff each pass so backoff never masks an attempt.
	now := time.Now()
	for i := 0; i < 8; i++ {
		starter.proc(starter.callCount() - 1).exit()
		enginehost.SuperviseOnce(sup, context.Background(), now)
		now = now.Add(time.Second)
	}

	// Bounded to the cap: initial spawn + exactly 3 restarts, even though 8 passes
	// each saw the instance down. Without the window bound, the brief health-pass on
	// each restart would reset the counter and it would be restarted every pass.
	if got := starter.callCount(); got != 1+3 {
		t.Errorf("successful starts = %d, want 4 (initial + 3 capped restarts); slow-crasher not bounded", got)
	}
	// And it STAYS degraded through the cooldown — the trailing passes attempt nothing.
	if !rr.isDegraded(10) {
		t.Error("slow-crasher source not degraded after hitting the restart cap")
	}
	if degraded, _, ok := enginehost.InstanceSupervisionState(l, "k1"); !ok || !degraded {
		t.Errorf("state ok=%v degraded=%v, want managed + degraded during cooldown", ok, degraded)
	}
}

// TestSupervise_PassPanicRecovers proves a panic inside a supervision pass is
// recovered so the background loop survives and keeps running: the first pass
// panics, and later passes still execute (the loop is not taken down with the
// process).
func TestSupervise_PassPanicRecovers(t *testing.T) {
	starter := &fakeStarter{} // closeOnSignal false: the instance stays alive
	rr := newFakeRerouter()

	var armed atomic.Bool
	var supCalls atomic.Int32
	// Healthy during setup; once armed, panic on the FIRST supervise probe then
	// report healthy — so the panic lands inside a pass, not during the spawn.
	prober := func(string) error {
		if !armed.Load() {
			return nil
		}
		if supCalls.Add(1) == 1 {
			panic("supervise pass boom")
		}
		return nil
	}
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, prober,
		enginehost.WithRerouter(rr))
	sup := enginehost.NewSupervisor(l, fixedInterval(2*time.Millisecond))

	if _, err := l.EnsureProfile(context.Background(), profileWithSources("k1", 10)); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}
	armed.Store(true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sup.Start(ctx)

	// The first pass panics and is recovered; the loop must run further passes.
	deadline := time.After(2 * time.Second)
	for supCalls.Load() < 2 {
		select {
		case <-deadline:
			t.Fatalf("supervise loop did not survive a panicking pass (supCalls=%d)", supCalls.Load())
		case <-time.After(2 * time.Millisecond):
		}
	}
	cancel()
}

// TestSupervise_StartLoopDisabledIdles proves a 0 interval disables supervision:
// the loop idles (re-reading the setting) and never runs a pass, so a dead
// instance is left untouched — the hot-reloadable off switch.
func TestSupervise_StartLoopDisabledIdles(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	rr := newFakeRerouter()
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithRerouter(rr))
	sup := enginehost.NewSupervisor(l, fixedInterval(0)) // disabled

	if _, err := l.EnsureProfile(context.Background(), profileWithSources("k1", 10)); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}
	starter.proc(0).exit()

	ctx, cancel := context.WithCancel(context.Background())
	sup.Start(ctx)
	time.Sleep(30 * time.Millisecond) // give a disabled loop time to (not) act
	cancel()

	if starter.callCount() != 1 {
		t.Errorf("disabled supervisor restarted the instance (%d spawns), want 1 (idle)", starter.callCount())
	}
	if rr.isDegraded(10) {
		t.Error("disabled supervisor degraded a source, want no supervision action")
	}
}
