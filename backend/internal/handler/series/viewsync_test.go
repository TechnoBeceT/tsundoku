package series_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	handler "github.com/technobecet/tsundoku/internal/handler/series"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	seriessvc "github.com/technobecet/tsundoku/internal/series"
	sourceenginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
)

// fakeViewSyncer is a handler/series.ViewSyncer test double recording every
// call it received, guarded by a mutex + a buffered channel so a test can
// wait for the DETACHED goroutine Detail fires without a flaky sleep
// (mirrors internal/series/tracksync_test.go's fakeProgressPusher).
type fakeViewSyncer struct {
	mu      sync.Mutex
	calls   []uuid.UUID
	done    chan struct{}
	callErr error
}

func newFakeViewSyncer() *fakeViewSyncer {
	return &fakeViewSyncer{done: make(chan struct{}, 8)}
}

func (f *fakeViewSyncer) SyncOnView(_ context.Context, seriesID uuid.UUID) error {
	f.mu.Lock()
	f.calls = append(f.calls, seriesID)
	f.mu.Unlock()
	f.done <- struct{}{}
	return f.callErr
}

func (f *fakeViewSyncer) waitForCall(t *testing.T) {
	t.Helper()
	select {
	case <-f.done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the detached SyncOnView call")
	}
}

func (f *fakeViewSyncer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeViewSyncer) lastSeriesID() uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

// viewSyncEnv is a narrow sibling of testEnv used only by this file's tests:
// it wires just GET /api/series/:id (Detail) over a fresh testdb client, so
// each test can attach (or omit) a ViewSyncer independently without
// disturbing the shared newTestEnv fixture used everywhere else.
type viewSyncEnv struct {
	e        *echo.Echo
	token    string
	seriesID uuid.UUID
}

// newViewSyncEnv builds a viewSyncEnv with viewSyncer attached via
// WithViewSyncer when non-nil (a nil viewSyncer exercises the default
// no-hook shape every pre-existing Detail test already covers).
func newViewSyncEnv(t *testing.T, viewSyncer handler.ViewSyncer) *viewSyncEnv {
	t.Helper()

	client := testdb.New(t)
	storage := t.TempDir()
	authSvc := auth.NewService(testSecret)
	svc := seriessvc.NewService(client, storage, 14)
	h := handler.NewHandler(svc, func() {}, sourceenginefake.New())
	if viewSyncer != nil {
		h = h.WithViewSyncer(viewSyncer)
	}

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.GET("/series/:id", h.Detail)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}

	ctx := context.Background()
	series := client.Series.Create().
		SetTitle("View Sync Series").
		SetSlug("view-sync-series").
		SetCategoryID(catID(ctx, client, "Manga")).
		SaveX(ctx)

	return &viewSyncEnv{e: e, token: token, seriesID: series.ID}
}

// do issues an authenticated GET /api/series/:id request for env's seeded series.
func (env *viewSyncEnv) do() *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/api/series/"+env.seriesID.String(), nil)
	r.Header.Set("Authorization", "Bearer "+env.token)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	return rec
}

// TestDetail_FiresViewSyncerOnSuccess confirms a successful Detail load
// fires the attached ViewSyncer, detached, with the series id — and that the
// 200 + DTO response is returned WITHOUT waiting on the sync (best-effort,
// never blocks the response).
func TestDetail_FiresViewSyncerOnSuccess(t *testing.T) {
	syncer := newFakeViewSyncer()
	env := newViewSyncEnv(t, syncer)

	rec := env.do()
	if rec.Code != http.StatusOK {
		t.Fatalf("Detail: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got seriessvc.SeriesDetailDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("Detail: decode: %v", err)
	}
	if got.ID != env.seriesID.String() {
		t.Fatalf("Detail: got id %v, want %v", got.ID, env.seriesID)
	}

	syncer.waitForCall(t)
	if syncer.callCount() != 1 {
		t.Fatalf("SyncOnView calls = %d, want 1", syncer.callCount())
	}
	if syncer.lastSeriesID() != env.seriesID {
		t.Fatalf("SyncOnView called with %v, want %v", syncer.lastSeriesID(), env.seriesID)
	}
}

// TestDetail_NoViewSyncerIsSafe confirms a Handler with no ViewSyncer
// attached (every pre-existing handler/series test's shape, incl. every test
// built via newTestEnv) serves Detail normally without panicking or
// blocking — the default, untouched behaviour.
func TestDetail_NoViewSyncerIsSafe(t *testing.T) {
	env := newViewSyncEnv(t, nil)

	rec := env.do()
	if rec.Code != http.StatusOK {
		t.Fatalf("Detail: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestDetail_ReturnsOKEvenWhenViewSyncerErrors confirms a ViewSyncer failure
// never affects the Detail response — the sync is fire-and-forget
// best-effort, so a tracker being unreachable must not surface as an error
// or delay to the caller.
func TestDetail_ReturnsOKEvenWhenViewSyncerErrors(t *testing.T) {
	syncer := newFakeViewSyncer()
	syncer.callErr = errors.New("tracker unreachable")
	env := newViewSyncEnv(t, syncer)

	rec := env.do()
	if rec.Code != http.StatusOK {
		t.Fatalf("Detail: want 200 despite ViewSyncer error, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got seriessvc.SeriesDetailDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("Detail: decode: %v", err)
	}
	if got.ID != env.seriesID.String() {
		t.Fatalf("Detail: got id %v, want %v", got.ID, env.seriesID)
	}

	// The error is swallowed (logged at WARN, never surfaced) — just confirm
	// the call still happened.
	syncer.waitForCall(t)
	if syncer.callCount() != 1 {
		t.Fatalf("SyncOnView calls = %d, want 1", syncer.callCount())
	}
}
