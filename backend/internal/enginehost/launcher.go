package enginehost

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/technobecet/tsundoku/internal/engineroute"
	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// ErrLauncherClosed is returned by EnsureProfile after Close has run — the
// launcher is shutting down and must not spawn anything new.
var ErrLauncherClosed = errors.New("enginehost: launcher closed")

// EngineHostLauncherConfig is the typed configuration the launcher needs, copied
// out of config.EngineConfig by main (config stays the sole env boundary — this
// package never reads env).
type EngineHostLauncherConfig struct {
	// HostBin is the engine-host launcher binary spawned per profile
	// (cfg.Engine.HostBin).
	HostBin string
	// DataDir is the engine-host data root; per-profile dirs live under
	// "<DataDir>/profiles/<hash>" (cfg.Engine.DataDir).
	DataDir string
	// KCEFBundle is the pre-downloaded Chromium runtime symlinked into each
	// profile's data dir; blank or absent ⇒ KCEF seeding is skipped
	// (cfg.Engine.KCEFBundle).
	KCEFBundle string
}

// managedInstance is one running (or previously-running) engine-host process the
// launcher owns, keyed by its profile Key.
type managedInstance struct {
	key     string
	port    int
	dataDir string
	baseURL string
	proc    RunningProcess
	client  sourceengine.Client
	// profile is the full profile this instance serves — retained so a supervisor
	// restart reuses the same port/data dir + KCEF mode, and so degrade/restore
	// know which source ids to move (profile.SourceIDs).
	profile engineroute.Profile

	// Supervision state (GAP-114), mutated only under Launcher.mu by the
	// supervisor: whether this instance's sources are currently degraded to the
	// default engine, the count of consecutive failed restart attempts, and the
	// earliest time the next restart may be attempted (backoff / post-cap cooldown
	// gate).
	degraded        bool
	restartFailures int
	nextRestartAt   time.Time
	// restartTimes is a rolling window of restart-ATTEMPT timestamps — successful
	// AND failed — bounding restarts to maxRestarts per cooldown-window even when
	// each restart briefly passes /health then dies (the slow-crasher failure mode:
	// a post-settle crash that would otherwise reset restartFailures and be
	// restarted every interval forever). Entries age out of the window; a whole
	// window with no restart empties it, which is the only thing that clears the
	// cap — a transient post-restart health-pass deliberately does NOT (GAP-114).
	restartTimes []time.Time
}

// instance projects a managedInstance into the engineroute.Instance the reconcile
// consumes.
func (m *managedInstance) instance() engineroute.Instance {
	return engineroute.Instance{Key: m.key, BaseURL: m.baseURL, Client: m.client}
}

// Launcher spawns and supervises one engine-host JVM per non-default network
// profile, satisfying engineroute.Launcher. Construct with New.
//
// CONCURRENCY. All state (the instance map + the closed flag) is guarded by mu.
// EnsureProfile holds mu for its whole body, INCLUDING the spawn + health-poll,
// so two concurrent reconcile passes can never double-spawn the same profile
// (the second blocks, then observes the first's healthy instance and reuses it).
// This is safe because ReconcileNetwork calls EnsureProfile sequentially within a
// pass, and the health-poll respects the passed ctx — a shutdown cancels ctx, so
// an in-flight spawn returns promptly and releases mu for Close. Retire and Close
// collect their victims under mu but stop them OUTSIDE it, so a graceful-stop
// wait never blocks an EnsureProfile.
//
// SUPERVISION (GAP-114). The Supervisor (supervisor.go) keeps managed instances
// alive after spawn. It PROBES /health outside mu, then takes any restart under
// mu via superviseInstance — a restart holds mu across its own health wait
// exactly as EnsureProfile does, so a supervisor restart and a concurrent
// reconcile EnsureProfile serialise on mu and can never double-spawn one profile.
// Route degrade/restore go through the optional Rerouter (a disjoint overlay on
// engineroute.Router), never the base routing table, so supervision never clobbers
// ReconcileNetwork's routing.
type Launcher struct {
	cfg     EngineHostLauncherConfig
	factory engineroute.ClientFactory

	// Injectable seams (production defaults set by New; overridden in tests).
	starter   ProcessStarter
	prober    HealthProber
	allocPort PortAllocator

	// rerouter degrades a down profile's sources to the default engine and
	// restores them on recovery (GAP-114). Optional: nil in deployments/tests
	// without per-source routing, in which case degrade/restore are pure no-ops
	// and the launcher behaves exactly as before. Set via WithRerouter;
	// *engineroute.Router satisfies it.
	rerouter Rerouter

	// Tunables (production defaults set by New; overridden in tests).
	startTimeout time.Duration // how long a spawn waits for the first healthy /health
	pollInterval time.Duration // gap between health polls during a spawn
	settleDelay  time.Duration // post-healthy re-probe delay (catches healthy-then-dead; 0 disables)
	stopGrace    time.Duration // SIGTERM→SIGKILL grace on stop

	mu        sync.Mutex
	instances map[string]*managedInstance
	closed    bool
}

