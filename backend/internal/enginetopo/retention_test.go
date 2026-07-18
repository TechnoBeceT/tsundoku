package enginetopo_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/enginetopo"
	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	entharvestedextension "github.com/technobecet/tsundoku/internal/ent/harvestedextension"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	sourceenginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
)

// TestSeedExtensions_RetentionPrunesAndRecordsHeldSet proves a seed pass, after
// caching the newly-installed version, prunes the package's cached .apks to the
// newest N ∪ the installed version AND records the resulting held set in
// cached_versions — the durable rollback history the UI lists.
func TestSeedExtensions_RetentionPrunesAndRecordsHeldSet(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	const repo = "https://repo.test/repo"
	const indexURL = "https://repo.test/repo/index.min.json"
	const apkURL = "https://repo.test/repo/apk/pkg.one-v5.apk"

	// Pre-populate the cache with three older versions of pkg.one, as if prior
	// updates had left them behind.
	for _, v := range []int{1, 2, 3} {
		if _, _, err := cache.Put("pkg.one", v, strings.NewReader("old")); err != nil {
			t.Fatalf("seed cache v%d: %v", v, err)
		}
	}

	stub := &stubHTTP{routes: map[string]stubResp{
		indexURL: {status: 200, body: []byte(`[{"pkg":"pkg.one","apk":"pkg.one-v5.apk","code":5}]`)},
		apkURL:   {status: 200, body: []byte("APK-V5")},
	}}
	client := sourceenginefake.New(
		sourceenginefake.WithRepos([]string{repo}),
		sourceenginefake.WithExtensions([]sourceengine.Extension{
			installedExt("pkg.one", repo, 5, sourceengine.Source{ID: 111}),
		}),
	)

	// retained = 2 → keep newest 2 (v5, v3) ∪ the installed version (5).
	if _, err := enginetopo.SeedExtensions(ctx, client, db, cache, stub.get, func(context.Context) int { return 2 }); err != nil {
		t.Fatalf("SeedExtensions: %v", err)
	}

	// Files: v5 + v3 kept; v1 + v2 pruned.
	assertCached(t, cache, map[int]bool{5: true, 3: true, 2: false, 1: false})

	// cached_versions records exactly the held set {5, 3}, newest-first.
	row, err := db.HarvestedExtension.Query().Where(entharvestedextension.PkgName("pkg.one")).Only(ctx)
	if err != nil {
		t.Fatalf("load row: %v", err)
	}
	got := heldVersionCodes(row.CachedVersions)
	sort.Sort(sort.Reverse(sort.IntSlice(got)))
	if len(got) != 2 || got[0] != 5 || got[1] != 3 {
		t.Fatalf("cached_versions = %v, want [5 3]", got)
	}
	// The just-cached version carries its name from the index/ext.
	if name := heldVersionName(row.CachedVersions, 5); name != "1.0.0" {
		t.Errorf("v5 versionName = %q, want 1.0.0", name)
	}
}

// assertCached checks each version's presence on disk against want.
func assertCached(t *testing.T, cache *apkcache.Store, want map[int]bool) {
	t.Helper()
	for v, exp := range want {
		if got := cache.Exists("pkg.one", v); got != exp {
			t.Errorf("cache.Exists(v%d) = %v, want %v", v, got, exp)
		}
	}
}

// heldVersionCodes extracts the version codes from a held set.
func heldVersionCodes(held []apkcache.CachedVersion) []int {
	out := make([]int, 0, len(held))
	for _, cv := range held {
		out = append(out, cv.VersionCode)
	}
	return out
}

// heldVersionName returns the stored name for a version code ("" if absent).
func heldVersionName(held []apkcache.CachedVersion, version int) string {
	for _, cv := range held {
		if cv.VersionCode == version {
			return cv.VersionName
		}
	}
	return ""
}
