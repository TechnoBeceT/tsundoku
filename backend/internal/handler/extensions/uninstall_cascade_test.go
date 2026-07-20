package extensions_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/ent"
	handler "github.com/technobecet/tsundoku/internal/handler/extensions"
	"github.com/technobecet/tsundoku/internal/metrics"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/settings"
	sourceenginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/sourcepurge"
)

// TestUninstall_AutoCascadesPurge proves that uninstalling an extension via the
// app also purges Tsundoku's DB footprint for its now-orphaned source(s): the
// SeriesProviders + feed + metric + breaker rows are gone, while every CBZ/Chapter
// row is kept (never-auto-delete). The extension's source ids are read from the
// durable HarvestedExtension store BEFORE the write-through deletes that row.
func TestUninstall_AutoCascadesPurge(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	// Seed one source's footprint the cascade must remove.
	s := db.Series.Create().SetTitle("Cascade").SetSlug("cascade").SaveX(ctx)
	p := db.SeriesProvider.Create().
		SetSeriesID(s.ID).SetProvider("7").SetProviderName("Test Source").SetSuwayomiID(7).SaveX(ctx)
	db.ProviderChapter.Create().SetSeriesProviderID(p.ID).SetChapterKey("1").SaveX(ctx)
	// A downloaded chapter that must survive (row + CBZ reference kept).
	db.Chapter.Create().SetSeriesID(s.ID).SetChapterKey("1").SetNumber(1).
		SetState("downloaded").SetFilename("cascade-001.cbz").
		SetSatisfiedByProviderID(p.ID).SetSatisfiedImportance(10).SaveX(ctx)
	db.SourceMetric.Create().SetSourceID("7").SetSourceName("Test Source").SaveX(ctx)
	db.SourceCircuitState.Create().SetSourceKey("Test Source").SaveX(ctx)
	// The durable pkgName→source-ids map (source 7 = the seeded footprint).
	db.HarvestedExtension.Create().SetPkgName("pkg.test.one").SetSourceIds([]int64{7}).SaveX(ctx)

	env := newCascadeEnv(t, db)
	rec := env.do(http.MethodDelete, "/api/suwayomi/extensions/pkg.test.one")
	if rec.Code != http.StatusOK {
		t.Fatalf("Uninstall: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	// The source's DB footprint is gone.
	if n := db.SeriesProvider.Query().CountX(ctx); n != 0 {
		t.Errorf("providers after uninstall cascade = %d, want 0", n)
	}
	if n := db.ProviderChapter.Query().CountX(ctx); n != 0 {
		t.Errorf("feed rows after cascade = %d, want 0", n)
	}
	if n := db.SourceMetric.Query().CountX(ctx); n != 0 {
		t.Errorf("metric rows after cascade = %d, want 0", n)
	}
	if n := db.SourceCircuitState.Query().CountX(ctx); n != 0 {
		t.Errorf("breaker rows after cascade = %d, want 0", n)
	}
	// The downloaded Chapter row + CBZ reference survive (never-auto-delete).
	if n := db.Chapter.Query().CountX(ctx); n != 1 {
		t.Errorf("chapter rows after cascade = %d, want 1 (never-auto-delete)", n)
	}
	// The durable extension row is also removed (the existing write-through).
	if n := db.HarvestedExtension.Query().CountX(ctx); n != 0 {
		t.Errorf("harvested extension rows after uninstall = %d, want 0", n)
	}
}

// cascadeEnv is a full Echo + testdb env whose extensions handler has the purge
// service wired (unlike the pure-passthrough handler_test env).
type cascadeEnv struct {
	e     *echo.Echo
	token string
}

func newCascadeEnv(t *testing.T, db *ent.Client) *cascadeEnv {
	t.Helper()
	authSvc := auth.NewService(testSecret)
	store := apkcache.New(t.TempDir())
	fc := sourceenginefake.New() // Uninstall returns the (empty) list; the test asserts DB state.

	seriesSvc := series.NewService(db, t.TempDir(), 14)
	purgeSvc := sourcepurge.NewService(db, seriesSvc, metrics.NewService(db), sourcegate.NewService(db, settings.Static{}))
	h := handler.NewHandler(fc, db, store, nil, nil, nil, nil).WithPurge(purgeSvc)

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.DELETE("/suwayomi/extensions/:pkgName", h.Uninstall)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}
	return &cascadeEnv{e: e, token: token}
}

func (env *cascadeEnv) do(method, target string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	r.Header.Set("Authorization", "Bearer "+env.token)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	return rec
}
