package enginetopo_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/engineroute"
	"github.com/technobecet/tsundoku/internal/enginetopo"
	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/network"
	"github.com/technobecet/tsundoku/internal/runtimepolicy"
	"github.com/technobecet/tsundoku/internal/settings"
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

type acceptingTransportCatalog struct{}

func (acceptingTransportCatalog) RequireSource(context.Context, int64) error { return nil }

type topologyTransportDefaults struct{}

func (topologyTransportDefaults) ImageConnectionMode(context.Context) sourcetransport.ImageConnectionMode {
	return sourcetransport.ImageConnectionFresh
}

func (topologyTransportDefaults) ResolveBypassSession(context.Context, int64, *bool) (bool, sourcetransport.BypassSessionMode, error) {
	return false, sourcetransport.BypassSessionDisabled, nil
}

func (f fakeSnapshotter) RoutingSnapshot(context.Context) ([]network.ResolvedBinding, error) {
	return f.bindings, f.err
}

// fakeLauncher returns ONE shared instance fake for every profile (the tests use
// a single profile), records its calls, and can be told to fail EnsureProfile.
type fakeLauncher struct {
	instance     *sourceenginefake.Client
	fail         bool
	onComplete   func()
	prepareCalls int
	prepared     [][]engineroute.Profile
	ensureCalls  int
	profiles     []engineroute.Profile
	retireCalls  int
	lastKeep     map[string]bool
}

func kcefPolicyPtr(value runtimepolicy.KCEFPolicy) *runtimepolicy.KCEFPolicy { return &value }

type preferenceReadClient struct {
	*sourceenginefake.Client
	errs map[int64]error
	seen []int64
}

func (c *preferenceReadClient) Preferences(ctx context.Context, sourceID int64) ([]sourceengine.Preference, error) {
	c.seen = append(c.seen, sourceID)
	if err := c.errs[sourceID]; err != nil {
		return nil, err
	}
	return c.Client.Preferences(ctx, sourceID)
}

type preferenceLauncher struct{ instance sourceengine.Client }

type noopProfilePreparation struct{}

func (noopProfilePreparation) CompletePublication() {}

type callbackProfilePreparation struct{ complete func() }

func (p callbackProfilePreparation) CompletePublication() {
	if p.complete != nil {
		p.complete()
	}
}

func (preferenceLauncher) PrepareProfiles(context.Context, []engineroute.Profile) engineroute.ProfilePreparation {
	return noopProfilePreparation{}
}

func (l preferenceLauncher) EnsureProfile(_ context.Context, p engineroute.Profile) (engineroute.Instance, error) {
	return engineroute.Instance{Key: p.Key, BaseURL: "http://instance/" + p.Key, Client: l.instance}, nil
}

func (preferenceLauncher) Retire(context.Context, map[string]bool) {}

type runtimeConfigClient struct {
	*sourceenginefake.Client
	mu              sync.Mutex
	imageSourceIDs  []int64
	proxySourceIDs  []int64
	flareSession    string
	beforeImagePush func()
}

type mutableRuntimeConfig struct {
	fakeConfig
	mu         sync.Mutex
	impSources []int64
}

func (c *mutableRuntimeConfig) ImpersonateSources(context.Context) []int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]int64(nil), c.impSources...)
}

func (c *mutableRuntimeConfig) setImpersonateSources(sourceIDs []int64) {
	c.mu.Lock()
	c.impSources = append([]int64(nil), sourceIDs...)
	c.mu.Unlock()
}

func (c *runtimeConfigClient) SetFlareSolverr(ctx context.Context, patch sourceengine.FlareSolverrPatch) (sourceengine.FlareSolverrConfig, error) {
	cfg, err := c.Client.SetFlareSolverr(ctx, patch)
	if err == nil {
		c.mu.Lock()
		c.flareSession = cfg.Session
		c.mu.Unlock()
	}
	return cfg, err
}

func (c *runtimeConfigClient) SetImageTransport(ctx context.Context, patch sourceengine.ImageTransportPatch) (sourceengine.ImageTransportConfig, error) {
	if c.beforeImagePush != nil {
		c.beforeImagePush()
	}
	cfg, err := c.Client.SetImageTransport(ctx, patch)
	if err == nil {
		c.mu.Lock()
		c.imageSourceIDs = append([]int64(nil), cfg.ReuseSourceIDs...)
		c.mu.Unlock()
	}
	return cfg, err
}

func (c *runtimeConfigClient) SetImpersonate(ctx context.Context, patch sourceengine.ImpersonatePatch) (sourceengine.ImpersonateConfig, error) {
	cfg, err := c.Client.SetImpersonate(ctx, patch)
	if err == nil {
		c.mu.Lock()
		c.proxySourceIDs = append([]int64(nil), cfg.SourceIDs...)
		c.mu.Unlock()
	}
	return cfg, err
}

func (c *runtimeConfigClient) runtimeSets() ([]int64, []int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]int64(nil), c.imageSourceIDs...), append([]int64(nil), c.proxySourceIDs...)
}

func (c *runtimeConfigClient) session() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.flareSession
}

type lifecycleLauncher struct {
	instances map[string]*runtimeConfigClient
	fail      map[string]error
	prepared  [][]engineroute.Profile
	retired   []string
	lastKeep  map[string]bool
	onCreate  func(engineroute.Profile, *runtimeConfigClient)
}

type blockingRetireLauncher struct {
	instance *sourceenginefake.Client
	entered  chan struct{}
	release  chan struct{}
}

