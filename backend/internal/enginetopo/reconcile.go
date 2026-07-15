package enginetopo

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/ent"
	entharvestedrepo "github.com/technobecet/tsundoku/internal/ent/harvestedrepo"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// ConfigProvider is the narrow read surface Reconcile needs to push Tsundoku's
// OWN FlareSolverr + SOCKS config onto the engine — exactly the settings the
// engine-config seed (SeedEngineConfig) captured. It is the reverse of
// flareSolverrUpdates/socksUpdates: those turn engine settings into Tsundoku
// tunables, whereas Reconcile reads the resolved tunables back to rebuild the
// engine's settings. Kept to precisely the ten typed getters used here so a test
// double is trivial; *settings.Service satisfies it directly.
type ConfigProvider interface {
	FlareSolverrEnabled(ctx context.Context) bool
	FlareSolverrURL(ctx context.Context) string
	FlareSolverrTimeout(ctx context.Context) int
	FlareSolverrSessionName(ctx context.Context) string
	FlareSolverrSessionTTL(ctx context.Context) int
	FlareSolverrResponseFallback(ctx context.Context) bool
	EngineSocksEnabled(ctx context.Context) bool
	EngineSocksHost(ctx context.Context) string
	EngineSocksPort(ctx context.Context) int
	EngineSocksVersion(ctx context.Context) int
}

// Compile-time proof that the production settings overlay satisfies
// ConfigProvider — the wiring main.go relies on.
var _ ConfigProvider = (*settings.Service)(nil)

// ReconcileResult reports what a Reconcile pass did — the observable outcome of
// pushing Tsundoku's durable engine-topology store back onto a wiped/swapped/
// rebuilt engine.
type ReconcileResult struct {
	// InSync is true when the engine already matched the durable store, so the
	// pass made ZERO engine mutations (and recorded no gaps).
	InSync bool
	// ReposSet is true when the engine's extension-repo list was rewritten from
	// the durable HarvestedRepo set (it drifted from the engine's list).
	ReposSet bool
	// ExtensionsInstalled is the number of required-but-missing extensions the
	// pass installed via SetExtensionState.
	ExtensionsInstalled int
	// PrefsApplied is the number of source-preference writes issued — one per
	// stored preference whose engine value had drifted from the durable value.
	PrefsApplied int
	// ConfigApplied is true when the engine's FlareSolverr/SOCKS settings were
	// (re)pushed because they drifted from Tsundoku's owned config.
	ConfigApplied bool
	// Gaps holds every per-item failure that was ISOLATED (a failed install, an
	// unparseable stored preference, a config push error, …). Each is logged and
	// recorded here; none aborts the rest of the pass. A non-empty Gaps means the
	// pass is not fully in sync even if every other field is zero.
	Gaps []error
}

