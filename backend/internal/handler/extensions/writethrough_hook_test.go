package extensions_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/enginetopo"
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

type blockingProtectedClient struct {
	*sourceenginefake.Client
	activationEntered chan struct{}
	releaseActivation chan struct{}
}

func (c *blockingProtectedClient) ActivatePreparedExtensionUpdate(ctx context.Context, req sourceengine.ActivatePreparedExtensionUpdate) ([]sourceengine.Extension, error) {
	close(c.activationEntered)
	select {
	case <-c.releaseActivation:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return c.Client.ActivatePreparedExtensionUpdate(ctx, req)
}

// newDurableEnv builds a durableEnv over fc with a real testdb + a temp-dir apk
// cache and the given httpGet, registering the mutating extension routes.
func newDurableEnv(t *testing.T, fc *sourceenginefake.Client, httpGet func(string) (*http.Response, error), exact bool) *durableEnv {
	t.Helper()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())
	authSvc := auth.NewService(testSecret)
	h := handler.NewHandler(fc, db, cache, httpGet, nil, nil, nil)
	if exact {
		h.WithArchive(enginetopo.NewExtensionArchive(fc, db, cache, nil))
	}

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.POST("/suwayomi/extensions/:pkgName/install", h.Install)
	authed.POST("/suwayomi/extensions/:pkgName/update", h.Update)
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
			VersionName: "1.0.9",
			VersionCode: 9,
			RepoURL:     &repo,
			IsInstalled: false,
			Sources:     []sourceengine.Source{{ID: 5}},
		},
	}))
}

// TestUpdate_WritesThroughConfiguredWrappedIndexAndRetainsPriorVersion is the
// repository-index capture regression guard. The configured URL names the current
// index.json wrapper, while the sibling legacy index.min.json deliberately does
// not advertise the package. A successful engine update must therefore fetch
// the configured file verbatim, cache bytes for the exact post-update installed
// version, and return both that version and the prior held version for rollback.
func TestUpdate_WritesThroughConfiguredWrappedIndexAndRetainsPriorVersion(t *testing.T) {
	ctx := context.Background()
	env := newWrappedIndexUpdateEnv(t)
	seedPriorExtensionCapture(t, ctx, env)

	rec := env.do(http.MethodPost, "/api/suwayomi/extensions/pkg.test.one/update")
	if rec.Code != http.StatusOK {
		t.Fatalf("update: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	assertWrappedIndexCapture(t, ctx, env)
	assertRetainedVersionsResponse(t, rec)
}

func TestUpdate_ArchiveFailurePreventsEngineMutation(t *testing.T) {
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())
	repo := "https://repo.test/index.json"
	ext := sourceengine.Extension{PkgName: "pkg.test.one", VersionCode: 57, VersionName: "1.57", RepoURL: &repo, IsInstalled: true}
	fc := sourceenginefake.New(
		sourceenginefake.WithExtensions([]sourceengine.Extension{ext}),
		sourceenginefake.WithError("InstalledAPK", errors.New("export unavailable")),
	)
	archive := enginetopo.NewExtensionArchive(fc, db, cache, nil)
	h := handler.NewHandler(fc, db, cache, nil, nil, nil, nil).WithArchive(archive)
	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authSvc := auth.NewService(testSecret)
	e.Group("/api", middleware.RequireOwner(authSvc, false)).POST("/suwayomi/extensions/:pkgName/update", h.Update)
	token, _ := authSvc.Issue(uuid.New())
	r := httptest.NewRequest(http.MethodPost, "/api/suwayomi/extensions/pkg.test.one/update", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, r)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := fc.CallCount("UpdateExtension"); got != 0 {
		t.Fatalf("UpdateExtension calls=%d, want 0", got)
	}
	if got := fc.CallCount("InstalledAPK"); got != 1 {
		t.Fatalf("InstalledAPK calls=%d, want 1", got)
	}
}

func TestUpdate_MissingProtectedCapabilityFailsClosed(t *testing.T) {
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())
	ext := sourceengine.Extension{PkgName: "pkg.test.one", VersionCode: 1, IsInstalled: true}
	base := sourceenginefake.New(sourceenginefake.WithExtensions([]sourceengine.Extension{ext}))
	narrow := struct{ sourceengine.Client }{Client: base}
	h := handler.NewHandler(narrow, db, cache, nil, nil, nil, nil).WithArchive(enginetopo.NewExtensionArchive(narrow, db, cache, nil))
	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authSvc := auth.NewService(testSecret)
	e.Group("/api", middleware.RequireOwner(authSvc, false)).POST("/suwayomi/extensions/:pkgName/update", h.Update)
	token, _ := authSvc.Issue(uuid.New())
	r := httptest.NewRequest(http.MethodPost, "/api/suwayomi/extensions/pkg.test.one/update", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, r)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if base.CallCount("UpdateExtension") != 0 || base.CallCount("PrepareExtensionUpdate") != 0 {
		t.Fatal("missing capability must not mutate or prepare")
	}
}