func (l *blockingRetireLauncher) PrepareProfiles(context.Context, []engineroute.Profile) engineroute.ProfilePreparation {
	return noopProfilePreparation{}
}

func (l *blockingRetireLauncher) EnsureProfile(_ context.Context, p engineroute.Profile) (engineroute.Instance, error) {
	return engineroute.Instance{Key: p.Key, BaseURL: "http://instance/" + p.Key, Client: l.instance}, nil
}

func (l *blockingRetireLauncher) Retire(ctx context.Context, _ map[string]bool) {
	close(l.entered)
	select {
	case <-l.release:
	case <-ctx.Done():
	}
}

type blockingTransportSnapshotter struct {
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

type sourceRuntimeDefaults struct{}

func (sourceRuntimeDefaults) ImageConnectionMode(context.Context) sourcetransport.ImageConnectionMode {
	return sourcetransport.ImageConnectionFresh
}

func (sourceRuntimeDefaults) ResolveBypassSession(context.Context, int64, *bool) (bool, sourcetransport.BypassSessionMode, error) {
	return false, sourcetransport.BypassSessionDisabled, nil
}

type sourceCatalog struct{}

func (sourceCatalog) RequireSource(context.Context, int64) error { return nil }

type signalingRuntimeConverger struct {
	entered  chan struct{}
	delegate interface {
		ReconcileRuntime(context.Context) error
	}
}

func (c signalingRuntimeConverger) ReconcileRuntime(ctx context.Context) error {
	close(c.entered)
	return c.delegate.ReconcileRuntime(ctx)
}

func (s *blockingTransportSnapshotter) Snapshot(ctx context.Context) (map[int64]sourcetransport.Override, error) {
	first := false
	s.once.Do(func() {
		first = true
		close(s.entered)
	})
	if first {
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return map[int64]sourcetransport.Override{}, nil
}

func (l *lifecycleLauncher) EnsureProfile(_ context.Context, p engineroute.Profile) (engineroute.Instance, error) {
	if err := l.fail[p.Key]; err != nil {
		return engineroute.Instance{}, err
	}
	client := l.instances[p.Key]
	if client == nil {
		client = &runtimeConfigClient{Client: sourceenginefake.New(
			sourceenginefake.WithSearchResult(p.SourceIDs[0], sourceengine.SearchResult{Manga: []sourceengine.MangaEntry{{URL: p.Key}}}),
		)}
		l.instances[p.Key] = client
		if l.onCreate != nil {
			l.onCreate(p, client)
		}
	}
	return engineroute.Instance{Key: p.Key, BaseURL: "http://instance/" + p.Key, Client: client}, nil
}

func (l *lifecycleLauncher) PrepareProfiles(_ context.Context, desired []engineroute.Profile) engineroute.ProfilePreparation {
	l.prepared = append(l.prepared, append([]engineroute.Profile(nil), desired...))
	return noopProfilePreparation{}
}

func (l *lifecycleLauncher) Retire(_ context.Context, keep map[string]bool) {
	l.lastKeep = keep
	for key := range l.instances {
		if !keep[key] {
			l.retired = append(l.retired, key)
			delete(l.instances, key)
		}
	}
}

func (f *fakeLauncher) EnsureProfile(_ context.Context, p engineroute.Profile) (engineroute.Instance, error) {
	f.ensureCalls++
	f.profiles = append(f.profiles, p)
	if f.fail {
		return engineroute.Instance{}, errors.New("launch failed")
	}
	return engineroute.Instance{Key: p.Key, BaseURL: "http://instance/" + p.Key, Client: f.instance}, nil
}

func (f *fakeLauncher) PrepareProfiles(_ context.Context, desired []engineroute.Profile) engineroute.ProfilePreparation {
	f.prepareCalls++
	f.prepared = append(f.prepared, append([]engineroute.Profile(nil), desired...))
	return callbackProfilePreparation{complete: f.onComplete}
}

// TestReconcileNetwork_RuntimeSnapshotUnionsSessionOffSources proves profile
// derivation sees both explicit network bindings and policy-only Off sources.
// The latter has no SourceNetworkBinding row but still needs a non-default
// disposable-session profile when the global bypass session is reusable.
func TestReconcileNetwork_RuntimeSnapshotUnionsSessionOffSources(t *testing.T) { //nolint:cyclop // Snapshot union scenario asserts the complete resulting topology.
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

// TestReconcileNetwork_DerivesKCEFAgainstDefaultHost exercises the live
// reconcile caller rather than profile derivation in isolation. Auto global
// capability follows the default host, while Auto endpoint capability remains
// a managed KCEF-off profile with its historical key.
func TestReconcileNetwork_DerivesKCEFAgainstDefaultHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		defaultKCEF  bool
		binding      network.ResolvedBinding
		wantProfiles int
		wantKey      string
		wantKCEF     bool
		policies     map[int64]sourcetransport.Override
	}{
		{
			name: "default on global auto stays default", defaultKCEF: true,
			binding: network.ResolvedBinding{SourceID: 7, FlareMode: network.FlareModeGlobal},
		},
		{
			name: "default off global auto creates on profile", defaultKCEF: false,
			binding:      network.ResolvedBinding{SourceID: 7, FlareMode: network.FlareModeGlobal},
			wantProfiles: 1, wantKey: "kcef=on", wantKCEF: true,
		},
		{
			name: "default on endpoint auto keeps legacy off key", defaultKCEF: true,
			binding:      network.ResolvedBinding{SourceID: 7, FlareMode: network.FlareModeEndpoint, Flare: &network.ResolvedFlare{ID: "flare"}},
			wantProfiles: 1, wantKey: "|endpoint|flare",
		},
		{
			name: "required endpoint creates KCEF on profile", defaultKCEF: true,
			binding:      network.ResolvedBinding{SourceID: 7, FlareMode: network.FlareModeEndpoint, Flare: &network.ResolvedFlare{ID: "flare"}},
			wantProfiles: 1, wantKey: "|endpoint|flare|kcef=on", wantKCEF: true,
			policies: map[int64]sourcetransport.Override{7: {KCEFPolicy: kcefPolicyPtr(runtimepolicy.KCEFPolicyRequired)}},
		},
		{
			name: "disabled global creates KCEF off profile", defaultKCEF: true,
			binding:      network.ResolvedBinding{SourceID: 7, FlareMode: network.FlareModeGlobal},
			wantProfiles: 1, wantKey: "kcef=off", wantKCEF: false,
			policies: map[int64]sourcetransport.Override{7: {KCEFPolicy: kcefPolicyPtr(runtimepolicy.KCEFPolicyDisabled)}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			launcher := &fakeLauncher{fail: true}
			result := mustReconcileNetwork(t, enginetopo.NetworkReconcileDeps{
				Snapshot:          fakeSnapshotter{bindings: []network.ResolvedBinding{tt.binding}},
				TransportSnapshot: fakeTransportSnapshotter{policies: tt.policies},
				Router:            engineroute.NewRouter(sourceenginefake.New()), Launcher: launcher,
				BaseConfig: baseConfig(), DefaultKCEFEnabled: tt.defaultKCEF,
			})
			if result.Profiles != tt.wantProfiles || len(launcher.profiles) != tt.wantProfiles {
				t.Fatalf("result/launches = %d/%d, want %d profiles", result.Profiles, len(launcher.profiles), tt.wantProfiles)
			}
			if tt.wantProfiles == 0 {
				return
			}
			profile := launcher.profiles[0]
			if profile.Key != tt.wantKey || profile.KCEFEnabled != tt.wantKCEF {
				t.Fatalf("derived profile = %+v, want key %q and KCEF %v", profile, tt.wantKey, tt.wantKCEF)
			}
		})
	}
}

func (f *fakeLauncher) Retire(_ context.Context, keep map[string]bool) {
	f.retireCalls++
	f.lastKeep = keep
}

func TestReconcileNetwork_PreparesCanonicalDesiredProfilesBeforeEnsure(t *testing.T) {
	launcher := &fakeLauncher{fail: true}
	result := mustReconcileNetwork(t, enginetopo.NetworkReconcileDeps{
		Snapshot: fakeSnapshotter{bindings: []network.ResolvedBinding{
			{SourceID: 2, FlareMode: network.FlareModeEndpoint, Flare: &network.ResolvedFlare{ID: "z"}},
			{SourceID: 1, FlareMode: network.FlareModeEndpoint, Flare: &network.ResolvedFlare{ID: "a"}},
		}},
		Router: engineroute.NewRouter(sourceenginefake.New()), Launcher: launcher,
		BaseConfig: baseConfig(), DefaultKCEFEnabled: true,
	})
	if result.Profiles != 2 || launcher.prepareCalls != 1 || len(launcher.prepared) != 1 {
		t.Fatalf("profiles/prepare calls = %d/%d (%v), want 2/1", result.Profiles, launcher.prepareCalls, launcher.prepared)
	}
	got := launcher.prepared[0]
	if len(got) != 2 || got[0].Key > got[1].Key {
		t.Fatalf("prepared profiles = %+v, want canonical key order", got)
	}
}

func TestReconcileNetwork_CompletesPreparationAfterFailedProfileRoutePublication(t *testing.T) {
	defaultClient := sourceenginefake.New(sourceenginefake.WithSearchResult(1, sourceengine.SearchResult{
		Manga: []sourceengine.MangaEntry{{URL: "default"}},
	}))
	staleClient := sourceenginefake.New(sourceenginefake.WithSearchResult(1, sourceengine.SearchResult{
		Manga: []sourceengine.MangaEntry{{URL: "stale-old-profile"}},
	}))
	router := engineroute.NewRouter(defaultClient)
	router.SetRoutes(map[int64]sourceengine.Client{1: staleClient})
	launcher := &fakeLauncher{fail: true}
	completed := false
	launcher.onComplete = func() {
		completed = true
		got, err := router.Search(context.Background(), 1, "q", 1)
		if err != nil {
			t.Fatalf("Search during publication completion: %v", err)
		}
		if len(got.Manga) != 1 || got.Manga[0].URL != "default" {
			t.Fatalf("route at publication completion = %+v, want failed profile removed before degradation release", got)
		}
	}

	result := mustReconcileNetwork(t, enginetopo.NetworkReconcileDeps{
		Snapshot: fakeSnapshotter{bindings: []network.ResolvedBinding{{
			SourceID: 1, FlareMode: network.FlareModeEndpoint, Flare: &network.ResolvedFlare{ID: "new"},
		}}},
		Router: router, Launcher: launcher, BaseConfig: baseConfig(), DefaultKCEFEnabled: true,
	})
	if len(result.Gaps) != 1 {
		t.Fatalf("gaps = %v, want failed replacement gap", result.Gaps)
	}
	if !completed {
		t.Fatal("preparation was not completed after route publication")
	}
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

// TestReconcileNetwork_ProfilePreferencesIgnoreUnroutedHistory proves a
// profile instance is provisioned only with preferences for sources routed to
// that profile. Durable SourcePreference rows intentionally outlive removed
// providers; asking a fresh profile instance for those absent source IDs must
// not degrade the profile, while a real read failure for a routed source must
// still remain a provisioning gap.
func TestReconcileNetwork_ProfilePreferencesIgnoreUnroutedHistory(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())
	seedStoredPref(ctx, t, db, 42, "nsfw", "true", sourceengine.PreferenceCheckBox)
	seedStoredPref(ctx, t, db, 99, "nsfw", "true", sourceengine.PreferenceCheckBox)

	instance := &preferenceReadClient{
		Client: sourceenginefake.New(),
		errs: map[int64]error{
			99: &sourceengine.BadRequestError{Msg: "unknown sourceId 99"},
		},
	}
	deps := enginetopo.NetworkReconcileDeps{
		Snapshot:   fakeSnapshotter{bindings: []network.ResolvedBinding{socksBinding(42)}},
		Router:     engineroute.NewRouter(sourceenginefake.New()),
		Launcher:   preferenceLauncher{instance: instance},
		DB:         db,
		Cache:      cache,
		BaseConfig: baseConfig(),
	}
	res := mustReconcileNetwork(t, deps)
	if res.InstancesRouted != 1 || len(res.Gaps) != 0 {
		t.Fatalf("result = %+v, want stale preference history not to degrade profile", res)
	}
	if !slices.Equal(instance.seen, []int64{42}) {
		t.Fatalf("preference reads = %v, want only routed source [42] (stale source 99 ignored)", instance.seen)
	}

	instance.seen = nil
	instance.errs[42] = errors.New("routed source preference read failed")
	res = mustReconcileNetwork(t, deps)
	if res.InstancesRouted != 0 || len(res.Gaps) != 1 {
		t.Fatalf("result = %+v, want routed-source failure to degrade the profile with one gap", res)
	}
	if !strings.Contains(res.Gaps[0].Error(), "routed source preference read failed") {
		t.Fatalf("gap = %v, want routed source preference failure", res.Gaps[0])
	}
	if !slices.Equal(instance.seen, []int64{42}) {
		t.Fatalf("preference reads after routed failure = %v, want only routed source [42]", instance.seen)
	}
}

// TestSourceRuntimeApplierConvergesEveryDesiredInstanceBeforeRouting catches a
// partial runtime apply: missing image/proxy sets on any active instance,
// activating a newly-created route before its config lands, retaining an
// obsolete profile, or treating a fallback as success.
// TestSourcePolicyRevisionWaitsForItsKCEFProfile proves an applied source-policy
// revision means the requested KCEF topology was actually converged.
func TestSourcePolicyRevisionWaitsForItsKCEFProfile(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	defaultClient := &runtimeConfigClient{Client: sourceenginefake.New()}
	launcher := &fakeLauncher{fail: true}
	service := sourcetransport.NewService(db, topologyTransportDefaults{}, acceptingTransportCatalog{})
	applier := enginetopo.NewSourceRuntimeApplier(defaultClient, enginetopo.NetworkReconcileDeps{
		Snapshot:           fakeSnapshotter{bindings: []network.ResolvedBinding{{SourceID: 42, FlareMode: network.FlareModeEndpoint, Flare: &network.ResolvedFlare{ID: "flare"}}}},
		TransportSnapshot:  service,
		Router:             engineroute.NewRouter(defaultClient),
		Launcher:           launcher,
		DB:                 db,
		Cache:              apkcache.New(t.TempDir()),
		BaseConfig:         baseConfig(),
		DefaultKCEFEnabled: true,
	})
	service.WithRuntimeApplier(applier)

	updated, err := service.Update(ctx, 42, sourcetransport.Patch{KCEFPolicy: sourcetransport.Set(runtimepolicy.KCEFPolicyRequired)})
	if err == nil {
		t.Fatal("Update error = nil, want KCEF profile convergence failure")
	}
	if updated.Intent.DesiredRevision != 1 || updated.Intent.AppliedRevision != 0 || updated.Intent.LastApplyAttempt == nil {
		t.Fatalf("intent = %+v, want desired 1/applied 0 with failed convergence attempt", updated.Intent)
	}
	if len(launcher.profiles) != 1 || launcher.profiles[0].Key != "|endpoint|flare|kcef=on" || !launcher.profiles[0].KCEFEnabled {
		t.Fatalf("requested profiles = %+v, want one KCEF-on endpoint profile", launcher.profiles)
	}
}

func TestSourceRuntimeApplierConvergesEveryDesiredInstanceBeforeRouting(t *testing.T) { //nolint:cyclop // Ordering test intentionally checks every phase and instance.
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())
	imageReuse := sourcetransport.ImageConnectionReuse
	disableSessionReuse := false
	policies := map[int64]sourcetransport.Override{
		11: {ImageConnectionMode: &imageReuse, ReuseBypassSession: &disableSessionReuse},
		22: {ImageConnectionMode: &imageReuse},
	}
	bindings := []network.ResolvedBinding{socksBinding(11), socksBinding(22), socksBinding(33)}
	bindings[0].Socks.ID = "new"
	bindings[1].Socks.ID = "reused"
	bindings[2].Socks.ID = "fallback"
	profiles := engineroute.Derive(true, []engineroute.BindingInput{
		{SourceID: 11, Socks: &engineroute.SocksEndpoint{ID: "new"}, FlareMode: engineroute.FlareModeGlobal, DisableBypassSession: true},
		{SourceID: 22, Socks: &engineroute.SocksEndpoint{ID: "reused"}, FlareMode: engineroute.FlareModeGlobal},
		{SourceID: 33, Socks: &engineroute.SocksEndpoint{ID: "fallback"}, FlareMode: engineroute.FlareModeGlobal},
	})
	keys := map[int64]string{}
	for _, profile := range profiles {
		keys[profile.SourceIDs[0]] = profile.Key
	}

	defaultClient := &runtimeConfigClient{Client: sourceenginefake.New(
		sourceenginefake.WithSearchResult(11, sourceengine.SearchResult{Manga: []sourceengine.MangaEntry{{URL: "default"}}}),
		sourceenginefake.WithSearchResult(33, sourceengine.SearchResult{Manga: []sourceengine.MangaEntry{{URL: "default"}}}),
	)}
	router := engineroute.NewRouter(defaultClient)
	reusedClient := &runtimeConfigClient{Client: sourceenginefake.New(
		sourceenginefake.WithSearchResult(22, sourceengine.SearchResult{Manga: []sourceengine.MangaEntry{{URL: "reused"}}}),
	)}
	launcher := &lifecycleLauncher{
		instances: map[string]*runtimeConfigClient{
			keys[22]:   reusedClient,
			"obsolete": {Client: sourceenginefake.New()},
		},
		fail: map[string]error{keys[33]: errors.New("profile unavailable")},
	}
	base := baseConfig()
	base.impSources = []int64{22, 33}

	newClientConfiguredBeforeRoute := false
	launcher.onCreate = func(profile engineroute.Profile, client *runtimeConfigClient) {
		if profile.Key != keys[11] {
			return
		}
		client.beforeImagePush = func() {
			got, searchErr := router.Search(ctx, 11, "q", 1)
			newClientConfiguredBeforeRoute = searchErr == nil && len(got.Manga) == 1 && got.Manga[0].URL == "default"
		}
	}
	applier := enginetopo.NewSourceRuntimeApplier(defaultClient, enginetopo.NetworkReconcileDeps{
		Snapshot:           fakeSnapshotter{bindings: bindings},
		TransportSnapshot:  fakeTransportSnapshotter{policies: policies},
		Router:             router,
		Launcher:           launcher,
		DB:                 db,
		Cache:              cache,
		BaseConfig:         base,
		DefaultKCEFEnabled: true,
	})

	err := applier.ApplySourceRuntime(ctx, 11)
	if err == nil || !strings.Contains(err.Error(), "profile unavailable") {
		t.Fatalf("ApplySourceRuntime error = %v, want fallback failure", err)
	}
	if !newClientConfiguredBeforeRoute {
		t.Fatal("new profile route became active before its full runtime config completed")
	}
	if !slices.Contains(launcher.retired, "obsolete") {
		t.Fatalf("retired profiles = %v, want obsolete", launcher.retired)
	}
	if launcher.lastKeep[keys[33]] {
		t.Fatalf("fallback profile %q present in keep set", keys[33])
	}

	for name, client := range map[string]*runtimeConfigClient{
		"default": defaultClient,
		"new":     launcher.instances[keys[11]],
		"reused":  reusedClient,
	} {
		images, proxy := client.runtimeSets()
		if !slices.Equal(images, []int64{11, 22}) {
			t.Errorf("%s image transport sources = %v, want [11 22]", name, images)
		}
		if !slices.Equal(proxy, []int64{22, 33}) {
			t.Errorf("%s proxy sources = %v, want [22 33]", name, proxy)
		}
	}
	if got := launcher.instances[keys[11]].session(); got != "" {
		t.Errorf("mixed-policy profile session = %q, want disposable blank session", got)
	}
	assertRoutedTo(t, router, 11, keys[11])
	assertRoutedTo(t, router, 22, "reused")
	assertRoutedTo(t, router, 33, "default")
}

