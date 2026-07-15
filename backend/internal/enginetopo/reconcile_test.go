package enginetopo_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/enginetopo"
	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	sourceenginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
)

// --- reconcile test fake -----------------------------------------------------

// reconcileClient embeds the shared sourceengine fake and adds PER-ITEM error
// injection the base fake can't express (its WithError is blanket-per-method,
// not per-argument): a per-pkgName InstallExtension failure, a per-sourceID
// SetPreferences failure, and a per-(sourceID,key) SetPreferences REJECTION —
// all needed to prove fault ISOLATION (one bad item/source/key must not block
// its siblings). It also records the config actually applied via
// SetFlareSolverr/SetSocks (the base fake returns it but Reconcile itself
// discards the return value), so tests can assert the pushed config without a
// redundant extra call that would corrupt CallCount assertions.
type reconcileClient struct {
	*sourceenginefake.Client

	installErr  map[string]error // per-pkgName InstallExtension failure
	setPrefsErr map[int64]error  // per-sourceID (whole-call) SetPreferences failure

	// rejectKeys models an engine that validates each key in a
	// SetPreferences batch INDEPENDENTLY: it PARTIALLY applies every
	// accepted key (see SetPreferences below) before returning ONE
	// batch-level error naming the rejected key(s) — sourceengine.Client's
	// SetPreferences has no per-key result, only a single error for the
	// whole call, so this is the sharpest fake extension that can prove a
	// rejected sibling key does not silently drop an ACCEPTED sibling key's
	// intent. See TestReconcile_MixedBatchKeyRejectionIsolatedWithoutLosingSiblingIntent.
	rejectKeys map[int64]map[string]bool

	mu               sync.Mutex
	lastFlareSolverr sourceengine.FlareSolverrConfig
	lastSocks        sourceengine.SocksConfig
	// lastBatchKeys records the sorted key set Reconcile attempted to push
	// for a source on its MOST RECENT SetPreferences call, regardless of
	// whether that call ultimately succeeded — lets a test prove exactly
	// which keys were IN a given pass's batch (e.g. that an already-in-sync
	// key is excluded from a LATER pass's batch entirely).
	lastBatchKeys map[int64][]string
}

func (c *reconcileClient) InstallExtension(ctx context.Context, pkgName, apkURL string) ([]sourceengine.Extension, error) {
	if err, ok := c.installErr[pkgName]; ok {
		return nil, err
	}
	return c.Client.InstallExtension(ctx, pkgName, apkURL)
}

func (c *reconcileClient) SetPreferences(ctx context.Context, sourceID int64, changes map[string]any) ([]sourceengine.Preference, error) {
	c.recordBatchKeys(sourceID, changes)
	if err, ok := c.setPrefsErr[sourceID]; ok {
		return nil, err
	}
	if bad := c.rejectKeys[sourceID]; len(bad) > 0 {
		accepted := make(map[string]any, len(changes))
		var rejected []string
		for k, v := range changes {
			if bad[k] {
				rejected = append(rejected, k)
				continue
			}
			accepted[k] = v
		}
		if len(rejected) > 0 {
			if len(accepted) > 0 {
				// Partial apply: push the accepted keys straight through to
				// the embedded fake before surfacing the batch-level error
				// for the rejected one(s) — modelling an engine that
				// validates independently rather than atomically discarding
				// the whole call.
				if _, err := c.Client.SetPreferences(ctx, sourceID, accepted); err != nil {
					return nil, err
				}
			}
			return nil, fmt.Errorf("source %d rejected key(s) %v", sourceID, rejected)
		}
	}
	return c.Client.SetPreferences(ctx, sourceID, changes)
}

