package enginetopo_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/enginetopo"
	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/ent"
	entharvestedextension "github.com/technobecet/tsundoku/internal/ent/harvestedextension"
	entharvestedrepo "github.com/technobecet/tsundoku/internal/ent/harvestedrepo"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	sourceenginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
)

// stubResp is one canned HTTP response body for the httpGet stub.
type stubResp struct {
	status int
	body   []byte
}

// stubHTTP builds an httpGet func serving `routes` by exact URL; a URL present
// in `fail` returns that transport error; any other URL is a 404. It counts
// every call so a test can assert an idempotent second pass makes zero requests.
type stubHTTP struct {
	routes map[string]stubResp
	fail   map[string]error
	mu     sync.Mutex
	calls  int
}

func (h *stubHTTP) get(url string) (*http.Response, error) {
	h.mu.Lock()
	h.calls++
	h.mu.Unlock()
	if err, ok := h.fail[url]; ok {
		return nil, err
	}
	r, ok := h.routes[url]
	if !ok {
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("not found"))}, nil
	}
	return &http.Response{StatusCode: r.status, Body: io.NopCloser(strings.NewReader(string(r.body)))}, nil
}

func (h *stubHTTP) callCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls
}

// hexSHA is the expected hex digest of data, computed independently of the code
// under test.
func hexSHA(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// installedExt builds an installed Extension from the given repo, with
// sources embedded directly (the engine host reports them on the same
// Extensions() response — no separate lookup call).
func installedExt(pkg, repo string, versionCode int64, sources ...sourceengine.Source) sourceengine.Extension {
	return sourceengine.Extension{
		PkgName:     pkg,
		Name:        pkg,
		VersionName: "1.0.0",
		VersionCode: versionCode,
		RepoURL:     &repo,
		IsInstalled: true,
		Sources:     sources,
	}
}

// TestSeedExtensions_HappyPath proves repos are upserted, an installed
// extension's apk is downloaded + cached with the right sha256 + source ids,
// and a non-installed extension is ignored.
func TestSeedExtensions_HappyPath(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	const repo = "https://repo.test/repo"
	const indexURL = "https://repo.test/repo/index.min.json"
	const apkURL = "https://repo.test/repo/apk/pkg.one-v1.apk"
	apkBytes := []byte("APK-BYTES-ONE")

	stub := &stubHTTP{routes: map[string]stubResp{
		indexURL: {status: 200, body: []byte(`[{"pkg":"pkg.one","apk":"pkg.one-v1.apk","code":5}]`)},
		apkURL:   {status: 200, body: apkBytes},
	}}

	client := sourceenginefake.New(
		sourceenginefake.WithRepos([]string{repo}),
		sourceenginefake.WithExtensions([]sourceengine.Extension{
			installedExt("pkg.one", repo, 5, sourceengine.Source{ID: 111}, sourceengine.Source{ID: 222}),
			// A non-installed (available) extension must be ignored entirely.
			{PkgName: "pkg.available", RepoURL: strPtr(repo), IsInstalled: false},
		}),
	)

	res, err := enginetopo.SeedExtensions(ctx, client, db, cache, stub.get, nil)
	if err != nil {
		t.Fatalf("SeedExtensions: %v", err)
	}
	assertResult(t, res, enginetopo.Result{Repos: 1, Cached: 1, Gaps: 0})

	// Repo row persisted.
	if ok, _ := db.HarvestedRepo.Query().Where(entharvestedrepo.URL(repo)).Exist(ctx); !ok {
		t.Errorf("HarvestedRepo %q not persisted", repo)
	}

	// Extension row persisted with correct fields.
	row := db.HarvestedExtension.Query().Where(entharvestedextension.PkgName("pkg.one")).OnlyX(ctx)
	assertCachedExtension(t, row, hexSHA(apkBytes), 5, []int64{111, 222})

	// The apk bytes are readable from the cache, and the ignored (non-installed)
	// extension was never persisted.
	assertCachedBytes(t, cache, "pkg.one", 5, apkBytes)
	if ok, _ := db.HarvestedExtension.Query().Where(entharvestedextension.PkgName("pkg.available")).Exist(ctx); ok {
		t.Error("non-installed extension was persisted, want ignored")
	}
}

// strPtr is a small pointer-literal helper for building sourceengine.Extension
// RepoURL fixtures.
func strPtr(s string) *string { return &s }

// assertResult fails unless the seed Result matches want exactly.
func assertResult(t *testing.T, got, want enginetopo.Result) {
	t.Helper()
	if got != want {
		t.Fatalf("Result = %+v, want %+v", got, want)
	}
}

// assertCachedExtension fails unless the row is marked cached with the expected
// sha256, version code, and source ids.
func assertCachedExtension(t *testing.T, row *ent.HarvestedExtension, wantSHA string, wantVersion int, wantSourceIDs []int64) {
	t.Helper()
	if !row.ApkCached {
		t.Error("HarvestedExtension.ApkCached = false, want true")
	}
	if row.ApkSha256 != wantSHA {
		t.Errorf("ApkSha256 = %q, want %q", row.ApkSha256, wantSHA)
	}
	if row.VersionCode != wantVersion {
		t.Errorf("VersionCode = %d, want %d", row.VersionCode, wantVersion)
	}
	if !slices.Equal(row.SourceIds, wantSourceIDs) {
		t.Errorf("SourceIds = %v, want %v", row.SourceIds, wantSourceIDs)
	}
}

// assertCachedBytes fails unless the cache holds exactly want for (pkg, version).
func assertCachedBytes(t *testing.T, cache *apkcache.Store, pkg string, version int, want []byte) {
	t.Helper()
	rc, err := cache.Open(pkg, version)
	if err != nil {
		t.Fatalf("cache.Open: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, want) {
		t.Errorf("cached apk = %q, want %q", got, want)
	}
}

// TestSeedExtensions_RepoIndexFailureIsGap proves a dead repo index makes only
// that extension a gap (apk_cached=false) while others still cache.
func TestSeedExtensions_RepoIndexFailureIsGap(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	const repoA = "https://good.test/repo"
	const repoB = "https://dead.test/repo"
	apkBytes := []byte("APK-A")

	stub := &stubHTTP{
		routes: map[string]stubResp{
			"https://good.test/repo/index.min.json": {status: 200, body: []byte(`[{"pkg":"pkg.one","apk":"one.apk","code":1}]`)},
			"https://good.test/repo/apk/one.apk":    {status: 200, body: apkBytes},
		},
		fail: map[string]error{
			"https://dead.test/repo/index.min.json": errors.New("connection refused"),
		},
	}

	client := sourceenginefake.New(
		sourceenginefake.WithRepos([]string{repoA, repoB}),
		sourceenginefake.WithExtensions([]sourceengine.Extension{
			installedExt("pkg.one", repoA, 1, sourceengine.Source{ID: 1}),
			installedExt("pkg.two", repoB, 1),
		}),
	)

	res, err := enginetopo.SeedExtensions(ctx, client, db, cache, stub.get, nil)
	if err != nil {
		t.Fatalf("SeedExtensions: %v", err)
	}
	if res.Repos != 2 || res.Cached != 1 || res.Gaps != 1 {
		t.Fatalf("Result = %+v, want {Repos:2 Cached:1 Gaps:1}", res)
	}

	good := db.HarvestedExtension.Query().Where(entharvestedextension.PkgName("pkg.one")).OnlyX(ctx)
	if !good.ApkCached {
		t.Error("pkg.one ApkCached = false, want true")
	}
	gap := db.HarvestedExtension.Query().Where(entharvestedextension.PkgName("pkg.two")).OnlyX(ctx)
	if gap.ApkCached {
		t.Error("pkg.two ApkCached = true, want false (repo index dead)")
	}
}

// TestSeedExtensions_IdempotentSecondRun proves a re-run over an unchanged,
// already-cached library caches 0 and makes ZERO http calls for those
// extensions.
func TestSeedExtensions_IdempotentSecondRun(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	const repo = "https://repo.test/repo"
	stub := &stubHTTP{routes: map[string]stubResp{
		"https://repo.test/repo/index.min.json": {status: 200, body: []byte(`[{"pkg":"pkg.one","apk":"one.apk","code":1}]`)},
		"https://repo.test/repo/apk/one.apk":    {status: 200, body: []byte("APK")},
	}}
	client := sourceenginefake.New(
		sourceenginefake.WithRepos([]string{repo}),
		sourceenginefake.WithExtensions([]sourceengine.Extension{installedExt("pkg.one", repo, 1, sourceengine.Source{ID: 1})}),
	)

	res1, err := enginetopo.SeedExtensions(ctx, client, db, cache, stub.get, nil)
	if err != nil {
		t.Fatalf("first SeedExtensions: %v", err)
	}
	if res1.Cached != 1 {
		t.Fatalf("first pass Cached = %d, want 1", res1.Cached)
	}
	firstCalls := stub.callCount()
	if firstCalls == 0 {
		t.Fatal("first pass made zero http calls, expected index + apk fetch")
	}

	res2, err := enginetopo.SeedExtensions(ctx, client, db, cache, stub.get, nil)
	if err != nil {
		t.Fatalf("second SeedExtensions: %v", err)
	}
	if res2.Cached != 0 || res2.Gaps != 0 {
		t.Errorf("second pass Result = %+v, want Cached:0 Gaps:0", res2)
	}
	if extra := stub.callCount() - firstCalls; extra != 0 {
		t.Errorf("second pass made %d http calls, want 0 (already cached)", extra)
	}
}

// TestSeedExtensions_RecordsIndexVersionNotInstalled proves the recorded
// version_code + apk_sha256 + cache file describe the BYTES the index points at
// (its own version), NOT the older installed version — so all four
// (version_code, sha, file name, serve URL) stay mutually consistent when the
// repo has advanced past the installed version.
func TestSeedExtensions_RecordsIndexVersionNotInstalled(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	const repo = "https://repo.test/repo"
	apkBytes := []byte("APK-V7")
	stub := &stubHTTP{routes: map[string]stubResp{
		// Repo advertises version 7; the extension is installed at version 3.
		"https://repo.test/repo/index.min.json": {status: 200, body: []byte(`[{"pkg":"pkg.one","apk":"one.apk","code":7}]`)},
		"https://repo.test/repo/apk/one.apk":    {status: 200, body: apkBytes},
	}}
	client := sourceenginefake.New(
		sourceenginefake.WithRepos([]string{repo}),
		sourceenginefake.WithExtensions([]sourceengine.Extension{installedExt("pkg.one", repo, 3, sourceengine.Source{ID: 9})}),
	)

	res, err := enginetopo.SeedExtensions(ctx, client, db, cache, stub.get, nil)
	if err != nil {
		t.Fatalf("SeedExtensions: %v", err)
	}
	assertResult(t, res, enginetopo.Result{Repos: 1, Cached: 1, Gaps: 0})

	row := db.HarvestedExtension.Query().Where(entharvestedextension.PkgName("pkg.one")).OnlyX(ctx)
	// version_code + sha describe the downloaded (index) bytes, and the cache file
	// is keyed by version 7 — not the installed 3.
	assertCachedExtension(t, row, hexSHA(apkBytes), 7, []int64{9})
	assertCachedBytes(t, cache, "pkg.one", 7, apkBytes)
	if cache.Exists("pkg.one", 3) {
		t.Error("apk cached under installed version 3, want only the index version 7")
	}
}

// TestSeedExtensions_ReDownloadsWhenFileMissing proves the idempotency skip is
// gated on the FILE, not just the DB row: a row claiming apk_cached=true whose
// cache file was removed (e.g. the engine volume was recreated) is re-downloaded.
func TestSeedExtensions_ReDownloadsWhenFileMissing(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	const repo = "https://repo.test/repo"
	stub := &stubHTTP{routes: map[string]stubResp{
		"https://repo.test/repo/index.min.json": {status: 200, body: []byte(`[{"pkg":"pkg.one","apk":"one.apk","code":1}]`)},
		"https://repo.test/repo/apk/one.apk":    {status: 200, body: []byte("APK")},
	}}
	client := sourceenginefake.New(
		sourceenginefake.WithRepos([]string{repo}),
		sourceenginefake.WithExtensions([]sourceengine.Extension{installedExt("pkg.one", repo, 1, sourceengine.Source{ID: 1})}),
	)

	if _, err := enginetopo.SeedExtensions(ctx, client, db, cache, stub.get, nil); err != nil {
		t.Fatalf("first SeedExtensions: %v", err)
	}
	// The row now says cached — but delete the bytes out from under it.
	if err := cache.Remove("pkg.one", 1); err != nil {
		t.Fatalf("cache.Remove: %v", err)
	}
	if cache.Exists("pkg.one", 1) {
		t.Fatal("cache file still present after Remove")
	}

	res2, err := enginetopo.SeedExtensions(ctx, client, db, cache, stub.get, nil)
	if err != nil {
		t.Fatalf("second SeedExtensions: %v", err)
	}
	if res2.Cached != 1 {
		t.Errorf("second pass Cached = %d, want 1 (file was missing → re-download)", res2.Cached)
	}
	if !cache.Exists("pkg.one", 1) {
		t.Error("apk not re-cached after its file was removed")
	}
}

// TestSeedExtensions_ReCachesWhenInstalledVersionAdvances proves the version
// guard: after the owner upgrades an extension so the INSTALLED version changes,
// a re-seed re-resolves + re-downloads and updates the row to the new version —
// and a further unchanged run skips again (re-cache is once-per-change, no loop).
func TestSeedExtensions_ReCachesWhenInstalledVersionAdvances(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	const repo = "https://repo.test/repo"
	const indexURL = "https://repo.test/repo/index.min.json"
	const apkURL = "https://repo.test/repo/apk/one.apk"

	stub := &stubHTTP{routes: map[string]stubResp{
		indexURL: {status: 200, body: []byte(`[{"pkg":"pkg.one","apk":"one.apk","code":3}]`)},
		apkURL:   {status: 200, body: []byte("APK-V3")},
	}}
	client := sourceenginefake.New(
		sourceenginefake.WithRepos([]string{repo}),
		sourceenginefake.WithExtensions([]sourceengine.Extension{installedExt("pkg.one", repo, 3, sourceengine.Source{ID: 1})}),
	)

	res1, err := enginetopo.SeedExtensions(ctx, client, db, cache, stub.get, nil)
	if err != nil {
		t.Fatalf("first SeedExtensions: %v", err)
	}
	assertResult(t, res1, enginetopo.Result{Repos: 1, Cached: 1, Gaps: 0})

	// The owner upgrades the extension: the installed version overtakes the cached
	// index version (3 → 5), and the repo now advertises the new build.
	client = sourceenginefake.New(
		sourceenginefake.WithRepos([]string{repo}),
		sourceenginefake.WithExtensions([]sourceengine.Extension{installedExt("pkg.one", repo, 5, sourceengine.Source{ID: 1})}),
	)
	stub.routes[indexURL] = stubResp{status: 200, body: []byte(`[{"pkg":"pkg.one","apk":"one.apk","code":5}]`)}
	stub.routes[apkURL] = stubResp{status: 200, body: []byte("APK-V5")}

	res2, err := enginetopo.SeedExtensions(ctx, client, db, cache, stub.get, nil)
	if err != nil {
		t.Fatalf("second SeedExtensions: %v", err)
	}
	if res2.Cached != 1 {
		t.Errorf("second pass Cached = %d, want 1 (installed version advanced past the cached version)", res2.Cached)
	}
	row := db.HarvestedExtension.Query().Where(entharvestedextension.PkgName("pkg.one")).OnlyX(ctx)
	assertCachedExtension(t, row, hexSHA([]byte("APK-V5")), 5, []int64{1})
	assertCachedBytes(t, cache, "pkg.one", 5, []byte("APK-V5"))

	// A THIRD run with nothing changed must skip again (once-per-change, no loop):
	// installed ext.VersionCode (5) == stored installed_version_code (5) → skip,
	// zero http calls.
	callsBefore := stub.callCount()
	res3, err := enginetopo.SeedExtensions(ctx, client, db, cache, stub.get, nil)
	if err != nil {
		t.Fatalf("third SeedExtensions: %v", err)
	}
	if res3.Cached != 0 || res3.Gaps != 0 {
		t.Errorf("third pass Result = %+v, want Cached:0 Gaps:0 (no re-cache loop)", res3)
	}
	if extra := stub.callCount() - callsBefore; extra != 0 {
		t.Errorf("third pass made %d http calls, want 0 (re-cache is monotonic)", extra)
	}
}

// TestSeedExtensions_IndexLagsInstalledNoLoop is the regression guard for the
// refetch-loop bug: when the repo INDEX version PERSISTENTLY LAGS the INSTALLED
// version (the owner sideloaded a newer build, or the repo rolled its published
// version back), the old `ext.VersionCode <= existing.VersionCode` skip condition
// compared two different axes (installed vs index) and was false every boot, so
// the extension was re-resolved + re-downloaded from the upstream repo on EVERY
// boot forever. Keying the skip on installed-version equality ends the loop: once
// the bytes are cached (storing installed_version_code=100, version_code=90), a
// second seed with the SAME installed version (100) skips with ZERO http calls.
func TestSeedExtensions_IndexLagsInstalledNoLoop(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	const repo = "https://repo.test/repo"
	stub := &stubHTTP{routes: map[string]stubResp{
		// The index advertises version 90 while the extension is installed at 100 —
		// a PERSISTENT lag (not a transient one that a re-seed would resolve).
		"https://repo.test/repo/index.min.json": {status: 200, body: []byte(`[{"pkg":"pkg.one","apk":"one.apk","code":90}]`)},
		"https://repo.test/repo/apk/one.apk":    {status: 200, body: []byte("APK-V90")},
	}}
	client := sourceenginefake.New(
		sourceenginefake.WithRepos([]string{repo}),
		sourceenginefake.WithExtensions([]sourceengine.Extension{installedExt("pkg.one", repo, 100, sourceengine.Source{ID: 1})}),
	)

	res1, err := enginetopo.SeedExtensions(ctx, client, db, cache, stub.get, nil)
	if err != nil {
		t.Fatalf("first SeedExtensions: %v", err)
	}
	if res1.Cached != 1 {
		t.Fatalf("first pass Cached = %d, want 1", res1.Cached)
	}
	// The bytes are cached under the INDEX version (90); the row records both the
	// index version and the INSTALLED version (100) as the change-detector.
	row := db.HarvestedExtension.Query().Where(entharvestedextension.PkgName("pkg.one")).OnlyX(ctx)
	if row.VersionCode != 90 {
		t.Errorf("VersionCode = %d, want 90 (index version of the cached bytes)", row.VersionCode)
	}
	if row.InstalledVersionCode != 100 {
		t.Errorf("InstalledVersionCode = %d, want 100 (installed version at cache time)", row.InstalledVersionCode)
	}
	firstCalls := stub.callCount()

	// SECOND seed, installed version UNCHANGED (still 100). This is the boot that
	// the old `<=`-against-index-version bug re-downloaded: it must now SKIP with
	// zero http calls (the loop is gone).
	res2, err := enginetopo.SeedExtensions(ctx, client, db, cache, stub.get, nil)
	if err != nil {
		t.Fatalf("second SeedExtensions: %v", err)
	}
	if res2.Cached != 0 || res2.Gaps != 0 {
		t.Errorf("second pass Result = %+v, want Cached:0 Gaps:0 (no refetch loop)", res2)
	}
	if extra := stub.callCount() - firstCalls; extra != 0 {
		t.Errorf("second pass made %d http calls, want 0 (index lagging installed must not loop)", extra)
	}
}

// TestSeedExtensions_ListReposErrorAborts proves an enumerating failure (listing
// repos) aborts the pass with an error rather than partial success.
func TestSeedExtensions_ListReposErrorAborts(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	client := sourceenginefake.New(sourceenginefake.WithError("Repos", errors.New("engine down")))
	stub := &stubHTTP{}

	if _, err := enginetopo.SeedExtensions(ctx, client, db, cache, stub.get, nil); err == nil {
		t.Fatal("SeedExtensions: want error when listing repos fails, got nil")
	}
}