// Compile-time assertion: *Launcher is a drop-in engineroute.Launcher, so main
// can swap it for the placeholder engineroute.DisabledLauncher.
var _ engineroute.Launcher = (*Launcher)(nil)

// Default lifecycle tunables. startTimeout mirrors the entrypoint's own bounded
// /health wait (60 polls × ~2s ≈ 60s); the others are conservative.
const (
	defaultStartTimeout = 60 * time.Second
	defaultPollInterval = 500 * time.Millisecond
	// defaultSettleDelay is the post-healthy re-probe window: long enough for a
	// crash-after-health (e.g. a failed Chromium init — GAP-094) to manifest,
	// short enough to add negligible latency to a rare profile spawn.
	defaultSettleDelay  = 1 * time.Second
	defaultStopGrace    = 5 * time.Second
	defaultProbeTimeout = 5 * time.Second
)

// New constructs a Launcher wired with the production seams: a real
// exec.Command-based ProcessStarter, an HTTP GET /health prober, and a
// loopback free-port allocator. factory turns an instance's base URL into a
// sourceengine.Client (main passes sourceengine.New bound to the shared HTTP
// client). Tests pass With* options to replace any seam or tunable.
func New(cfg EngineHostLauncherConfig, factory engineroute.ClientFactory, opts ...Option) *Launcher {
	l := &Launcher{
		cfg:          cfg,
		factory:      factory,
		starter:      execStarter{hostBin: cfg.HostBin},
		prober:       newHTTPHealthProber(defaultProbeTimeout),
		allocPort:    allocFreePort,
		startTimeout: defaultStartTimeout,
		pollInterval: defaultPollInterval,
		settleDelay:  defaultSettleDelay,
		stopGrace:    defaultStopGrace,
		instances:    map[string]*managedInstance{},
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// EnsureProfile brings up (or reuses) the engine-host instance for p and returns
// a handle to it. It is idempotent: a call for an already-running, healthy
// profile returns the cached instance without relaunching. A cached instance
// whose process has died — or which no longer answers /health — is discarded and
// respawned. An error means the instance could not be brought up; the caller
// (ReconcileNetwork) degrades p's sources to the default instance.
//
// ctx bounds only the readiness WAIT — the spawned process itself is owned by the
// launcher and outlives ctx (see the ProcessStarter contract).
func (l *Launcher) EnsureProfile(ctx context.Context, p engineroute.Profile) (engineroute.Instance, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return engineroute.Instance{}, ErrLauncherClosed
	}

	if mi, ok := l.instances[p.Key]; ok {
		if l.reusable(mi) {
			// A confirmed-healthy instance: clear any stale degrade overlay left by
			// the supervisor from a prior down episode, so its sources resume using
			// their base route.
			mi.profile = p
			l.markHealthyLocked(mi)
			return mi.instance(), nil
		}
		// Dead or wedged: tear it down and fall through to a fresh spawn.
		slog.WarnContext(ctx, "enginehost: cached instance is not reusable, respawning",
			"profile", p.Key, "pid", mi.proc.Pid())
		l.stopInstance(mi)
		delete(l.instances, p.Key)
	}

	return l.spawn(ctx, p)
}

// reusable reports whether a cached instance can be handed back as-is: its
// process must still be running AND its /health must answer. An alive-but-wedged
// JVM (health failing) is treated as NOT reusable so EnsureProfile restarts it,
// rather than routing a source at a dead engine.
func (l *Launcher) reusable(mi *managedInstance) bool {
	return alive(mi.proc) && l.prober(mi.baseURL) == nil
}

// Retire stops every running instance whose key is NOT in keep and removes it
// from the map. Best-effort: a stop failure is swallowed (a lingering process
// wastes memory but never breaks routing). Retire on an empty launcher with an
// empty keep-set is a safe no-op — the zero-disruption path.
//
// It also clears any degrade overlay for a retired profile's sources: a profile
// no longer referenced by any binding must not leave its sources force-routed to
// the default, or a stale overlay entry would mask a future rebinding.
func (l *Launcher) Retire(_ context.Context, keep map[string]bool) {
	doomed := l.detach(func(mi *managedInstance) bool { return !keep[mi.key] })
	for _, mi := range doomed {
		l.stopInstance(mi)
		if l.rerouter != nil {
			l.rerouter.Restore(mi.profile.SourceIDs)
		}
	}
}

// markHealthyLocked records that mi is confirmed healthy: it resets the
// supervision backoff counters and clears any degrade overlay so mi's sources
// resume using their base route. Idempotent (Restore of non-degraded ids is a
// no-op). Called under mu whenever a path proves the instance up — a fresh spawn,
// a reuse, or a successful supervisor restart.
//
// It deliberately does NOT clear mi.restartTimes: a slow-crasher passes /health on
// every restart, so resetting the cap window on a transient health-pass would let
// it be restarted every interval forever. The window is cleared only by genuine
// stability — its entries ageing out over a whole cooldown-window (GAP-114).
func (l *Launcher) markHealthyLocked(mi *managedInstance) {
	mi.degraded = false
	mi.restartFailures = 0
	mi.nextRestartAt = time.Time{}
	if l.rerouter != nil {
		l.rerouter.Restore(mi.profile.SourceIDs)
	}
}

// degradeLocked marks mi down and force-routes its sources to the default engine
// (GAP-114), so they stop hitting the dead port. Idempotent — a second call while
// already degraded only re-sets the (already-set) overlay. Called under mu.
func (l *Launcher) degradeLocked(mi *managedInstance) {
	mi.degraded = true
	if l.rerouter != nil {
		l.rerouter.Degrade(mi.profile.SourceIDs)
	}
}

// Close stops ALL instances and marks the launcher closed so no further profile
// can be brought up. It is wired into main's graceful-shutdown path. Idempotent;
// always returns nil (teardown is best-effort). The error return exists so main
// can treat it uniformly with the other closers.
func (l *Launcher) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	l.mu.Unlock()

	for _, mi := range l.detach(func(*managedInstance) bool { return true }) {
		l.stopInstance(mi)
	}
	return nil
}

// detach removes every instance matching pred from the map under mu and returns
// them, so the caller can stop them OUTSIDE the lock (a graceful-stop wait must
// never block an EnsureProfile).
func (l *Launcher) detach(pred func(*managedInstance) bool) []*managedInstance {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []*managedInstance
	for key, mi := range l.instances {
		if pred(mi) {
			out = append(out, mi)
			delete(l.instances, key)
		}
	}
	return out
}

// alive reports whether proc has NOT yet exited — a non-blocking read of its
// Done channel.
func alive(proc RunningProcess) bool {
	select {
	case <-proc.Done():
		return false
	default:
		return true
	}
}
