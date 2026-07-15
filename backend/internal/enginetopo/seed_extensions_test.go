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
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// seedClient embeds the seed_url_test fakeClient (a full suwayomi.Client stub)
// and overrides only the three methods SeedExtensions exercises, so the ~25
// interface stubs are reused (DRY) rather than re-declared.
type seedClient struct {
	*fakeClient
	repos    []string
	exts     []suwayomi.Extension
	sources  map[string][]suwayomi.Source
	reposErr error
	extsErr  error
	srcErr   map[string]error
}

func (s *seedClient) ExtensionRepos(context.Context) ([]string, error) {
	return s.repos, s.reposErr
}

func (s *seedClient) Extensions(context.Context) ([]suwayomi.Extension, error) {
	return s.exts, s.extsErr
}

func (s *seedClient) ExtensionSources(_ context.Context, pkgName string) ([]suwayomi.Source, error) {
	if err, ok := s.srcErr[pkgName]; ok {
		return nil, err
	}
	return s.sources[pkgName], nil
}

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

// installedExt builds an installed Extension from the given repo.
func installedExt(pkg, repo string, versionCode int) suwayomi.Extension {
	return suwayomi.Extension{
		PkgName:     pkg,
		Name:        pkg,
		VersionName: "1.0.0",
		VersionCode: versionCode,
		Repo:        repo,
		IsInstalled: true,
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
		indexURL: {status: 200, body: []byte(`[{"pkg":"pkg.one","apk":"pkg.one-v1.apk"}]`)},
		apkURL:   {status: 200, body: apkBytes},
	}}

	client := &seedClient{
		fakeClient: &fakeClient{},
		repos:      []string{repo},
		exts: []suwayomi.Extension{
			installedExt("pkg.one", repo, 5),
			// A non-installed (available) extension must be ignored entirely.
			{PkgName: "pkg.available", Repo: repo, IsInstalled: false},
		},
		sources: map[string][]suwayomi.Source{
			"pkg.one": {{ID: "111"}, {ID: "222"}},
		},
	}

	res, err := enginetopo.SeedExtensions(ctx, client, db, cache, stub.get)
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
			"https://good.test/repo/index.min.json": {status: 200, body: []byte(`[{"pkg":"pkg.one","apk":"one.apk"}]`)},
			"https://good.test/repo/apk/one.apk":    {status: 200, body: apkBytes},
		},
		fail: map[string]error{
			"https://dead.test/repo/index.min.json": errors.New("connection refused"),
		},
	}

	client := &seedClient{
		fakeClient: &fakeClient{},
		repos:      []string{repoA, repoB},
		exts: []suwayomi.Extension{
			installedExt("pkg.one", repoA, 1),
			installedExt("pkg.two", repoB, 1),
		},
		sources: map[string][]suwayomi.Source{"pkg.one": {{ID: "1"}}},
	}

	res, err := enginetopo.SeedExtensions(ctx, client, db, cache, stub.get)
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
		"https://repo.test/repo/index.min.json": {status: 200, body: []byte(`[{"pkg":"pkg.one","apk":"one.apk"}]`)},
		"https://repo.test/repo/apk/one.apk":    {status: 200, body: []byte("APK")},
	}}
	client := &seedClient{
		fakeClient: &fakeClient{},
		repos:      []string{repo},
		exts:       []suwayomi.Extension{installedExt("pkg.one", repo, 1)},
		sources:    map[string][]suwayomi.Source{"pkg.one": {{ID: "1"}}},
	}

	res1, err := enginetopo.SeedExtensions(ctx, client, db, cache, stub.get)
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

	res2, err := enginetopo.SeedExtensions(ctx, client, db, cache, stub.get)
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

// TestSeedExtensions_ListReposErrorAborts proves an enumerating failure (listing
// repos) aborts the pass with an error rather than partial success.
func TestSeedExtensions_ListReposErrorAborts(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	client := &seedClient{
		fakeClient: &fakeClient{},
		reposErr:   errors.New("engine down"),
	}
	stub := &stubHTTP{}

	if _, err := enginetopo.SeedExtensions(ctx, client, db, cache, stub.get); err == nil {
		t.Fatal("SeedExtensions: want error when listing repos fails, got nil")
	}
}