// Reconcile pushes Tsundoku's durable engine-topology store (HarvestedRepo /
// HarvestedExtension / SourcePreference / the owned FlareSolverr+SOCKS config)
// back onto the live engine — the INVERSE of the boot seed passes (which capture
// engine→DB). It is how a wiped, swapped, or freshly-rebuilt engine is restored
// to the topology Tsundoku remembers.
//
// It runs the drift-detect + reapply steps in order: repos → extensions →
// source preferences → config. Each step READS the engine to detect drift and
// only MUTATES when the engine differs, so the pass is IDEMPOTENT: against an
// already-in-sync engine it issues zero mutations and returns InSync=true.
//
// EXTENSION INSTALL IS REPO-BASED. The Suwayomi Client installs an extension by
// pkgName after a FetchExtensions refresh of the repo list — there is NO apkUrl
// parameter on the interface. The apk byte cache (cache, apk_cached) is therefore
// the durability RECORD (it guarantees the bytes still exist for a future
// engine-host install-from-cache path), NOT something this pass installs from;
// cache is unused here by design. A dead upstream repo means that pkg's install
// may fail, which is isolated as a gap (below).
//
// FAULT ISOLATION. A per-item failure — one extension that won't install, one
// preference that won't parse or write, a config push error — is logged
// (slog.WarnContext) and recorded in Gaps; the remaining items still apply. Only
// an ENUMERATING failure returns a hard error: it leaves the pass unable to even
// determine drift, so it must not silently do nothing (mirrors SeedExtensions'
// per-item vs enumerating distinction). The enumerating calls are: querying the
// library's required-extension set + stored preferences (DB), and listing the
// engine's installed extensions + repo list (engine).
//
// PRECONDITION — SEED BEFORE RECONCILE. Reconcile trusts Tsundoku's DB as the
// authoritative record of engine state, so the boot seed passes (especially
// SeedEngineConfig, plus the source-preference / extension / repo seeds) MUST
// have run first to CAPTURE that state before Reconcile pushes it back. It
// deliberately does NOT gate its config push on per-key ownership — it treats
// the durable store as reality. Running it against a live, UN-captured engine
// BEFORE the seed could therefore push Tsundoku's default (disabled/empty)
// config over the engine's real settings; sequencing seed → reconcile is the
// caller's responsibility, not something Reconcile can detect. The forward path
// — provisioning Tsundoku's OWN internal engine, which starts empty — satisfies
// this naturally. Ratified as decision QCAT-250.
func Reconcile(
	ctx context.Context,
	client suwayomi.Client,
	db *ent.Client,
	cache *apkcache.Store,
	cfg ConfigProvider,
) (ReconcileResult, error) {
	_ = cache // intentionally unused — see the doc comment (repo-based install).

	var res ReconcileResult

	required, err := requiredPkgSet(ctx, db)
	if err != nil {
		return res, err
	}
	installed, err := installedPkgSet(ctx, client)
	if err != nil {
		return res, err
	}

	reposChanged, repoGaps, err := reconcileRepos(ctx, client, db)
	if err != nil {
		return res, err
	}
	res.ReposSet = reposChanged
	res.Gaps = append(res.Gaps, repoGaps...)

	installedCount, extGaps := reconcileExtensions(ctx, client, required, installed, reposChanged)
	res.ExtensionsInstalled = installedCount
	res.Gaps = append(res.Gaps, extGaps...)

	prefsApplied, prefGaps, err := reconcilePrefs(ctx, client, db)
	if err != nil {
		return res, err
	}
	res.PrefsApplied = prefsApplied
	res.Gaps = append(res.Gaps, prefGaps...)

	configApplied, cfgGaps := reconcileConfig(ctx, client, cfg)
	res.ConfigApplied = configApplied
	res.Gaps = append(res.Gaps, cfgGaps...)

	res.InSync = isInSync(res)
	return res, nil
}

// isInSync reports whether the pass made no engine mutation AND recorded no gap —
// the honest "nothing needed changing" answer.
func isInSync(res ReconcileResult) bool {
	return !res.ReposSet &&
		res.ExtensionsInstalled == 0 &&
		res.PrefsApplied == 0 &&
		!res.ConfigApplied &&
		len(res.Gaps) == 0
}

// requiredPkgSet is the durable set of extensions the library actually needs:
// every HarvestedExtension whose source_ids intersect the library's NUMERIC
// SeriesProvider.provider values (a disk-origin provider stores a display name,
// not a source id, and is excluded — the same numeric/name split
// SeedSourcePreferences + TopologyStatus apply). Querying either table failing is
// an enumerating error.
func requiredPkgSet(ctx context.Context, db *ent.Client) ([]string, error) {
	providers, err := db.SeriesProvider.Query().
		Unique(true).
		Select(entseriesprovider.FieldProvider).
		Strings(ctx)
	if err != nil {
		return nil, fmt.Errorf("enginetopo.Reconcile: query providers: %w", err)
	}
	numeric := make(map[int64]bool, len(providers))
	for _, p := range providers {
		if id, perr := strconv.ParseInt(p, 10, 64); perr == nil {
			numeric[id] = true
		}
	}

	exts, err := db.HarvestedExtension.Query().All(ctx)
	if err != nil {
		return nil, fmt.Errorf("enginetopo.Reconcile: query harvested extensions: %w", err)
	}
	var required []string
	for _, ext := range exts {
		if intersectsNumeric(ext.SourceIds, numeric) {
			required = append(required, ext.PkgName)
		}
	}
	return required, nil
}

