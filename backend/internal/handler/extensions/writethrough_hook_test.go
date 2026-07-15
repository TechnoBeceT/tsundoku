package extensions_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/ent"
	entharvestedextension "github.com/technobecet/tsundoku/internal/ent/harvestedextension"
	handler "github.com/technobecet/tsundoku/internal/handler/extensions"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	sourceenginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
)

// durableEnv is a test harness that wires the extensions handler to a REAL Ent
// client + apk cache (unlike newTestEnv, which passes nil to exercise the pure
// proxy), so the best-effort topology write-through actually runs.
type durableEnv struct {
	e     *echo.Echo
	db    *ent.Client
	cache *apkcache.Store
	token string
}

// newDurableEnv builds a durableEnv over fc with a real testdb + a temp-dir apk
// cache and the given httpGet, registering the mutating extension routes.
func newDurableEnv(t *testing.T, fc *sourceenginefake.Client, httpGet func(string) (*http.Response, error)) *durableEnv {
	t.Helper()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())
	authSvc := auth.NewService(testSecret)
	h := handler.NewHandler(fc, db, cache, httpGet)

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.POST("/suwayomi/extensions/:pkgName/install", h.Install)
	authed.DELETE("/suwayomi/extensions/:pkgName", h.Uninstall)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}
	return &durableEnv{e: e, db: db, cache: cache, token: token}
}

// do issues an authenticated request through the durable env.
func (env *durableEnv) do(method, target string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	r.Header.Set("Authorization", "Bearer "+env.token)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	return rec
}

// serveRoutes builds an httpGet that returns 200 + the mapped body for a known
// URL and a 404 for anything else — the repo index + .apk the capture fetches.
func serveRoutes(routes map[string]string) func(string) (*http.Response, error) {
	return func(url string) (*http.Response, error) {
		body, ok := routes[url]
		status := http.StatusOK
		if !ok {
			status = http.StatusNotFound
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
	}
}

// installableFake models an engine host that has pkg.test.one available:
// InstallExtension succeeds (the base fake flips IsInstalled on its own
// stored copy) and the seeded extension's Sources are embedded directly (no
// separate lookup call, unlike the retired Suwayomi shape) so the capture can
// resolve source ids straight off the mutation's own response.
func installableFake() *sourceenginefake.Client {
	repo := "https://repo.test/index.min.json" // matches seededExt's RepoURL
	return sourceenginefake.New(sourceenginefake.WithExtensions([]sourceengine.Extension{
		{
			PkgName:     "pkg.test.one",
			RepoURL:     &repo,
			IsInstalled: false,
			Sources:     []sourceengine.Source{{ID: 5}},
		},
	}))
}

// TestInstall_WritesThroughToDurableStore proves a successful install captures the
// extension into the durable store: HTTP 200 AND a HarvestedExtension row backed by
// cached apk bytes.
func TestInstall_WritesThroughToDurableStore(t *testing.T) {
	ctx := context.Background()
	routes := map[string]string{
		"https://repo.test/index.min.json": `[{"pkg":"pkg.test.one","apk":"one.apk","code":9}]`,
		"https://repo.test/apk/one.apk":    "APK-BYTES",
	}
	env := newDurableEnv(t, installableFake(), serveRoutes(routes))

	rec := env.do(http.MethodPost, "/api/suwayomi/extensions/pkg.test.one/install")
	if rec.Code != http.StatusOK {
		t.Fatalf("install: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	row, err := env.db.HarvestedExtension.Query().
		Where(entharvestedextension.PkgName("pkg.test.one")).Only(ctx)
	if err != nil {
		t.Fatalf("HarvestedExtension row not written: %v", err)
	}
	if !row.ApkCached || row.VersionCode != 9 {
		t.Errorf("row = {ApkCached:%v VersionCode:%d}, want {true 9}", row.ApkCached, row.VersionCode)
	}
	if !env.cache.Exists("pkg.test.one", 9) {
		t.Error("apk not cached after install write-through")
	}
}

// TestInstall_WriteThroughFailureStillReturns200 is the BEST-EFFORT proof: when the
// durable capture fails (here the repo index fetch errors), the handler STILL returns
// its normal 200 success response — a topology-store hiccup never turns a successful
// engine install into an HTTP 500 — and no HarvestedExtension row is written.
func TestInstall_WriteThroughFailureStillReturns200(t *testing.T) {
	ctx := context.Background()
	failingGet := func(string) (*http.Response, error) { return nil, errors.New("repo unreachable") }
	env := newDurableEnv(t, installableFake(), failingGet)

	rec := env.do(http.MethodPost, "/api/suwayomi/extensions/pkg.test.one/install")
	if rec.Code != http.StatusOK {
		t.Fatalf("install with failing write-through: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	if ok, _ := env.db.HarvestedExtension.Query().
		Where(entharvestedextension.PkgName("pkg.test.one")).Exist(ctx); ok {
		t.Error("HarvestedExtension row written despite capture failure, want none (best-effort swallowed)")
	}
}

// TestUninstall_RemovesFromDurableStore proves an uninstall drops the row + cached
// apk (after a prior install seeded them) and returns 200.
func TestUninstall_RemovesFromDurableStore(t *testing.T) {
	ctx := context.Background()
	routes := map[string]string{
		"https://repo.test/index.min.json": `[{"pkg":"pkg.test.one","apk":"one.apk","code":9}]`,
		"https://repo.test/apk/one.apk":    "APK-BYTES",
	}
	env := newDurableEnv(t, installableFake(), serveRoutes(routes))

	// Seed the durable store via a real install.
	if rec := env.do(http.MethodPost, "/api/suwayomi/extensions/pkg.test.one/install"); rec.Code != http.StatusOK {
		t.Fatalf("seed install: want 200, got %d", rec.Code)
	}
	if !env.cache.Exists("pkg.test.one", 9) {
		t.Fatal("precondition: apk not cached before uninstall")
	}

	rec := env.do(http.MethodDelete, "/api/suwayomi/extensions/pkg.test.one")
	if rec.Code != http.StatusOK {
		t.Fatalf("uninstall: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if ok, _ := env.db.HarvestedExtension.Query().
		Where(entharvestedextension.PkgName("pkg.test.one")).Exist(ctx); ok {
		t.Error("HarvestedExtension row still present after uninstall")
	}
	if env.cache.Exists("pkg.test.one", 9) {
		t.Error("cached apk still present after uninstall")
	}
}
