package enginehost

import (
	"context"
	"log/slog"
	"time"
)

// Default supervision tunables. The interval itself is a runtime setting
// (jobs.engine_supervise_interval) supplied by the caller; these bound the
// restart policy and are launcher constants (test-overridable via With* options).
const (
	managedSourceWorkers      = 8
	managedExhaustionOldest   = 180 * time.Second
	managedExhaustionSamples  = 6
	managedExhaustionCadence  = 30 * time.Second
	managedExhaustionCooldown = 10 * time.Minute

	// defaultSuperviseMaxRestarts is how many restart attempts the supervisor makes
	// PER COOLDOWN-WINDOW for one profile before giving up and entering the post-cap
	// cooldown (staying degraded). It is measured over a rolling window (the cooldown
	// duration) of restart-ATTEMPT timestamps, not a consecutive-failure count, so a
	// slow-crasher — one that passes /health on each restart then dies seconds later
	// — is bounded to ~this many restarts per window too, instead of resetting the
	// count on every brief health-pass and flapping forever (GAP-114). INVARIANT: no
	// more than ~maxRestarts restarts per cooldown-window per profile, regardless of
	// how many of them briefly pass health before dying.
	defaultSuperviseMaxRestarts = 5
	// defaultSuperviseBaseBackoff / defaultSuperviseMaxBackoff bound the
	// exponential gap between restart attempts within an episode (doubling from
	// base, capped at max), so attempts space out instead of firing every tick.
	defaultSuperviseBaseBackoff = 10 * time.Second
	defaultSuperviseMaxBackoff  = 5 * time.Minute
	// defaultSuperviseCooldown is how long the supervisor waits after exhausting
	// its restart budget before resetting and trying again — a genuinely
	// persistent crash (e.g. Xvfb/KCEF display contention) stays degraded during
	// it rather than being hammered. Mirrors the sourcegate breaker cooldown.
	defaultSuperviseCooldown = 30 * time.Minute
	// superviseDisabledRecheck is how long the loop idles when the interval is 0
	// (supervision disabled) before re-reading the setting — the hot-reload idle,
	// same shape as the warm-up / extension-check loops.
	superviseDisabledRecheck = time.Hour
)

// Supervisor keeps the launcher's non-default engine-host instances ALIVE after
// they are spawned (GAP-114). The launcher brings an instance up once (via
// EnsureProfile, driven by ReconcileNetwork); nothing watched it afterwards, so a
// profile instance that died — Xvfb/KCEF contention is a known cause, see
// spawn.go — stayed dead and its bound sources kept hitting the dead port with no
// degrade to the default engine.
//
// The Supervisor closes that gap: on a fixed interval it PROBES each managed
// instance's /health, and for one that is down it
//   - DEGRADES its sources to the default engine (so they attempt against a live
//     instance instead of connection-refusing to a corpse), then
//   - RESTARTS it on its existing port via the launcher's shared spawn path, with
//     bounded exponential backoff and a max-consecutive-failure cap, and
//   - RESTORES its sources to their own instance once a restart brings it back
//     healthy.
//
// A health-responsive instance is also sampled through the bounded /status
// contract. Six unchanged samples proving all eight physical source workers have
// been occupied for longer than 180 seconds trigger the same process-control
// restart path, with diagnostics first and a ten-minute recovery cooldown.
//
// It only ever touches instances the launcher SPAWNED (the non-default profiles).
// The default instance (port 7777) is owned by the container entrypoint, is never
// in the launcher's map, and is therefore never probed, restarted, or re-routed.
//
// COOPERATION WITH RECONCILE. Route degrade/restore go through the degrade OVERLAY
// (engineroute.Router.Degrade/Restore), which is disjoint from the base routing
// table ReconcileNetwork rebuilds via SetRoutes — so the supervisor and a
// concurrent reconcile never clobber each other's routing decision (see the
// Router doc). Restarts run under the launcher's mu, so they can never
// double-spawn against a concurrent EnsureProfile.
//
// VPN CAVEAT. A VPN-required source (e.g. an Omega mirror bound to a SOCKS
// profile) that is degraded to the default engine may be GEO-BLOCKED there — the
// degrade keeps it attempting and visible (a real error beats connection-refused)
// but is not a full substitute for its own egress. Restarting the instance is the
// primary remedy; degrade is the safety net while it is down.
type Supervisor struct {
	launcher *Launcher
	interval func(context.Context) time.Duration

	// Restart policy (defaults above; overridden in tests via With* options).
	maxRestarts int
	baseBackoff time.Duration
	maxBackoff  time.Duration
	cooldown    time.Duration
}

