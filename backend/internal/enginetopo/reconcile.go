package enginetopo

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strconv"

	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/ent"
	entharvestedrepo "github.com/technobecet/tsundoku/internal/ent/harvestedrepo"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// ConfigProvider is the narrow read surface Reconcile needs to push Tsundoku's
// OWN FlareSolverr + SOCKS config onto the engine. It reads Tsundoku's OWN
// runtime settings (never the engine), so it has no sourceengine dependency.
// Kept to precisely the ten typed getters used here so a test double is
// trivial; *settings.Service satisfies it directly.
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
	// InSync is true when the engine already matched the durable store on every
	// DRIFT-DETECTED axis (repos/extensions/prefs), so the pass made zero
	// drift-driven engine mutations and recorded no gaps. See isInSync's doc
	// comment for why ConfigApplied is deliberately excluded from this check.
	InSync bool
	// ReposSet is true when the engine's extension-repo list was PUSHED the
	// UNION of the durable HarvestedRepo set and the engine's own live list
	// (the durable set knew a repo the engine didn't have). reconcileRepos is
	// ADDITIVE-ONLY: it never removes a repo the engine already has, so
	// ReposSet is false whenever the durable set is a subset of (or equal to)
	// the engine's live list, even if the DB's own record is incomplete.
	ReposSet bool
	// ExtensionsInstalled is the number of required-but-missing extensions the
	// pass installed via InstallExtension.
	ExtensionsInstalled int
	// PrefsApplied is the number of individual (source,key) preference values
	// that were pushed — the sum, across every source with at least one drifted
	// key, of the keys included in that source's ONE batched SetPreferences
	// call (see reconcilePrefs's doc comment on batching).
	PrefsApplied int
	// ConfigApplied is true when the unconditional FlareSolverr+SOCKS config
	// push succeeded (both SetFlareSolverr and SetSocks returned no error).
	// Config performs NO drift detection (see reconcileConfig's doc comment) —
	// it is pushed on EVERY pass by design — so ConfigApplied being true does
	// NOT by itself indicate InSync.
	ConfigApplied bool
	// Gaps holds every per-item failure that was ISOLATED (a failed install, an
	// unparseable stored preference, a failed source's preference push, a
	// config push error, …). Each is logged and recorded here; none aborts the
	// rest of the pass. A non-empty Gaps means the pass is not fully in sync
	// even if every other field is zero.
	Gaps []error
}

