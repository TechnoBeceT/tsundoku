package enginetopo

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/ent"
	entharvestedextension "github.com/technobecet/tsundoku/internal/ent/harvestedextension"
	entharvestedrepo "github.com/technobecet/tsundoku/internal/ent/harvestedrepo"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// This file holds the LIVE write-through half of the engine-topology store: when
// the OWNER performs an engine-mutating action through Tsundoku (install/update/
// uninstall an extension, replace the repo list, change FlareSolverr/SOCKS
// config), the durable DB store + apk cache are updated IMMEDIATELY so the store
// never lags a live change — instead of only being re-captured at the next boot
// seed (RunSeed).
//
// Every helper here is BEST-EFFORT and runs AFTER the engine mutation has already
// succeeded: a DB/cache/settings failure is logged and swallowed, NEVER returned,
// so a topology-capture hiccup can never turn the owner's successful engine
// operation into an HTTP 500. None of them logs a secret value — only keys,
// pkgNames, repo urls, and ids.
//
// The extension + repo write-through (OnExtensionInstalled/OnExtensionUninstalled/
// OnReposSet) targets sourceengine.Client/sourceengine.Extension — the P2
// Suwayomi-removal repoint. WriteThroughEngineConfig is UNCHANGED (still
// suwayomi.SuwayomiSettings): it captures the retired Suwayomi settings-proxy's
// post-mutation re-read and is deleted alongside handler/suwayomi in a later
// slice, not here.
//
// The DB→engine RECONCILE direction (re-applying the durable store back onto a
// fresh/rebuilt engine) lives in reconcile.go, not here.

// ConfigWriter is the minimal write surface the engine-config write-through needs
// from the runtime settings overlay: SetMany overwrites the given keys. It is
// narrowed so a caller/test double implements just this one method;
// *settings.Service satisfies it directly.
type ConfigWriter interface {
	SetMany(ctx context.Context, updates []settings.KeyValue) error
}

// OnExtensionInstalled captures a just-installed-or-updated extension into the
// durable topology store: it upserts the HarvestedExtension row and caches the
// .apk bytes via the shared RecordInstalledExtension core.
//
// ext is the extension AS OBSERVED IN THE HANDLER'S POST-MUTATION RE-READ (§16),
// passed in so this issues no redundant client.Extensions() call — ext.Sources
// already carries the source ids RecordInstalledExtension needs, so (unlike the
// retired Suwayomi shape) no live client call is needed here at all. Best-effort:
// a resolution/download/persist failure is logged and swallowed — the engine
// install already succeeded, and the next boot seed will retry the capture.
func OnExtensionInstalled(
	ctx context.Context,
	db *ent.Client,
	cache *apkcache.Store,
	httpGet func(url string) (*http.Response, error),
	ext sourceengine.Extension,
) {
	if err := RecordInstalledExtension(ctx, db, cache, httpGet, ext); err != nil {
		slog.WarnContext(ctx, "enginetopo: write-through could not capture installed extension",
			"pkg_name", ext.PkgName, "repo", repoURLOf(ext), "err", err)
	}
}

// OnExtensionUninstalled removes a just-uninstalled extension from the durable
// store: it deletes the HarvestedExtension row for pkgName and removes its cached
// .apk file. Both are idempotent — a missing row and a missing cache file are not
// errors (uninstalling an already-absent extension is a no-op) — so it is safe to
// call unconditionally on every uninstall. Best-effort: any DB/cache failure is
// logged and swallowed.
//
// The cache file is keyed by (pkgName, version_code); the version is read from the
// row before deletion, so an absent row means there is nothing to remove from the
// cache either (rows and cached bytes are written together by the capture path).
func OnExtensionUninstalled(ctx context.Context, db *ent.Client, cache *apkcache.Store, pkgName string) {
	row, err := db.HarvestedExtension.Query().
		Where(entharvestedextension.PkgName(pkgName)).
		Only(ctx)
	if ent.IsNotFound(err) {
		return // already absent — nothing to remove, nothing to fail on
	}
	if err != nil {
		slog.WarnContext(ctx, "enginetopo: write-through could not load extension to uninstall",
			"pkg_name", pkgName, "err", err)
		return
	}

	// Remove the cached bytes first, then the row. A missing file is a no-op, so a
	// crash between the two still converges (a later uninstall or boot reconciles).
	if err := cache.Remove(pkgName, row.VersionCode); err != nil {
		slog.WarnContext(ctx, "enginetopo: write-through could not remove cached apk on uninstall",
			"pkg_name", pkgName, "version_code", row.VersionCode, "err", err)
	}
	if _, err := db.HarvestedExtension.Delete().
		Where(entharvestedextension.PkgName(pkgName)).
		Exec(ctx); err != nil {
		slog.WarnContext(ctx, "enginetopo: write-through could not delete harvested extension row",
			"pkg_name", pkgName, "err", err)
	}
}

