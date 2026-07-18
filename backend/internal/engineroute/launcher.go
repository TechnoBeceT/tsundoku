package engineroute

import (
	"context"
	"errors"

	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// ErrLauncherDisabled is returned by EnsureProfile when the process launcher is
// not available in this deployment, so a bound source cannot be given its own
// instance. ReconcileNetwork treats it as a graceful DEGRADE (the source falls
// back to the default instance and a warning is logged), never a hard failure —
// the default/other instances are unaffected.
var ErrLauncherDisabled = errors.New("engineroute: per-profile launcher disabled")

// Instance is a running (or reused) engine-host instance for one profile: its
// profile key, its base URL, and a typed client aimed at it. ReconcileNetwork
// uses the client to provision the instance (a full reconcile against it) and to
// push the profile's SOCKS/FlareSolverr config, then routes the profile's
// sources to it.
type Instance struct {
	// Key is the profile key this instance serves (see profileKey).
	Key string
	// BaseURL is the instance's engine-host base HTTP address.
	BaseURL string
	// Client is a sourceengine.Client aimed at BaseURL.
	Client sourceengine.Client
}

// Launcher is the port ReconcileNetwork uses to bring up (and tear down) the
// per-profile engine-host instances. The DEFAULT profile is never launched here
// — it is the already-running engine-host the container entrypoint owns; the
// Launcher manages only the ADDITIONAL non-default instances.
//
// Implementations:
//   - DisabledLauncher (this file): the conservative default-only launcher —
//     every profile degrades to the default instance. Shipped first (QCAT-284
//     router+reconcile slice) so the whole routing data-path is live and proven
//     non-disruptive before the OS-heavy JVM-spawning launcher lands.
//   - a process launcher (follow-up slice): spawns one engine-host JVM per
//     profile with a distinct port + data dir, mirroring the entrypoint's single
//     launch, with lifecycle + fault isolation.
type Launcher interface {
	// EnsureProfile ensures an engine-host instance for p exists and returns a
	// handle to it. It MUST be idempotent: a second call for an already-running
	// profile returns the same instance without relaunching. An error means the
	// profile could not be brought up — the caller degrades that profile's
	// sources to the default instance.
	EnsureProfile(ctx context.Context, p Profile) (Instance, error)

	// Retire stops every running non-default instance whose key is NOT in keep
	// (a profile no longer referenced by any binding after an owner edit).
	// Best-effort: a teardown failure is logged, never returned — a lingering
	// instance wastes memory but never breaks routing.
	Retire(ctx context.Context, keep map[string]bool)
}

// ClientFactory builds a sourceengine.Client aimed at an instance's base URL.
// Production passes sourceengine.New bound to the shared HTTP client; tests pass
// a factory returning a fake. It is the ONE seam between "a base URL" and "a
// usable client" so both launchers (and their tests) construct clients the same
// way.
type ClientFactory func(baseURL string) sourceengine.Client

// DisabledLauncher is the conservative default-only Launcher: it refuses every
// profile with ErrLauncherDisabled, so ReconcileNetwork routes all sources to
// the default instance (byte-for-byte today's behavior) and logs a degrade for
// any source that WANTED a non-default instance. It is the production Launcher
// until the process launcher lands, and keeps the router+reconcile slice fully
// non-disruptive.
type DisabledLauncher struct{}

// Compile-time assertion.
var _ Launcher = DisabledLauncher{}

// EnsureProfile always returns ErrLauncherDisabled — no non-default instance can
// be brought up without the process launcher.
func (DisabledLauncher) EnsureProfile(_ context.Context, _ Profile) (Instance, error) {
	return Instance{}, ErrLauncherDisabled
}

// Retire is a no-op — a DisabledLauncher never launched anything to retire.
func (DisabledLauncher) Retire(_ context.Context, _ map[string]bool) {}
