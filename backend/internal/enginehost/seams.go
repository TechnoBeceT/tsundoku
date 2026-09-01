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
// (spawn), HealthProber (readiness), StatusProber (bounded recovery evidence),
// and PortAllocator (free-port pick). New
// wires the production implementations (exec_process.go / health.go / status.go
// / port.go); tests pass fakes via the With* options. /health remains RPC
// liveness only: a launch samples strict /status after health and before its
// settle recheck of both contracts, admitting KCEF-enabled profiles only at
// ready and KCEF-off profiles only at disabled. The supervisor uses the same
// status capability to restart only a profile that loses its browser, and
// requires six stable full-pool samples before recovering an exhausted managed
// profile.
package enginehost

import (
	"context"
	"os"
	"time"
)

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
	//
	// kcefEnabled is the policy-resolved embedded-browser setting for this
	// profile. Every child receives it explicitly so it never inherits a stale
	// value from the entrypoint-managed default host.
	Start(port int, dataDir string, kcefEnabled bool) (RunningProcess, error)
}

// RunningProcess is a handle to a spawned engine-host process. The launcher uses
// it to detect an unexpected JVM exit (Done), to stop the entire owned process
// group gracefully (SignalGracefully), and to force-kill that group (Kill) when
// it ignores TERM or its health-poll times out. The exited leader remains
// unreaped as a PGID pin while the production reaper initiates terminal group
// KILL—immediately after a spontaneous exit or at an active graceful-stop
// deadline—and that group-signal syscall is serialized against the sole Wait.
// Numeric PGID reuse therefore cannot retarget delivery. GroupExists keeps
// lifecycle ownership until an identity mismatch or the kernel's ESRCH group
// probe proves the original generation absent; uncertainty remains owned.
// The production implementation is execProcess (exec_process.go); tests provide
// a fully in-memory fake.
type RunningProcess interface {
	// Pid is the OS process id (used only for logging).
	Pid() int
	// GroupID is the dedicated process-group id assigned at spawn.
	GroupID() int
	// Signal delivers a raw signal to the entire owned process group and MUST keep
	// ownership pinned through the final syscall. Lifecycle TERM callers use
	// SignalGracefully so the reaper receives their configured grace window.
	Signal(sig os.Signal) error
	// SignalGracefully records grace before delivering SIGTERM, allowing the
	// process reaper to preserve the configured TERM-to-KILL window while still
	// owning a bounded autonomous fallback if the stopping caller disappears.
	SignalGracefully(grace time.Duration) error
	// Kill force-terminates the entire owned process group (SIGKILL) and MUST
	// refuse a recycled group identity.
	Kill() error
	// GroupExists reports whether any process remains in the original owned group.
	// False means either the OS returned ESRCH or the numeric PGID now names a
	// different leader identity; every uncertain result retains ownership
	// fail-closed.
	GroupExists() (bool, error)
	// Done is closed once process exit is observed. The launcher selects on it to
	// notice a crash during startup and to wait out a graceful stop before
	// escalating to Kill. Production retains the exited leader as an identity pin,
	// autonomously delivers the terminal group syscall immediately for a crash or
	// at the registered graceful deadline, then its single reaper calls Wait
	// exactly once; the later ESRCH group probe covers killed descendant zombies.
	Done() <-chan struct{}
}

// HealthProber reports whether the engine-host at baseURL is serving — a nil
// return means "ready" (its GET /health answered 200). The launcher polls it
// after a spawn (readiness gate) and once on a cache hit (liveness check). The
// production implementation (health.go) issues a short-timeout HTTP GET; tests
// inject a deterministic function. The context bounds each in-flight request.
type HealthProber func(context.Context, string) error

// StatusProber returns the bounded, typed operational snapshot served by
// GET /status. The context must cancel an in-flight read promptly. Production
// uses a short-timeout HTTP client; tests inject deterministic snapshots.
type StatusProber func(context.Context, string) (EngineStatus, error)

// ExhaustionDiagnosticSink receives approved-fields-only evidence immediately
// before a managed profile exhaustion restart. It must remain bounded and must
// not retain request payloads or secrets; the production sink emits structured
// process/profile/status fields through slog.
type ExhaustionDiagnosticSink func(context.Context, ExhaustionDiagnostic)

// PortAllocator returns a free TCP port on the loopback interface for a new
// instance to listen on. The production implementation (port.go) binds
// 127.0.0.1:0 and hands back the kernel-assigned port; tests inject a
// deterministic allocator. A distinct port per instance is mandatory — the JVM
// enforces a single-instance file lock per data dir AND binds its own port, so
// two instances must never collide on either.
type PortAllocator func() (int, error)
