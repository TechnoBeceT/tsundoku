// Package enginehost is the OS process launcher for per-profile engine-host
// instances: it spawns one tsundoku-engine-host JVM per distinct network
// profile (each with its own TCP port + data dir), satisfying the
// engineroute.Launcher port that internal/enginetopo.ReconcileNetwork drives.
//
// WHY IT EXISTS. The per-source network-routing feature (QCAT-284) runs one
// engine-host instance per distinct {SOCKS, FlareSolverr} profile so a bound
// source's page fetches egress through the right VPN/proxy. engineroute stays
// PURE (router + profile-derivation + the Launcher interface); this package
// owns the OS-heavy half — spawning, health-gating, and tearing down the JVMs —
// so engineroute never imports os/exec. The DEFAULT instance (port 7777) is
// launched by the container entrypoint, NOT here: this launcher manages only
// the ADDITIONAL non-default instances a binding requires.
//
// ZERO-DISRUPTION. With no non-default bindings, ReconcileNetwork calls
// EnsureProfile zero times and Retire with an empty keep-set, so this launcher
// spawns nothing and the deployment is byte-for-byte the single-instance one.
// The launcher only ever does OS work when a source is actually bound to a
// non-default profile.
//
// FAULT ISOLATION. A spawn that fails (bad binary path, port race, the JVM
// never reporting /health) returns an error from EnsureProfile; ReconcileNetwork
// then degrades just that profile's sources to the default instance and carries
// on — one broken profile never takes down the default or the others.
//
// SEAMS. All OS/network touch points are injectable interfaces so the lifecycle
// logic is unit-testable with no real process and no real network: ProcessStarter
// (spawn), HealthProber (readiness), and PortAllocator (free-port pick). New
// wires the production implementations (exec_process.go / health.go / port.go);
// tests pass fakes via the With* options.
package enginehost

import "os"

// ProcessStarter spawns one engine-host process listening on port with its data
// root at dataDir, returning a handle to the running process. It is the ONE seam
// between the launcher's lifecycle logic and os/exec, so tests inject a fake
// process instead of forking a real JVM. The production implementation is
// execStarter (exec_process.go).
//
// The started process MUST outlive the call that spawned it — it is owned by the
// launcher, not by any per-request context — so implementations use a
// context-free spawn (plain exec.Command) and expose lifecycle control through
// the returned RunningProcess (signal/kill/done), never through context
// cancellation.
type ProcessStarter interface {
	// Start launches the process. A non-nil error means nothing was spawned (the
	// caller does not need to clean anything up).
	Start(port int, dataDir string) (RunningProcess, error)
}

// RunningProcess is a handle to a spawned engine-host process. The launcher uses
// it to detect an unexpected exit (Done), to stop the instance gracefully
// (Signal SIGTERM), and to force-kill it (Kill) when it ignores the term signal
// or its health-poll times out. The production implementation is execProcess
// (exec_process.go); tests provide a fully in-memory fake.
type RunningProcess interface {
	// Pid is the OS process id (used only for logging).
	Pid() int
	// Signal delivers sig to the process (SIGTERM for a graceful stop).
	Signal(sig os.Signal) error
	// Kill force-terminates the process (SIGKILL).
	Kill() error
	// Done is closed once the process has exited and been reaped. The launcher
	// selects on it to notice a crash during startup and to wait out a graceful
	// stop before escalating to Kill. Implementations MUST close it exactly once,
	// from the single goroutine that reaps the process, so it never zombies.
	Done() <-chan struct{}
}

// HealthProber reports whether the engine-host at baseURL is serving — a nil
// return means "ready" (its GET /health answered 200). The launcher polls it
// after a spawn (readiness gate) and once on a cache hit (liveness check). The
// production implementation (health.go) issues a short-timeout HTTP GET; tests
// inject a deterministic function.
type HealthProber func(baseURL string) error

// PortAllocator returns a free TCP port on the loopback interface for a new
// instance to listen on. The production implementation (port.go) binds
// 127.0.0.1:0 and hands back the kernel-assigned port; tests inject a
// deterministic allocator. A distinct port per instance is mandatory — the JVM
// enforces a single-instance file lock per data dir AND binds its own port, so
// two instances must never collide on either.
type PortAllocator func() (int, error)