// recordBatchKeys stores the sorted key set of a SetPreferences call for
// sourceID into lastBatchKeys.
func (c *reconcileClient) recordBatchKeys(sourceID int64, changes map[string]any) {
	keys := make([]string, 0, len(changes))
	for k := range changes {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	c.mu.Lock()
	if c.lastBatchKeys == nil {
		c.lastBatchKeys = map[int64][]string{}
	}
	c.lastBatchKeys[sourceID] = keys
	c.mu.Unlock()
}

func (c *reconcileClient) SetFlareSolverr(ctx context.Context, patch sourceengine.FlareSolverrPatch) (sourceengine.FlareSolverrConfig, error) {
	cfg, err := c.Client.SetFlareSolverr(ctx, patch)
	if err == nil {
		c.mu.Lock()
		c.lastFlareSolverr = cfg
		c.mu.Unlock()
	}
	return cfg, err
}

func (c *reconcileClient) SetSocks(ctx context.Context, patch sourceengine.SocksPatch) (sourceengine.SocksConfig, error) {
	cfg, err := c.Client.SetSocks(ctx, patch)
	if err == nil {
		c.mu.Lock()
		c.lastSocks = cfg
		c.mu.Unlock()
	}
	return cfg, err
}

// --- ConfigProvider test double ----------------------------------------------

// fakeConfig is a ConfigProvider whose accessors return fixed values, so a test
// can drive Reconcile's FlareSolverr + SOCKS push without a settings.Service.
type fakeConfig struct {
	fsEnabled     bool
	fsURL         string
	fsTimeout     int
	fsSessionName string
	fsSessionTTL  int
	fsFallback    bool
	socksEnabled  bool
	socksHost     string
	socksPort     int
	socksVersion  int
}

func (c fakeConfig) FlareSolverrEnabled(context.Context) bool          { return c.fsEnabled }
func (c fakeConfig) FlareSolverrURL(context.Context) string            { return c.fsURL }
func (c fakeConfig) FlareSolverrTimeout(context.Context) int           { return c.fsTimeout }
func (c fakeConfig) FlareSolverrSessionName(context.Context) string    { return c.fsSessionName }
func (c fakeConfig) FlareSolverrSessionTTL(context.Context) int        { return c.fsSessionTTL }
func (c fakeConfig) FlareSolverrResponseFallback(context.Context) bool { return c.fsFallback }
func (c fakeConfig) EngineSocksEnabled(context.Context) bool           { return c.socksEnabled }
func (c fakeConfig) EngineSocksHost(context.Context) string            { return c.socksHost }
func (c fakeConfig) EngineSocksPort(context.Context) int               { return c.socksPort }
func (c fakeConfig) EngineSocksVersion(context.Context) int            { return c.socksVersion }

// baseConfig is a fixed FlareSolverr+SOCKS ConfigProvider fixture shared by
// every test below (the specific values are arbitrary — reconcileConfig no
// longer compares them against anything, it just pushes them).
func baseConfig() fakeConfig {
	return fakeConfig{
		fsEnabled:     true,
		fsURL:         "http://flare.test:8191",
		fsTimeout:     60,
		fsSessionName: "sess",
		fsSessionTTL:  15,
		fsFallback:    false,
		socksEnabled:  true,
		socksHost:     "127.0.0.1",
		socksPort:     1080,
		socksVersion:  5,
	}
}

// seedHarvestedExtension inserts a HarvestedExtension row mapping pkgName to
// the given source ids, so requiredPkgSet can match it against the library's
// numeric providers.
func seedHarvestedExtension(ctx context.Context, t *testing.T, db *ent.Client, pkgName string, sourceIDs []int64) {
	t.Helper()
	db.HarvestedExtension.Create().
		SetPkgName(pkgName).
		SetSourceIds(sourceIDs).
		SaveX(ctx)
}

// seedStoredPref inserts a durable SourcePreference row (source_id, key) →
// (value, value_type) directly, as the boot seed would have captured it.
func seedStoredPref(ctx context.Context, t *testing.T, db *ent.Client, sourceID int64, key, value, typ string) {
	t.Helper()
	db.SourcePreference.Create().
		SetSourceID(sourceID).
		SetKey(key).
		SetValue(value).
		SetValueType(typ).
		SaveX(ctx)
}

// TestReconcile_FreshEnginePopulatedDB proves the recovery core: an empty engine
// with a populated durable store gets its repos set, missing extensions
// installed, drifted preferences pushed, and config applied — with the Result
// counts reflecting every action and InSync=false.
func TestReconcile_FreshEnginePopulatedDB(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	seedProvider(ctx, t, db, "Solo Leveling", "111", 111)
	seedHarvestedExtension(ctx, t, db, "pkg.one", []int64{111})
	db.HarvestedRepo.Create().SetURL("https://repo.test/repo").SaveX(ctx)
	seedStoredPref(ctx, t, db, 111, "nsfw", "true", sourceengine.PreferenceCheckBox)

	cfg := baseConfig()

	client := &reconcileClient{
		Client: sourceenginefake.New(
			// Live value (false) differs from the stored value (true) → push.
			sourceenginefake.WithPreferences(111, []sourceengine.Preference{
				{Type: sourceengine.PreferenceCheckBox, Key: "nsfw", CurrentValue: false},
			}),
			// engineRepos empty → repo drift; no extensions → pkg.one missing.
		),
	}

	res, err := enginetopo.Reconcile(ctx, client, db, cache, cfg)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	assertFreshResult(t, res)
	assertFreshMutations(ctx, t, client)
}

// assertFreshResult checks the Result counts a full-recovery pass reports.
func assertFreshResult(t *testing.T, res enginetopo.ReconcileResult) {
	t.Helper()
	if res.InSync {
		t.Error("InSync = true, want false (fresh engine needed every drift-detected change)")
	}
	if !res.ReposSet {
		t.Error("ReposSet = false, want true")
	}
	if res.ExtensionsInstalled != 1 {
		t.Errorf("ExtensionsInstalled = %d, want 1", res.ExtensionsInstalled)
	}
	if res.PrefsApplied != 1 {
		t.Errorf("PrefsApplied = %d, want 1", res.PrefsApplied)
	}
	if !res.ConfigApplied {
		t.Error("ConfigApplied = false, want true")
	}
	if len(res.Gaps) != 0 {
		t.Errorf("Gaps = %v, want none", res.Gaps)
	}
}

// assertFreshMutations checks the exact engine mutations a full-recovery pass
// issued (repo set, refresh, install, pref push, config push).
func assertFreshMutations(ctx context.Context, t *testing.T, client *reconcileClient) {
	t.Helper()
	repos, err := client.Repos(ctx)
	if err != nil || len(repos) != 1 || repos[0] != "https://repo.test/repo" {
		t.Errorf("engine repos after reconcile = %v (err=%v), want [https://repo.test/repo]", repos, err)
	}
	if client.CallCount("RefreshExtensions") == 0 {
		t.Error("RefreshExtensions not called before installing a missing extension")
	}
	if client.CallCount("InstallExtension") != 1 {
		t.Errorf("InstallExtension calls = %d, want 1", client.CallCount("InstallExtension"))
	}
	prefs, err := client.Preferences(ctx, 111)
	if err != nil || len(prefs) != 1 || prefs[0].CurrentValue != true {
		t.Errorf("source 111 preferences after reconcile = %+v (err=%v), want nsfw=true", prefs, err)
	}
	// ONE batched call carried the single changed key (proves the batching
	// design — never one call per key).
	if got := client.CallCount("SetPreferences"); got != 1 {
		t.Errorf("SetPreferences calls = %d, want 1 (one batched call per source)", got)
	}
	assertFreshConfigPushed(t, client)
}

// assertFreshConfigPushed checks the unconditional config push landed the
// ConfigProvider's values on the engine.
func assertFreshConfigPushed(t *testing.T, client *reconcileClient) {
	t.Helper()
	if client.lastFlareSolverr.URL != "http://flare.test:8191" {
		t.Errorf("pushed FlareSolverr URL = %q, want the cfg url", client.lastFlareSolverr.URL)
	}
	if client.lastSocks.Port != "1080" {
		t.Errorf("pushed SOCKS port = %q, want \"1080\" (numeric-string wire form)", client.lastSocks.Port)
	}
}

// TestReconcile_InSyncEngineMakesZeroMutations proves idempotency on the
// DRIFT-DETECTED axes: when the engine already matches the durable store,
// Reconcile issues no repo/extension/preference mutation and reports
// InSync=true — even though config is STILL pushed unconditionally every pass
// (ConfigApplied=true, SetFlareSolverr/SetSocks each called once), because
// config performs no drift detection by design (see reconcileConfig's doc
// comment) and is deliberately excluded from InSync.
func TestReconcile_InSyncEngineMakesZeroMutations(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	seedProvider(ctx, t, db, "Solo Leveling", "111", 111)
	seedHarvestedExtension(ctx, t, db, "pkg.one", []int64{111})
	db.HarvestedRepo.Create().SetURL("https://repo.test/repo").SaveX(ctx)
	seedStoredPref(ctx, t, db, 111, "nsfw", "true", sourceengine.PreferenceCheckBox)

	cfg := baseConfig()

	client := &reconcileClient{
		Client: sourceenginefake.New(
			sourceenginefake.WithRepos([]string{"https://repo.test/repo"}), // matches DB
			sourceenginefake.WithExtensions([]sourceengine.Extension{{PkgName: "pkg.one", IsInstalled: true}}),
			sourceenginefake.WithPreferences(111, []sourceengine.Preference{
				// Live value equals stored ("true") → no write.
				{Type: sourceengine.PreferenceCheckBox, Key: "nsfw", CurrentValue: true},
			}),
		),
	}

	res, err := enginetopo.Reconcile(ctx, client, db, cache, cfg)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !res.InSync {
		t.Errorf("InSync = false, want true (Result=%+v)", res)
	}
	if !res.ConfigApplied {
		t.Error("ConfigApplied = false, want true (config always pushes)")
	}
	if client.CallCount("SetRepos") != 0 {
		t.Errorf("SetRepos called %d times, want 0", client.CallCount("SetRepos"))
	}
	if client.CallCount("RefreshExtensions") != 0 {
		t.Errorf("RefreshExtensions called %d times, want 0", client.CallCount("RefreshExtensions"))
	}
	if client.CallCount("InstallExtension") != 0 {
		t.Errorf("InstallExtension called %d times, want 0", client.CallCount("InstallExtension"))
	}
	if client.CallCount("SetPreferences") != 0 {
		t.Errorf("SetPreferences called %d times, want 0", client.CallCount("SetPreferences"))
	}
	if client.CallCount("SetFlareSolverr") != 1 || client.CallCount("SetSocks") != 1 {
		t.Errorf("config push calls = flare:%d socks:%d, want 1/1 (unconditional every pass)",
			client.CallCount("SetFlareSolverr"), client.CallCount("SetSocks"))
	}
}

// TestReconcile_ConfigAlwaysPushesEvenWhenNothingElseDrifted is the direct
// unconditional-push proof: with an empty durable store (nothing to reconcile
// on the other three axes) Reconcile STILL issues exactly one SetFlareSolverr +
// one SetSocks call carrying the ConfigProvider's current values — there is no
// engine-side comparison that could ever suppress it (sourceengine.Client has
// no matching GET).
func TestReconcile_ConfigAlwaysPushesEvenWhenNothingElseDrifted(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	cfg := baseConfig()
	client := &reconcileClient{Client: sourceenginefake.New()}

	res, err := enginetopo.Reconcile(ctx, client, db, cache, cfg)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !res.ConfigApplied {
		t.Error("ConfigApplied = false, want true")
	}
	if client.CallCount("SetFlareSolverr") != 1 || client.CallCount("SetSocks") != 1 {
		t.Fatalf("config push calls = flare:%d socks:%d, want 1/1",
			client.CallCount("SetFlareSolverr"), client.CallCount("SetSocks"))
	}
	if client.lastFlareSolverr.URL != cfg.fsURL {
		t.Errorf("pushed FlareSolverr URL = %q, want %q", client.lastFlareSolverr.URL, cfg.fsURL)
	}
	if !client.lastSocks.Enabled || client.lastSocks.Host != cfg.socksHost {
		t.Errorf("pushed SOCKS = %+v, want enabled with host %q", client.lastSocks, cfg.socksHost)
	}
}

// TestReconcile_PkgInstallErrorIsolated proves fault isolation: when ONE required
// extension fails to install, the other required extension, the drifted
// preference, and the config are all still applied, and the failure is recorded
// in Gaps rather than aborting the pass.
func TestReconcile_PkgInstallErrorIsolated(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	seedProvider(ctx, t, db, "Solo Leveling", "111", 111)
	seedProvider(ctx, t, db, "Omniscient Reader", "222", 222)
	seedHarvestedExtension(ctx, t, db, "pkg.one", []int64{111})
	seedHarvestedExtension(ctx, t, db, "pkg.two", []int64{222})
	db.HarvestedRepo.Create().SetURL("https://repo.test/repo").SaveX(ctx)
	seedStoredPref(ctx, t, db, 111, "nsfw", "true", sourceengine.PreferenceCheckBox)

	cfg := baseConfig()

	client := &reconcileClient{
		Client: sourceenginefake.New(
			sourceenginefake.WithRepos([]string{"https://repo.test/repo"}), // repos match; only extensions missing
			sourceenginefake.WithPreferences(111, []sourceengine.Preference{
				{Type: sourceengine.PreferenceCheckBox, Key: "nsfw", CurrentValue: false},
			}),
		),
		installErr: map[string]error{"pkg.one": errors.New("repo unreachable")},
	}

	res, err := enginetopo.Reconcile(ctx, client, db, cache, cfg)
	if err != nil {
		t.Fatalf("Reconcile returned hard error, want per-item isolation: %v", err)
	}
	if res.ExtensionsInstalled != 1 {
		t.Errorf("ExtensionsInstalled = %d, want 1 (pkg.two)", res.ExtensionsInstalled)
	}
	if len(res.Gaps) != 1 || !strings.Contains(res.Gaps[0].Error(), "pkg.one") {
		t.Fatalf("Gaps = %v, want exactly 1 naming pkg.one", res.Gaps)
	}
	if res.PrefsApplied != 1 {
		t.Errorf("PrefsApplied = %d, want 1 (pref applied despite the install failure)", res.PrefsApplied)
	}
	if !res.ConfigApplied {
		t.Error("ConfigApplied = false, want true (config applied despite the install failure)")
	}
	if res.InSync {
		t.Error("InSync = true, want false (a gap was recorded)")
	}
}

// TestReconcile_PrefDriftAllKinds proves the impedance-mismatch step across
// every value kind: EVERY drifted key for source 111 is pushed in ONE batched
// SetPreferences call (proving the batching design), while a stored preference
// EQUAL to its live value is left out of that batch.
func TestReconcile_PrefDriftAllKinds(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	// No harvested extensions + no repos → the extension/repo steps are no-ops, so
	// ONLY the preference step can produce writes.
	seedStoredPref(ctx, t, db, 111, "nsfw", "true", sourceengine.PreferenceCheckBox)
	seedStoredPref(ctx, t, db, 111, "quality", "high", sourceengine.PreferenceList)
	seedStoredPref(ctx, t, db, 111, "blocked", `["A","B"]`, sourceengine.PreferenceMultiSelect)
	seedStoredPref(ctx, t, db, 111, "https", "true", sourceengine.PreferenceSwitchCompat)

	cfg := baseConfig()

	client := &reconcileClient{
		Client: sourceenginefake.New(sourceenginefake.WithPreferences(111, []sourceengine.Preference{
			{Type: sourceengine.PreferenceCheckBox, Key: "nsfw", CurrentValue: false},               // differs → push bool
			{Type: sourceengine.PreferenceList, Key: "quality", CurrentValue: "low"},                // differs → push string
			{Type: sourceengine.PreferenceMultiSelect, Key: "blocked", CurrentValue: []string{"A"}}, // differs → push list
			{Type: sourceengine.PreferenceSwitchCompat, Key: "https", CurrentValue: true},           // EQUAL → no write
		})),
	}

	res, err := enginetopo.Reconcile(ctx, client, db, cache, cfg)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.PrefsApplied != 3 {
		t.Errorf("PrefsApplied = %d, want 3", res.PrefsApplied)
	}
	if got := client.CallCount("SetPreferences"); got != 1 {
		t.Fatalf("SetPreferences called %d times, want 1 (ONE batched call for all 3 drifted keys)", got)
	}

	prefs, err := client.Preferences(ctx, 111)
	if err != nil {
		t.Fatalf("Preferences: %v", err)
	}
	assertPrefDriftAllKinds(t, prefs)
}

// assertPrefDriftAllKinds checks every variant landed its pushed/unchanged
// value after TestReconcile_PrefDriftAllKinds's reconcile pass. Split out
// purely to keep the test body's cyclomatic complexity within the project's
// cyclop gate.
func assertPrefDriftAllKinds(t *testing.T, prefs []sourceengine.Preference) {
	t.Helper()
	byKey := make(map[string]sourceengine.Preference, len(prefs))
	for _, p := range prefs {
		byKey[p.Key] = p
	}
	assertBoolPref(t, byKey, "nsfw", true)
	assertStringPref(t, byKey, "quality", "high")
	assertListPref(t, byKey, "blocked", []string{"A", "B"})
	assertBoolPref(t, byKey, "https", true) // unchanged (was already in sync)
}

// assertBoolPref fails unless byKey[key].CurrentValue is the bool want.
func assertBoolPref(t *testing.T, byKey map[string]sourceengine.Preference, key string, want bool) {
	t.Helper()
	if v, ok := byKey[key].CurrentValue.(bool); !ok || v != want {
		t.Errorf("%s = %v, want %v", key, byKey[key].CurrentValue, want)
	}
}

// assertStringPref fails unless byKey[key].CurrentValue is the string want.
func assertStringPref(t *testing.T, byKey map[string]sourceengine.Preference, key, want string) {
	t.Helper()
	if v, ok := byKey[key].CurrentValue.(string); !ok || v != want {
		t.Errorf("%s = %v, want %q", key, byKey[key].CurrentValue, want)
	}
}

// assertListPref fails unless byKey[key].CurrentValue is the []string want.
func assertListPref(t *testing.T, byKey map[string]sourceengine.Preference, key string, want []string) {
	t.Helper()
	v, ok := byKey[key].CurrentValue.([]string)
	if !ok || !slices.Equal(v, want) {
		t.Errorf("%s = %v, want %v", key, byKey[key].CurrentValue, want)
	}
}

// TestReconcile_EnumerateExtensionsErrorIsHard proves an ENUMERATING failure
// (listing the engine's installed extensions) aborts the pass with an error
// rather than silently degrading — the distinction from a per-item failure.
func TestReconcile_EnumerateExtensionsErrorIsHard(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	seedProvider(ctx, t, db, "Solo Leveling", "111", 111)
	seedHarvestedExtension(ctx, t, db, "pkg.one", []int64{111})

	cfg := baseConfig()

	client := sourceenginefake.New(sourceenginefake.WithError("Extensions", errors.New("engine down")))

	if _, err := enginetopo.Reconcile(ctx, client, db, cache, cfg); err == nil {
		t.Fatal("Reconcile: want hard error when listing extensions fails, got nil")
	}
}

// TestReconcile_WipedEnginePrefRepush proves THE real-recovery path: a stored
// preference whose live pref exists (same key + a real Type) but has NO current
// value set (a wiped engine, CurrentValue nil) is (re)pushed.
// encodePreferenceValue returns ok=false for a nil current value, so prefInSync
// is false and the stored value repopulates the engine.
func TestReconcile_WipedEnginePrefRepush(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	seedStoredPref(ctx, t, db, 111, "https", "true", sourceengine.PreferenceSwitchCompat)

	cfg := baseConfig()

	// Live pref present but with CurrentValue nil — the engine has the option but
	// no value, exactly what a wipe/rebuild leaves behind.
	client := &reconcileClient{
		Client: sourceenginefake.New(sourceenginefake.WithPreferences(111, []sourceengine.Preference{
			{Type: sourceengine.PreferenceSwitchCompat, Key: "https", CurrentValue: nil},
		})),
	}

	res, err := enginetopo.Reconcile(ctx, client, db, cache, cfg)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.PrefsApplied != 1 {
		t.Errorf("PrefsApplied = %d, want 1", res.PrefsApplied)
	}
	prefs, err := client.Preferences(ctx, 111)
	if err != nil || len(prefs) != 1 {
		t.Fatalf("Preferences after reconcile: %+v, %v", prefs, err)
	}
	if v, ok := prefs[0].CurrentValue.(bool); !ok || !v {
		t.Errorf("https = %v, want true (wiped pref repushed)", prefs[0].CurrentValue)
	}
}

// TestReconcile_RepoWriteErrorIsolated proves a SetRepos write failure is
// isolated as a gap, not a hard error: the pass records the failure, leaves
// ReposSet=false, and still applies the drifted preference and config.
func TestReconcile_RepoWriteErrorIsolated(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	db.HarvestedRepo.Create().SetURL("https://repo.test/repo").SaveX(ctx)
	seedStoredPref(ctx, t, db, 111, "nsfw", "true", sourceengine.PreferenceCheckBox)

	cfg := baseConfig()

	client := &reconcileClient{
		Client: sourceenginefake.New(
			sourceenginefake.WithRepos([]string{"https://stale.test/repo"}), // differs from DB → drift → write attempted
			sourceenginefake.WithPreferences(111, []sourceengine.Preference{
				{Type: sourceengine.PreferenceCheckBox, Key: "nsfw", CurrentValue: false},
			}),
			sourceenginefake.WithError("SetRepos", errors.New("repo store rejected")),
		),
	}

	res, err := enginetopo.Reconcile(ctx, client, db, cache, cfg)
	if err != nil {
		t.Fatalf("Reconcile returned hard error, want repo write isolated as a gap: %v", err)
	}
	if res.ReposSet {
		t.Error("ReposSet = true, want false (the repo write failed)")
	}
	if len(res.Gaps) != 1 {
		t.Fatalf("Gaps = %v, want exactly 1 (the failed repo write)", res.Gaps)
	}
	if res.PrefsApplied != 1 {
		t.Errorf("PrefsApplied = %d, want 1 (pref applied despite the repo failure)", res.PrefsApplied)
	}
	if !res.ConfigApplied {
		t.Error("ConfigApplied = false, want true (config applied despite the repo failure)")
	}
}

// TestReconcile_RepoUnionKeepsEngineExtraAndAddsDBExtra proves reconcileRepos
// is ADDITIVE-ONLY: when the engine already has a repo the DB has never
// captured AND the DB knows a repo the engine lacks, Reconcile pushes the
// UNION — the engine's un-captured repo survives (never dropped) and the DB's
// repo is added. This is the exact regression the fix closes: before it,
// SetRepos(dbRepos) would have REPLACED the engine's list, silently wiping the
// engine-only repo.
func TestReconcile_RepoUnionKeepsEngineExtraAndAddsDBExtra(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	db.HarvestedRepo.Create().SetURL("https://db-only.test/repo").SaveX(ctx)

	cfg := baseConfig()
	client := &reconcileClient{
		Client: sourceenginefake.New(
			sourceenginefake.WithRepos([]string{"https://engine-only.test/repo"}),
		),
	}

	res, err := enginetopo.Reconcile(ctx, client, db, cache, cfg)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !res.ReposSet {
		t.Error("ReposSet = false, want true (the union added a repo the engine lacked)")
	}
	if client.CallCount("SetRepos") != 1 {
		t.Fatalf("SetRepos calls = %d, want 1", client.CallCount("SetRepos"))
	}

	gotRepos, err := client.Repos(ctx)
	if err != nil {
		t.Fatalf("Repos: %v", err)
	}
	want := []string{"https://db-only.test/repo", "https://engine-only.test/repo"}
	got := append([]string(nil), gotRepos...)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("engine repos after reconcile = %v, want %v (union — engine's extra kept, DB's extra added)", got, want)
	}
}