func TestSourceRuntimeApplierFreezesFullConfigAcrossDefaultAndProfiles(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	defaultClient := &runtimeConfigClient{Client: sourceenginefake.New()}
	var profileClient *runtimeConfigClient
	launcher := &lifecycleLauncher{instances: map[string]*runtimeConfigClient{}}

	base := &mutableRuntimeConfig{fakeConfig: baseConfig(), impSources: []int64{11}}
	defaultClient.beforeImagePush = func() {
		base.setImpersonateSources([]int64{22})
	}
	launcher.onCreate = func(_ engineroute.Profile, client *runtimeConfigClient) {
		profileClient = client
	}
	applier := enginetopo.NewSourceRuntimeApplier(defaultClient, enginetopo.NetworkReconcileDeps{
		Snapshot:   fakeSnapshotter{bindings: []network.ResolvedBinding{socksBinding(11)}},
		Router:     engineroute.NewRouter(defaultClient),
		Launcher:   launcher,
		DB:         db,
		Cache:      apkcache.New(t.TempDir()),
		BaseConfig: base,
	})

	if err := applier.ApplySourceRuntime(ctx, 11); err != nil {
		t.Fatalf("ApplySourceRuntime: %v", err)
	}
	if profileClient == nil {
		t.Fatal("profile instance was not created")
	}
	_, defaultProxy := defaultClient.runtimeSets()
	_, profileProxy := profileClient.runtimeSets()
	if !slices.Equal(defaultProxy, []int64{11}) || !slices.Equal(profileProxy, []int64{11}) {
		t.Fatalf("impersonate snapshots default=%v profile=%v, want one captured [11] snapshot", defaultProxy, profileProxy)
	}
}

