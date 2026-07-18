package enginetopo

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/technobecet/tsundoku/internal/engineroute"
	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/network"
	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// NetworkSnapshotter is the narrow read surface ReconcileNetwork needs from the
// DB-truth network domain: the resolved per-source bindings (secrets included)
// to derive profiles from. *network.Service satisfies it.
type NetworkSnapshotter interface {
	RoutingSnapshot(ctx context.Context) ([]network.ResolvedBinding, error)
}

// NetworkReconcileDeps bundles everything ReconcileNetwork needs. It is a struct
// (not a long parameter list) so the boot pass and the write-through both build
// it once and the call site stays legible.
type NetworkReconcileDeps struct {
	// Snapshot reads the resolved per-source bindings from the network domain.
	Snapshot NetworkSnapshotter
	// Router is the shared engine client seam whose routing table this pass
	// rebuilds.
	Router *engineroute.Router
	// Launcher brings up (and retires) the per-profile engine-host instances.
	Launcher engineroute.Launcher
	// DB is the durable topology store, used to PROVISION each non-default
	// instance (install the library's extensions/prefs on it) by reusing
	// Reconcile against that instance.
	DB *ent.Client
	// Cache is the apk byte cache Reconcile takes (unused by the repo-based
	// install path, threaded for parity).
	Cache *apkcache.Store
	// BaseConfig is Tsundoku's OWN global FlareSolverr/SOCKS config — the source
	// of the "global" flare mode a profile may inherit.
	BaseConfig ConfigProvider
}

// NetworkReconcileResult reports what a ReconcileNetwork pass did.
type NetworkReconcileResult struct {
	// Profiles is the number of distinct non-default profiles derived from the
	// current bindings.
	Profiles int
	// InstancesRouted is the number of profiles whose instance was successfully
	// ensured + provisioned and whose sources are now routed to it.
	InstancesRouted int
	// SourcesRouted is the total number of source ids routed to a non-default
	// instance (the rest use the default).
	SourcesRouted int
	// Gaps holds every per-profile failure that was ISOLATED (an instance that
	// could not be launched or provisioned). Each degrades that profile's sources
	// to the default instance; none aborts the rest of the pass.
	Gaps []error
}

// ReconcileNetwork is the DB→engine per-source routing reconcile (QCAT-284): it
// reads the current bindings, derives the distinct non-default network profiles,
// ensures + provisions an engine-host instance for each, and rebuilds the
// Router's source-id → instance table so every bound source's RPC egresses
// through its profile's instance. It is the multi-instance sibling of Reconcile
// (which provisions the DEFAULT instance) and is called RIGHT AFTER it on boot,
// so the global config is pushed first (Reconcile) before per-profile config.
//
// IDEMPOTENT. Deriving profiles from the same bindings yields the same routing
// map, and EnsureProfile is required to reuse a running instance, so a pass that
// changes nothing relaunches nothing and simply re-pushes each instance's
// (unchanged) config + re-installs its (already-installed) extensions as no-ops —
// exactly Reconcile's own idempotency, per instance.
//
// ZERO-DISRUPTION. With no bindings (the deploy-day state, and whenever no source
// has a non-default binding) Derive returns no profiles, the Router's table is
// cleared, and every source uses the default instance — byte-for-byte today's
// single-instance behavior. Pinned by
// TestReconcileNetwork_NoBindingsClearsRoutes.
//
// FAULT ISOLATION + DEGRADE. A profile whose instance cannot be launched
// (ErrLauncherDisabled from the conservative default-only launcher, or any
// launch/provision failure) is logged and recorded as a gap; its sources are
// simply NOT added to the routing map, so they fall back to the default instance.
// A single broken profile never takes down the default or the other profiles.
//
// Only the snapshot read is a HARD error (it leaves the pass unable to determine
// the desired routing at all); everything after it is best-effort per profile.
func ReconcileNetwork(ctx context.Context, deps NetworkReconcileDeps) (NetworkReconcileResult, error) {
	var res NetworkReconcileResult

	snapshot, err := deps.Snapshot.RoutingSnapshot(ctx)
	if err != nil {
		return res, fmt.Errorf("enginetopo.ReconcileNetwork: snapshot: %w", err)
	}

	profiles := engineroute.Derive(toBindingInputs(snapshot))
	res.Profiles = len(profiles)

	// No non-default profiles: clear the routing table (everything → default) and
	// retire any lingering instances. This is the zero-disruption fast path.
	if len(profiles) == 0 {
		deps.Router.SetRoutes(nil)
		deps.Launcher.Retire(ctx, map[string]bool{})
		return res, nil
	}

	routes := make(map[int64]sourceengine.Client)
	keep := make(map[string]bool, len(profiles))
	for _, p := range profiles {
		inst, perr := ensureProvisionedInstance(ctx, deps, p)
		if perr != nil {
			slog.WarnContext(ctx, "enginetopo: network reconcile could not bring up profile instance, degrading its sources to the default engine",
				"profile", p.Key, "sources", p.SourceIDs, "err", perr)
			res.Gaps = append(res.Gaps, perr)
			continue
		}
		keep[p.Key] = true
		for _, sid := range p.SourceIDs {
			routes[sid] = inst.Client
		}
		res.InstancesRouted++
		res.SourcesRouted += len(p.SourceIDs)
	}

	// Retire instances no longer needed (a binding was removed/re-pointed), then
	// swap in the freshly-built routing table.
	deps.Launcher.Retire(ctx, keep)
	deps.Router.SetRoutes(routes)
	return res, nil
}