// TestReconcile_RepoSubsetOfEngineMakesZeroCalls proves reconcileRepos never
// regresses the engine when the DB's known repo set is a STRICT SUBSET of
// what the engine already has — e.g. a DB restored from an old backup, or a
// fresh Tsundoku pointed at an already-configured engine. The union equals
// the engine's own list, so no SetRepos call is issued at all and every one
// of the engine's repos (including the one the DB never captured) survives.
func TestReconcile_RepoSubsetOfEngineMakesZeroCalls(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	db.HarvestedRepo.Create().SetURL("https://repo.test/repo").SaveX(ctx)

	cfg := baseConfig()
	client := &reconcileClient{
		Client: sourceenginefake.New(
			// engineRepos is a strict SUPERSET of dbRepos — the DB hasn't
			// captured "https://extra.test/repo" yet.
			sourceenginefake.WithRepos([]string{"https://repo.test/repo", "https://extra.test/repo"}),
		),
	}

	res, err := enginetopo.Reconcile(ctx, client, db, cache, cfg)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.ReposSet {
		t.Error("ReposSet = true, want false (DB is a subset of the engine's repos — already in sync)")
	}
	if client.CallCount("SetRepos") != 0 {
		t.Errorf("SetRepos called %d times, want 0 (must never regress the engine's un-captured repo)", client.CallCount("SetRepos"))
	}

	gotRepos, err := client.Repos(ctx)
	if err != nil {
		t.Fatalf("Repos: %v", err)
	}
	want := []string{"https://extra.test/repo", "https://repo.test/repo"}
	got := append([]string(nil), gotRepos...)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("engine repos after reconcile = %v, want %v (unchanged — the engine's extra repo must survive)", got, want)
	}
}