func TestReconcileNetworkPublishesRoutesBeforeRetiringObsoleteInstance(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())
	oldClient := sourceenginefake.New(
		sourceenginefake.WithSearchResult(42, sourceengine.SearchResult{Manga: []sourceengine.MangaEntry{{URL: "obsolete"}}}),
	)
	newClient := sourceenginefake.New(
		sourceenginefake.WithSearchResult(42, sourceengine.SearchResult{Manga: []sourceengine.MangaEntry{{URL: "current"}}}),
	)
	router := engineroute.NewRouter(sourceenginefake.New())
	router.SetRoutes(map[int64]sourceengine.Client{42: oldClient})
	launcher := &blockingRetireLauncher{
		instance: newClient,
		entered:  make(chan struct{}),
		release:  make(chan struct{}),
	}
	done := make(chan error, 1)
	go func() {
		_, err := enginetopo.ReconcileNetwork(ctx, enginetopo.NetworkReconcileDeps{
			Snapshot:   fakeSnapshotter{bindings: []network.ResolvedBinding{socksBinding(42)}},
			Router:     router,
			Launcher:   launcher,
			DB:         db,
			Cache:      cache,
			BaseConfig: baseConfig(),
		})
		done <- err
	}()
	<-launcher.entered
	assertRoutedTo(t, router, 42, "current")
	close(launcher.release)
	if err := <-done; err != nil {
		t.Fatalf("ReconcileNetwork: %v", err)
	}
}

