package enginetopo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/engineroute"
	"github.com/technobecet/tsundoku/internal/enginetopo"
	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/network"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	sourceenginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
	"github.com/technobecet/tsundoku/internal/sourcetransport"
)

// --- test doubles ------------------------------------------------------------

// fakeSnapshotter is a NetworkSnapshotter returning fixed bindings.
type fakeSnapshotter struct {
	bindings []network.ResolvedBinding
	err      error
}

type fakeTransportSnapshotter struct {
	policies map[int64]sourcetransport.Override
	err      error
}

func (f fakeTransportSnapshotter) Snapshot(context.Context) (map[int64]sourcetransport.Override, error) {
	return f.policies, f.err
}

func (f fakeSnapshotter) RoutingSnapshot(context.Context) ([]network.ResolvedBinding, error) {
	return f.bindings, f.err
}

// fakeLauncher returns ONE shared instance fake for every profile (the tests use
// a single profile), records its calls, and can be told to fail EnsureProfile.
type fakeLauncher struct {
	instance    *sourceenginefake.Client
	fail        bool
	ensureCalls int
	profiles    []engineroute.Profile
	retireCalls int
	lastKeep    map[string]bool
}

func (f *fakeLauncher) EnsureProfile(_ context.Context, p engineroute.Profile) (engineroute.Instance, error) {
	f.ensureCalls++
	f.profiles = append(f.profiles, p)
	if f.fail {
		return engineroute.Instance{}, errors.New("launch failed")
	}
	return engineroute.Instance{Key: p.Key, BaseURL: "http://instance/" + p.Key, Client: f.instance}, nil
}

// TestReconcileNetwork_RuntimeSnapshotUnionsSessionOffSources proves profile
// derivation sees both explicit network bindings and policy-only Off sources.
// The latter has no SourceNetworkBinding row but still needs a non-default
// disposable-session profile when the global bypass session is reusable.
func TestReconcileNetwork_RuntimeSnapshotUnionsSessionOffSources(t *testing.T) {
	t.Parallel()

	off := false
	launcher := &fakeLauncher{fail: true}
	res := mustReconcileNetwork(t, enginetopo.NetworkReconcileDeps{
		Snapshot: fakeSnapshotter{bindings: []network.ResolvedBinding{
			{SourceID: 12, FlareMode: network.FlareModeEndpoint, Flare: &network.ResolvedFlare{ID: "flare", Session: "bound"}},
		}},
		TransportSnapshot: fakeTransportSnapshotter{policies: map[int64]sourcetransport.Override{
			12: {ReuseBypassSession: &off},
			44: {ReuseBypassSession: &off},
		}},
		Router:     engineroute.NewRouter(sourceenginefake.New()),
		Launcher:   launcher,
		BaseConfig: baseConfig(),
	})
	if res.Profiles != 2 {
		t.Fatalf("Profiles = %d, want 2 (bound endpoint Off + global policy-only Off)", res.Profiles)
	}
	if len(launcher.profiles) != 2 {
		t.Fatalf("EnsureProfile calls = %d, want 2", len(launcher.profiles))
	}

	seen := map[int64]engineroute.Profile{}
	for _, profile := range launcher.profiles {
		for _, sourceID := range profile.SourceIDs {
			seen[sourceID] = profile
		}
	}
	for _, sourceID := range []int64{12, 44} {
		profile, ok := seen[sourceID]
		if !ok {
			t.Fatalf("source %d absent from derived profiles: %+v", sourceID, launcher.profiles)
		}
		if !profile.DisableBypassSession {
			t.Fatalf("source %d profile DisableBypassSession = false, want true", sourceID)
		}
	}
	if seen[44].FlareMode != engineroute.FlareModeGlobal || seen[44].Flare != nil || seen[44].Socks != nil {
		t.Fatalf("policy-only source profile = %+v, want otherwise-global binding", seen[44])
	}
}

func (f *fakeLauncher) Retire(_ context.Context, keep map[string]bool) {
	f.retireCalls++
	f.lastKeep = keep
}

// mustReconcileNetwork runs one pass and fails the test on a hard error.
func mustReconcileNetwork(t *testing.T, deps enginetopo.NetworkReconcileDeps) enginetopo.NetworkReconcileResult {
	t.Helper()
	res, err := enginetopo.ReconcileNetwork(context.Background(), deps)
	if err != nil {
		t.Fatalf("ReconcileNetwork: %v", err)
	}
	return res
}

// assertRoutedTo fails unless a Search for sourceID returns the given marker URL
// (i.e. it was routed to the client that carries that marker).
func assertRoutedTo(t *testing.T, router *engineroute.Router, sourceID int64, wantURL string) {
	t.Helper()
	got, err := router.Search(context.Background(), sourceID, "q", 1)
	if err != nil {
		t.Fatalf("Search(%d): %v", sourceID, err)
	}
	if len(got.Manga) != 1 || got.Manga[0].URL != wantURL {
		t.Fatalf("Search(%d) = %+v, want marker %q", sourceID, got, wantURL)
	}
}

// assertInstanceConfigured checks a provisioned instance received its config
// pushes: one FlareSolverr push (reconcileConfig) and two SOCKS pushes
// (reconcileConfig + the supplementary credential push for a credentialed
// endpoint).
func assertInstanceConfigured(t *testing.T, instance *sourceenginefake.Client) {
	t.Helper()
	if got := instance.CallCount("SetFlareSolverr"); got != 1 {
		t.Fatalf("instance FlareSolverr pushes = %d, want 1", got)
	}
	if got := instance.CallCount("SetSocks"); got != 2 {
		t.Fatalf("instance SOCKS pushes = %d, want 2 (config + credentials)", got)
	}
}

