package series_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	handler "github.com/technobecet/tsundoku/internal/handler/series"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	seriessvc "github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/tracker"
)

// fakeTrackerProgressSetter is a handler/series.TrackerProgressSetter test
// double recording every call, with a configurable failure — mirrors
// fakeViewSyncer's shape (viewsync_test.go) for the same third hook.
type fakeTrackerProgressSetter struct {
	mu       sync.Mutex
	calls    []float64
	seriesID uuid.UUID
	err      error
}

func (f *fakeTrackerProgressSetter) SetSeriesProgress(_ context.Context, seriesID uuid.UUID, target float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, target)
	f.seriesID = seriesID
	return f.err
}

func (f *fakeTrackerProgressSetter) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// readingProgressEnv is a narrow sibling of testEnv (mirrors viewSyncEnv)
// that wires only POST /api/series/:id/reading-progress, so each test can
// attach its own fakeTrackerProgressSetter without disturbing the shared
// newTestEnv fixture used everywhere else.
type readingProgressEnv struct {
	e        *echo.Echo
	client   *ent.Client
	token    string
	seriesID uuid.UUID
	tracker  *fakeTrackerProgressSetter
}

func newReadingProgressEnv(t *testing.T) *readingProgressEnv {
	t.Helper()

	client := testdb.New(t)
	storage := t.TempDir()
	authSvc := auth.NewService(testSecret)
	svc := seriessvc.NewService(client, storage, 14)
	ft := &fakeTrackerProgressSetter{}
	h := handler.NewHandler(svc, func() {}, &fakeSuwayomiClient{}).WithTrackerProgressSetter(ft)

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.POST("/series/:id/reading-progress", h.SetReadingProgress)
	authed.GET("/series/:id", h.Detail)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}

	ctx := context.Background()
	series := client.Series.Create().
		SetTitle("Reset Series").SetSlug("reset-series").
		SetCategoryID(catID(ctx, client, "Manga")).
		SaveX(ctx)
	client.Chapter.Create().
		SetSeriesID(series.ID).SetChapterKey("rp-1").SetNumber(1.0).
		SetState("downloaded").SaveX(ctx)
	client.Chapter.Create().
		SetSeriesID(series.ID).SetChapterKey("rp-2").SetNumber(2.0).
		SetState("downloaded").SetRead(true).SetLastReadPage(5).SaveX(ctx)

	return &readingProgressEnv{e: e, client: client, token: token, seriesID: series.ID, tracker: ft}
}

func (env *readingProgressEnv) do(method, target, body string) *httptest.ResponseRecorder {
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

// TestSetReadingProgress_OK confirms a successful reset resets local
// chapters, force-sets the attached tracker hook with the same target, and
// returns the refreshed SeriesDetailDTO (§16 round-trip).
func TestSetReadingProgress_OK(t *testing.T) {
	env := newReadingProgressEnv(t)

	rec := env.do(http.MethodPost, "/api/series/"+env.seriesID.String()+"/reading-progress", `{"chapter":1}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("SetReadingProgress: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	var got seriessvc.SeriesDetailDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != env.seriesID.String() {
		t.Fatalf("detail id = %v, want %v", got.ID, env.seriesID)
	}

	ctx := context.Background()
	ch1 := env.client.Chapter.Query().Where(entchapter.ChapterKey("rp-1")).OnlyX(ctx)
	if !ch1.Read {
		t.Fatalf("rp-1 (<= target): want read=true, got false")
	}
	ch2 := env.client.Chapter.Query().Where(entchapter.ChapterKey("rp-2")).OnlyX(ctx)
	if ch2.Read || ch2.LastReadPage != 0 {
		t.Fatalf("rp-2 (> target): want read=false lastReadPage=0, got read=%v lastReadPage=%d", ch2.Read, ch2.LastReadPage)
	}

	if env.tracker.callCount() != 1 {
		t.Fatalf("SetSeriesProgress calls = %d, want 1", env.tracker.callCount())
	}
	if env.tracker.seriesID != env.seriesID {
		t.Fatalf("SetSeriesProgress called with series %v, want %v", env.tracker.seriesID, env.seriesID)
	}
}

// TestSetReadingProgress_NegativeChapter confirms a negative chapter is a
// 400 and never reaches either service.
func TestSetReadingProgress_NegativeChapter(t *testing.T) {
	env := newReadingProgressEnv(t)

	rec := env.do(http.MethodPost, "/api/series/"+env.seriesID.String()+"/reading-progress", `{"chapter":-1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("SetReadingProgress negative: want 400, got %d", rec.Code)
	}
	if env.tracker.callCount() != 0 {
		t.Fatalf("SetSeriesProgress calls = %d, want 0 (validation must fail before either service runs)", env.tracker.callCount())
	}
}

// TestSetReadingProgress_MissingChapter confirms an absent chapter field is
// a 400 (a pointer field, so omission is distinguishable from an explicit 0).
func TestSetReadingProgress_MissingChapter(t *testing.T) {
	env := newReadingProgressEnv(t)

	rec := env.do(http.MethodPost, "/api/series/"+env.seriesID.String()+"/reading-progress", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("SetReadingProgress missing chapter: want 400, got %d", rec.Code)
	}
}

// TestSetReadingProgress_UnknownSeries confirms a bogus series id 404s.
func TestSetReadingProgress_UnknownSeries(t *testing.T) {
	env := newReadingProgressEnv(t)

	rec := env.do(http.MethodPost, "/api/series/"+uuid.New().String()+"/reading-progress", `{"chapter":1}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("SetReadingProgress unknown series: want 404, got %d", rec.Code)
	}
}

// TestSetReadingProgress_TrackerFailureSurfacesAs4xx confirms a genuine
// tracker upstream failure (wrapped as tracker.UpstreamError, mirroring
// syncsvc.SetSeriesProgress's own wrapping) renders as a 4xx carrying the
// real message — never a silent drop, never a bare opaque 500 (QCAT-242 +
// the trackers error-visibility fix).
func TestSetReadingProgress_TrackerFailureSurfacesAs4xx(t *testing.T) {
	env := newReadingProgressEnv(t)
	env.tracker.err = tracker.WrapUpstream("fake", errors.New("account not found upstream"))

	rec := env.do(http.MethodPost, "/api/series/"+env.seriesID.String()+"/reading-progress", `{"chapter":1}`)
	if rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("SetReadingProgress tracker failure: want a 4xx, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "account not found upstream") {
		t.Fatalf("SetReadingProgress tracker failure: want the real message in the body, got %s", rec.Body.String())
	}

	// The local chapter reset must still have gone through — only the
	// tracker half failed.
	ctx := context.Background()
	ch1 := env.client.Chapter.Query().Where(entchapter.ChapterKey("rp-1")).OnlyX(ctx)
	if !ch1.Read {
		t.Fatalf("rp-1: want read=true even though the tracker force-set failed")
	}
}

// TestSetReadingProgress_Unauthenticated confirms the route sits behind
// RequireOwner.
func TestSetReadingProgress_Unauthenticated(t *testing.T) {
	env := newReadingProgressEnv(t)
	r := httptest.NewRequest(http.MethodPost, "/api/series/"+env.seriesID.String()+"/reading-progress", strings.NewReader(`{"chapter":1}`))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("SetReadingProgress unauth: want 401, got %d", rec.Code)
	}
}
