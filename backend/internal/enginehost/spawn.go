package enginehost

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/technobecet/tsundoku/internal/engineroute"
	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// spawn allocates a port, seeds KCEF, launches a fresh engine-host process for p,
// waits for it to become healthy, and — on success — records it and returns its
// handle. On any failure the (possibly-started) process is killed and the error
// is returned so the caller degrades p to the default instance. Called with mu
// held (see Launcher's concurrency contract).
func (l *Launcher) spawn(ctx context.Context, p engineroute.Profile) (engineroute.Instance, error) {
	port, err := l.allocPort()
	if err != nil {
		return engineroute.Instance{}, fmt.Errorf("enginehost: allocate port for profile %q: %w", p.Key, err)
	}
	dataDir := dataDirFor(l.cfg.DataDir, p.Key)

	proc, client, baseURL, group, err := l.startProcess(ctx, p, port, dataDir)
	if err != nil {
		return engineroute.Instance{}, err
	}

	mi := &managedInstance{
		key:       p.Key,
		port:      port,
		dataDir:   dataDir,
		baseURL:   baseURL,
		proc:      proc,
		client:    client,
		kcefGroup: group,
		profile:   p,
	}
	l.instances[p.Key] = mi
	// A freshly-healthy instance: clear any stale degrade overlay for its sources
	// left by a prior down episode + reset the supervision counters.
	l.markHealthyLocked(mi)
	slog.InfoContext(ctx, "enginehost: profile instance ready",
		"profile", p.Key, "port", port, "pid", proc.Pid(), "data_dir", dataDir)
	return mi.instance(), nil
}

// startProcess seeds KCEF, links the shared extensions dir, launches the
// engine-host process for p on the given port + data dir, and waits for it to
// become healthy-and-stable. On success it returns the running process, a
// factory-built client aimed at the instance, its base URL, and any KCEF group
// reservation. On any failure the (possibly-started) process is killed and
// reaped; an unconfirmed group remains charged to capacity. It is the SHARED
// core of both the initial spawn (fresh allocated port) and a supervisor restart
// (existing port + data dir), so the KCEF/extensions/health-gate logic lives in
// one place (§2 DRY). Called with mu held.
func (l *Launcher) startProcess(ctx context.Context, p engineroute.Profile, port int, dataDir string) (RunningProcess, sourceengine.Client, string, *kcefProcessGroup, error) {
	// Profile derivation owns this decision. Route mode is insufficient: Required
	// can keep an endpoint profile on, and Disabled can turn a global profile off.
	kcefEnabled := p.KCEFEnabled

	// KCEF seeding is best-effort — a failure only degrades WebView sources on
	// this instance, never the spawn (see seedKCEF). Skip it entirely when KCEF is
	// disabled: there is no Chromium to seed, so touching the shared bundle symlink
	// + singleton locks would be pointless work. On a RESTART this also clears the
	// dead instance's stale Chromium singleton locks, so the new Chromium can start.
	if kcefEnabled {
		l.seedKCEF(dataDir)
	}

	// Sharing the default instance's extensions dir is NOT best-effort: without
	// it the profile boots with an empty extensions/ and every routed source
	// fails "unknown sourceId". A failure aborts the spawn so the profile
	// degrades to the fully-provisioned default engine (see linkSharedExtensions).
	if err := l.linkSharedExtensions(dataDir); err != nil {
		return nil, nil, "", nil, fmt.Errorf("enginehost: link shared extensions for profile %q: %w", p.Key, err)
	}

	spawnedAt := l.readinessClock.Now()
	var group *kcefProcessGroup
	if kcefEnabled {
		var reserveErr error
		group, reserveErr = l.reserveKCEFGroupLocked(p.Key)
		if reserveErr != nil {
			return nil, nil, "", nil, reserveErr
		}
	}
	proc, err := l.starter.Start(port, dataDir, kcefEnabled)
	if err != nil {
		l.cancelStartingKCEFGroupLocked(group)
		return nil, nil, "", nil, fmt.Errorf("enginehost: start profile %q: %w", p.Key, err)
	}
	if group != nil {
		group.proc = proc
	}

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	readinessDeadline := spawnedAt.Add(l.readinessTimeout)
	readinessCtx, cancelReadiness := l.readinessContext(ctx, readinessDeadline)
	defer cancelReadiness()
	if err := l.awaitReady(readinessCtx, proc, baseURL, p.KCEFEnabled, readinessDeadline); err != nil {
		// The instance never came up. Teardown uses an independent finite budget so
		// caller cancellation cannot orphan the process, while an unconfirmed group
		// remains charged to capacity for a later reap attempt.
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), l.stopGrace)
		gone := killProcessGroup(cleanupCtx, proc, l.stopGrace)
		cancelCleanup()
		if group != nil {
			group.retiring = true
			if gone {
				delete(l.kcefGroups, group)
			}
		}
		return nil, nil, "", nil, fmt.Errorf("enginehost: profile %q not ready: %w", p.Key, err)
	}
	return proc, l.factory(baseURL), baseURL, group, nil
}