func TestUpdate_SourceRetirementConflictIsStableAndCountsOnlyLiveReferences(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())
	before := sourceengine.Extension{PkgName: "pkg.test.one", VersionCode: 57, VersionName: "1.57", IsInstalled: true, Sources: []sourceengine.Source{{ID: 11}, {ID: 22}}}
	after := sourceengine.Extension{PkgName: "pkg.test.one", VersionCode: 58, VersionName: "1.58", IsInstalled: true, Sources: []sourceengine.Source{{ID: 22}}}
	fc := sourceenginefake.New(sourceenginefake.WithExtensions([]sourceengine.Extension{before}), sourceenginefake.WithUpdateExtensions([]sourceengine.Extension{after}), sourceenginefake.WithInstalledAPK(before.PkgName, 57, before.VersionName, []byte("EXACT-57")))
	seriesA := db.Series.Create().SetTitle("A").SetSlug("retire-a").SaveX(ctx)
	seriesB := db.Series.Create().SetTitle("B").SetSlug("retire-b").SaveX(ctx)
	db.SeriesProvider.Create().SetSeries(seriesA).SetProvider("11").SaveX(ctx)
	db.SeriesProvider.Create().SetSeries(seriesA).SetProvider(" 11 ").SaveX(ctx)
	db.SeriesProvider.Create().SetSeries(seriesB).SetProvider("Disk Source").SaveX(ctx)
	h := handler.NewHandler(fc, db, cache, nil, nil, nil, nil).WithArchive(enginetopo.NewExtensionArchive(fc, db, cache, nil))
	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authSvc := auth.NewService(testSecret)
	e.Group("/api", middleware.RequireOwner(authSvc, false)).POST("/suwayomi/extensions/:pkgName/update", h.Update)
	token, _ := authSvc.Issue(uuid.New())
	r := httptest.NewRequest(http.MethodPost, "/api/suwayomi/extensions/pkg.test.one/update", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, r)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeRetirementConflict(t, rec)
	if body.Code != "source_retirement_conflict" || body.PkgName != before.PkgName || len(body.SourceIDs) != 1 || body.SourceIDs[0] != "11" || body.AffectedProviderCount != 2 || body.AffectedSeriesCount != 1 {
		t.Fatalf("body=%+v", body)
	}
	if fc.CallCount("ActivatePreparedExtensionUpdate") != 1 || fc.CallCount("DiscardPreparedExtensionUpdate") != 1 {
		t.Fatalf("activate=%d discard=%d", fc.CallCount("ActivatePreparedExtensionUpdate"), fc.CallCount("DiscardPreparedExtensionUpdate"))
	}
}