// SupervisorOption customizes a Supervisor at construction (tests shrink the
// timers + cap to exercise the backoff/cap paths fast).
type SupervisorOption func(*Supervisor)

// WithSuperviseMaxRestarts sets the consecutive-failure cap per down episode.
func WithSuperviseMaxRestarts(n int) SupervisorOption {
	return func(s *Supervisor) { s.maxRestarts = n }
}

// WithSuperviseBackoff sets the base + max exponential restart backoff.
func WithSuperviseBackoff(base, max time.Duration) SupervisorOption {
	return func(s *Supervisor) { s.baseBackoff, s.maxBackoff = base, max }
}

// WithSuperviseCooldown sets the post-cap cooldown before restart attempts reset.
func WithSuperviseCooldown(d time.Duration) SupervisorOption {
	return func(s *Supervisor) { s.cooldown = d }
}

// NewSupervisor builds a Supervisor over a launcher, reading its supervise
// interval from the injected accessor at the top of every pass (hot reload, like
// the other job loops). interval returning 0 disables supervision. The restart
// policy uses the package defaults unless overridden by opts.
func NewSupervisor(l *Launcher, interval func(context.Context) time.Duration, opts ...SupervisorOption) *Supervisor {
	s := &Supervisor{
		launcher:    l,
		interval:    interval,
		maxRestarts: defaultSuperviseMaxRestarts,
		baseBackoff: defaultSuperviseBaseBackoff,
		maxBackoff:  defaultSuperviseMaxBackoff,
		cooldown:    defaultSuperviseCooldown,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Start launches the supervision loop in a background goroutine and returns
// immediately. Each iteration re-reads the CURRENT interval (a dynamic timer, not
// a captured ticker): a non-zero interval waits that long then runs one pass; a
// zero interval idles for superviseDisabledRecheck then re-reads, so enabling a
// disabled supervisor at runtime takes effect without a restart. The loop runs
// until ctx is cancelled. A pass never fires more than one restart attempt per
// instance, so restarts are spaced by at least the interval on top of the
// per-instance backoff — no tight loop.
func (s *Supervisor) Start(ctx context.Context) {
	go func() {
		for {
			iv := s.interval(ctx)
			wait := iv
			if iv <= 0 {
				wait = superviseDisabledRecheck // disabled: idle, re-read later (hot reload)
			}
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				slog.InfoContext(ctx, "enginehost: supervisor loop stopped (context cancelled)")
				return
			case <-timer.C:
				if iv <= 0 {
					continue // still disabled; re-read interval on the next pass
				}
				s.runPass(ctx, time.Now())
			}
		}
	}()
}

// runPass runs one supervision pass under a panic guard, so a bug in a single
// pass is logged and the loop survives to the next interval instead of a panic
// taking down the whole process. Mirrors the detached-goroutine recover
// convention in job.Runner.SourcesSummaryHook. Only the background Start loop
// routes through this guard; the direct superviseOnce test seam does not, so a
// test still sees the raw panic.
func (s *Supervisor) runPass(ctx context.Context, now time.Time) {
	defer func() {
		if p := recover(); p != nil {
			slog.WarnContext(ctx, "enginehost: supervisor pass panicked (recovered)", "panic", p)
		}
	}()
	s.superviseOnce(ctx, now)
}

// superviseOnce probes every managed instance once and reconciles its health: a
// down one follows the existing degrade/restart policy; a health-responsive one
// contributes one bounded /status exhaustion sample. Probing happens OUTSIDE the
// launcher mutex; identity checks, evidence updates, and restarts run under it.
func (s *Supervisor) superviseOnce(ctx context.Context, now time.Time) {
	for _, t := range s.launcher.supervisedSnapshot() {
		if ctx.Err() != nil {
			return
		}
		healthy := alive(t.proc) && s.launcher.prober(t.baseURL) == nil
		if !healthy {
			s.launcher.superviseInstance(ctx, s, t, false, now)
			continue
		}

		status, statusErr := s.launcher.statusProber(ctx, t.baseURL)
		if ctx.Err() != nil {
			return
		}
		diagnostic := s.launcher.observeHealthyStatus(t, status, statusErr, now)
		if diagnostic == nil {
			continue
		}
		s.launcher.exhaustionDiagnostics(ctx, *diagnostic)
		if ctx.Err() != nil {
			return
		}
		s.launcher.restartExhausted(ctx, t, *diagnostic, now)
	}
}

// observeHealthyStatus applies one status result under the launcher mutex after
// rechecking the snapshot identity. It returns a bounded diagnostic only after
// six unchanged qualifying samples and once the recovery cooldown is eligible.
func (l *Launcher) observeHealthyStatus(t superviseTarget, status EngineStatus, statusErr error, now time.Time) *ExhaustionDiagnostic {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	mi, ok := l.instances[t.key]
	if !ok || mi != t.mi || mi.proc != t.proc {
		return nil
	}

	// Health succeeded, so any stale health-failure degrade/backoff is cleared even
	// when status is unavailable. Status failure can only decline exhaustion
	// recovery; it never degrades or restarts a control-responsive process.
	l.markHealthyLocked(mi)
	fingerprint, valid := status.exhaustionFingerprint()
	qualifies := statusErr == nil && valid && status.Ready &&
		status.SourceWorkers == managedSourceWorkers &&
		status.Running == managedSourceWorkers &&
		status.OldestRunningMillis > managedExhaustionOldest.Milliseconds()
	if !qualifies {
		resetExhaustionEvidence(mi)
		return nil
	}

	if fingerprint != mi.exhaustionFingerprint || status.CompletionSequence != mi.exhaustionCompletionSequence {
		mi.exhaustionFingerprint = fingerprint
		mi.exhaustionCompletionSequence = status.CompletionSequence
		mi.exhaustionConsecutive = 1
		mi.exhaustionFirstSampleAt = now
		mi.exhaustionNextSampleAt = now.Add(managedExhaustionCadence)
	} else if mi.exhaustionConsecutive < managedExhaustionSamples &&
		!now.Before(mi.exhaustionNextSampleAt) {
		mi.exhaustionConsecutive++
		mi.exhaustionNextSampleAt = now.Add(managedExhaustionCadence)
	}
	if mi.exhaustionConsecutive < managedExhaustionSamples || now.Before(mi.exhaustionNextEligibleAt) {
		return nil
	}

	status.BusiestSources = append([]EngineSourceStatus(nil), status.BusiestSources...)
	return &ExhaustionDiagnostic{
		ProfileKey:   mi.key,
		PID:          mi.proc.Pid(),
		FirstSample:  mi.exhaustionFirstSampleAt,
		LatestSample: now,
		Fingerprint:  fingerprint,
		Status:       status,
	}
}

// restartExhausted rechecks both instance identity and evidence after the
// diagnostic sink ran outside the mutex, then restarts through the launcher's
// existing same-port process-control path. The cooldown is armed before the
// attempt, so success, failure, or a later health-down observation cannot create
// a restart loop.
func (l *Launcher) restartExhausted(ctx context.Context, t superviseTarget, diagnostic ExhaustionDiagnostic, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || ctx.Err() != nil {
		return
	}
	mi, ok := l.instances[t.key]
	if !ok || mi != t.mi || mi.proc != t.proc ||
		mi.exhaustionFingerprint != diagnostic.Fingerprint ||
		mi.exhaustionCompletionSequence != diagnostic.Status.CompletionSequence ||
		mi.exhaustionConsecutive < managedExhaustionSamples ||
		now.Before(mi.exhaustionNextEligibleAt) {
		return
	}

	mi.exhaustionNextEligibleAt = now.Add(managedExhaustionCooldown)
	resetExhaustionEvidence(mi)
	l.degradeLocked(mi)
	if err := l.restartLocked(ctx, mi); err != nil {
		mi.restartFailures++
		mi.nextRestartAt = mi.exhaustionNextEligibleAt
		slog.WarnContext(ctx, "enginehost: exhausted managed instance restart failed",
			"profile", mi.key, "err", err, "next_attempt_at", mi.exhaustionNextEligibleAt)
		return
	}
	l.markHealthyLocked(mi)
	slog.InfoContext(ctx, "enginehost: exhausted managed instance restarted and healthy",
		"profile", mi.key, "port", mi.port, "next_exhaustion_restart_at", mi.exhaustionNextEligibleAt)
}

func resetExhaustionEvidence(mi *managedInstance) {
	mi.exhaustionFingerprint = ""
	mi.exhaustionCompletionSequence = 0
	mi.exhaustionConsecutive = 0
	mi.exhaustionFirstSampleAt = time.Time{}
	mi.exhaustionNextSampleAt = time.Time{}
}

// pruneRestartTimes drops restart-attempt timestamps at or before cutoff (the
// start of the rolling window) and returns the retained slice; its length is the
// number of attempts still inside the window — the value the restart cap is
// measured against. It reuses the input's backing array (the caller reassigns the
// field), so no allocation on the hot path.
func pruneRestartTimes(times []time.Time, cutoff time.Time) []time.Time {
	kept := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	return kept
}

// backoffFor returns the restart backoff after the given number of consecutive
// failures: baseBackoff doubled per prior failure, capped at maxBackoff.
func (s *Supervisor) backoffFor(failures int) time.Duration {
	d := s.baseBackoff
	for i := 1; i < failures; i++ {
		d *= 2
		if d >= s.maxBackoff {
			return s.maxBackoff
		}
	}
	return d
}

// superviseTarget is a launcher-mu snapshot of one managed instance the
// supervisor probes: enough to health-check it (baseURL + proc) and to re-find
// the exact same instance under mu afterwards (key + pointer identity).
type superviseTarget struct {
	key     string
	mi      *managedInstance
	baseURL string
	proc    RunningProcess
}

// supervisedSnapshot returns a mu-guarded snapshot of the current managed
// instances for the supervisor to probe outside the lock. Empty when the launcher
// is closed or manages nothing (the zero-disruption single-instance deployment).
func (l *Launcher) supervisedSnapshot() []superviseTarget {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	out := make([]superviseTarget, 0, len(l.instances))
	for key, mi := range l.instances {
		out = append(out, superviseTarget{key: key, mi: mi, baseURL: mi.baseURL, proc: mi.proc})
	}
	return out
}

// superviseInstance applies one probe result to instance t under mu. It re-finds
// the instance by key and pointer identity (skipping it if it was retired or
// replaced by a concurrent reconcile since the snapshot), then:
//   - healthy: clears any degrade + resets the backoff counters (markHealthy).
//   - down: degrades its sources to the default engine, and — if the backoff gate
//     has elapsed and the restart cap is not yet hit — restarts it on its existing
//     port. The cap is measured over a rolling window (the cooldown duration) of
//     restart-attempt timestamps, counting successful AND failed attempts, so a
//     slow-crasher that briefly passes health each time is bounded too (GAP-114). A
//     successful restart restores routing; a failed one also arms exponential
//     backoff. Once the window count hits the cap the profile enters a cooldown
//     (stays degraded, no further attempts) until the window empties and it is
//     retried.
func (l *Launcher) superviseInstance(ctx context.Context, s *Supervisor, t superviseTarget, healthy bool, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	mi, ok := l.instances[t.key]
	if !ok || mi != t.mi {
		return // retired or replaced since the snapshot — next pass handles the current one
	}

	if healthy {
		if mi.degraded {
			slog.InfoContext(ctx, "enginehost: supervised instance recovered, restoring routing",
				"profile", mi.key, "port", mi.port)
		}
		l.markHealthyLocked(mi)
		return
	}

	// Down: keep its sources on the default engine while we try to bring it back.
	if !mi.degraded {
		slog.WarnContext(ctx, "enginehost: supervised instance is down, degrading its sources to the default engine",
			"profile", mi.key, "port", mi.port, "sources", mi.profile.SourceIDs)
	}
	l.degradeLocked(mi)

	if now.Before(mi.nextRestartAt) {
		return // backoff / cooldown still active — stay degraded, do not attempt
	}
	if now.Before(mi.exhaustionNextEligibleAt) {
		return // a recent exhaustion restart owns the stricter ten-minute cooldown
	}
	// Drop restart attempts that have aged out of the rolling window, then measure
	// the cap against what remains. Counting every attempt — including ones that
	// briefly passed health — is what bounds a slow-crasher (GAP-114).
	mi.restartTimes = pruneRestartTimes(mi.restartTimes, now.Add(-s.cooldown))
	if len(mi.restartTimes) >= s.maxRestarts {
		// Restart budget for this window exhausted: cool down (staying degraded). The
		// window entries age out during the cooldown, so a later, possibly-recovered
		// environment gets another try once they clear.
		mi.nextRestartAt = now.Add(s.cooldown)
		mi.restartFailures = 0
		slog.WarnContext(ctx, "enginehost: supervised instance exhausted restart attempts, cooling down before retry",
			"profile", mi.key, "cooldown", s.cooldown)
		return
	}

	mi.restartTimes = append(mi.restartTimes, now)
	attempt := len(mi.restartTimes)
	slog.InfoContext(ctx, "enginehost: restarting down supervised instance",
		"profile", mi.key, "port", mi.port, "attempt", attempt)
	if err := l.restartLocked(ctx, mi); err != nil {
		mi.restartFailures++
		mi.nextRestartAt = now.Add(s.backoffFor(mi.restartFailures))
		slog.WarnContext(ctx, "enginehost: supervised instance restart failed",
			"profile", mi.key, "attempt", attempt, "err", err, "next_attempt_at", mi.nextRestartAt)
		return
	}
	// Restart succeeded and the instance is healthy (startProcess health-gated it):
	// clear the degrade + reset counters so its sources resume using their instance.
	l.markHealthyLocked(mi)
	slog.InfoContext(ctx, "enginehost: supervised instance restarted and healthy, routing restored",
		"profile", mi.key, "port", mi.port)
}