// intersectsNumeric reports whether any of sourceIDs is in the numeric provider
// set — the "this extension backs a source the library uses" test.
func intersectsNumeric(sourceIDs []int64, numeric map[int64]bool) bool {
	for _, sid := range sourceIDs {
		if numeric[sid] {
			return true
		}
	}
	return false
}

// installedPkgSet reads the engine's currently-installed extensions into a
// pkgName set. Listing extensions failing is an enumerating error.
func installedPkgSet(ctx context.Context, client suwayomi.Client) (map[string]bool, error) {
	exts, err := client.Extensions(ctx)
	if err != nil {
		return nil, fmt.Errorf("enginetopo.Reconcile: list extensions: %w", err)
	}
	installed := make(map[string]bool, len(exts))
	for _, e := range exts {
		if e.IsInstalled {
			installed[e.PkgName] = true
		}
	}
	return installed, nil
}

// reconcileRepos rewrites the engine's extension-repo list from the durable
// HarvestedRepo set WHEN it has drifted; an already-matching list is a no-op
// (changed=false, no mutation). Reading either list failing is an enumerating
// error; a SetExtensionRepos write failure is isolated as a gap (changed stays
// false, so the extension step still runs but skips the repo-driven fetch).
func reconcileRepos(ctx context.Context, client suwayomi.Client, db *ent.Client) (bool, []error, error) {
	dbRepos, err := db.HarvestedRepo.Query().Select(entharvestedrepo.FieldURL).Strings(ctx)
	if err != nil {
		return false, nil, fmt.Errorf("enginetopo.Reconcile: query harvested repos: %w", err)
	}
	engineRepos, err := client.ExtensionRepos(ctx)
	if err != nil {
		return false, nil, fmt.Errorf("enginetopo.Reconcile: list engine repos: %w", err)
	}
	if sameStringSet(dbRepos, engineRepos) {
		return false, nil, nil
	}
	if err := client.SetExtensionRepos(ctx, dbRepos); err != nil {
		slog.WarnContext(ctx, "enginetopo: reconcile could not set engine repos", "err", err)
		return false, []error{fmt.Errorf("set engine repos: %w", err)}, nil
	}
	return true, nil, nil
}

// reconcileExtensions installs every required-but-missing extension. It first
// refreshes the engine's available list from the repos (FetchExtensions) when
// there is anything to do — a repo change OR a missing pkg — so a just-set repo's
// extensions become installable. When nothing is missing and repos did not
// change it is a pure no-op (zero mutations, honouring idempotency). A per-pkg
// install failure is isolated as a gap.
func reconcileExtensions(
	ctx context.Context,
	client suwayomi.Client,
	required []string,
	installed map[string]bool,
	reposChanged bool,
) (int, []error) {
	var missing []string
	for _, pkg := range required {
		if !installed[pkg] {
			missing = append(missing, pkg)
		}
	}
	if !reposChanged && len(missing) == 0 {
		return 0, nil
	}

	var gaps []error
	if _, err := client.FetchExtensions(ctx); err != nil {
		// The repo-cache refresh failed; the installs below may still succeed from a
		// prior cache, so record the gap and continue rather than abort.
		slog.WarnContext(ctx, "enginetopo: reconcile could not refresh extensions from repos", "err", err)
		gaps = append(gaps, fmt.Errorf("fetch extensions: %w", err))
	}

	count := 0
	for _, pkg := range missing {
		if err := client.SetExtensionState(ctx, pkg, suwayomi.ExtensionInstall); err != nil {
			slog.WarnContext(ctx, "enginetopo: reconcile could not install extension, recording gap",
				"pkg_name", pkg, "err", err)
			gaps = append(gaps, fmt.Errorf("install extension %q: %w", pkg, err))
			continue
		}
		count++
	}
	return count, gaps
}