// OnReposSet REPLACES the durable HarvestedRepo set with exactly repos — the
// authoritative list the owner just set through Tsundoku. It upserts every url in
// repos (reusing the seed's upsertRepo) and deletes every HarvestedRepo row whose
// url is no longer present, so a removed repo's row goes. An empty repos slice
// clears every row (the owner cleared the list). Best-effort: any DB failure is
// logged and swallowed.
func OnReposSet(ctx context.Context, db *ent.Client, repos []string) {
	if len(repos) == 0 {
		if _, err := db.HarvestedRepo.Delete().Exec(ctx); err != nil {
			slog.WarnContext(ctx, "enginetopo: write-through could not clear harvested repos", "err", err)
		}
		return
	}

	for _, url := range repos {
		if err := upsertRepo(ctx, db, url); err != nil {
			slog.WarnContext(ctx, "enginetopo: write-through could not upsert harvested repo",
				"repo", url, "err", err)
		}
	}
	if _, err := db.HarvestedRepo.Delete().
		Where(entharvestedrepo.URLNotIn(repos...)).
		Exec(ctx); err != nil {
		slog.WarnContext(ctx, "enginetopo: write-through could not prune removed harvested repos", "err", err)
	}
}

// WriteThroughEngineConfig captures the owner's just-applied engine FlareSolverr +
// SOCKS settings into Tsundoku's settings overlay. This is the OPPOSITE of the
// (now-retired) boot seed's engine-config capture: it is an UNCONDITIONAL
// capture — a plain SetMany that overwrites the keys — because the owner has
// EXPLICITLY changed the config through Tsundoku's Suwayomi settings proxy.
//
// live is the settings AS OBSERVED IN THE HANDLER'S POST-MUTATION RE-READ (§16).
// It reuses the seed's flareSolverrUpdates / socksUpdates key mapping (the SAME
// key list, the SAME SOCKS-off skip, and the SAME deliberate omission of the
// SOCKS username/password — those are not tunables), so the mapping lives in
// exactly one place. FlareSolverr and SOCKS are written as two independent
// batches for the same reason the seed splits them: a stock Suwayomi's empty
// SOCKS port would otherwise sink the FlareSolverr write as collateral.
//
// STILL TARGETS suwayomi.SuwayomiSettings — this is the Suwayomi settings-proxy
// (handler/suwayomi) write-through, unrelated to the sourceengine-backed
// extension/repo write-through above. It does not use SeedDeps.Client and so is
// unaffected by this slice's repoint; handler/suwayomi + this function are
// retired together in a later Suwayomi-removal slice.
//
// Best-effort: a settings-write failure is logged and swallowed, never returned.
func WriteThroughEngineConfig(ctx context.Context, store ConfigWriter, live suwayomi.SuwayomiSettings) {
	if err := store.SetMany(ctx, flareSolverrUpdates(live)); err != nil {
		slog.WarnContext(ctx, "enginetopo: write-through could not capture flaresolverr config", "err", err)
	}
	if socks := socksUpdates(live); socks != nil {
		if err := store.SetMany(ctx, socks); err != nil {
			slog.WarnContext(ctx, "enginetopo: write-through could not capture socks config", "err", err)
		}
	}
}

// flareSolverrUpdates maps the (retired) Suwayomi settings-proxy's FlareSolverr
// settings onto the existing Tsundoku flaresolverr.* tunable keys (QCAT-238).
// These fields are all NON_NULL on Suwayomi's wire, so the batch always carries
// a valid value. Was the boot seed's mapping too (internal/enginetopo/
// seed_config.go, retired by QCAT-253 — the engine has no readable config to
// gap-fill from any more); kept here as WriteThroughEngineConfig's sole caller.
func flareSolverrUpdates(live suwayomi.SuwayomiSettings) []settings.KeyValue {
	return []settings.KeyValue{
		{Key: settings.KeyFlareSolverrEnabled, Value: strconv.FormatBool(live.FlareSolverrEnabled)},
		{Key: settings.KeyFlareSolverrURL, Value: live.FlareSolverrURL},
		{Key: settings.KeyFlareSolverrTimeout, Value: strconv.Itoa(live.FlareSolverrTimeout)},
		{Key: settings.KeyFlareSolverrSessionName, Value: live.FlareSolverrSessionName},
		{Key: settings.KeyFlareSolverrSessionTTL, Value: strconv.Itoa(live.FlareSolverrSessionTTL)},
		{Key: settings.KeyFlareSolverrResponseFallback, Value: strconv.FormatBool(live.FlareSolverrAsResponseFallback)},
	}
}

// socksUpdates maps the (retired) Suwayomi settings-proxy's SOCKS settings onto
// the engine.socks_* tunable keys, or returns nil when SOCKS is off — disabled
// OR a blank port means there is nothing configured to capture (a stock
// Suwayomi reports an empty socksProxyPort). SocksProxyPort is already a
// numeric string on Suwayomi's own wire, so a non-blank value passes straight
// through to the int tunable's validator.
func socksUpdates(live suwayomi.SuwayomiSettings) []settings.KeyValue {
	if !live.SocksProxyEnabled || live.SocksProxyPort == "" {
		return nil
	}
	return []settings.KeyValue{
		{Key: settings.KeyEngineSocksEnabled, Value: strconv.FormatBool(live.SocksProxyEnabled)},
		{Key: settings.KeyEngineSocksHost, Value: live.SocksProxyHost},
		{Key: settings.KeyEngineSocksPort, Value: live.SocksProxyPort},
		{Key: settings.KeyEngineSocksVersion, Value: strconv.Itoa(live.SocksProxyVersion)},
		// SocksProxyUsername / SocksProxyPassword are DELIBERATELY OMITTED: the
		// generic Settings.value column is NOT .Sensitive() and IS exposed
		// verbatim via GET /api/settings, so a SOCKS credential must never become
		// a tunable (contrast SourcePreference.value, which IS .Sensitive() and
		// is the only sanctioned home for a seeded secret).
	}
}