// readinessContext preserves the caller's cancellation while applying the one
// spawn-relative readiness deadline to every in-flight probe. Context deadlines
// use wall time in production; the clock timer also makes the same absolute
// budget deterministic in tests and prevents a blocking probe from extending it.
func (l *Launcher) readinessContext(parent context.Context, deadline time.Time) (context.Context, context.CancelFunc) {
	effectiveDeadline := deadline
	if parentDeadline, ok := parent.Deadline(); ok && parentDeadline.Before(effectiveDeadline) {
		effectiveDeadline = parentDeadline
	}
	ctx, cancel := context.WithDeadline(parent, effectiveDeadline)
	remaining := effectiveDeadline.Sub(l.readinessClock.Now())
	if remaining <= 0 {
		cancel()
		return ctx, cancel
	}
	timer := l.readinessClock.NewTimer(remaining)
	go func() {
		select {
		case <-timer.C():
			cancel()
		case <-ctx.Done():
			_ = timer.Stop()
		}
	}()
	return ctx, func() {
		_ = timer.Stop()
		cancel()
	}
}

// restartLocked respawns a dead-or-wedged managed instance on its EXISTING port +
// data dir, replacing its process + client IN PLACE (the *managedInstance pointer,
// its key, profile, and supervision counters are preserved). Reusing the same port
// keeps the base routing entry — a client keyed by the unchanged base URL — valid
// the moment a fresh process listens there, so a restart needs no base-table
// rebuild; clearing the degrade overlay (the caller's job on success) is enough to
// restore routing. Called with mu held by the supervisor. Returns an error if the
// respawn failed (the instance stays dead + degraded; the caller backs off).
//
// The supervisor restarts an instance that FAILS /health, which includes a
// wedged-but-still-ALIVE JVM. Such a process still holds mi.port (and its Chromium
// singleton lock), so a fresh process could never bind — the restart would fail
// every attempt forever and the wedged process would leak. So, mirroring
// EnsureProfile's not-reusable teardown, stop the old process first when it is
// still alive: the crash case (already exited) skips it, and the hang case is
// killed here, both freeing the port + clearing the stale singleton lock the
// startProcess KCEF-reseed step expects.
func (l *Launcher) restartLocked(ctx context.Context, mi *managedInstance) error {
	// Done proves only that the JVM was reaped; owned Chromium descendants may
	// still keep its group alive. Always drive the old generation through group
	// teardown before reserving the same key again.
	if !l.stopInstanceLocked(ctx, mi) {
		return lingeringProcessGroupError(mi)
	}
	proc, client, _, group, err := l.startProcess(ctx, mi.profile, mi.port, mi.dataDir)
	if err != nil {
		return err
	}
	mi.proc = proc
	mi.client = client
	mi.kcefGroup = group
	resetExhaustionEvidence(mi)
	return nil
}

// awaitReady gates a spawn on the instance being healthy, capability-compatible,
// and stable: it polls /health, requires the profile's KCEF state from /status,
// then re-probes health after a short settle window. The settle recheck exists
// because an engine-host JVM can pass /health (its HTTP server is up) and then
// die moments later — the GAP-094 failure mode, where the extra Chromium init
// crashed the process right after it reported healthy, so the FIRST reconcile
// RPC hit an EOF instead of a clean degrade. Catching "healthy-then-dead" here
// lets the caller degrade the profile to the default instance instead of routing
// sources at a corpse.
func (l *Launcher) awaitReady(ctx context.Context, proc RunningProcess, baseURL string, kcefEnabled bool, deadline time.Time) error {
	if err := l.pollHealthy(ctx, proc, baseURL, deadline); err != nil {
		return err
	}
	if err := l.pollKCEF(ctx, proc, baseURL, kcefEnabled, deadline); err != nil {
		return err
	}
	return l.settle(ctx, proc, baseURL, deadline)
}