func TestSourceRuntimeApplierQueuedWaitHonorsContextCancellation(t *testing.T) {
	snapshot := &blockingTransportSnapshotter{entered: make(chan struct{}), release: make(chan struct{})}
	applier := enginetopo.NewSourceRuntimeApplier(sourceenginefake.New(), enginetopo.NetworkReconcileDeps{
		Snapshot:          fakeSnapshotter{},
		TransportSnapshot: snapshot,
		Router:            engineroute.NewRouter(sourceenginefake.New()),
		Launcher:          &fakeLauncher{},
		BaseConfig:        baseConfig(),
	})
	firstDone := make(chan error, 1)
	go func() { firstDone <- applier.ApplySourceRuntime(context.Background(), 11) }()
	<-snapshot.entered

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	secondDone := make(chan error, 1)
	go func() { secondDone <- applier.ApplySourceRuntime(cancelled, 22) }()
	select {
	case err := <-secondDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued ApplySourceRuntime error = %v, want context canceled", err)
		}
	case <-time.After(2 * time.Second):
		close(snapshot.release)
		<-firstDone
		<-secondDone
		t.Fatal("queued ApplySourceRuntime did not return while the lifecycle serializer was occupied")
	}
	close(snapshot.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first ApplySourceRuntime: %v", err)
	}
}

