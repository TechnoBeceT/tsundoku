package enginehost

import (
	"context"
	"fmt"
	"log/slog"
	"syscall"
	"time"

	"github.com/technobecet/tsundoku/internal/engineroute"
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

	// A profile that solves Cloudflare through its OWN FlareSolverr endpoint does
	// not need the embedded Chromium (KCEF) WebView, so it is spawned with KCEF
	// off. This is the GAP-094 fix: on prod, 2 bound profiles meant 3 engine-host
	// JVMs (default + 2 profiles) each initializing Chromium against the one shared
	// Xvfb, which crashed the extra instances right after they reported healthy.
	// Dropping KCEF for endpoint-mode profiles removes that contention. Profiles
	// WITHOUT their own FlareSolverr (global/none mode) keep KCEF, because they may
	// still need the WebView to solve a challenge themselves.
	disableKCEF := p.FlareMode == engineroute.FlareModeEndpoint

	// KCEF seeding is best-effort — a failure only degrades WebView sources on
	// this instance, never the spawn (see seedKCEF). Skip it entirely when KCEF is
	// disabled: there is no Chromium to seed, so touching the shared bundle symlink
	// + singleton locks would be pointless work.
	if !disableKCEF {
		l.seedKCEF(dataDir)
	}

	// Sharing the default instance's extensions dir is NOT best-effort: without
	// it the profile boots with an empty extensions/ and every routed source
	// fails "unknown sourceId". A failure aborts the spawn so the profile
	// degrades to the fully-provisioned default engine (see linkSharedExtensions).
	if err := l.linkSharedExtensions(dataDir); err != nil {
		return engineroute.Instance{}, fmt.Errorf("enginehost: link shared extensions for profile %q: %w", p.Key, err)
	}

	proc, err := l.starter.Start(port, dataDir, disableKCEF)
	if err != nil {
		return engineroute.Instance{}, fmt.Errorf("enginehost: start profile %q: %w", p.Key, err)
	}

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	if err := l.awaitReady(ctx, proc, baseURL); err != nil {
		// The instance never came up: kill it so it does not linger, then report.
		_ = proc.Kill()
		<-proc.Done() // reap
		return engineroute.Instance{}, fmt.Errorf("enginehost: profile %q not ready: %w", p.Key, err)
	}

	mi := &managedInstance{
		key:     p.Key,
		port:    port,
		dataDir: dataDir,
		baseURL: baseURL,
		proc:    proc,
		client:  l.factory(baseURL),
	}
	l.instances[p.Key] = mi
	slog.InfoContext(ctx, "enginehost: profile instance ready",
		"profile", p.Key, "port", port, "pid", proc.Pid(), "data_dir", dataDir)
	return mi.instance(), nil
}

// awaitReady gates a spawn on the instance being not just healthy but STABLE: it
// first polls /health until it answers, then re-probes once after a short settle
// window. The settle recheck exists because an engine-host JVM can pass /health
// (its HTTP server is up) and then die moments later — the GAP-094 failure mode,
// where the extra Chromium init crashed the process right after it reported
// healthy, so the FIRST reconcile RPC hit an EOF instead of a clean degrade.
// Catching "healthy-then-dead" here lets the caller degrade the profile to the
// default instance instead of routing sources at a corpse.
func (l *Launcher) awaitReady(ctx context.Context, proc RunningProcess, baseURL string) error {
	if err := l.pollHealthy(ctx, proc, baseURL); err != nil {
		return err
	}
	return l.settle(ctx, proc, baseURL)
}

// pollHealthy polls the instance's /health until it answers (ready → nil), the
// process exits early (a boot crash → error), the startup timeout elapses
// (→ error), or ctx is cancelled (a shutdown → ctx.Err()). It probes once
// immediately so an already-healthy instance returns without waiting a tick.
func (l *Launcher) pollHealthy(ctx context.Context, proc RunningProcess, baseURL string) error {
	deadline := time.NewTimer(l.startTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(l.pollInterval)
	defer ticker.Stop()

	for {
		if err := l.prober(baseURL); err == nil {
			return nil
		}
		select {
		case <-proc.Done():
			return fmt.Errorf("process exited before becoming healthy")
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("timed out after %s waiting for /health", l.startTimeout)
		case <-ticker.C:
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
func (l *Launcher) settle(ctx context.Context, proc RunningProcess, baseURL string) error {
	if l.settleDelay <= 0 {
		return nil
	}
	select {
	case <-proc.Done():
		return fmt.Errorf("process exited during settle after becoming healthy")
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(l.settleDelay):
	}
	if err := l.prober(baseURL); err != nil {
		return fmt.Errorf("instance unhealthy after settle: %w", err)
	}
	return nil
}

// stopInstance stops mi's process gracefully: SIGTERM, wait up to stopGrace for a
// clean exit, then SIGKILL if it is still running, and finally wait for the
// process to be reaped. Best-effort — signal/kill errors are ignored (the
// process may already be gone). Callers invoke it OUTSIDE mu.
func (l *Launcher) stopInstance(mi *managedInstance) {
	_ = mi.proc.Signal(syscall.SIGTERM)
	select {
	case <-mi.proc.Done():
		return // exited within the grace period
	case <-time.After(l.stopGrace):
	}
	_ = mi.proc.Kill()
	<-mi.proc.Done()
}
