package enginetopo_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/enginetopo"
	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// --- reconcile test fake -----------------------------------------------------

// setPrefCall records one SetSourcePreference invocation so a test can assert
// the reconcile pushed the right value at the right position for the right
// source. value is compared via reflect.DeepEqual against the same
// exported-constructor output the reconcile builds.
type setPrefCall struct {
	sourceID string
	position int
	value    suwayomi.PreferenceValue
}

// reconcileClient embeds the shared fakeClient (a full suwayomi.Client stub) and
// overrides only the methods Reconcile drives, recording every ENGINE MUTATION
// so a test can assert call counts (0 on an in-sync engine). Reads
// (SourcePreferences) reuse the base fakeClient's prefsBySource machinery.
type reconcileClient struct {
	*fakeClient

	engineRepos  []string
	reposErr     error
	exts         []suwayomi.Extension
	extsErr      error
	liveSettings suwayomi.SuwayomiSettings
	settingsErr  error
	installErr   map[string]error // per-pkg SetExtensionState failure
	setReposErr  error            // SetExtensionRepos write failure (else nil)

	rmu           sync.Mutex
	setReposCalls int
	lastSetRepos  []string
	fetchCalls    int
	installed     []string
	setPrefs      []setPrefCall
	setSettings   []suwayomi.SuwayomiSettingsPatch
}

func (c *reconcileClient) ExtensionRepos(context.Context) ([]string, error) {
	return c.engineRepos, c.reposErr
}

func (c *reconcileClient) Extensions(context.Context) ([]suwayomi.Extension, error) {
	return c.exts, c.extsErr
}

func (c *reconcileClient) FetchExtensions(context.Context) ([]suwayomi.Extension, error) {
	c.rmu.Lock()
	c.fetchCalls++
	c.rmu.Unlock()
	return c.exts, nil
}

func (c *reconcileClient) SetExtensionRepos(_ context.Context, repos []string) error {
	// Fail (without recording) when a test injects a write error — mirrors the
	// return-before-record style SetExtensionState uses for installErr.
	if c.setReposErr != nil {
		return c.setReposErr
	}
	c.rmu.Lock()
	c.setReposCalls++
	c.lastSetRepos = repos
	c.rmu.Unlock()
	return nil
}

func (c *reconcileClient) SetExtensionState(_ context.Context, pkgName string, _ suwayomi.ExtensionAction) error {
	if err, ok := c.installErr[pkgName]; ok {
		return err
	}
	c.rmu.Lock()
	c.installed = append(c.installed, pkgName)
	c.rmu.Unlock()
	return nil
}

func (c *reconcileClient) SetSourcePreference(_ context.Context, sourceID string, position int, value suwayomi.PreferenceValue) ([]suwayomi.SourcePreference, error) {
	c.rmu.Lock()
	c.setPrefs = append(c.setPrefs, setPrefCall{sourceID: sourceID, position: position, value: value})
	c.rmu.Unlock()
	return nil, nil
}

func (c *reconcileClient) ServerSettings(context.Context) (suwayomi.SuwayomiSettings, error) {
	if c.settingsErr != nil {
		return suwayomi.SuwayomiSettings{}, c.settingsErr
	}
	return c.liveSettings, nil
}

func (c *reconcileClient) SetServerSettings(_ context.Context, patch suwayomi.SuwayomiSettingsPatch) error {
	c.rmu.Lock()
	c.setSettings = append(c.setSettings, patch)
	c.rmu.Unlock()
	return nil
}