// ensureProvisionedInstance brings up p's instance and provisions it: it ensures
// the instance (Launcher), reconciles the library's source PREFERENCES AND the
// profile's own FlareSolverr/SOCKS config onto it (reusing Reconcile with a
// profile-scoped ConfigProvider), then pushes the profile's SOCKS credentials
// (which the ConfigProvider surface can't express) when it has any. A failure at
// any step is returned so the caller degrades this profile to the default.
//
// EXTENSIONS ARE DELIBERATELY NOT RECONCILED HERE (WithoutExtensions): every
// profile instance SHARES the default instance's extensions dir (see
// enginehost.linkSharedExtensions), which the default-instance boot Reconcile
// already populated. Re-installing per profile would be a redundant network hit
// and a concurrent write into that shared dir — so a profile pass touches only
// prefs + config.
func ensureProvisionedInstance(ctx context.Context, deps NetworkReconcileDeps, p engineroute.Profile) (engineroute.Instance, error) {
	inst, err := deps.Launcher.EnsureProfile(ctx, p)
	if err != nil {
		return engineroute.Instance{}, fmt.Errorf("ensure profile %q: %w", p.Key, err)
	}

	cfg := profileConfigProvider{profile: p, base: deps.BaseConfig}
	if _, err := Reconcile(ctx, inst.Client, deps.DB, deps.Cache, cfg, WithoutExtensions()); err != nil {
		return engineroute.Instance{}, fmt.Errorf("provision profile %q instance: %w", p.Key, err)
	}

	// SOCKS credentials are not expressible via ConfigProvider (its socksPatch
	// deliberately omits username/password); push them explicitly when the
	// profile's SOCKS endpoint carries auth, so an authed proxy actually works.
	if err := pushSocksCredentials(ctx, inst.Client, p); err != nil {
		return engineroute.Instance{}, fmt.Errorf("push profile %q socks credentials: %w", p.Key, err)
	}
	return inst, nil
}

// pushSocksCredentials sends a supplementary SetSocks carrying the profile's
// SOCKS username/password (plus host/port/version so the credentialed push is
// self-consistent). It is a no-op when the profile has no SOCKS endpoint or the
// endpoint carries no credentials — the ConfigProvider push already fully
// configured a credential-less proxy.
func pushSocksCredentials(ctx context.Context, client sourceengine.Client, p engineroute.Profile) error {
	if p.Socks == nil || (p.Socks.Username == "" && p.Socks.Password == "") {
		return nil
	}
	enabled := true
	port := fmt.Sprintf("%d", p.Socks.Port)
	_, err := client.SetSocks(ctx, sourceengine.SocksPatch{
		Enabled:  &enabled,
		Host:     &p.Socks.Host,
		Port:     &port,
		Version:  &p.Socks.Version,
		Username: &p.Socks.Username,
		Password: &p.Socks.Password,
	})
	return err
}

// toBindingInputs maps the DB-truth resolved bindings into the engine-side
// derive inputs (a pure value copy — no secrets are dropped, they are needed to
// push each instance's SOCKS config).
func toBindingInputs(snapshot []network.ResolvedBinding) []engineroute.BindingInput {
	out := make([]engineroute.BindingInput, len(snapshot))
	for i, b := range snapshot {
		out[i] = engineroute.BindingInput{
			SourceID:  b.SourceID,
			FlareMode: b.FlareMode,
			Socks:     toSocksEndpoint(b.Socks),
			Flare:     toFlareEndpoint(b.Flare),
		}
	}
	return out
}

// toSocksEndpoint maps a resolved SOCKS endpoint to the engine-side value (nil
// passes through).
func toSocksEndpoint(s *network.ResolvedSocks) *engineroute.SocksEndpoint {
	if s == nil {
		return nil
	}
	return &engineroute.SocksEndpoint{
		ID:       s.ID,
		Host:     s.Host,
		Port:     s.Port,
		Version:  s.Version,
		Username: s.Username,
		Password: s.Password,
	}
}

// toFlareEndpoint maps a resolved FlareSolverr endpoint to the engine-side value
// (nil passes through).
func toFlareEndpoint(f *network.ResolvedFlare) *engineroute.FlareEndpoint {
	if f == nil {
		return nil
	}
	return &engineroute.FlareEndpoint{
		ID:                 f.ID,
		URL:                f.URL,
		Session:            f.Session,
		SessionTTL:         f.SessionTTL,
		Timeout:            f.Timeout,
		AsResponseFallback: f.AsResponseFallback,
	}
}