// Reconcile pushes Tsundoku's durable engine-topology store (HarvestedRepo /
// HarvestedExtension / SourcePreference / the owned FlareSolverr+SOCKS config)
// back onto the live engine — the INVERSE of the boot seed passes (which capture
// engine→DB). It is how a wiped, swapped, or freshly-rebuilt engine is restored
// to the topology Tsundoku remembers.
//
// It runs the drift-detect + reapply steps in order: repos → extensions →
// source preferences → config. The first three READ the engine to detect drift
// and only MUTATE when the engine differs; config is a deliberate exception (see
// reconcileConfig) — an unconditional push every pass, "Tsundoku is reality"
// (QCAT-250). Against an already-in-sync engine (repos/extensions/prefs) the
// pass issues zero DRIFT-DRIVEN mutations and returns InSync=true (config still
// pushes, but is excluded from that check — see isInSync).
//
// EXTENSION INSTALL IS REPO-BASED. sourceengine.Client.InstallExtension installs
// by pkgName (resolved against the configured repos) after a RefreshExtensions
// refresh of the repo list — there is no apkUrl fallback wired here (the
// apk-cache-backed install path is DEFERRED, tracked as future work). The apk
// byte cache (cache, apk_cached) is therefore the durability RECORD (it
// guarantees the bytes still exist for a future engine-host install-from-cache
// path), NOT something this pass installs from; cache is unused here by design.
// A dead upstream repo means that pkg's install may fail, which is isolated as
// a gap (below).
//
// FAULT ISOLATION. A per-item failure — one extension that won't install, one
// preference that won't parse, one source's preference push, a config push
// error — is logged (slog.WarnContext) and recorded in Gaps; the remaining
// items still apply. Only an ENUMERATING failure returns a hard error: it
// leaves the pass unable to even determine drift, so it must not silently do
// nothing (mirrors SeedExtensions' per-item vs enumerating distinction). The
// enumerating calls are: querying the library's required-extension set +
// stored preferences (DB), and listing the engine's installed extensions + repo
// list (engine). Preference pushes are batched ONE PER SOURCE (see
// reconcilePrefs) — a push failure isolates the whole source's batch, not each
// individual key; a per-row COERCION failure (a stored value the live pref's
// kind can't parse) is still isolated per-key, before any network call.
//
// PRECONDITION — SEED BEFORE RECONCILE (extensions + preferences only).
// Reconcile trusts Tsundoku's DB as the authoritative record of the library's
// REQUIRED extensions and preferences, so the boot seed passes (SeedExtensions
// + SeedSourcePreferences) SHOULD have run first to CAPTURE that state before
// Reconcile pushes it back for those two axes — running it against a live,
// un-captured engine means reconcileExtensions/reconcilePrefs see an
// incomplete durable snapshot (fewer installs / fewer pushed preferences than
// a fully-captured DB would produce). Neither axis is destructive either way:
// reconcileExtensions only INSTALLS required-but-missing packages (never
// uninstalls one the engine already has), and reconcilePrefs only pushes
// STORED keys onto matching LIVE keys (never touches a live preference the DB
// has no row for) — an un-captured DB under-reconciles, it does not regress
// the engine.
//
// REPOS ARE THE EXCEPTION THAT NEEDS NO PRECONDITION AT ALL. reconcileRepos is
// ADDITIVE-ONLY (see its doc comment): it pushes the UNION of the durable
// HarvestedRepo set and the engine's own live repo list, so a stale or
// un-captured DB (restored from an old backup, or a fresh Tsundoku pointed at
// an already-configured engine) can only ADD repos the engine is missing,
// never drop ones it already has. This is exactly what makes it safe for
// cmd/tsundoku/main.go's startEngineTopo to run Reconcile on EVERY boot BEFORE
// RunSeed's capture pass — repos self-heal regardless of ordering.
//
// Config has NO such precondition either: it is pushed straight from
// Tsundoku's OWN settings (ConfigProvider), never a captured engine snapshot
// (there is no engine-config seed any more — Tsundoku owns this config from
// the start, QCAT-250). Sequencing seed → reconcile for extensions/preferences
// is the caller's responsibility, not something Reconcile can detect. The
// forward path — provisioning Tsundoku's OWN internal engine, which starts
// empty — satisfies it naturally. Ratified as decision QCAT-250.
func Reconcile(
	ctx context.Context,
	client sourceengine.Client,
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

// isInSync reports whether the pass made no DRIFT-DETECTED engine mutation AND
// recorded no gap — the honest "nothing needed changing" answer over the three
// axes that actually detect drift (repos/extensions/prefs). Config is EXCLUDED:
// reconcileConfig no longer reads the engine to compare (see its doc comment),
// so it "applies" (issues a PUT) on every single pass by design — folding it
// into this check would make InSync=true permanently unreachable.
func isInSync(res ReconcileResult) bool {
	return !res.ReposSet &&
		res.ExtensionsInstalled == 0 &&
		res.PrefsApplied == 0 &&
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
func installedPkgSet(ctx context.Context, client sourceengine.Client) (map[string]bool, error) {
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

// reconcileRepos is ADDITIVE-ONLY: it computes the UNION of the durable
// HarvestedRepo set and the engine's OWN live repo list, and pushes that union
// via client.SetRepos ONLY when the union differs from the engine's current
// list — i.e. only when the DB knows a repo the engine doesn't have yet. When
// dbRepos is a subset of (or equal to) engineRepos, the union equals
// engineRepos and reconcileRepos makes NO call: the engine already has
// everything the DB knows, possibly plus extras, and those extras must never
// be dropped just because the DB hasn't captured them.
//
// This is deliberately NOT a "make the engine match the DB" replace: unlike
// extensions/preferences (see Reconcile's PRECONDITION doc comment), Reconcile
// now runs on EVERY boot BEFORE the seed captures the engine's true repo list
// (cmd/tsundoku/main.go startEngineTopo), so a DB that is a stale or partial
// snapshot of the engine's real repos (restored from an old backup, or a
// fresh Tsundoku pointed at an already-configured engine) must never REPLACE
// the engine's list with its own incomplete one — that would silently wipe
// the un-captured repos, and the next RunSeed would re-capture the wiped
// (smaller) list, making the loss self-reinforcing and permanently
// undetectable (InSync=true forever after).
//
// Reading either list failing is an enumerating error; a SetRepos write
// failure is isolated as a gap (changed stays false, so the extension step
// still runs but skips the repo-driven refresh).
func reconcileRepos(ctx context.Context, client sourceengine.Client, db *ent.Client) (bool, []error, error) {
	dbRepos, err := db.HarvestedRepo.Query().Select(entharvestedrepo.FieldURL).Strings(ctx)
	if err != nil {
		return false, nil, fmt.Errorf("enginetopo.Reconcile: query harvested repos: %w", err)
	}
	engineRepos, err := client.Repos(ctx)
	if err != nil {
		return false, nil, fmt.Errorf("enginetopo.Reconcile: list engine repos: %w", err)
	}
	union := unionStringSet(dbRepos, engineRepos)
	if sameStringSet(union, engineRepos) {
		return false, nil, nil
	}
	if _, err := client.SetRepos(ctx, union); err != nil {
		slog.WarnContext(ctx, "enginetopo: reconcile could not set engine repos", "err", err)
		return false, []error{fmt.Errorf("set engine repos: %w", err)}, nil
	}
	return true, nil, nil
}

// reconcileExtensions installs every required-but-missing extension. It first
// refreshes the engine's available list from the repos (RefreshExtensions) when
// there is anything to do — a repo change OR a missing pkg — so a just-set repo's
// extensions become installable. When nothing is missing and repos did not
// change it is a pure no-op (zero mutations, honouring idempotency). A per-pkg
// install failure is isolated as a gap.
func reconcileExtensions(
	ctx context.Context,
	client sourceengine.Client,
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
	if _, err := client.RefreshExtensions(ctx); err != nil {
		// The repo-cache refresh failed; the installs below may still succeed from a
		// prior cache, so record the gap and continue rather than abort.
		slog.WarnContext(ctx, "enginetopo: reconcile could not refresh extensions from repos", "err", err)
		gaps = append(gaps, fmt.Errorf("refresh extensions: %w", err))
	}

	count := 0
	for _, pkg := range missing {
		// apkURL "" — REPO-based install (resolved by the engine host against its
		// configured repos); the apk-cache fallback is deferred (see Reconcile's
		// doc comment).
		if _, err := client.InstallExtension(ctx, pkg, ""); err != nil {
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
// drifted from the durable value back onto its source, addressed by KEY — the
// engine host's SetPreferences write is key-addressed (no Position machinery;
// contrast the retired Suwayomi GraphQL union, which required a 0-based array
// position). Loading the stored rows failing is an enumerating error; a
// per-source read failure, a per-row coercion failure, or a per-source WRITE
// failure is isolated as a gap.
//
// BATCHING (a real behaviour change from the per-preference Suwayomi writes):
// every drifted key for a given source is collected into ONE map and pushed via
// a SINGLE SetPreferences(sourceID, changes) call — the engine host's own
// Preferences.apply also applies a whole batch atomically-per-call. This means
// fault isolation is now per-SOURCE for the network write (a batch failure gaps
// every key in it together), not per-KEY as before; a per-row COERSION failure
// (a stored value that fails to parse for the live pref's kind) is still
// isolated per-key BEFORE the batch is built, so one unparseable stored value
// never blocks its sibling keys on the same source from being pushed.
//
// ACCEPTED TRADE-OFF — a network/validation-level SetPreferences failure (as
// opposed to a pre-batch coercion failure) is isolated per-SOURCE, not
// per-KEY: sourceengine.Client.SetPreferences returns exactly ONE error for
// the whole call, so Reconcile has no way to see whether an engine that
// validates keys individually still partially applied some of them before
// reporting the failure — forcing a per-key push isn't warranted by that
// uncertainty alone. This is safe rather than lossy because every pass reads
// LIVE preference values fresh: whatever a partial-apply engine actually
// accepted shows up in-sync on the NEXT pass and stops being retried, while a
// genuinely still-drifted key keeps being retried (and keeps gapping) until
// its underlying issue is fixed — nothing vanishes across passes, it is at
// worst deferred one boot. See
// TestReconcile_MixedBatchKeyRejectionIsolatedWithoutLosingSiblingIntent.
func reconcilePrefs(ctx context.Context, client sourceengine.Client, db *ent.Client) (int, []error, error) {
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
		live, err := client.Preferences(ctx, sourceID)
		if err != nil {
			slog.WarnContext(ctx, "enginetopo: reconcile could not read source preferences, skipping source",
				"source_id", sourceID, "err", err)
			gaps = append(gaps, fmt.Errorf("read source %d preferences: %w", sourceID, err))
			continue
		}

		pending, changes, buildGaps := buildSourceChanges(sourceID, stored, indexPrefsByKey(live))
		gaps = append(gaps, buildGaps...)
		if pending == 0 {
			continue
		}
		if _, err := client.SetPreferences(ctx, sourceID, changes); err != nil {
			slog.WarnContext(ctx, "enginetopo: reconcile could not set preferences, recording gap",
				"source_id", sourceID, "err", err)
			gaps = append(gaps, fmt.Errorf("set source %d preferences: %w", sourceID, err))
			continue
		}
		applied += pending
	}
	return applied, gaps, nil
}

// indexPrefsByKey maps live preferences by their Key (skipping keyless ones,
// which cannot be matched to a stored (source_id, key) row).
func indexPrefsByKey(live []sourceengine.Preference) map[string]sourceengine.Preference {
	byKey := make(map[string]sourceengine.Preference, len(live))
	for _, p := range live {
		if p.Key != "" {
			byKey[p.Key] = p
		}
	}
	return byKey
}

// buildSourceChanges walks one source's stored preferences against its live
// values and returns the count of keys that need pushing, the key->coerced-
// value batch to send in ONE SetPreferences call, and any per-row gaps hit
// while building it. A stored key with no live match is silently skipped (the
// option no longer exists — not a gap); a stored value already equal to the
// live value is left out of the batch (idempotency); a value that fails to
// coerce for the live pref's kind is isolated as a gap and left out of the
// batch (its sibling keys on the same source are unaffected).
func buildSourceChanges(
	sourceID int64,
	stored []*ent.SourcePreference,
	liveByKey map[string]sourceengine.Preference,
) (int, map[string]any, []error) {
	changes := make(map[string]any)
	var gaps []error
	for _, row := range stored {
		live, ok := liveByKey[row.Key]
		if !ok || prefInSync(row.Value, live) {
			continue
		}
		value, err := coercePrefValue(live.Type, row.Value)
		if err != nil {
			slog.Warn("enginetopo: reconcile could not build preference value, recording gap",
				"source_id", sourceID, "key", row.Key, "err", err)
			gaps = append(gaps, fmt.Errorf("build preference %q for source %d: %w", row.Key, sourceID, err))
			continue
		}
		changes[row.Key] = value
	}
	return len(changes), changes, gaps
}

// prefInSync reports whether a stored value already matches the live preference's
// current value. Both are compared in their ENCODED string form (the live pref
// through encodePreferenceValue, the same function the seed captured it with), so
// the comparison is symmetric with how the value was stored. A live pref with no
// current value set (encode ok=false) is never "in sync" with a stored value, so
// the stored value is (re)pushed.
//
// MULTISELECT IS ORDER-INSENSITIVE. The engine's backing value is a
// Set<String> with no stable iteration order, so a byte-for-byte JSON-array
// string compare would treat a merely-reordered (semantically unchanged) set
// as drifted and re-push it EVERY reconcile pass. encodePreferenceValue
// already sorts a freshly-captured value (see its doc comment), which covers
// the live side here — but the STORED side may be an older row captured
// before that canonicalization (or written any other way), so both sides are
// re-canonicalized here via multiSelectSetsEqual rather than trusting the
// write path alone.
func prefInSync(storedValue string, live sourceengine.Preference) bool {
	liveValue, _, ok := encodePreferenceValue(live)
	if !ok {
		return false
	}
	if live.Type == sourceengine.PreferenceMultiSelect {
		return multiSelectSetsEqual(storedValue, liveValue)
	}
	return storedValue == liveValue
}

// multiSelectSetsEqual reports whether two JSON-array-encoded MultiSelect
// values hold the SAME SET of strings, ignoring order (see prefInSync's doc
// comment for why). Malformed JSON on either side is never "in sync" — it
// falls through to a re-push, which self-heals on the next successful
// capture rather than getting stuck.
func multiSelectSetsEqual(storedJSON, liveJSON string) bool {
	stored, err := sortedStringSet(storedJSON)
	if err != nil {
		return false
	}
	live, err := sortedStringSet(liveJSON)
	if err != nil {
		return false
	}
	return slices.Equal(stored, live)
}

// sortedStringSet JSON-decodes raw as a []string and returns a sorted copy,
// leaving the input untouched.
func sortedStringSet(raw string) ([]string, error) {
	var list []string
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil, err
	}
	sorted := append([]string(nil), list...)
	slices.Sort(sorted)
	return sorted, nil
}

// coercePrefValue REVERSES encodePreferenceValue: it turns a stored string value
// back into the JSON-native Go value (bool/string/[]string) the engine host's
// SetPreferences wants for the live pref's variant. The kind comes from the
// LIVE pref (it is what the engine host's Preferences.apply coerces against),
// not the stored value_type. A parse failure or unknown kind is an error the
// caller isolates as a gap.
func coercePrefValue(kind, stored string) (any, error) {
	switch kind {
	case sourceengine.PreferenceCheckBox, sourceengine.PreferenceSwitchCompat:
		b, err := strconv.ParseBool(stored)
		if err != nil {
			return nil, fmt.Errorf("parse bool %q: %w", stored, err)
		}
		return b, nil
	case sourceengine.PreferenceList, sourceengine.PreferenceEditText:
		return stored, nil
	case sourceengine.PreferenceMultiSelect:
		var list []string
		if err := json.Unmarshal([]byte(stored), &list); err != nil {
			return nil, fmt.Errorf("parse multiselect %q: %w", stored, err)
		}
		return list, nil
	default:
		return nil, fmt.Errorf("unknown preference kind %q", kind)
	}
}

// reconcileConfig pushes Tsundoku's owned FlareSolverr + SOCKS config onto the
// engine UNCONDITIONALLY, every pass — no drift READ, no comparison. This is a
// deliberate simplification from the retired Suwayomi settings proxy (which had
// a `settings` query to read-before-write): sourceengine.Client exposes ONLY
// SetFlareSolverr/SetSocks (both PUT, no matching GET), so there is nothing to
// compare against. "Tsundoku is reality" (QCAT-250): the durable ConfigProvider
// IS the desired state, so pushing it every reconcile is just an idempotent PUT
// of owned config — the engine converges to it regardless of what it currently
// holds. The loss of drift detection here is intentional, not an oversight (see
// isInSync, which excludes this step from InSync accordingly).
//
// Both SetFlareSolverr and SetSocks are attempted independently so a SOCKS
// failure never blocks the FlareSolverr push (or vice versa); either failure
// is isolated as its own gap. ConfigApplied reports whether BOTH calls
// succeeded.
func reconcileConfig(ctx context.Context, client sourceengine.Client, cfg ConfigProvider) (bool, []error) {
	desired := snapshotConfig(ctx, cfg)

	var gaps []error
	if _, err := client.SetFlareSolverr(ctx, desired.flarePatch()); err != nil {
		slog.WarnContext(ctx, "enginetopo: reconcile could not push flaresolverr config, recording gap", "err", err)
		gaps = append(gaps, fmt.Errorf("set flaresolverr config: %w", err))
	}
	if _, err := client.SetSocks(ctx, desired.socksPatch()); err != nil {
		slog.WarnContext(ctx, "enginetopo: reconcile could not push socks config, recording gap", "err", err)
		gaps = append(gaps, fmt.Errorf("set socks config: %w", err))
	}
	return len(gaps) == 0, gaps
}

// desiredConfig is a one-shot snapshot of Tsundoku's owned FlareSolverr + SOCKS
// config, read from the ConfigProvider once so the two patches below are built
// from identical values (and issue no repeated DB reads). socksPort is stored in
// its wire form (a numeric string) to match sourceengine.SocksPatch.Port.
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

// flarePatch builds the FlareSolverrPatch carrying every field of this
// desiredConfig — ALL-POINTER but every pointer is always non-nil (the
// unconditional-push design has nothing to omit).
func (d desiredConfig) flarePatch() sourceengine.FlareSolverrPatch {
	return sourceengine.FlareSolverrPatch{
		Enabled:            &d.fsEnabled,
		URL:                &d.fsURL,
		Session:            &d.fsSessionName,
		SessionTTL:         &d.fsSessionTTL,
		Timeout:            &d.fsTimeout,
		AsResponseFallback: &d.fsFallback,
	}
}

// socksPatch builds the SocksPatch carrying this desiredConfig's enabled/host/
// port/version — ALWAYS, including when SOCKS is disabled (Tsundoku's own
// "off" state is itself the desired state to push; "Tsundoku is reality").
// Username/Password are deliberately left nil: Tsundoku never seeds or owns the
// SOCKS credentials (mirrors the retired seed's same omission).
func (d desiredConfig) socksPatch() sourceengine.SocksPatch {
	enabled := d.socksEnabled
	return sourceengine.SocksPatch{
		Enabled: &enabled,
		Host:    &d.socksHost,
		Port:    &d.socksPort,
		Version: &d.socksVersion,
	}
}

// unionStringSet returns the deduplicated union of a and b, sorted for a
// stable, order-insensitive result — reconcileRepos's ADDITIVE target list.
// Sorting means the result (and therefore whether reconcileRepos even issues
// a SetRepos call) never depends on either input's original ordering.
func unionStringSet(a, b []string) []string {
	set := make(map[string]struct{}, len(a)+len(b))
	for _, s := range a {
		set[s] = struct{}{}
	}
	for _, s := range b {
		set[s] = struct{}{}
	}
	union := make([]string, 0, len(set))
	for s := range set {
		union = append(union, s)
	}
	slices.Sort(union)
	return union
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