func (c *reconcileClient) prefsByPosition() map[int]setPrefCall {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	m := make(map[int]setPrefCall, len(c.setPrefs))
	for _, p := range c.setPrefs {
		m[p.position] = p
	}
	return m
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

// matchedFlareConfig returns a fakeConfig and a SuwayomiSettings whose
// FlareSolverr fields are IDENTICAL (SOCKS disabled), so configInSync is true —
// the shared "config already matches" fixture.
func matchedFlareConfig() (fakeConfig, suwayomi.SuwayomiSettings) {
	cfg := fakeConfig{
		fsEnabled:     true,
		fsURL:         "http://flare.test:8191",
		fsTimeout:     60,
		fsSessionName: "sess",
		fsSessionTTL:  15,
		fsFallback:    false,
		socksEnabled:  false,
	}
	live := suwayomi.SuwayomiSettings{
		FlareSolverrEnabled:            true,
		FlareSolverrURL:                "http://flare.test:8191",
		FlareSolverrTimeout:            60,
		FlareSolverrSessionName:        "sess",
		FlareSolverrSessionTTL:         15,
		FlareSolverrAsResponseFallback: false,
	}
	return cfg, live
}

// matchedSocksConfig extends matchedFlareConfig with an ENABLED SOCKS proxy
// whose host/port/version also match the live settings, so configInSync
// exercises socksInSync's already-matching (no-push) branch.
func matchedSocksConfig() (fakeConfig, suwayomi.SuwayomiSettings) {
	cfg, live := matchedFlareConfig()
	cfg.socksEnabled = true
	cfg.socksHost = "127.0.0.1"
	cfg.socksPort = 1080
	cfg.socksVersion = 5
	live.SocksProxyEnabled = true
	live.SocksProxyHost = "127.0.0.1"
	live.SocksProxyPort = "1080" // Suwayomi's wire form is a numeric string.
	live.SocksProxyVersion = 5
	return cfg, live
}

// newPrefOnlyClient builds a reconcileClient whose repo/extension/config steps
// are all no-ops (no engine repos, no extensions, config matched via live), so
// ONLY the source-preference step can act — the shared fixture for the
// preference-branch tests.
func newPrefOnlyClient(live suwayomi.SuwayomiSettings, prefs map[string][]suwayomi.SourcePreference) *reconcileClient {
	return &reconcileClient{
		fakeClient:   &fakeClient{prefsBySource: prefs},
		liveSettings: live,
	}
}

// seedHarvestedExtension inserts a HarvestedExtension row mapping pkgName to the
// given source ids, so requiredPkgSet can match it against the library's numeric
// providers.
func seedHarvestedExtension(ctx context.Context, t *testing.T, db *ent.Client, pkgName string, sourceIDs []int64) {
	t.Helper()
	db.HarvestedExtension.Create().
		SetPkgName(pkgName).
		SetSourceIds(sourceIDs).
		SaveX(ctx)
}

// seedStoredPref inserts a durable SourcePreference row (source_id, key) →
// (value, value_type) directly, as the boot seed would have captured it.
func seedStoredPref(ctx context.Context, t *testing.T, db *ent.Client, sourceID int64, key, value string, typ suwayomi.PreferenceType) {
	t.Helper()
	db.SourcePreference.Create().
		SetSourceID(sourceID).
		SetKey(key).
		SetValue(value).
		SetValueType(string(typ)).
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
	seedStoredPref(ctx, t, db, 111, "nsfw", "true", suwayomi.PreferenceCheckBox)

	cfg, _ := matchedFlareConfig()
	cfg.socksEnabled = true
	cfg.socksHost = "127.0.0.1"
	cfg.socksPort = 1080
	cfg.socksVersion = 5

	client := &reconcileClient{
		fakeClient: &fakeClient{
			prefsBySource: map[string][]suwayomi.SourcePreference{
				// Live value (false) differs from the stored value (true) → push.
				"111": {{Type: suwayomi.PreferenceCheckBox, Position: 0, Key: "nsfw", CurrentBool: boolPtr(false)}},
			},
		},
		engineRepos: nil, // empty engine → repo drift
		exts:        nil, // pkg.one not installed → missing
		// Live FlareSolverr URL differs from cfg → config drift.
		liveSettings: suwayomi.SuwayomiSettings{FlareSolverrURL: "http://stale.test"},
	}

	res, err := enginetopo.Reconcile(ctx, client, db, cache, cfg)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	assertFreshResult(t, res)
	assertFreshMutations(t, client)
}

// assertFreshResult checks the Result counts a full-recovery pass reports.
func assertFreshResult(t *testing.T, res enginetopo.ReconcileResult) {
	t.Helper()
	if res.InSync {
		t.Error("InSync = true, want false (fresh engine needed every change)")
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
// issued (repo set, refresh, install, settings push with a numeric-string port).
func assertFreshMutations(t *testing.T, client *reconcileClient) {
	t.Helper()
	gotRepos := client.setReposCalls == 1 && len(client.lastSetRepos) == 1 && client.lastSetRepos[0] == "https://repo.test/repo"
	if !gotRepos {
		t.Errorf("SetExtensionRepos calls=%d repos=%v, want 1 call with the durable repo", client.setReposCalls, client.lastSetRepos)
	}
	if client.fetchCalls == 0 {
		t.Error("FetchExtensions not called before installing a missing extension")
	}
	gotInstall := len(client.installed) == 1 && client.installed[0] == "pkg.one"
	if !gotInstall {
		t.Errorf("installed = %v, want [pkg.one]", client.installed)
	}
	assertFreshSettingsPatch(t, client)
}

// assertFreshSettingsPatch checks the single settings push carried the expected
// FlareSolverr URL and the SOCKS port as a numeric STRING (Suwayomi's wire type).
func assertFreshSettingsPatch(t *testing.T, client *reconcileClient) {
	t.Helper()
	if len(client.setSettings) != 1 {
		t.Fatalf("SetServerSettings calls = %d, want 1", len(client.setSettings))
	}
	patch := client.setSettings[0]
	if patch.SocksProxyPort == nil || *patch.SocksProxyPort != "1080" {
		t.Errorf("patch SocksProxyPort = %v, want \"1080\"", patch.SocksProxyPort)
	}
	if patch.FlareSolverrURL == nil || *patch.FlareSolverrURL != "http://flare.test:8191" {
		t.Errorf("patch FlareSolverrURL = %v, want the cfg url", patch.FlareSolverrURL)
	}
}

// TestReconcile_InSyncEngineMakesZeroMutations proves idempotency: when the
// engine already matches the durable store, Reconcile issues NO engine mutation
// (no repo set, no fetch, no install, no pref write, no settings write) and
// reports InSync=true.
func TestReconcile_InSyncEngineMakesZeroMutations(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	seedProvider(ctx, t, db, "Solo Leveling", "111", 111)
	seedHarvestedExtension(ctx, t, db, "pkg.one", []int64{111})
	db.HarvestedRepo.Create().SetURL("https://repo.test/repo").SaveX(ctx)
	seedStoredPref(ctx, t, db, 111, "nsfw", "true", suwayomi.PreferenceCheckBox)

	cfg, live := matchedFlareConfig()

	client := &reconcileClient{
		fakeClient: &fakeClient{
			prefsBySource: map[string][]suwayomi.SourcePreference{
				// Live value equals stored ("true") → no write.
				"111": {{Type: suwayomi.PreferenceCheckBox, Position: 0, Key: "nsfw", CurrentBool: boolPtr(true)}},
			},
		},
		engineRepos:  []string{"https://repo.test/repo"}, // matches DB
		exts:         []suwayomi.Extension{{PkgName: "pkg.one", IsInstalled: true}},
		liveSettings: live,
	}

	res, err := enginetopo.Reconcile(ctx, client, db, cache, cfg)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !res.InSync {
		t.Errorf("InSync = false, want true (Result=%+v)", res)
	}
	if client.setReposCalls != 0 {
		t.Errorf("SetExtensionRepos called %d times, want 0", client.setReposCalls)
	}
	if client.fetchCalls != 0 {
		t.Errorf("FetchExtensions called %d times, want 0", client.fetchCalls)
	}
	if len(client.installed) != 0 {
		t.Errorf("installed = %v, want none", client.installed)
	}
	if len(client.setPrefs) != 0 {
		t.Errorf("SetSourcePreference called %d times, want 0", len(client.setPrefs))
	}
	if len(client.setSettings) != 0 {
		t.Errorf("SetServerSettings called %d times, want 0", len(client.setSettings))
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
	seedStoredPref(ctx, t, db, 111, "nsfw", "true", suwayomi.PreferenceCheckBox)

	cfg, _ := matchedFlareConfig()

	client := &reconcileClient{
		fakeClient: &fakeClient{
			prefsBySource: map[string][]suwayomi.SourcePreference{
				"111": {{Type: suwayomi.PreferenceCheckBox, Position: 0, Key: "nsfw", CurrentBool: boolPtr(false)}},
			},
		},
		engineRepos:  []string{"https://repo.test/repo"}, // repos match; only extensions missing
		exts:         nil,                                // neither installed
		liveSettings: suwayomi.SuwayomiSettings{FlareSolverrURL: "http://stale.test"},
		installErr:   map[string]error{"pkg.one": errors.New("repo unreachable")},
	}

	res, err := enginetopo.Reconcile(ctx, client, db, cache, cfg)
	if err != nil {
		t.Fatalf("Reconcile returned hard error, want per-item isolation: %v", err)
	}
	if res.ExtensionsInstalled != 1 {
		t.Errorf("ExtensionsInstalled = %d, want 1 (pkg.two)", res.ExtensionsInstalled)
	}
	if len(client.installed) != 1 || client.installed[0] != "pkg.two" {
		t.Errorf("installed = %v, want [pkg.two]", client.installed)
	}
	if len(res.Gaps) != 1 {
		t.Fatalf("Gaps = %v, want exactly 1 (the failed pkg.one install)", res.Gaps)
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

// TestReconcile_PrefDriftAllKinds proves the impedance-mismatch step across every
// value kind: a stored preference whose live value DIFFERS is pushed exactly once
// at the live pref's position with the correctly-typed reversed value, while a
// stored preference EQUAL to its live value is not written at all.
func TestReconcile_PrefDriftAllKinds(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	// No harvested extensions + no repos → the extension/repo steps are no-ops, so
	// ONLY the preference step can produce writes.
	seedStoredPref(ctx, t, db, 111, "nsfw", "true", suwayomi.PreferenceCheckBox)
	seedStoredPref(ctx, t, db, 111, "quality", "high", suwayomi.PreferenceList)
	seedStoredPref(ctx, t, db, 111, "blocked", `["A","B"]`, suwayomi.PreferenceMultiSelect)
	seedStoredPref(ctx, t, db, 111, "https", "true", suwayomi.PreferenceSwitch)

	cfg, live := matchedFlareConfig()

	client := &reconcileClient{
		fakeClient: &fakeClient{
			prefsBySource: map[string][]suwayomi.SourcePreference{
				"111": {
					{Type: suwayomi.PreferenceCheckBox, Position: 0, Key: "nsfw", CurrentBool: boolPtr(false)},            // differs → push bool
					{Type: suwayomi.PreferenceList, Position: 1, Key: "quality", CurrentString: stringPtr("low")},         // differs → push string
					{Type: suwayomi.PreferenceMultiSelect, Position: 2, Key: "blocked", CurrentStringList: []string{"A"}}, // differs → push list
					{Type: suwayomi.PreferenceSwitch, Position: 3, Key: "https", CurrentBool: boolPtr(true)},              // EQUAL → no write
				},
			},
		},
		liveSettings: live,
	}

	res, err := enginetopo.Reconcile(ctx, client, db, cache, cfg)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.PrefsApplied != 3 {
		t.Errorf("PrefsApplied = %d, want 3", res.PrefsApplied)
	}
	if len(client.setPrefs) != 3 {
		t.Fatalf("SetSourcePreference called %d times, want 3 (equal pref must not be written)", len(client.setPrefs))
	}

	byPos := client.prefsByPosition()
	assertPrefValue(t, byPos, 0, suwayomi.BoolPreferenceValue(suwayomi.PreferenceCheckBox, true))
	assertPrefValue(t, byPos, 1, suwayomi.StringPreferenceValue(suwayomi.PreferenceList, "high"))
	assertPrefValue(t, byPos, 2, suwayomi.MultiSelectPreferenceValue([]string{"A", "B"}))
	if _, ok := byPos[3]; ok {
		t.Error("position 3 (the equal Switch pref) was written, want skipped")
	}
}

// assertPrefValue fails unless a SetSourcePreference call was recorded at pos
// carrying exactly want (compared by reflect.DeepEqual over the opaque
// PreferenceValue built from the same exported constructors).
func assertPrefValue(t *testing.T, byPos map[int]setPrefCall, pos int, want suwayomi.PreferenceValue) {
	t.Helper()
	call, ok := byPos[pos]
	if !ok {
		t.Errorf("no SetSourcePreference call at position %d", pos)
		return
	}
	if call.sourceID != "111" {
		t.Errorf("position %d sourceID = %q, want \"111\"", pos, call.sourceID)
	}
	if !reflect.DeepEqual(call.value, want) {
		t.Errorf("position %d value = %+v, want %+v", pos, call.value, want)
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

	cfg, _ := matchedFlareConfig()

	client := &reconcileClient{
		fakeClient: &fakeClient{},
		extsErr:    errors.New("engine down"),
	}

	if _, err := enginetopo.Reconcile(ctx, client, db, cache, cfg); err == nil {
		t.Fatal("Reconcile: want hard error when listing extensions fails, got nil")
	}
}

// TestReconcile_WipedEnginePrefRepush proves THE real-recovery path: a stored
// preference whose live pref exists (same key + a real Type) but has NO current
// value set (a wiped engine) is (re)pushed exactly once, at the live pref's
// position, with the correctly-typed reversed value. encodePreferenceValue
// returns ok=false for a nil current value, so prefInSync is false and the
// stored value repopulates the engine.
func TestReconcile_WipedEnginePrefRepush(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	seedStoredPref(ctx, t, db, 111, "https", "true", suwayomi.PreferenceSwitch)

	cfg, live := matchedFlareConfig()

	// Live pref present but with CurrentBool nil — the engine has the option but no
	// value, exactly what a wipe/rebuild leaves behind.
	client := newPrefOnlyClient(live, map[string][]suwayomi.SourcePreference{
		"111": {{Type: suwayomi.PreferenceSwitch, Position: 5, Key: "https", CurrentBool: nil}},
	})

	res, err := enginetopo.Reconcile(ctx, client, db, cache, cfg)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.PrefsApplied != 1 {
		t.Errorf("PrefsApplied = %d, want 1", res.PrefsApplied)
	}
	if len(client.setPrefs) != 1 {
		t.Fatalf("SetSourcePreference called %d times, want 1 (wiped pref must be repushed)", len(client.setPrefs))
	}
	byPos := client.prefsByPosition()
	assertPrefValue(t, byPos, 5, suwayomi.BoolPreferenceValue(suwayomi.PreferenceSwitch, true))
}

// TestReconcile_SocksInSyncMakesZeroConfigMutation proves socksInSync's no-push
// branch: when SOCKS is ENABLED and its host/port/version (plus FlareSolverr)
// already match the engine, Reconcile issues no settings write and reports
// ConfigApplied=false. No existing test drives an enabled-and-matching SOCKS, so
// a wrong field comparison there would flap the config push every boot.
func TestReconcile_SocksInSyncMakesZeroConfigMutation(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	cfg, live := matchedSocksConfig()

	client := &reconcileClient{
		fakeClient:   &fakeClient{},
		liveSettings: live,
	}

	res, err := enginetopo.Reconcile(ctx, client, db, cache, cfg)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.ConfigApplied {
		t.Error("ConfigApplied = true, want false (SOCKS + FlareSolverr already match)")
	}
	if len(client.setSettings) != 0 {
		t.Errorf("SetServerSettings called %d times, want 0", len(client.setSettings))
	}
}

// TestReconcile_RepoWriteErrorIsolated proves a SetExtensionRepos write failure
// is isolated as a gap, not a hard error: the pass records the failure, leaves
// ReposSet=false, and still applies the drifted preference and config.
func TestReconcile_RepoWriteErrorIsolated(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	db.HarvestedRepo.Create().SetURL("https://repo.test/repo").SaveX(ctx)
	seedStoredPref(ctx, t, db, 111, "nsfw", "true", suwayomi.PreferenceCheckBox)

	cfg, _ := matchedFlareConfig()

	client := &reconcileClient{
		fakeClient: &fakeClient{
			prefsBySource: map[string][]suwayomi.SourcePreference{
				"111": {{Type: suwayomi.PreferenceCheckBox, Position: 0, Key: "nsfw", CurrentBool: boolPtr(false)}},
			},
		},
		engineRepos:  []string{"https://stale.test/repo"}, // differs from DB → drift → write attempted
		setReposErr:  errors.New("repo store rejected"),
		liveSettings: suwayomi.SuwayomiSettings{FlareSolverrURL: "http://stale.test"}, // config drift
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

// TestReconcile_ConfigReadErrorIsolated proves the deliberate design that a
// ServerSettings READ failure is an isolated gap (NOT a hard error, unlike the
// enumerating extension/repo reads): config recovery is independent of the
// extension + preference recovery, so it must not abort the whole pass.
func TestReconcile_ConfigReadErrorIsolated(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	cfg, _ := matchedFlareConfig()

	client := &reconcileClient{
		fakeClient:  &fakeClient{},
		settingsErr: errors.New("server settings unreachable"),
	}

	res, err := enginetopo.Reconcile(ctx, client, db, cache, cfg)
	if err != nil {
		t.Fatalf("Reconcile returned hard error, want config read isolated as a gap: %v", err)
	}
	if res.ConfigApplied {
		t.Error("ConfigApplied = true, want false (settings read failed)")
	}
	if len(res.Gaps) != 1 {
		t.Fatalf("Gaps = %v, want exactly 1 (the failed settings read)", res.Gaps)
	}
	if len(client.setSettings) != 0 {
		t.Errorf("SetServerSettings called %d times, want 0", len(client.setSettings))
	}
}

// TestReconcile_BuildPrefValueParseFailureIsolated proves a stored preference
// whose value cannot be rebuilt for the live pref's kind (here a Switch stored as
// the non-bool "notabool") is isolated as a gap: no hard error, no write for that
// pref.
func TestReconcile_BuildPrefValueParseFailureIsolated(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	seedStoredPref(ctx, t, db, 111, "https", "notabool", suwayomi.PreferenceSwitch)

	cfg, live := matchedFlareConfig()

	// Live value (false) differs from the unparseable stored value → a build is
	// attempted, and strconv.ParseBool fails.
	client := newPrefOnlyClient(live, map[string][]suwayomi.SourcePreference{
		"111": {{Type: suwayomi.PreferenceSwitch, Position: 0, Key: "https", CurrentBool: boolPtr(false)}},
	})

	res, err := enginetopo.Reconcile(ctx, client, db, cache, cfg)
	if err != nil {
		t.Fatalf("Reconcile returned hard error, want parse failure isolated as a gap: %v", err)
	}
	if len(res.Gaps) != 1 {
		t.Fatalf("Gaps = %v, want exactly 1 (the unparseable stored pref)", res.Gaps)
	}
	if len(client.setPrefs) != 0 {
		t.Errorf("SetSourcePreference called %d times, want 0 (parse failed before any write)", len(client.setPrefs))
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

	seedStoredPref(ctx, t, db, 111, "ghost", "true", suwayomi.PreferenceSwitch)

	cfg, live := matchedFlareConfig()

	// The live source carries a DIFFERENT key, so "ghost" has no live match.
	client := newPrefOnlyClient(live, map[string][]suwayomi.SourcePreference{
		"111": {{Type: suwayomi.PreferenceSwitch, Position: 0, Key: "other", CurrentBool: boolPtr(true)}},
	})

	res, err := enginetopo.Reconcile(ctx, client, db, cache, cfg)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(client.setPrefs) != 0 {
		t.Errorf("SetSourcePreference called %d times, want 0 (removed option skipped)", len(client.setPrefs))
	}
	if len(res.Gaps) != 0 {
		t.Errorf("Gaps = %v, want none (a removed option is silent, not a gap)", res.Gaps)
	}
	if !res.InSync {
		t.Errorf("InSync = false, want true (nothing needed changing); Result=%+v", res)
	}
}