func TestSourceRuntimeApplierShutdownClosesAdmissionsAndJoinsActiveTail(t *testing.T) { //nolint:cyclop // Concurrency test keeps admission, shutdown, and join assertions in one timeline.
	applier := enginetopo.NewSourceRuntimeApplier(sourceenginefake.New(), enginetopo.NetworkReconcileDeps{})
	entered := make(chan struct{})
	canceled := make(chan struct{})
	releaseTail := make(chan struct{})
	finished := make(chan struct{})
	callDone := make(chan error, 1)
	go func() {
		callDone <- applier.RunRuntime(context.Background(), func(ctx context.Context) error {
			close(entered)
			<-ctx.Done()
			close(canceled)
			<-releaseTail
			close(finished)
			return ctx.Err()
		})
	}()
	<-entered

	shutdownDone := make(chan error, 1)
	go func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		shutdownDone <- applier.ShutdownRuntimeConvergence(shutdownCtx)
	}()
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("active convergence did not receive lifecycle cancellation")
	}
	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown returned before active tail completed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	lateDone := make(chan error, 1)
	go func() {
		lateDone <- applier.RunRuntime(context.Background(), func(context.Context) error {
			return errors.New("late callback ran")
		})
	}()
	select {
	case err := <-lateDone:
		if !errors.Is(err, enginetopo.ErrRuntimeConvergenceClosed) {
			t.Fatalf("late admission error = %v, want ErrRuntimeConvergenceClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("late convergence admission did not fail fast")
	}

	close(releaseTail)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("active convergence tail did not finish")
	}
	select {
	case err := <-callDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("active convergence error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("active convergence call did not return")
	}
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("ShutdownRuntimeConvergence: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not join completed convergence")
	}
	if err := applier.ShutdownRuntimeConvergence(context.Background()); err != nil {
		t.Fatalf("repeated ShutdownRuntimeConvergence: %v", err)
	}
}

func TestSourceRuntimeApplierEscapedAdmissionContextCannotReenter(t *testing.T) {
	applier := enginetopo.NewSourceRuntimeApplier(sourceenginefake.New(), enginetopo.NetworkReconcileDeps{})
	var escaped context.Context
	if err := applier.RunRuntime(context.Background(), func(ctx context.Context) error {
		escaped = context.WithoutCancel(ctx)
		return nil
	}); err != nil {
		t.Fatalf("outer RunRuntime: %v", err)
	}

	executed := false
	err := applier.RunRuntime(escaped, func(context.Context) error {
		executed = true
		return nil
	})
	if !errors.Is(err, enginetopo.ErrRuntimeConvergenceClosed) {
		t.Fatalf("escaped RunRuntime error = %v, want ErrRuntimeConvergenceClosed", err)
	}
	if executed {
		t.Fatal("escaped admission context executed after its outer operation returned")
	}
}

