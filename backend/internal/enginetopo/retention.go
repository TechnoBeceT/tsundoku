package enginetopo

import (
	"context"
	"log/slog"
	"time"

	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/ent"
	entharvestedextension "github.com/technobecet/tsundoku/internal/ent/harvestedextension"
)

// defaultRetainedVersions is the apk-cache rollback-history depth used when no
// retention resolver is wired (e.g. a focused test). It mirrors the config /
// settings default (extensions.retained_versions = 3) so the two never drift.
const defaultRetainedVersions = 3

// resolveRetained resolves the number of .apk versions to keep per extension
// from an optional resolver, defaulting to defaultRetainedVersions when the
// resolver is nil or returns a non-positive value (a prune must always keep at
// least the current build).
func resolveRetained(ctx context.Context, fn func(context.Context) int) int {
	if fn == nil {
		return defaultRetainedVersions
	}
	if n := fn(ctx); n >= 1 {
		return n
	}
	return defaultRetainedVersions
}

// pruneAndBuildCachedVersions prunes pkg's cached .apk files to the newest
// `retained` versions ∪ the installed version, then returns the refreshed
// held-version set to store on the row — merging the existing stored version
// NAMES with the just-cached newVersion (named newName). Best-effort: a prune
// failure is logged and the held set falls back to just the newly-cached
// version, so the stored set never advertises a version whose bytes were not
// actually kept on disk.
//
// installedVersion is passed to Prune as the always-keep guard, so the engine's
// currently-running .apk is never evicted even when it is older than the newest
// N (the reversible-update invariant).
func pruneAndBuildCachedVersions(
	ctx context.Context,
	db *ent.Client,
	cache *apkcache.Store,
	pkgName string,
	retained, newVersion int,
	newName string,
	installedVersion int,
) []apkcache.CachedVersion {
	existing := loadCachedVersions(ctx, db, pkgName)
	retainedVers, err := cache.Prune(pkgName, retained, installedVersion)
	if err != nil {
		slog.WarnContext(ctx, "enginetopo: could not prune cached apk versions",
			"pkg_name", pkgName, "err", err)
		retainedVers = []int{newVersion}
	}
	return buildCachedVersions(existing, retainedVers, newVersion, newName, time.Now())
}

// buildCachedVersions is the PURE merge (unit-tested in isolation) producing the
// held-version set a HarvestedExtension row stores: for each retained version
// code (already newest-first) it emits a CachedVersion, reusing the version name
// + original cachedAt from the existing stored set when known, and stamping the
// just-cached newVersion with newName (a blank newName keeps any previously
// stored name). A retained version with no prior record and that is not
// newVersion is emitted with an empty name + the current time (it was cached out
// of band; the display simply lacks its name until the next harvest).
func buildCachedVersions(
	existing []apkcache.CachedVersion,
	retained []int,
	newVersion int,
	newName string,
	now time.Time,
) []apkcache.CachedVersion {
	byVer := make(map[int]apkcache.CachedVersion, len(existing))
	for _, cv := range existing {
		byVer[cv.VersionCode] = cv
	}

	nv := apkcache.CachedVersion{VersionCode: newVersion, VersionName: newName, CachedAt: now}
	if prev, ok := byVer[newVersion]; ok {
		nv.CachedAt = prev.CachedAt // preserve the original cache time
		if newName == "" {
			nv.VersionName = prev.VersionName
		}
	}
	byVer[newVersion] = nv

	out := make([]apkcache.CachedVersion, 0, len(retained))
	for _, v := range retained {
		cv, ok := byVer[v]
		if !ok {
			cv = apkcache.CachedVersion{VersionCode: v, CachedAt: now}
		}
		out = append(out, cv)
	}
	return out
}

// loadCachedVersions reads a HarvestedExtension's stored held-version set by
// pkgName, returning nil when there is no row yet (a fresh extension) or on a
// read error (best-effort: a failed read must not abort the surrounding capture,
// it just starts the merge from an empty prior set).
func loadCachedVersions(ctx context.Context, db *ent.Client, pkgName string) []apkcache.CachedVersion {
	row, err := db.HarvestedExtension.Query().
		Where(entharvestedextension.PkgName(pkgName)).
		Only(ctx)
	if err != nil {
		return nil
	}
	return row.CachedVersions
}