// TestReconcile_RepoEqualSetsMakeZeroCalls proves the ordinary in-sync case:
// when the DB's repo set and the engine's live repo set are already equal
// (order-insensitive), Reconcile issues zero SetRepos calls.
func TestReconcile_RepoEqualSetsMakeZeroCalls(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	db.HarvestedRepo.Create().SetURL("https://repo.test/repo").SaveX(ctx)
	db.HarvestedRepo.Create().SetURL("https://repo.test/other").SaveX(ctx)

	cfg := baseConfig()
	client := &reconcileClient{
		Client: sourceenginefake.New(
			// Same set, deliberately different ORDER — proves the comparison
			// is order-insensitive, not just a coincidental match.
			sourceenginefake.WithRepos([]string{"https://repo.test/other", "https://repo.test/repo"}),
		),
	}

	res, err := enginetopo.Reconcile(ctx, client, db, cache, cfg)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.ReposSet {
		t.Error("ReposSet = true, want false (equal sets — already in sync)")
	}
	if client.CallCount("SetRepos") != 0 {
		t.Errorf("SetRepos called %d times, want 0", client.CallCount("SetRepos"))
	}
}

// TestReconcile_ConfigPushErrorIsolated proves a config push failure (here
// SetFlareSolverr) is isolated as a gap — NOT a hard error — because config
// recovery is independent of the extension + preference recovery.
func TestReconcile_ConfigPushErrorIsolated(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	cfg := baseConfig()

	client := &reconcileClient{
		Client: sourceenginefake.New(sourceenginefake.WithError("SetFlareSolverr", errors.New("engine unreachable"))),
	}

	res, err := enginetopo.Reconcile(ctx, client, db, cache, cfg)
	if err != nil {
		t.Fatalf("Reconcile returned hard error, want config push isolated as a gap: %v", err)
	}
	if res.ConfigApplied {
		t.Error("ConfigApplied = true, want false (FlareSolverr push failed)")
	}
	if len(res.Gaps) != 1 {
		t.Fatalf("Gaps = %v, want exactly 1 (the failed flaresolverr push)", res.Gaps)
	}
	// SetSocks is independent and must still have been attempted.
	if client.CallCount("SetSocks") != 1 {
		t.Errorf("SetSocks calls = %d, want 1 (attempted despite the FlareSolverr failure)", client.CallCount("SetSocks"))
	}
}