func TestSourceRuntimeApplierEscapedContextCannotEnterAfterShutdownStarts(t *testing.T) {
	applier := enginetopo.NewSourceRuntimeApplier(sourceenginefake.New(), enginetopo.NetworkReconcileDeps{})
	escapedReady := make(chan context.Context, 1)
	outerCanceled := make(chan struct{})
	releaseOuter := make(chan struct{})
	outerDone := make(chan error, 1)
	go func() {
		outerDone <- applier.RunRuntime(context.Background(), func(ctx context.Context) error {
			escapedReady <- context.WithoutCancel(ctx)
			<-ctx.Done()
			close(outerCanceled)
			<-releaseOuter
			return ctx.Err()
		})
	}()
	escaped := <-escapedReady
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- applier.ShutdownRuntimeConvergence(context.Background()) }()
	<-outerCanceled

	executed := false
	err := applier.RunRuntime(escaped, func(context.Context) error {
		executed = true
		return nil
	})
	if !errors.Is(err, enginetopo.ErrRuntimeConvergenceClosed) {
		t.Fatalf("post-shutdown escaped RunRuntime error = %v, want ErrRuntimeConvergenceClosed", err)
	}
	if executed {
		t.Fatal("escaped admission context executed after convergence shutdown started")
	}
	close(releaseOuter)
	if err := <-outerDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("outer RunRuntime error = %v, want context canceled", err)
	}
	if err := <-shutdownDone; err != nil {
		t.Fatalf("ShutdownRuntimeConvergence: %v", err)
	}
}

func TestSourceRuntimeApplierOuterReturnJoinsConcurrentNestedUse(t *testing.T) {
	applier := enginetopo.NewSourceRuntimeApplier(sourceenginefake.New(), enginetopo.NetworkReconcileDeps{})
	nestedEntered := make(chan struct{})
	releaseNested := make(chan struct{})
	outerDone := make(chan error, 1)
	var nestedDone chan error
	go func() {
		outerDone <- applier.RunRuntime(context.Background(), func(ctx context.Context) error {
			nestedDone = make(chan error, 1)
			nestedCtx := context.WithoutCancel(ctx)
			go func() {
				nestedDone <- applier.RunRuntime(nestedCtx, func(context.Context) error {
					close(nestedEntered)
					<-releaseNested
					return nil
				})
			}()
			<-nestedEntered
			return nil
		})
	}()

	<-nestedEntered
	select {
	case err := <-outerDone:
		t.Fatalf("outer admission returned before nested use completed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseNested)
	if err := <-nestedDone; err != nil {
		t.Fatalf("nested RunRuntime: %v", err)
	}
	if err := <-outerDone; err != nil {
		t.Fatalf("outer RunRuntime: %v", err)
	}
}

func TestSourceRuntimeApplierSerializedScopeJoinsNestedUseBeforeRelease(t *testing.T) {
	applier := enginetopo.NewSourceRuntimeApplier(sourceenginefake.New(), enginetopo.NetworkReconcileDeps{})
	nestedEntered := make(chan struct{})
	releaseNested := make(chan struct{})
	outerCallbackReturned := make(chan struct{})
	outerDone := make(chan error, 1)
	go func() {
		outerDone <- applier.RunSerializedRuntime(context.Background(), func(ctx context.Context) error {
			go func() {
				_ = applier.RunSerializedRuntime(context.WithoutCancel(ctx), func(context.Context) error {
					close(nestedEntered)
					<-releaseNested
					return nil
				})
			}()
			<-nestedEntered
			close(outerCallbackReturned)
			return nil
		})
	}()
	<-outerCallbackReturned

	independentEntered := make(chan struct{})
	independentDone := make(chan error, 1)
	go func() {
		independentDone <- applier.RunSerializedRuntime(context.Background(), func(context.Context) error {
			close(independentEntered)
			return nil
		})
	}()
	select {
	case <-independentEntered:
		t.Fatal("serializer released while a nested serialized use was still active")
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseNested)
	if err := <-outerDone; err != nil {
		t.Fatalf("outer serialized runtime: %v", err)
	}
	if err := <-independentDone; err != nil {
		t.Fatalf("independent serialized runtime: %v", err)
	}
}

type shutdownTransportSnapshotter struct {
	entered  chan struct{}
	canceled chan struct{}
	release  chan struct{}
}

func (s *shutdownTransportSnapshotter) Snapshot(ctx context.Context) (map[int64]sourcetransport.Override, error) {
	close(s.entered)
	<-ctx.Done()
	close(s.canceled)
	<-s.release
	return nil, ctx.Err()
}

func TestSourceRuntimeApplierShutdownJoinsDirectRuntimeCall(t *testing.T) { //nolint:cyclop // Concurrency timeline requires multiple timeout failure branches.
	snapshot := &shutdownTransportSnapshotter{
		entered:  make(chan struct{}),
		canceled: make(chan struct{}),
		release:  make(chan struct{}),
	}
	applier := enginetopo.NewSourceRuntimeApplier(sourceenginefake.New(), enginetopo.NetworkReconcileDeps{
		Snapshot:          fakeSnapshotter{},
		TransportSnapshot: snapshot,
		Router:            engineroute.NewRouter(sourceenginefake.New()),
		Launcher:          &fakeLauncher{},
		BaseConfig:        baseConfig(),
	})
	callDone := make(chan error, 1)
	go func() { callDone <- applier.ReconcileRuntime(context.Background()) }()
	<-snapshot.entered

	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- applier.ShutdownRuntimeConvergence(context.Background()) }()
	select {
	case <-snapshot.canceled:
	case <-time.After(time.Second):
		t.Fatal("direct ReconcileRuntime did not receive lifecycle cancellation")
	}
	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown returned before direct runtime tail: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(snapshot.release)
	select {
	case err := <-callDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ReconcileRuntime error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("direct ReconcileRuntime did not finish")
	}
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("ShutdownRuntimeConvergence: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not join direct ReconcileRuntime")
	}
}