func decodeRetirementConflict(t *testing.T, rec *httptest.ResponseRecorder) struct {
	Code, PkgName         string
	SourceIDs             []string `json:"sourceIds"`
	AffectedProviderCount int      `json:"affectedProviderCount"`
	AffectedSeriesCount   int      `json:"affectedSeriesCount"`
} {
	t.Helper()
	var body struct {
		Code, PkgName         string
		SourceIDs             []string `json:"sourceIds"`
		AffectedProviderCount int      `json:"affectedProviderCount"`
		AffectedSeriesCount   int      `json:"affectedSeriesCount"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestUpdate_ProviderMutationCannotEnterEnumerationToActivationWindow(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())
	before := sourceengine.Extension{PkgName: "pkg.test.one", VersionCode: 57, VersionName: "1.57", IsInstalled: true, Sources: []sourceengine.Source{{ID: 11}}}
	after := sourceengine.Extension{PkgName: "pkg.test.one", VersionCode: 58, VersionName: "1.58", IsInstalled: true, Sources: []sourceengine.Source{{ID: 11}}}
	base := sourceenginefake.New(sourceenginefake.WithExtensions([]sourceengine.Extension{before}), sourceenginefake.WithUpdateExtensions([]sourceengine.Extension{after}), sourceenginefake.WithInstalledAPK(before.PkgName, 57, before.VersionName, []byte("EXACT-57")), sourceenginefake.WithInstalledAPK(after.PkgName, 58, after.VersionName, []byte("EXACT-58")))
	client := &blockingProtectedClient{Client: base, activationEntered: make(chan struct{}), releaseActivation: make(chan struct{})}
	target := db.Series.Create().SetTitle("Target").SetSlug("lock-target").SaveX(ctx)
	h := handler.NewHandler(client, db, cache, nil, nil, nil, nil).WithArchive(enginetopo.NewExtensionArchive(client, db, cache, nil))
	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authSvc := auth.NewService(testSecret)
	e.Group("/api", middleware.RequireOwner(authSvc, false)).POST("/suwayomi/extensions/:pkgName/update", h.Update)
	token, _ := authSvc.Issue(uuid.New())
	requestDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRequest(http.MethodPost, "/api/suwayomi/extensions/pkg.test.one/update", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, r)
		requestDone <- rec
	}()
	<-client.activationEntered
	mutationDone := make(chan error, 1)
	go func() {
		_, err := db.SeriesProvider.Create().SetSeries(target).SetProvider("11").Save(ctx)
		mutationDone <- err
	}()
	select {
	case err := <-mutationDone:
		t.Fatalf("provider mutation entered protected window: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	close(client.releaseActivation)
	rec := <-requestDone
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	select {
	case err := <-mutationDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("provider mutation remained blocked after commit")
	}
}

func TestUpdate_ArchivesBeforeAndAfterSuccessfulMutation(t *testing.T) {
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())
	repo := "https://repo.test/index.json"
	ext := sourceengine.Extension{PkgName: "pkg.test.one", VersionCode: 57, VersionName: "1.57", RepoURL: &repo, IsInstalled: true}
	updated := ext
	updated.VersionCode, updated.VersionName = 58, "1.58"
	fc := sourceenginefake.New(
		sourceenginefake.WithExtensions([]sourceengine.Extension{ext}),
		sourceenginefake.WithInstalledAPK(ext.PkgName, 57, "1.57", []byte("EXACT-57")),
		sourceenginefake.WithInstalledAPK(ext.PkgName, 58, "1.58", []byte("EXACT-58")),
		sourceenginefake.WithUpdateExtensions([]sourceengine.Extension{updated}),
	)
	archive := enginetopo.NewExtensionArchive(fc, db, cache, nil)
	h := handler.NewHandler(fc, db, cache, nil, nil, nil, nil).WithArchive(archive)
	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authSvc := auth.NewService(testSecret)
	e.Group("/api", middleware.RequireOwner(authSvc, false)).POST("/suwayomi/extensions/:pkgName/update", h.Update)
	token, _ := authSvc.Issue(uuid.New())
	r := httptest.NewRequest(http.MethodPost, "/api/suwayomi/extensions/pkg.test.one/update", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := fc.CallCount("InstalledAPK"); got != 2 {
		t.Fatalf("InstalledAPK calls=%d, want 2", got)
	}
	if got := fc.CallCount("ActivatePreparedExtensionUpdate"); got != 1 {
		t.Fatalf("ActivatePreparedExtensionUpdate calls=%d, want 1", got)
	}
	if !cache.Exists(ext.PkgName, 57) || !cache.Exists(ext.PkgName, 58) {
		t.Fatal("old and new exact generations must both be cached")
	}
}

func TestUpdate_PostCaptureFailurePreservesSuccessfulResponse(t *testing.T) {
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())
	repo := "https://repo.test/index.json"
	ext := sourceengine.Extension{PkgName: "pkg.test.one", VersionCode: 57, VersionName: "1.57", RepoURL: &repo, IsInstalled: true}
	updated := ext
	updated.VersionCode = 58
	fc := sourceenginefake.New(
		sourceenginefake.WithExtensions([]sourceengine.Extension{ext}),
		sourceenginefake.WithInstalledAPK(ext.PkgName, 57, "1.57", []byte("EXACT-57")),
		sourceenginefake.WithUpdateExtensions([]sourceengine.Extension{updated}),
		sourceenginefake.WithErrorSequence("InstalledAPK", nil, errors.New("post export unavailable")),
	)
	h := handler.NewHandler(fc, db, cache, nil, nil, nil, nil).WithArchive(enginetopo.NewExtensionArchive(fc, db, cache, nil))
	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authSvc := auth.NewService(testSecret)
	e.Group("/api", middleware.RequireOwner(authSvc, false)).POST("/suwayomi/extensions/:pkgName/update", h.Update)
	token, _ := authSvc.Issue(uuid.New())
	r := httptest.NewRequest(http.MethodPost, "/api/suwayomi/extensions/pkg.test.one/update", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if fc.CallCount("ActivatePreparedExtensionUpdate") != 1 {
		t.Fatal("successful mutation was not retained")
	}
}

func TestUpdate_MissingPostMutationMetadataPreservesSuccessfulResponse(t *testing.T) {
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())
	ext := sourceengine.Extension{PkgName: "pkg.test.one", VersionCode: 57, IsInstalled: true}
	fc := sourceenginefake.New(
		sourceenginefake.WithExtensions([]sourceengine.Extension{ext}),
		sourceenginefake.WithInstalledAPK(ext.PkgName, 57, "v57", []byte("EXACT-57")),
		sourceenginefake.WithUpdateExtensions([]sourceengine.Extension{}),
	)
	h := handler.NewHandler(fc, db, cache, nil, nil, nil, nil).WithArchive(enginetopo.NewExtensionArchive(fc, db, cache, nil))
	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authSvc := auth.NewService(testSecret)
	e.Group("/api", middleware.RequireOwner(authSvc, false)).POST("/suwayomi/extensions/:pkgName/update", h.Update)
	token, _ := authSvc.Issue(uuid.New())
	r := httptest.NewRequest(http.MethodPost, "/api/suwayomi/extensions/pkg.test.one/update", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if fc.CallCount("ActivatePreparedExtensionUpdate") != 1 {
		t.Fatal("successful mutation was not retained")
	}
}

func TestInstall_UsesExactEngineExportDespiteRepositoryMismatch(t *testing.T) {
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())
	repo := "https://repo.test/index.json"
	ext := sourceengine.Extension{PkgName: "pkg.test.one", VersionCode: 57, VersionName: "1.57", RepoURL: &repo, IsInstalled: false}
	fc := sourceenginefake.New(
		sourceenginefake.WithExtensions([]sourceengine.Extension{ext}),
		sourceenginefake.WithInstalledAPK(ext.PkgName, 57, "1.57", []byte("EXACT-57")),
	)
	httpCalls := 0
	h := handler.NewHandler(fc, db, cache, func(string) (*http.Response, error) {
		httpCalls++
		return nil, errors.New("repository candidate is version 99")
	}, nil, nil, nil).WithArchive(enginetopo.NewExtensionArchive(fc, db, cache, nil))
	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authSvc := auth.NewService(testSecret)
	e.Group("/api", middleware.RequireOwner(authSvc, false)).POST("/suwayomi/extensions/:pkgName/install", h.Install)
	token, _ := authSvc.Issue(uuid.New())
	r := httptest.NewRequest(http.MethodPost, "/api/suwayomi/extensions/pkg.test.one/install", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if httpCalls != 0 || !cache.Exists(ext.PkgName, 57) {
		t.Fatalf("repoCalls=%d exact=%v", httpCalls, cache.Exists(ext.PkgName, 57))
	}
}

func TestInstall_RejectsAlreadyInstalledPackageBeforeMutation(t *testing.T) {
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())
	ext := sourceengine.Extension{PkgName: "pkg.test.one", VersionCode: 57, IsInstalled: true}
	fc := sourceenginefake.New(sourceenginefake.WithExtensions([]sourceengine.Extension{ext}))
	h := handler.NewHandler(fc, db, cache, nil, nil, nil, nil).WithArchive(enginetopo.NewExtensionArchive(fc, db, cache, nil))
	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authSvc := auth.NewService(testSecret)
	e.Group("/api", middleware.RequireOwner(authSvc, false)).POST("/suwayomi/extensions/:pkgName/install", h.Install)
	token, _ := authSvc.Issue(uuid.New())
	r := httptest.NewRequest(http.MethodPost, "/api/suwayomi/extensions/pkg.test.one/install", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, r)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s, want 409", rec.Code, rec.Body.String())
	}
	if got := fc.CallCount("InstallExtension"); got != 0 {
		t.Fatalf("InstallExtension calls=%d, want 0", got)
	}
}

const (
	wrappedIndexPkg        = "pkg.test.one"
	wrappedIndexRepo       = "https://repo.test/index.json"
	wrappedIndexNewAPKURL  = "https://cdn.test/pkg.test.one-v2.apk"
	wrappedIndexOldVersion = 1
	wrappedIndexNewVersion = 2
)

func newWrappedIndexUpdateEnv(t *testing.T) *durableEnv {
	t.Helper()
	repoURL := wrappedIndexRepo
	ext := sourceengine.Extension{
		PkgName:     wrappedIndexPkg,
		VersionName: "1.0.2",
		VersionCode: wrappedIndexNewVersion,
		RepoURL:     &repoURL,
		IsInstalled: true,
		Sources:     []sourceengine.Source{{ID: 5}},
	}
	fc := sourceenginefake.New(sourceenginefake.WithExtensions([]sourceengine.Extension{ext}), sourceenginefake.WithUpdateExtensions([]sourceengine.Extension{ext}), sourceenginefake.WithInstalledAPK(wrappedIndexPkg, wrappedIndexNewVersion, "1.0.2", []byte("APK-V2")))
	routes := map[string]string{
		wrappedIndexRepo: `{"extensionList":{"extensions":[{"packageName":"pkg.test.one","versionName":"1.0.2","versionCode":"2","resources":{"apkUrl":"https://cdn.test/pkg.test.one-v2.apk"}}]}}`,
		// This valid legacy index intentionally lacks the target package.
		"https://repo.test/index.min.json": `[{"pkg":"pkg.someone.else","apk":"else.apk","code":9}]`,
		wrappedIndexNewAPKURL:              "APK-V2",
	}
	return newDurableEnv(t, fc, serveRoutes(routes), true)
}

func seedPriorExtensionCapture(t *testing.T, ctx context.Context, env *durableEnv) {
	t.Helper()
	if _, _, err := env.cache.Put(wrappedIndexPkg, wrappedIndexOldVersion, bytes.NewReader([]byte("APK-V1"))); err != nil {
		t.Fatalf("cache old version: %v", err)
	}
	oldAt := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	if err := env.db.HarvestedExtension.Create().
		SetPkgName(wrappedIndexPkg).
		SetRepoURL(wrappedIndexRepo).
		SetVersionCode(wrappedIndexOldVersion).
		SetInstalledVersionCode(wrappedIndexOldVersion).
		SetVersionName("1.0.1").
		SetApkCached(true).
		SetCachedVersions([]apkcache.CachedVersion{{VersionCode: wrappedIndexOldVersion, VersionName: "1.0.1", CachedAt: oldAt}}).
		Exec(ctx); err != nil {
		t.Fatalf("seed prior extension capture: %v", err)
	}
}

func assertWrappedIndexCapture(t *testing.T, ctx context.Context, env *durableEnv) {
	t.Helper()

	row := env.db.HarvestedExtension.Query().
		Where(entharvestedextension.PkgName(wrappedIndexPkg)).
		OnlyX(ctx)
	if row.VersionCode != wrappedIndexNewVersion || row.InstalledVersionCode != wrappedIndexNewVersion {
		t.Fatalf("captured versions = {bytes:%d installed:%d}, want {%d %d}",
			row.VersionCode, row.InstalledVersionCode, wrappedIndexNewVersion, wrappedIndexNewVersion)
	}
	if !env.cache.Exists(wrappedIndexPkg, wrappedIndexNewVersion) || !env.cache.Exists(wrappedIndexPkg, wrappedIndexOldVersion) {
		t.Fatalf("cache presence = {new:%v old:%v}, want both true",
			env.cache.Exists(wrappedIndexPkg, wrappedIndexNewVersion), env.cache.Exists(wrappedIndexPkg, wrappedIndexOldVersion))
	}
	cached, err := env.cache.Open(wrappedIndexPkg, wrappedIndexNewVersion)
	if err != nil {
		t.Fatalf("open cached current version: %v", err)
	}
	cachedBytes, readErr := io.ReadAll(cached)
	closeErr := cached.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read cached current version: read=%v close=%v", readErr, closeErr)
	}
	if !bytes.Equal(cachedBytes, []byte("APK-V2")) {
		t.Errorf("cached current bytes = %q, want %q", cachedBytes, "APK-V2")
	}
}

func assertRetainedVersionsResponse(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	var got []handler.ExtensionDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 1 || len(got[0].CachedVersions) != 2 {
		t.Fatalf("response cachedVersions = %+v, want current and prior versions", got)
	}
	byVersion := make(map[int]bool, len(got[0].CachedVersions))
	for _, held := range got[0].CachedVersions {
		byVersion[held.VersionCode] = true
	}
	if !byVersion[wrappedIndexNewVersion] || !byVersion[wrappedIndexOldVersion] {
		t.Errorf("response held versions = %v, want {%d,%d}", byVersion, wrappedIndexNewVersion, wrappedIndexOldVersion)
	}
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
	env := newDurableEnv(t, installableFake(), serveRoutes(routes), false)

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
	env := newDurableEnv(t, installableFake(), failingGet, false)

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
	env := newDurableEnv(t, installableFake(), serveRoutes(routes), false)

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