// pollKCEF waits for the capability that the profile was explicitly launched
// with. /health only proves that RPC liveness is up; publishing an enabled
// profile before its browser producer settles would route WebView calls into an
// unbounded wait. A failed KCEF generation is terminal and rejects immediately;
// an initializing one consumes only the normal launcher startup budget.
func (l *Launcher) pollKCEF(ctx context.Context, proc RunningProcess, baseURL string, enabled bool, deadline time.Time) error {
	timeout, err := l.readinessTimer(deadline)
	if err != nil {
		return err
	}
	defer timeout.Stop()
	ticker := l.readinessClock.NewTicker(l.pollInterval)
	defer ticker.Stop()

	for {
		status, err := l.statusProber(ctx, baseURL)
		if err := l.readinessElapsed(deadline); err != nil {
			return err
		}
		if err == nil {
			if ready, terminalErr := kcefStatusReady(status.KCEF, enabled); terminalErr != nil {
				return terminalErr
			} else if ready {
				return nil
			}
		}
		select {
		case <-proc.Done():
			return fmt.Errorf("process exited before KCEF became ready")
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout.C():
			return l.readinessTimeoutError()
		case <-ticker.C():
		}
	}
}

func kcefStatusReady(status KCEFStatus, enabled bool) (bool, error) {
	if enabled {
		switch status.State {
		case KCEFStateReady:
			return true, nil
		case KCEFStateFailed:
			return false, fmt.Errorf("KCEF failed before readiness")
		case KCEFStateInitializing:
			return false, nil
		case KCEFStateDisabled:
			return false, fmt.Errorf("KCEF is disabled for an enabled profile")
		default:
			return false, fmt.Errorf("invalid KCEF status")
		}
	}
	if status.State == KCEFStateDisabled {
		return true, nil
	}
	return false, fmt.Errorf("KCEF is enabled for a disabled profile")
}

// pollHealthy polls the instance's /health until it answers (ready → nil), the
// process exits early (a boot crash → error), the coordinated launch budget elapses
// (→ error), or ctx is cancelled (a shutdown → ctx.Err()). It probes once
// immediately so an already-healthy instance returns without waiting a tick.
func (l *Launcher) pollHealthy(ctx context.Context, proc RunningProcess, baseURL string, deadline time.Time) error {
	timeout, err := l.readinessTimer(deadline)
	if err != nil {
		return err
	}
	defer timeout.Stop()
	ticker := l.readinessClock.NewTicker(l.pollInterval)
	defer ticker.Stop()

	for {
		if err := l.prober(ctx, baseURL); err == nil {
			if err := l.readinessElapsed(deadline); err != nil {
				return err
			}
			return nil
		}
		select {
		case <-proc.Done():
			return fmt.Errorf("process exited before becoming healthy")
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout.C():
			return l.readinessTimeoutError()
		case <-ticker.C():
			// Poll again at the top of the loop.
		}
	}
}

// settle waits settleDelay after the first healthy probe, then re-probes once, so
// a JVM that reports healthy and immediately crashes is caught as not-ready
// rather than handed back as a live instance (see awaitReady + GAP-094). A
// non-positive settleDelay skips the recheck (used by tests that pin the
// poll-only semantics). During the wait it also watches for an early process
// exit / ctx cancel so a crash or shutdown returns promptly.
func (l *Launcher) settle(ctx context.Context, proc RunningProcess, baseURL string, deadline time.Time) error {
	if l.settleDelay <= 0 {
		return l.readinessElapsed(deadline)
	}
	remaining := deadline.Sub(l.readinessClock.Now())
	if remaining <= 0 {
		return l.readinessTimeoutError()
	}
	delay := min(l.settleDelay, remaining)
	timer := l.readinessClock.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-proc.Done():
		return fmt.Errorf("process exited during settle after becoming healthy")
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C():
		if delay < l.settleDelay {
			return l.readinessTimeoutError()
		}
	}
	if err := l.prober(ctx, baseURL); err != nil {
		return fmt.Errorf("instance unhealthy after settle: %w", err)
	}
	return l.readinessElapsed(deadline)
}

func (l *Launcher) readinessTimer(deadline time.Time) (ReadinessTimer, error) {
	remaining := deadline.Sub(l.readinessClock.Now())
	if remaining <= 0 {
		return nil, l.readinessTimeoutError()
	}
	return l.readinessClock.NewTimer(remaining), nil
}

func (l *Launcher) readinessElapsed(deadline time.Time) error {
	if l.readinessClock.Now().Before(deadline) {
		return nil
	}
	return l.readinessTimeoutError()
}

func (l *Launcher) readinessTimeoutError() error {
	return fmt.Errorf("timed out after %s waiting for launch readiness", l.readinessTimeout)
}