func TestRuntimeSettingsWriteConvergesThroughSourceApplyLifecycle(t *testing.T) { //nolint:gocognit,cyclop // End-to-end lifecycle oracle validates each persisted and applied state.
	ctx := context.Background()
	db := testdb.New(t)
	settingsSvc := settings.NewService(db, settings.Defaults{
		FlareSolverrTimeout:    60,
		FlareSolverrSessionTTL: 15,
		EngineSocksPort:        1080,
		EngineSocksVersion:     5,
		ImpersonateSources:     "",
	})
	if err := settingsSvc.SetMany(ctx, []settings.KeyValue{
		{Key: settings.KeyImpersonateEnabled, Value: "true"},
		{Key: settings.KeyImpersonateURL, Value: "http://impersonate.test:8788"},
		{Key: settings.KeyImpersonateSources, Value: "11"},
	}); err != nil {
		t.Fatalf("seed runtime settings: %v", err)
	}

	defaultClient := &runtimeConfigClient{Client: sourceenginefake.New()}
	launcher := &lifecycleLauncher{instances: map[string]*runtimeConfigClient{}}
	router := engineroute.NewRouter(defaultClient)
	transportSvc := sourcetransport.NewService(db, sourceRuntimeDefaults{}, sourceCatalog{})
	applier := enginetopo.NewSourceRuntimeApplier(defaultClient, enginetopo.NetworkReconcileDeps{
		Snapshot:          fakeSnapshotter{bindings: []network.ResolvedBinding{socksBinding(11)}},
		TransportSnapshot: transportSvc,
		Router:            router,
		Launcher:          launcher,
		DB:                db,
		Cache:             apkcache.New(t.TempDir()),
		BaseConfig:        settingsSvc,
	})
	transportSvc.WithRuntimeApplier(applier)
	settingsConvergenceEntered := make(chan struct{})
	settingsSvc.WithRuntimeConverger(signalingRuntimeConverger{
		entered:  settingsConvergenceEntered,
		delegate: applier,
	})

	oldDefaultApplied := make(chan struct{})
	resumeSourceApply := make(chan struct{})
	var pauseOnce sync.Once
	defaultClient.beforeImagePush = func() {
		pauseOnce.Do(func() {
			close(oldDefaultApplied)
			<-resumeSourceApply
		})
	}
	transportDone := make(chan sourcetransport.UpdateResult, 1)
	transportErr := make(chan error, 1)
	go func() {
		result, err := transportSvc.Update(ctx, 11, sourcetransport.Patch{
			ImageConnectionMode: sourcetransport.Set(sourcetransport.ImageConnectionReuse),
		})
		transportDone <- result
		transportErr <- err
	}()
	<-oldDefaultApplied

	settingsErr := make(chan error, 1)
	go func() {
		settingsErr <- settingsSvc.SetMany(ctx, []settings.KeyValue{
			{Key: settings.KeyImpersonateSources, Value: "22"},
		})
	}()
	<-settingsConvergenceEntered
	_, defaultProxy := defaultClient.runtimeSets()
	if !slices.Equal(defaultProxy, []int64{11}) {
		t.Fatalf("default proxy set while source apply paused = %v, want frozen old [11] (no direct mirror)", defaultProxy)
	}
	pendingSettings, err := settingsSvc.RuntimeIntent(ctx)
	if err != nil {
		t.Fatalf("pending settings RuntimeIntent: %v", err)
	}
	if pendingSettings.DesiredRevision != 2 || pendingSettings.AppliedRevision != 0 {
		t.Fatalf("settings intent while source revision is applying = %+v, want desired 2 / applied 0", pendingSettings)
	}

	close(resumeSourceApply)
	result := <-transportDone
	if err := <-transportErr; err != nil {
		t.Fatalf("transport Update: %v", err)
	}
	if result.Intent.DesiredRevision != 1 || result.Intent.AppliedRevision != 1 {
		t.Fatalf("transport intent = %+v, want exact revision 1 acknowledged", result.Intent)
	}
	if err := <-settingsErr; err != nil {
		t.Fatalf("runtime settings update: %v", err)
	}
	settingsIntent, err := settingsSvc.RuntimeIntent(ctx)
	if err != nil {
		t.Fatalf("settings RuntimeIntent: %v", err)
	}
	if settingsIntent.DesiredRevision != 2 || settingsIntent.AppliedRevision != 2 {
		t.Fatalf("settings intent = %+v, want seeded revision 1 plus concurrent revision 2 exactly acknowledged", settingsIntent)
	}

	var profileClient *runtimeConfigClient
	for _, client := range launcher.instances {
		profileClient = client
	}
	if profileClient == nil {
		t.Fatal("desired profile instance missing")
	}
	for name, client := range map[string]*runtimeConfigClient{"default": defaultClient, "profile": profileClient} {
		_, proxy := client.runtimeSets()
		if !slices.Equal(proxy, []int64{22}) {
			t.Errorf("%s proxy sources after both writes = %v, want converged [22]", name, proxy)
		}
	}
}