// TestReconcile_BuildPrefValueParseFailureIsolated proves a stored preference
// whose value cannot be coerced for the live pref's kind (here a Switch stored
// as the non-bool "notabool") is isolated as a gap: no hard error, and the
// whole source's batch is skipped (nothing left to push once its only drifted
// key fails to coerce).
func TestReconcile_BuildPrefValueParseFailureIsolated(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	seedStoredPref(ctx, t, db, 111, "https", "notabool", sourceengine.PreferenceSwitchCompat)

	cfg := baseConfig()

	// Live value (false) differs from the unparseable stored value → a coercion is
	// attempted, and strconv.ParseBool fails.
	client := &reconcileClient{
		Client: sourceenginefake.New(sourceenginefake.WithPreferences(111, []sourceengine.Preference{
			{Type: sourceengine.PreferenceSwitchCompat, Key: "https", CurrentValue: false},
		})),
	}

	res, err := enginetopo.Reconcile(ctx, client, db, cache, cfg)
	if err != nil {
		t.Fatalf("Reconcile returned hard error, want coercion failure isolated as a gap: %v", err)
	}
	if len(res.Gaps) != 1 {
		t.Fatalf("Gaps = %v, want exactly 1 (the unparseable stored pref)", res.Gaps)
	}
	if client.CallCount("SetPreferences") != 0 {
		t.Errorf("SetPreferences called %d times, want 0 (coercion failed before any write)", client.CallCount("SetPreferences"))
	}
	if res.PrefsApplied != 0 {
		t.Errorf("PrefsApplied = %d, want 0", res.PrefsApplied)
	}
}

