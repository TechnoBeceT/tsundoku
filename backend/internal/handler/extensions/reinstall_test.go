package extensions_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/ent"
	handler "github.com/technobecet/tsundoku/internal/handler/extensions"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	sourceenginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
)

const reinstallPkg = "pkg.reinstall.one"

// reinstallEnv wires an Echo whose Handler holds a REAL topology store (testdb
// ent client + a temp apk cache), so the reversible-update reinstall path — held
// version lookup, cache-path install, durable write-through — is exercised
// end-to-end. queries counts DB queries for the no-N+1 assertion.
type reinstallEnv struct {
	e       *echo.Echo
	fake    *sourceenginefake.Client
	db      *ent.Client
	cache   *apkcache.Store
	token   string
	queries *int
}

func newReinstallEnv(t *testing.T, exts []sourceengine.Extension) *reinstallEnv {
	t.Helper()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())
	fake := sourceenginefake.New(sourceenginefake.WithExtensions(exts))

	queries := 0
	db.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) {
			queries++
			return next.Query(ctx, q)
		})
	}))

	authSvc := auth.NewService("reinstall-test-secret")
	// retained resolver returns 3 (the default depth).
	h := handler.NewHandler(fake, db, cache, http.Get, nil, func(context.Context) int { return 3 })

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.GET("/suwayomi/extensions", h.List)
	authed.POST("/suwayomi/extensions/:pkgName/reinstall", h.Reinstall)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}
	return &reinstallEnv{e: e, fake: fake, db: db, cache: cache, token: token, queries: &queries}
}

func (env *reinstallEnv) do(method, target, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("Authorization", "Bearer "+env.token)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	return rec
}

// seedHeldVersion creates a HarvestedExtension row installed at installedVersion
// with the given held version codes, and caches an apk for each held version
// on disk so heldVersionOnDisk finds the bytes.
func (env *reinstallEnv) seedHeldVersion(t *testing.T, pkg string, installedVersion int, heldVersions ...int) {
	t.Helper()
	ctx := context.Background()
	held := make([]apkcache.CachedVersion, 0, len(heldVersions))
	for _, v := range heldVersions {
		held = append(held, apkcache.CachedVersion{VersionCode: v, VersionName: "1.0." + string(rune('0'+v%10)), CachedAt: time.Now()})
		if _, _, err := env.cache.Put(pkg, v, strings.NewReader("apk")); err != nil {
			t.Fatalf("cache.Put v%d: %v", v, err)
		}
	}
	if err := env.db.HarvestedExtension.Create().
		SetPkgName(pkg).
		SetVersionCode(installedVersion).
		SetInstalledVersionCode(installedVersion).
		SetVersionName("installed").
		SetApkCached(true).
		SetCachedVersions(held).
		Exec(ctx); err != nil {
		t.Fatalf("seed HarvestedExtension: %v", err)
	}
}

func installedExt(pkg string, version int64) sourceengine.Extension {
	return sourceengine.Extension{PkgName: pkg, Name: "Reinstall One", Lang: "en", VersionName: "2.0", VersionCode: version, IsInstalled: true}
}

// TestReinstall_HappyPath proves the reinstall installs the HELD version by its
// CACHE PATH (not a repo/http URL), re-reads the list (§16), returns it, and the
// durable store pins installed_version_code to the reinstalled version.
func TestReinstall_HappyPath(t *testing.T) {
	env := newReinstallEnv(t, []sourceengine.Extension{installedExt(reinstallPkg, 42)})
	// Held versions 41 (rollback target) + 42 (current); engine reports v42.
	env.seedHeldVersion(t, reinstallPkg, 42, 41, 42)

	rec := env.do(http.MethodPost, "/api/suwayomi/extensions/"+reinstallPkg+"/reinstall", `{"versionCode":41}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("Reinstall: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if env.fake.CallCount("InstallExtension") != 1 {
		t.Fatalf("InstallExtension called %d times, want 1", env.fake.CallCount("InstallExtension"))
	}
	// The apkURL MUST be the cached apk's local path for (pkg, 41) — the engine
	// installs the held bytes, never a repo re-download of the latest.
	if got, want := env.fake.LastInstallApkURL(), env.cache.Path(reinstallPkg, 41); got != want {
		t.Fatalf("install apkURL = %q, want cache path %q", got, want)
	}
	var got []handler.ExtensionDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].PkgName != reinstallPkg {
		t.Fatalf("response = %+v, want the refreshed list", got)
	}
	// Durable write-through pinned installed_version_code to 41.
	row, err := env.db.HarvestedExtension.Query().Only(context.Background())
	if err != nil {
		t.Fatalf("load row: %v", err)
	}
	if row.InstalledVersionCode != 41 {
		t.Errorf("installed_version_code = %d, want 41", row.InstalledVersionCode)
	}
}

// TestReinstall_UnknownVersion404 proves a version not in the held set is a 404.
func TestReinstall_UnknownVersion404(t *testing.T) {
	env := newReinstallEnv(t, []sourceengine.Extension{installedExt(reinstallPkg, 42)})
	env.seedHeldVersion(t, reinstallPkg, 42, 42)

	rec := env.do(http.MethodPost, "/api/suwayomi/extensions/"+reinstallPkg+"/reinstall", `{"versionCode":41}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404 for unheld version, got %d (%s)", rec.Code, rec.Body.String())
	}
	if env.fake.CallCount("InstallExtension") != 0 {
		t.Errorf("InstallExtension must not be called for an unheld version")
	}
}

