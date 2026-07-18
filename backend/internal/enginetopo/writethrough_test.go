package enginetopo_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/enginetopo"
	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/ent"
	entharvestedextension "github.com/technobecet/tsundoku/internal/ent/harvestedextension"
	entharvestedrepo "github.com/technobecet/tsundoku/internal/ent/harvestedrepo"
	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// TestOnExtensionInstalled proves the live write-through captures a just-installed
// extension into the durable store: the HarvestedExtension row exists and the
// .apk bytes are cached (via the SAME RecordInstalledExtension core the seed uses).
func TestOnExtensionInstalled(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	const repo = "https://repo.test/repo"
	apkBytes := []byte("APK-INSTALL")
	stub := &stubHTTP{routes: map[string]stubResp{
		"https://repo.test/repo/index.min.json": {status: 200, body: []byte(`[{"pkg":"pkg.one","apk":"one.apk","code":4}]`)},
		"https://repo.test/repo/apk/one.apk":    {status: 200, body: apkBytes},
	}}

	enginetopo.OnExtensionInstalled(ctx, db, cache, stub.get, installedExt("pkg.one", repo, 4, sourceengine.Source{ID: 7}), 3)

	row := db.HarvestedExtension.Query().Where(entharvestedextension.PkgName("pkg.one")).OnlyX(ctx)
	assertCachedExtension(t, row, hexSHA(apkBytes), 4, []int64{7})
	assertCachedBytes(t, cache, "pkg.one", 4, apkBytes)
}

// TestOnExtensionUninstalled proves the write-through removes a just-uninstalled
// extension's row AND its cached apk, and that a SECOND call (already absent) is
// an idempotent no-op that touches nothing and never panics.
func TestOnExtensionUninstalled(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	const repo = "https://repo.test/repo"
	stub := &stubHTTP{routes: map[string]stubResp{
		"https://repo.test/repo/index.min.json": {status: 200, body: []byte(`[{"pkg":"pkg.one","apk":"one.apk","code":2}]`)},
		"https://repo.test/repo/apk/one.apk":    {status: 200, body: []byte("APK")},
	}}

	// Seed a real row + cached file via the capture path, then uninstall it.
	enginetopo.OnExtensionInstalled(ctx, db, cache, stub.get, installedExt("pkg.one", repo, 2, sourceengine.Source{ID: 1}), 3)
	if !cache.Exists("pkg.one", 2) {
		t.Fatal("precondition: apk not cached before uninstall")
	}

	enginetopo.OnExtensionUninstalled(ctx, db, cache, "pkg.one")

	if ok, _ := db.HarvestedExtension.Query().Where(entharvestedextension.PkgName("pkg.one")).Exist(ctx); ok {
		t.Error("HarvestedExtension row still present after uninstall, want deleted")
	}
	if cache.Exists("pkg.one", 2) {
		t.Error("cached apk still present after uninstall, want removed")
	}

	// A second uninstall of the now-absent extension must be a clean no-op.
	enginetopo.OnExtensionUninstalled(ctx, db, cache, "pkg.one")
}

// TestOnReposSet proves the repo write-through is a FULL REPLACE: pre-seeded
// {A,B} becomes exactly {B,C} — A (no longer present) is deleted and C (new) is
// added.
func TestOnReposSet(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	const a = "https://a.test/repo"
	const b = "https://b.test/repo"
	const c = "https://c.test/repo"

	// Pre-seed {A,B}.
	enginetopo.OnReposSet(ctx, db, []string{a, b})
	assertRepoSet(t, db, []string{a, b})

	// Replace with {B,C}: A must be pruned, C added, B kept.
	enginetopo.OnReposSet(ctx, db, []string{b, c})
	assertRepoSet(t, db, []string{b, c})
}

// TestOnReposSet_EmptyClearsAll proves an empty replacement list clears every row.
func TestOnReposSet_EmptyClearsAll(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	enginetopo.OnReposSet(ctx, db, []string{"https://a.test/repo", "https://b.test/repo"})
	enginetopo.OnReposSet(ctx, db, nil)
	assertRepoSet(t, db, nil)
}

// assertRepoSet fails unless the HarvestedRepo table holds exactly want (order-
// independent).
func assertRepoSet(t *testing.T, db *ent.Client, want []string) {
	t.Helper()
	rows, err := db.HarvestedRepo.Query().Select(entharvestedrepo.FieldURL).Strings(context.Background())
	if err != nil {
		t.Fatalf("query repos: %v", err)
	}
	got := make(map[string]bool, len(rows))
	for _, u := range rows {
		got[u] = true
	}
	if len(got) != len(want) {
		t.Fatalf("repo set = %v, want %v", rows, want)
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("repo %q missing from set %v, want present", w, rows)
		}
	}
}