// TestReconcile_StoredKeyWithNoLivePrefSkipped proves a stored preference whose
// key is absent from the live pref list (an extension update removed the option)
// is silently skipped — no write, no gap, no error.
func TestReconcile_StoredKeyWithNoLivePrefSkipped(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	seedStoredPref(ctx, t, db, 111, "ghost", "true", sourceengine.PreferenceSwitchCompat)

	cfg := baseConfig()

	// The live source carries a DIFFERENT key, so "ghost" has no live match.
	client := &reconcileClient{
		Client: sourceenginefake.New(sourceenginefake.WithPreferences(111, []sourceengine.Preference{
			{Type: sourceengine.PreferenceSwitchCompat, Key: "other", CurrentValue: true},
		})),
	}

	res, err := enginetopo.Reconcile(ctx, client, db, cache, cfg)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if client.CallCount("SetPreferences") != 0 {
		t.Errorf("SetPreferences called %d times, want 0 (removed option skipped)", client.CallCount("SetPreferences"))
	}
	if len(res.Gaps) != 0 {
		t.Errorf("Gaps = %v, want none (a removed option is silent, not a gap)", res.Gaps)
	}
	if !res.InSync {
		t.Errorf("InSync = false, want true (nothing needed changing on the drift-detected axes); Result=%+v", res)
	}
}