// reconcilePrefs pushes every stored SourcePreference whose engine value has
// drifted from the durable value back onto its source, addressed by the LIVE
// pref's Position (the DB stores no position). Loading the stored rows failing is
// an enumerating error; a per-source read failure, or a per-pref parse/write
// failure, is isolated as a gap.
func reconcilePrefs(ctx context.Context, client suwayomi.Client, db *ent.Client) (int, []error, error) {
	rows, err := db.SourcePreference.Query().All(ctx)
	if err != nil {
		return 0, nil, fmt.Errorf("enginetopo.Reconcile: query source preferences: %w", err)
	}
	bySource := make(map[int64][]*ent.SourcePreference)
	for _, r := range rows {
		bySource[r.SourceID] = append(bySource[r.SourceID], r)
	}

	applied := 0
	var gaps []error
	for sourceID, stored := range bySource {
		sidStr := strconv.FormatInt(sourceID, 10)
		live, err := client.SourcePreferences(ctx, sidStr)
		if err != nil {
			slog.WarnContext(ctx, "enginetopo: reconcile could not read source preferences, skipping source",
				"source_id", sourceID, "err", err)
			gaps = append(gaps, fmt.Errorf("read source %d preferences: %w", sourceID, err))
			continue
		}
		a, g := applySourcePrefs(ctx, client, sidStr, stored, indexPrefsByKey(live))
		applied += a
		gaps = append(gaps, g...)
	}
	return applied, gaps, nil
}

// indexPrefsByKey maps live preferences by their Key (skipping keyless ones,
// which cannot be matched to a stored (source_id, key) row).
func indexPrefsByKey(live []suwayomi.SourcePreference) map[string]suwayomi.SourcePreference {
	byKey := make(map[string]suwayomi.SourcePreference, len(live))
	for _, p := range live {
		if p.Key != "" {
			byKey[p.Key] = p
		}
	}
	return byKey
}

// applySourcePrefs writes each stored preference for one source whose live value
// differs, using the live pref's Position + Type. A stored key with no live pref
// is skipped; a stored value equal to the live value is left untouched
// (idempotency); a parse or write failure is isolated as a gap.
func applySourcePrefs(
	ctx context.Context,
	client suwayomi.Client,
	sidStr string,
	stored []*ent.SourcePreference,
	liveByKey map[string]suwayomi.SourcePreference,
) (int, []error) {
	applied := 0
	var gaps []error
	for _, row := range stored {
		live, ok := liveByKey[row.Key]
		if !ok || prefInSync(row.Value, live) {
			continue
		}
		value, err := buildPrefValue(live.Type, row.Value)
		if err != nil {
			slog.WarnContext(ctx, "enginetopo: reconcile could not build preference value, recording gap",
				"source", sidStr, "key", row.Key, "err", err)
			gaps = append(gaps, fmt.Errorf("build preference %q: %w", row.Key, err))
			continue
		}
		if _, err := client.SetSourcePreference(ctx, sidStr, live.Position, value); err != nil {
			slog.WarnContext(ctx, "enginetopo: reconcile could not set preference, recording gap",
				"source", sidStr, "key", row.Key, "err", err)
			gaps = append(gaps, fmt.Errorf("set preference %q: %w", row.Key, err))
			continue
		}
		applied++
	}
	return applied, gaps
}

