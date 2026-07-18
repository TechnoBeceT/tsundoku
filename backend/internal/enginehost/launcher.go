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
type Launcher struct {
	cfg     EngineHostLauncherConfig
	factory engineroute.ClientFactory

	// Injectable seams (production defaults set by New; overridden in tests).
	starter   ProcessStarter
	prober    HealthProber
	allocPort PortAllocator

	// Tunables (production defaults set by New; overridden in tests).
	startTimeout time.Duration // how long a spawn waits for the first healthy /health
	pollInterval time.Duration // gap between health polls during a spawn
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
func (l *Launcher) Retire(_ context.Context, keep map[string]bool) {
	doomed := l.detach(func(mi *managedInstance) bool { return !keep[mi.key] })
	for _, mi := range doomed {
		l.stopInstance(mi)
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