// TestReconcile_MultiSourcePrefFailureIsolatedPerSource proves the batching
// design's fault-isolation granularity: TWO sources each have one drifted
// preference; source 222's SetPreferences call fails while source 111's still
// succeeds — a batch failure on one source must never block another source's
// batch.
func TestReconcile_MultiSourcePrefFailureIsolatedPerSource(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	seedStoredPref(ctx, t, db, 111, "nsfw", "true", sourceengine.PreferenceCheckBox)
	seedStoredPref(ctx, t, db, 222, "nsfw", "true", sourceengine.PreferenceCheckBox)

	cfg := baseConfig()

	client := &reconcileClient{
		Client: sourceenginefake.New(
			sourceenginefake.WithPreferences(111, []sourceengine.Preference{
				{Type: sourceengine.PreferenceCheckBox, Key: "nsfw", CurrentValue: false},
			}),
			sourceenginefake.WithPreferences(222, []sourceengine.Preference{
				{Type: sourceengine.PreferenceCheckBox, Key: "nsfw", CurrentValue: false},
			}),
		),
		setPrefsErr: map[int64]error{222: errors.New("source 222 rejected the write")},
	}

	res, err := enginetopo.Reconcile(ctx, client, db, cache, cfg)
	if err != nil {
		t.Fatalf("Reconcile returned hard error, want per-source isolation: %v", err)
	}
	if res.PrefsApplied != 1 {
		t.Errorf("PrefsApplied = %d, want 1 (only source 111's key)", res.PrefsApplied)
	}
	if len(res.Gaps) != 1 || !strings.Contains(res.Gaps[0].Error(), "222") {
		t.Fatalf("Gaps = %v, want exactly 1 naming source 222", res.Gaps)
	}

	assertSourcePref(ctx, t, client, 111, true, "its push succeeded")
	assertSourcePref(ctx, t, client, 222, false, "its push failed")
}

// assertSourcePref checks sourceID's single "nsfw" preference reads back as
// want after TestReconcile_MultiSourcePrefFailureIsolatedPerSource's reconcile
// pass. Split out purely to keep the test body's cyclomatic complexity within
// the project's cyclop gate.
func assertSourcePref(ctx context.Context, t *testing.T, client *reconcileClient, sourceID int64, want bool, reason string) {
	t.Helper()
	prefs, err := client.Preferences(ctx, sourceID)
	if err != nil || len(prefs) != 1 || prefs[0].CurrentValue != want {
		t.Errorf("source %d after reconcile = %+v (err=%v), want nsfw=%v (%s)", sourceID, prefs, err, want, reason)
	}
}

// TestReconcile_MixedBatchKeyRejectionIsolatedWithoutLosingSiblingIntent is
// the carried-forward hardening item from the opus review of slice 5, now
// that Reconcile runs live every boot: ONE key ("quality") in source 111's
// drifted-preference batch is rejected by the engine while the SIBLING key
// ("nsfw") in the SAME batch is valid. The batching design (see
// reconcilePrefs's doc comment) means Reconcile itself sees only ONE
// call-level error for the whole batch — it cannot tell whether the engine
// partially applied "nsfw" before reporting the failure. This test proves the
// documented ACCEPTED TRADE-OFF is actually safe, across TWO passes:
//  1. Pass 1: both keys drift; ONE batched SetPreferences call is attempted;
//     the engine (this reconcileClient, configured as a partial-apply engine
//     via rejectKeys) accepts "nsfw" and rejects "quality" — the whole call
//     still reports one gap, but "nsfw"'s value IS on the engine afterwards
//     (not silently dropped).
//  2. Pass 2 (the next boot): "nsfw" is now in sync and is excluded from the
//     batch entirely (never re-attempted — its intent was not lost); only the
//     still-drifted "quality" is retried, and — its underlying issue
//     unchanged — gaps again. Nothing vanishes across passes; a fixed-later
//     "quality" would converge exactly the same way "nsfw" did.
func TestReconcile_MixedBatchKeyRejectionIsolatedWithoutLosingSiblingIntent(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	seedStoredPref(ctx, t, db, 111, "nsfw", "true", sourceengine.PreferenceCheckBox) // valid, engine accepts
	seedStoredPref(ctx, t, db, 111, "quality", "high", sourceengine.PreferenceList)  // engine rejects every pass

	cfg := baseConfig()

	client := &reconcileClient{
		Client: sourceenginefake.New(sourceenginefake.WithPreferences(111, []sourceengine.Preference{
			{Type: sourceengine.PreferenceCheckBox, Key: "nsfw", CurrentValue: false},
			{Type: sourceengine.PreferenceList, Key: "quality", CurrentValue: "low"},
		})),
		rejectKeys: map[int64]map[string]bool{111: {"quality": true}},
	}

	// Pass 1: both keys drift together in one batch; the engine accepts
	// "nsfw" and rejects "quality".
	res1 := reconcileMustSucceed(ctx, t, client, db, cache, cfg)
	assertMixedBatchPass(t, client, res1, []string{"nsfw", "quality"}, "pass 1")
	assertMixedBatchPrefs(ctx, t, client, true, "low", "pass 1: nsfw accepted, quality rejected+unchanged")

	// Pass 2 (the next boot): "nsfw" is already in sync and is excluded from
	// the batch entirely — its intent was not lost — while "quality" is
	// retried alone and gaps again (its underlying issue is unchanged).
	res2 := reconcileMustSucceed(ctx, t, client, db, cache, cfg)
	assertMixedBatchPass(t, client, res2, []string{"quality"}, "pass 2")
	assertMixedBatchPrefs(ctx, t, client, true, "low", "pass 2: nsfw still applied, quality still rejected+unchanged")
}