// prefInSync reports whether a stored value already matches the live preference's
// current value. Both are compared in their ENCODED string form (the live pref
// through encodePreferenceValue, the same function the seed captured it with), so
// the comparison is symmetric with how the value was stored. A live pref with no
// current value set (encode ok=false) is never "in sync" with a stored value, so
// the stored value is (re)pushed.
func prefInSync(storedValue string, live suwayomi.SourcePreference) bool {
	liveValue, _, ok := encodePreferenceValue(live)
	return ok && storedValue == liveValue
}

// buildPrefValue REVERSES encodePreferenceValue: it turns a stored string value
// back into a typed suwayomi.PreferenceValue for the live pref's variant. The
// kind comes from the LIVE pref (it is what SetSourcePreference validates
// against), not the stored value_type. A parse failure or unknown kind is an
// error the caller isolates as a gap.
func buildPrefValue(kind suwayomi.PreferenceType, stored string) (suwayomi.PreferenceValue, error) {
	switch kind {
	case suwayomi.PreferenceCheckBox, suwayomi.PreferenceSwitch:
		b, err := strconv.ParseBool(stored)
		if err != nil {
			return suwayomi.PreferenceValue{}, fmt.Errorf("parse bool %q: %w", stored, err)
		}
		return suwayomi.BoolPreferenceValue(kind, b), nil
	case suwayomi.PreferenceList, suwayomi.PreferenceEditText:
		return suwayomi.StringPreferenceValue(kind, stored), nil
	case suwayomi.PreferenceMultiSelect:
		var list []string
		if err := json.Unmarshal([]byte(stored), &list); err != nil {
			return suwayomi.PreferenceValue{}, fmt.Errorf("parse multiselect %q: %w", stored, err)
		}
		return suwayomi.MultiSelectPreferenceValue(list), nil
	default:
		return suwayomi.PreferenceValue{}, fmt.Errorf("unknown preference kind %q", kind)
	}
}

// reconcileConfig pushes Tsundoku's owned FlareSolverr + SOCKS config onto the
// engine WHEN it has drifted, comparing against a live ServerSettings read first
// so an already-matching engine is a no-op. Reading or writing the settings is a
// single item: a failure is isolated as a gap (never a hard error), because
// config is independent of the extension + preference recovery.
func reconcileConfig(ctx context.Context, client suwayomi.Client, cfg ConfigProvider) (bool, []error) {
	live, err := client.ServerSettings(ctx)
	if err != nil {
		slog.WarnContext(ctx, "enginetopo: reconcile could not read server settings, recording gap", "err", err)
		return false, []error{fmt.Errorf("read server settings: %w", err)}
	}
	desired := snapshotConfig(ctx, cfg)
	if configInSync(live, desired) {
		return false, nil
	}
	if err := client.SetServerSettings(ctx, desired.patch()); err != nil {
		slog.WarnContext(ctx, "enginetopo: reconcile could not set server settings, recording gap", "err", err)
		return false, []error{fmt.Errorf("set server settings: %w", err)}
	}
	return true, nil
}

// desiredConfig is a one-shot snapshot of Tsundoku's owned FlareSolverr + SOCKS
// config, read from the ConfigProvider once so the compare and the patch build
// use identical values (and issue no repeated DB reads). socksPort is stored in
// its wire form (a numeric string) to match SuwayomiSettings.SocksProxyPort.
type desiredConfig struct {
	fsEnabled     bool
	fsURL         string
	fsTimeout     int
	fsSessionName string
	fsSessionTTL  int
	fsFallback    bool
	socksEnabled  bool
	socksHost     string
	socksPort     string
	socksVersion  int
}