// TestReinstall_MissingBytes404 proves a version recorded in cached_versions but
// whose .apk is absent on disk is a 404 (the durable claim without the bytes is
// not reinstallable).
func TestReinstall_MissingBytes404(t *testing.T) {
	env := newReinstallEnv(t, []sourceengine.Extension{installedExt(reinstallPkg, 42)})
	// Row claims v40 held, but no cache.Put for 40 → bytes absent.
	ctx := context.Background()
	if err := env.db.HarvestedExtension.Create().
		SetPkgName(reinstallPkg).SetVersionCode(42).SetInstalledVersionCode(42).
		SetCachedVersions([]apkcache.CachedVersion{{VersionCode: 40, VersionName: "old", CachedAt: time.Now()}}).
		Exec(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rec := env.do(http.MethodPost, "/api/suwayomi/extensions/"+reinstallPkg+"/reinstall", `{"versionCode":40}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404 for missing bytes, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestReinstall_MissingVersionCode400 proves a body without versionCode is a 400
// (a missing field must not silently default to 0).
func TestReinstall_MissingVersionCode400(t *testing.T) {
	env := newReinstallEnv(t, []sourceengine.Extension{installedExt(reinstallPkg, 42)})
	env.seedHeldVersion(t, reinstallPkg, 42, 42)

	rec := env.do(http.MethodPost, "/api/suwayomi/extensions/"+reinstallPkg+"/reinstall", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for missing versionCode, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestReinstall_NoAuth401 proves the route requires an owner session.
func TestReinstall_NoAuth401(t *testing.T) {
	env := newReinstallEnv(t, []sourceengine.Extension{installedExt(reinstallPkg, 42)})
	r := httptest.NewRequest(http.MethodPost, "/api/suwayomi/extensions/"+reinstallPkg+"/reinstall", strings.NewReader(`{"versionCode":42}`))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 without auth, got %d", rec.Code)
	}
}

// TestList_SurfacesCachedVersionsNoNPlus1 proves the extension list DTO carries
// each package's held versions, sourced from ONE batched read regardless of how
// many extensions are listed (no per-extension query).
func TestList_SurfacesCachedVersionsNoNPlus1(t *testing.T) {
	exts := []sourceengine.Extension{
		installedExt("pkg.a", 10),
		installedExt("pkg.b", 20),
		installedExt("pkg.c", 30),
	}
	env := newReinstallEnv(t, exts)
	env.seedHeldVersion(t, "pkg.a", 10, 9, 10)
	env.seedHeldVersion(t, "pkg.b", 20, 20)
	env.seedHeldVersion(t, "pkg.c", 30, 29, 30)

	*env.queries = 0
	rec := env.do(http.MethodGet, "/api/suwayomi/extensions", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("List: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	// The whole 3-extension list must be served by a SINGLE DB query (the batched
	// held-versions read) — an N+1 would scale with the extension count.
	if *env.queries != 1 {
		t.Fatalf("List issued %d DB queries for 3 extensions, want 1 (no N+1)", *env.queries)
	}
	var got []handler.ExtensionDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byPkg := map[string][]handler.CachedVersionDTO{}
	for _, e := range got {
		if e.CachedVersions == nil {
			t.Errorf("cachedVersions is null for %s, want [] or a list", e.PkgName)
		}
		byPkg[e.PkgName] = e.CachedVersions
	}
	if len(byPkg["pkg.a"]) != 2 || len(byPkg["pkg.c"]) != 2 || len(byPkg["pkg.b"]) != 1 {
		t.Errorf("held-version counts wrong: a=%d b=%d c=%d, want 2/1/2",
			len(byPkg["pkg.a"]), len(byPkg["pkg.b"]), len(byPkg["pkg.c"]))
	}
}
