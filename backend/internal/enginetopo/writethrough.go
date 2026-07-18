package enginetopo

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/ent"
	entharvestedextension "github.com/technobecet/tsundoku/internal/ent/harvestedextension"
	entharvestedrepo "github.com/technobecet/tsundoku/internal/ent/harvestedrepo"
	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// This file holds the LIVE write-through half of the engine-topology store: when
// the OWNER performs an engine-mutating action through Tsundoku (install/update/
// uninstall an extension, replace the repo list), the durable DB store + apk
// cache are updated IMMEDIATELY so the store never lags a live change — instead
// of only being re-captured at the next boot seed (RunSeed).
//
// Every helper here is BEST-EFFORT and runs AFTER the engine mutation has already
// succeeded: a DB/cache failure is logged and swallowed, NEVER returned, so a
// topology-capture hiccup can never turn the owner's successful engine
// operation into an HTTP 500. None of them logs a secret value — only keys,
// pkgNames, repo urls, and ids.
//
// The extension + repo write-through (OnExtensionInstalled/OnExtensionUninstalled/
// OnReposSet) targets sourceengine.Client/sourceengine.Extension — the P2
// Suwayomi-removal repoint. The FlareSolverr/SOCKS config write-through
// (formerly WriteThroughEngineConfig, targeting suwayomi.SuwayomiSettings) is
// RETIRED (P2 slice 6) alongside handler/suwayomi: FlareSolverr is now pushed
// via handler/flaresolverr's best-effort mirror straight onto
// sourceengine.Client.SetFlareSolverr (no durable capture needed — Tsundoku's
// settings overlay is already the source of truth for that config, not a
// mirror of it). SOCKS runtime-push is DEFERRED to reconcile-on-boot (a later
// slice); a runtime SOCKS edit reaches the engine on the next reconcile.
//
// The DB→engine RECONCILE direction (re-applying the durable store back onto a
// fresh/rebuilt engine) lives in reconcile.go, not here.

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
	retained int,
) {
	if err := RecordInstalledExtension(ctx, db, cache, httpGet, ext, retained); err != nil {
		slog.WarnContext(ctx, "enginetopo: write-through could not capture installed extension",
			"pkg_name", ext.PkgName, "repo", repoURLOf(ext), "err", err)
	}
}

// OnExtensionReinstalled records an owner-initiated reinstall of a HELD (older)
// version from the apk cache — the reversible-update rollback path. Unlike
// OnExtensionInstalled it does NO repo fetch (the requested version's bytes are
// already cached; re-resolving from the repo would re-download the LATEST, which
// is exactly the version the owner is rolling AWAY from, and would fail outright
// when the repo is down — the very scenario a rollback exists for). It just
// pins installed_version_code to the reinstalled version and re-prunes the held
// set with that version as the always-keep anchor.
//
// Best-effort (mirrors the other write-throughs): the engine install already
// succeeded; a DB/cache hiccup here is logged and swallowed, never surfaced. The
// row is expected to exist (the reinstall endpoint validated the version against
// cached_versions before calling), so an absent row is logged as an anomaly.
func OnExtensionReinstalled(
	ctx context.Context,
	db *ent.Client,
	cache *apkcache.Store,
	pkgName string,
	versionCode, retained int,
) {
	row, err := db.HarvestedExtension.Query().
		Where(entharvestedextension.PkgName(pkgName)).
		Only(ctx)
	if err != nil {
		slog.WarnContext(ctx, "enginetopo: reinstall write-through could not load extension row",
			"pkg_name", pkgName, "version_code", versionCode, "err", err)
		return
	}
	// The reinstalled version is now the running one — pin it as the prune anchor
	// (newName "" ⇒ buildCachedVersions keeps the stored name) so it survives even
	// if it is older than the newest N.
	cachedVersions := pruneAndBuildCachedVersions(
		ctx, db, cache, pkgName, retained, versionCode, "", versionCode,
	)
	if err := db.HarvestedExtension.UpdateOne(row).
		SetInstalledVersionCode(versionCode).
		SetCachedVersions(cachedVersions).
		Exec(ctx); err != nil {
		slog.WarnContext(ctx, "enginetopo: reinstall write-through could not update extension row",
			"pkg_name", pkgName, "version_code", versionCode, "err", err)
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