// socksBinding is a one-source binding routed through a SOCKS endpoint (with
// credentials, so the supplementary credential push is exercised too).
func socksBinding(sourceID int64) network.ResolvedBinding {
	return network.ResolvedBinding{
		SourceID: sourceID,
		Socks: &network.ResolvedSocks{
			ID: "vpn-endpoint", Host: "10.8.0.1", Port: 1080, Version: 5,
			Username: "user", Password: "secret",
		},
		FlareMode: network.FlareModeGlobal,
	}
}

// --- tests -------------------------------------------------------------------

// TestReconcileNetwork_NoBindingsClearsRoutes pins the zero-disruption invariant:
// with no bindings, the Router's table is cleared (a previously-routed source
// falls back to the default), the launcher is asked to retire everything, and no
// DB is touched (nil DB proves it).
func TestReconcileNetwork_NoBindingsClearsRoutes(t *testing.T) {
	def := sourceenginefake.New(
		sourceenginefake.WithSearchResult(42, sourceengine.SearchResult{Manga: []sourceengine.MangaEntry{{URL: "default"}}}),
	)
	router := engineroute.NewRouter(def)
	// Pre-seed a stale route so we can prove it gets cleared.
	router.SetRoutes(map[int64]sourceengine.Client{42: sourceenginefake.New()})

	launcher := &fakeLauncher{}
	res := mustReconcileNetwork(t, enginetopo.NetworkReconcileDeps{
		Snapshot:   fakeSnapshotter{bindings: nil},
		Router:     router,
		Launcher:   launcher,
		DB:         nil, // must not be touched on the empty path
		Cache:      nil,
		BaseConfig: baseConfig(),
	})
	if res.Profiles != 0 || res.InstancesRouted != 0 || len(res.Gaps) != 0 {
		t.Fatalf("unexpected result on empty bindings: %+v", res)
	}
	if launcher.retireCalls != 1 || len(launcher.lastKeep) != 0 {
		t.Fatalf("Retire not called with empty keep: calls=%d keep=%v", launcher.retireCalls, launcher.lastKeep)
	}
	// The stale route is gone: source 42 now hits the default.
	assertRoutedTo(t, router, 42, "default")
}

// TestReconcileNetwork_LauncherFailureDegradesToDefault proves a profile whose
// instance can't be brought up (the DisabledLauncher case, modelled by a failing
// launcher) is isolated as a gap and its sources fall back to the default — no
// hard error, no route installed.
func TestReconcileNetwork_LauncherFailureDegradesToDefault(t *testing.T) {
	def := sourceenginefake.New(
		sourceenginefake.WithSearchResult(42, sourceengine.SearchResult{Manga: []sourceengine.MangaEntry{{URL: "default"}}}),
	)
	router := engineroute.NewRouter(def)

	res := mustReconcileNetwork(t, enginetopo.NetworkReconcileDeps{
		Snapshot:   fakeSnapshotter{bindings: []network.ResolvedBinding{socksBinding(42)}},
		Router:     router,
		Launcher:   &fakeLauncher{fail: true},
		DB:         nil, // EnsureProfile fails before any DB access
		Cache:      nil,
		BaseConfig: baseConfig(),
	})
	if res.Profiles != 1 || res.InstancesRouted != 0 || res.SourcesRouted != 0 {
		t.Fatalf("expected 1 profile, 0 routed: %+v", res)
	}
	if len(res.Gaps) != 1 {
		t.Fatalf("expected 1 gap, got %d", len(res.Gaps))
	}
	assertRoutedTo(t, router, 42, "default")
}

// TestReconcileNetwork_ProvisionsAndRoutes is the happy path: a bound source gets
// its instance provisioned (a full reconcile pushes its SOCKS + FlareSolverr
// config, plus the supplementary credential push) and its RPCs are routed to it.
// Idempotent: a second pass reuses the instance and yields the same routing.
func TestReconcileNetwork_ProvisionsAndRoutes(t *testing.T) {
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	instance := sourceenginefake.New(
		sourceenginefake.WithSearchResult(42, sourceengine.SearchResult{Manga: []sourceengine.MangaEntry{{URL: "via-instance"}}}),
	)
	def := sourceenginefake.New(
		sourceenginefake.WithSearchResult(42, sourceengine.SearchResult{Manga: []sourceengine.MangaEntry{{URL: "via-default"}}}),
	)
	router := engineroute.NewRouter(def)
	launcher := &fakeLauncher{instance: instance}

	deps := enginetopo.NetworkReconcileDeps{
		Snapshot:   fakeSnapshotter{bindings: []network.ResolvedBinding{socksBinding(42)}},
		Router:     router,
		Launcher:   launcher,
		DB:         db,
		Cache:      cache,
		BaseConfig: baseConfig(),
	}

	res := mustReconcileNetwork(t, deps)
	if res.Profiles != 1 || res.InstancesRouted != 1 || res.SourcesRouted != 1 || len(res.Gaps) != 0 {
		t.Fatalf("unexpected happy-path result: %+v", res)
	}
	assertRoutedTo(t, router, 42, "via-instance")
	// The instance was provisioned with config: reconcileConfig pushes SOCKS +
	// FlareSolverr once, and the supplementary credential push adds a second
	// SetSocks (the binding carries credentials).
	assertInstanceConfigured(t, instance)

	// Idempotency: a second pass reuses the instance and yields the same routing,
	// keeping exactly the one live profile.
	res2 := mustReconcileNetwork(t, deps)
	if res2.InstancesRouted != 1 || res2.SourcesRouted != 1 || len(res2.Gaps) != 0 {
		t.Fatalf("second pass not idempotent: %+v", res2)
	}
	if len(launcher.lastKeep) != 1 {
		t.Fatalf("Retire keep set = %v, want exactly the one live profile", launcher.lastKeep)
	}
}