// snapshotConfig reads every ConfigProvider accessor once into a desiredConfig.
func snapshotConfig(ctx context.Context, cfg ConfigProvider) desiredConfig {
	return desiredConfig{
		fsEnabled:     cfg.FlareSolverrEnabled(ctx),
		fsURL:         cfg.FlareSolverrURL(ctx),
		fsTimeout:     cfg.FlareSolverrTimeout(ctx),
		fsSessionName: cfg.FlareSolverrSessionName(ctx),
		fsSessionTTL:  cfg.FlareSolverrSessionTTL(ctx),
		fsFallback:    cfg.FlareSolverrResponseFallback(ctx),
		socksEnabled:  cfg.EngineSocksEnabled(ctx),
		socksHost:     cfg.EngineSocksHost(ctx),
		socksPort:     strconv.Itoa(cfg.EngineSocksPort(ctx)),
		socksVersion:  cfg.EngineSocksVersion(ctx),
	}
}

// patch builds the SuwayomiSettingsPatch that would make the engine match this
// desiredConfig. FlareSolverr fields are always set; the SOCKS fields are set
// ONLY when SOCKS is enabled — mirroring socksUpdates' skip-when-off rule (a
// disabled SOCKS proxy has nothing to push, and Tsundoku never seeds/pushes the
// SOCKS username/password, so they are omitted here too).
func (d desiredConfig) patch() suwayomi.SuwayomiSettingsPatch {
	p := suwayomi.SuwayomiSettingsPatch{
		FlareSolverrEnabled:            &d.fsEnabled,
		FlareSolverrURL:                &d.fsURL,
		FlareSolverrTimeout:            &d.fsTimeout,
		FlareSolverrSessionName:        &d.fsSessionName,
		FlareSolverrSessionTTL:         &d.fsSessionTTL,
		FlareSolverrAsResponseFallback: &d.fsFallback,
	}
	if d.socksEnabled {
		enabled := true
		p.SocksProxyEnabled = &enabled
		p.SocksProxyHost = &d.socksHost
		p.SocksProxyPort = &d.socksPort
		p.SocksProxyVersion = &d.socksVersion
	}
	return p
}

// configInSync reports whether the engine's live settings already match the
// desired config across every field the patch would set: always the FlareSolverr
// subset, plus the SOCKS subset only when SOCKS is enabled (a disabled SOCKS is
// never pushed, so it is not compared — matching patch()).
func configInSync(live suwayomi.SuwayomiSettings, d desiredConfig) bool {
	if !flareSolverrInSync(live, d) {
		return false
	}
	if d.socksEnabled && !socksInSync(live, d) {
		return false
	}
	return true
}

// flareSolverrInSync compares the six FlareSolverr fields.
func flareSolverrInSync(live suwayomi.SuwayomiSettings, d desiredConfig) bool {
	return live.FlareSolverrEnabled == d.fsEnabled &&
		live.FlareSolverrURL == d.fsURL &&
		live.FlareSolverrTimeout == d.fsTimeout &&
		live.FlareSolverrSessionName == d.fsSessionName &&
		live.FlareSolverrSessionTTL == d.fsSessionTTL &&
		live.FlareSolverrAsResponseFallback == d.fsFallback
}

// socksInSync compares the four SOCKS fields Reconcile pushes (enabled/host/
// port/version); the username/password are deliberately not owned by Tsundoku.
func socksInSync(live suwayomi.SuwayomiSettings, d desiredConfig) bool {
	return live.SocksProxyEnabled &&
		live.SocksProxyHost == d.socksHost &&
		live.SocksProxyPort == d.socksPort &&
		live.SocksProxyVersion == d.socksVersion
}

// sameStringSet reports whether a and b contain the same set of strings
// (order- and duplicate-independent) — the repo-drift test.
func sameStringSet(a, b []string) bool {
	sa := make(map[string]struct{}, len(a))
	for _, s := range a {
		sa[s] = struct{}{}
	}
	sb := make(map[string]struct{}, len(b))
	for _, s := range b {
		sb[s] = struct{}{}
	}
	if len(sa) != len(sb) {
		return false
	}
	for s := range sa {
		if _, ok := sb[s]; !ok {
			return false
		}
	}
	return true
}
