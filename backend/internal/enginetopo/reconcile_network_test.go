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
	retired   []string
	lastKeep  map[string]bool
	onCreate  func(engineroute.Profile, *runtimeConfigClient)
}

type blockingRetireLauncher struct {
	instance *sourceenginefake.Client
	entered  chan struct{}
	release  chan struct{}
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

// TestSourceRuntimeApplierConvergesEveryDesiredInstanceBeforeRouting catches a
// partial runtime apply: missing image/proxy sets on any active instance,
// activating a newly-created route before its config lands, retaining an
// obsolete profile, or treating a fallback as success.
func TestSourceRuntimeApplierConvergesEveryDesiredInstanceBeforeRouting(t *testing.T) {
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
	profiles := engineroute.Derive([]engineroute.BindingInput{
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
		Snapshot:          fakeSnapshotter{bindings: bindings},
		TransportSnapshot: fakeTransportSnapshotter{policies: policies},
		Router:            router,
		Launcher:          launcher,
		DB:                db,
		Cache:             cache,
		BaseConfig:        base,
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