// reconcileMustSucceed runs one Reconcile pass and fails the test if it
// returns a hard error — every pass in
// TestReconcile_MixedBatchKeyRejectionIsolatedWithoutLosingSiblingIntent
// expects the rejection isolated as a gap, never a hard error. Split out
// purely to keep the test bodies' cyclomatic complexity within the project's
// cyclop gate.
func reconcileMustSucceed(ctx context.Context, t *testing.T, client sourceengine.Client, db *ent.Client, cache *apkcache.Store, cfg enginetopo.ConfigProvider) enginetopo.ReconcileResult {
	t.Helper()
	res, err := enginetopo.Reconcile(ctx, client, db, cache, cfg)
	if err != nil {
		t.Fatalf("Reconcile returned hard error, want the rejection isolated as a gap: %v", err)
	}
	return res
}

// assertMixedBatchPass checks one pass's ReconcileResult + the exact
// SetPreferences batch keys attempted, for
// TestReconcile_MixedBatchKeyRejectionIsolatedWithoutLosingSiblingIntent.
// Split out purely to keep the test bodies' cyclomatic complexity within the
// project's cyclop gate.
func assertMixedBatchPass(t *testing.T, client *reconcileClient, res enginetopo.ReconcileResult, wantBatchKeys []string, label string) {
	t.Helper()
	if len(res.Gaps) != 1 || !strings.Contains(res.Gaps[0].Error(), "111") {
		t.Fatalf("%s Gaps = %v, want exactly 1 naming source 111", label, res.Gaps)
	}
	if res.PrefsApplied != 0 {
		t.Errorf("%s PrefsApplied = %d, want 0 (the whole batch call reported an error)", label, res.PrefsApplied)
	}
	if got := client.lastBatchKeys[111]; !slices.Equal(got, wantBatchKeys) {
		t.Errorf("%s batch keys = %v, want %v", label, got, wantBatchKeys)
	}
}

// assertMixedBatchPrefs checks source 111's "nsfw" and "quality" preference
// values after a TestReconcile_MixedBatchKeyRejectionIsolatedWithoutLosingSiblingIntent
// pass. Split out purely to keep the test bodies' cyclomatic complexity
// within the project's cyclop gate.
func assertMixedBatchPrefs(ctx context.Context, t *testing.T, client *reconcileClient, wantNsfw bool, wantQuality, reason string) {
	t.Helper()
	prefs, err := client.Preferences(ctx, 111)
	if err != nil {
		t.Fatalf("Preferences: %v", err)
	}
	byKey := make(map[string]sourceengine.Preference, len(prefs))
	for _, p := range prefs {
		byKey[p.Key] = p
	}
	if v, ok := byKey["nsfw"].CurrentValue.(bool); !ok || v != wantNsfw {
		t.Errorf("%s: nsfw = %v, want %v", reason, byKey["nsfw"].CurrentValue, wantNsfw)
	}
	if v, ok := byKey["quality"].CurrentValue.(string); !ok || v != wantQuality {
		t.Errorf("%s: quality = %v, want %q", reason, byKey["quality"].CurrentValue, wantQuality)
	}
}

// TestReconcile_MultiSelectOrderInsensitiveInSync proves the multiselect
// set-order canonicalization hardening (carried forward from the opus review
// of slice 5): a stored MultiSelect value and the engine's live value hold
// the SAME SET of strings in a DIFFERENT order (the engine's backing
// Set<String> has no stable iteration order) — this must be recognized as
// in-sync, not re-pushed every single reconcile pass.
func TestReconcile_MultiSelectOrderInsensitiveInSync(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	seedStoredPref(ctx, t, db, 111, "blocked", `["A","B","C"]`, sourceengine.PreferenceMultiSelect)

	cfg := baseConfig()
	client := &reconcileClient{
		Client: sourceenginefake.New(sourceenginefake.WithPreferences(111, []sourceengine.Preference{
			// Same set as stored, reordered.
			{Type: sourceengine.PreferenceMultiSelect, Key: "blocked", CurrentValue: []string{"C", "A", "B"}},
		})),
	}

	res, err := enginetopo.Reconcile(ctx, client, db, cache, cfg)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.PrefsApplied != 0 {
		t.Errorf("PrefsApplied = %d, want 0 (same set, different order = in sync)", res.PrefsApplied)
	}
	if client.CallCount("SetPreferences") != 0 {
		t.Errorf("SetPreferences called %d times, want 0", client.CallCount("SetPreferences"))
	}
	if !res.InSync {
		t.Errorf("InSync = false, want true; Result=%+v", res)
	}
}

// TestReconcile_MultiSelectDifferentSetStillDrifts proves the
// canonicalization is a SET compare, not a no-op — a genuinely different set
// (not just reordered) still drifts and is pushed.
func TestReconcile_MultiSelectDifferentSetStillDrifts(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	seedStoredPref(ctx, t, db, 111, "blocked", `["A","B"]`, sourceengine.PreferenceMultiSelect)

	cfg := baseConfig()
	client := &reconcileClient{
		Client: sourceenginefake.New(sourceenginefake.WithPreferences(111, []sourceengine.Preference{
			{Type: sourceengine.PreferenceMultiSelect, Key: "blocked", CurrentValue: []string{"A", "C"}},
		})),
	}

	res, err := enginetopo.Reconcile(ctx, client, db, cache, cfg)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.PrefsApplied != 1 {
		t.Errorf("PrefsApplied = %d, want 1 (a genuinely different set must still drift)", res.PrefsApplied)
	}
	if client.CallCount("SetPreferences") != 1 {
		t.Errorf("SetPreferences called %d times, want 1", client.CallCount("SetPreferences"))
	}
}
