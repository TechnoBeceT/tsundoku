// This file exercises CreateBinding's post-bind convergence (spec:
// converge-local-library-read-count-on-bind) end-to-end through a REAL
// syncsvc.Service — unlike every other test in this package, which uses
// fakeSyncService to keep the OTHER handler routes' tests independent of
// the full syncsvc wiring. These tests need the real thing because the
// behavior under test (three-way max-wins convergence pulling in the local
// library's actual read-count) lives inside syncsvc.Service.SyncNow itself,
// not in anything a handler-local fake could stand in for.
package trackers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	handler "github.com/technobecet/tsundoku/internal/handler/trackers"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
	"github.com/technobecet/tsundoku/internal/tracker"
	"github.com/technobecet/tsundoku/internal/tracker/bind"
	"github.com/technobecet/tsundoku/internal/tracker/connect"
	"github.com/technobecet/tsundoku/internal/tracker/retry"
	"github.com/technobecet/tsundoku/internal/tracker/syncsvc"
)

const convergeTrackerID = 903

// fakeConvergeTracker is an OAuth-shaped tracker.Tracker test double (mirrors
// fakeOAuthTracker in handler_test.go) used ONLY by this file's tests. Unlike
// fakeOAuthTracker, it tracks UpdateEntry's call count + the last pushed
// entry, so a test can assert the exact push-back shape (converged progress,
// with score/privacy/status preserved from the just-fetched remote entry).
type fakeConvergeTracker struct {
	id int

	getEntryFn func(ctx context.Context, token, remoteID string) (*tracker.TrackEntry, error)

	updateEntryCalls int
	lastUpdateEntry  tracker.TrackEntry
}

func (f *fakeConvergeTracker) Key() string      { return "fake-converge" }
func (f *fakeConvergeTracker) ID() int          { return f.id }
func (f *fakeConvergeTracker) Name() string     { return "Fake Converge Tracker" }
func (f *fakeConvergeTracker) NeedsOAuth() bool { return true }

func (f *fakeConvergeTracker) AuthURL(state, _ string) (string, string, error) {
	return "https://fake.test/authorize?state=" + state, "verifier-xyz", nil
}

func (f *fakeConvergeTracker) ExchangeCode(_ context.Context, code, _, _ string) (tracker.TokenSet, error) {
	return tracker.TokenSet{Access: "access-" + code}, nil
}

func (f *fakeConvergeTracker) Refresh(context.Context, string) (tracker.TokenSet, error) {
	return tracker.TokenSet{}, tracker.ErrNoRefresh
}

func (f *fakeConvergeTracker) Search(context.Context, string, string) ([]tracker.TrackSearchResult, error) {
	return nil, nil
}

func (f *fakeConvergeTracker) GetEntry(ctx context.Context, token, remoteID string) (*tracker.TrackEntry, error) {
	if f.getEntryFn != nil {
		return f.getEntryFn(ctx, token, remoteID)
	}
	return nil, nil
}

func (f *fakeConvergeTracker) SaveEntry(_ context.Context, _ string, entry tracker.TrackEntry) (tracker.TrackEntry, error) {
	return entry, nil
}

func (f *fakeConvergeTracker) UpdateEntry(_ context.Context, _ string, entry tracker.TrackEntry) (tracker.TrackEntry, error) {
	f.updateEntryCalls++
	f.lastUpdateEntry = entry
	return entry, nil
}

func (f *fakeConvergeTracker) DeleteEntry(context.Context, string, tracker.TrackEntry) error {
	return nil
}

var _ tracker.Tracker = (*fakeConvergeTracker)(nil)

// fakeConvergeAutoUpdate is a syncsvc.AutoUpdateTracker test double with a
// fixed enabled/disabled value — used to prove convergence-on-bind is NOT
// gated by the auto_update_track setting (that toggle only gates the
// reading-triggered PushProgress path).
type fakeConvergeAutoUpdate struct{ enabled bool }

func (f fakeConvergeAutoUpdate) AutoUpdateTrack(context.Context) bool { return f.enabled }

// convergeEnv bundles the wired Echo app (real bind.Service + real
// syncsvc.Service, sharing one registry over a single fakeConvergeTracker),
// the DB client, and a valid owner token.
type convergeEnv struct {
	e      *echo.Echo
	client *ent.Client
	token  string
	trk    *fakeConvergeTracker
}

// newConvergeEnv wires CreateBinding behind a REAL syncsvc.Service (not
// fakeSyncService), so its post-bind SyncNow call genuinely runs the
// three-way max-wins convergence against real TrackBinding + Chapter rows.
// autoUpdate is passed straight through to syncsvc.NewService (nil = always
// enabled, mirroring every other syncsvc test's default).
func newConvergeEnv(t *testing.T, autoUpdate syncsvc.AutoUpdateTracker) *convergeEnv {
	t.Helper()

	client := testdb.New(t)
	authSvc := auth.NewService(testSecret)

	trk := &fakeConvergeTracker{id: convergeTrackerID}
	registry := tracker.NewRegistry(trk)

	connectSvc := connect.NewService(client, registry, "https://tsundoku.example")
	bindSvc := bind.NewService(client, registry, t.TempDir())
	retryQueue := retry.NewQueue(client)
	syncSvc := syncsvc.NewService(client, registry, retryQueue, bindSvc, autoUpdate)
	h := handler.NewHandler(client, registry, connectSvc, bindSvc, syncSvc)

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.POST("/series/:id/tracking", h.CreateBinding)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}
	return &convergeEnv{e: e, client: client, token: token, trk: trk}
}

func (env *convergeEnv) do(method, target, body string) *httptest.ResponseRecorder {
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

// seedConvergeConnection creates a TrackerConnection row for trackerID —
// bypassing the OAuth login HTTP round-trip (this file's tests care about
// convergence, not login), mirroring syncsvc_test's own seedConnection.
func seedConvergeConnection(ctx context.Context, t *testing.T, client *ent.Client, trackerID int, accessToken string) {
	t.Helper()
	if _, err := client.TrackerConnection.Create().
		SetTrackerID(trackerID).
		SetAccessToken(accessToken).
		Save(ctx); err != nil {
		t.Fatalf("seed tracker connection: %v", err)
	}
}

// seedReadChapters creates total chapters numbered 1..total under seriesID,
// marking 1..readUpTo of them read=true, and returns every created row
// (index i == chapter number i+1) for the caller to re-read afterward.
func seedReadChapters(ctx context.Context, t *testing.T, client *ent.Client, seriesID uuid.UUID, total, readUpTo int) []*ent.Chapter {
	t.Helper()
	chapters := make([]*ent.Chapter, 0, total)
	for i := 1; i <= total; i++ {
		ch, err := client.Chapter.Create().
			SetSeriesID(seriesID).
			SetChapterKey(fmt.Sprintf("ch-%d", i)).
			SetNumber(float64(i)).
			SetState(entchapter.StateDownloaded).
			Save(ctx)
		if err != nil {
			t.Fatalf("seed chapter %d: %v", i, err)
		}
		if i <= readUpTo {
			ch, err = ch.Update().SetRead(true).SetReadAt(time.Now().UTC()).Save(ctx)
			if err != nil {
				t.Fatalf("mark chapter %d read: %v", i, err)
			}
		}
		chapters = append(chapters, ch)
	}
	return chapters
}

// reloadConvergeChapter re-reads chapterID's current row.
func reloadConvergeChapter(ctx context.Context, t *testing.T, client *ent.Client, chapterID uuid.UUID) *ent.Chapter {
	t.Helper()
	ch, err := client.Chapter.Query().Where(entchapter.IDEQ(chapterID)).Only(ctx)
	if err != nil {
		t.Fatalf("reload chapter %s: %v", chapterID, err)
	}
	return ch
}

// TestCreateBinding_ConvergesLocalAheadPushesBack proves the local-ahead
// direction end-to-end through the real HTTP handler: the local library has
// 60 chapters read, the remote (fetched during Bind AND again during the
// post-bind SyncNow) reports only 10. The response's binding must reflect
// the converged 60, and the fresh remote entry's Score/Private/Status must
// be preserved on the push-back (never zeroed/defaulted).
func TestCreateBinding_ConvergesLocalAheadPushesBack(t *testing.T) {
	ctx := context.Background()
	env := newConvergeEnv(t, nil)
	seedConvergeConnection(ctx, t, env.client, convergeTrackerID, "acct-token")
	seriesID := seedSeries(ctx, t, env.client, "Local Ahead Bind", "local-ahead-bind")
	seedReadChapters(ctx, t, env.client, seriesID, 70, 60)

	env.trk.getEntryFn = func(_ context.Context, _, remoteID string) (*tracker.TrackEntry, error) {
		return &tracker.TrackEntry{RemoteID: remoteID, Progress: 10, Score: 8, Private: true, Status: "CURRENT"}, nil
	}

	body := fmt.Sprintf(`{"trackerId":%d,"remoteId":"r1"}`, convergeTrackerID)
	rec := env.do(http.MethodPost, "/api/series/"+seriesID.String()+"/tracking", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", rec.Code, rec.Body.String())
	}
	var out handler.TrackBindingDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.LastChapterRead != 60 {
		t.Fatalf("LastChapterRead = %v, want 60 (converged to the local library's read-count, not the remote's 10)", out.LastChapterRead)
	}
	if env.trk.updateEntryCalls != 1 || env.trk.lastUpdateEntry.Progress != 60 {
		t.Fatalf("push-back = calls=%d entry=%+v, want exactly 1 call pushing Progress=60", env.trk.updateEntryCalls, env.trk.lastUpdateEntry)
	}
	if env.trk.lastUpdateEntry.Score != 8 || !env.trk.lastUpdateEntry.Private || env.trk.lastUpdateEntry.Status != "CURRENT" {
		t.Fatalf("push-back entry = %+v, want the remote's own Score=8/Private=true/Status=CURRENT preserved, never zeroed", env.trk.lastUpdateEntry)
	}
}

// TestCreateBinding_ConvergesRemoteAheadMarksLocalRead proves the
// remote-ahead direction: the remote reports 60 while only 10 chapters are
// read locally, so the local library's chapters 1..60 must be marked read
// and the binding must land on 60 — with NO downward push (the remote
// already knows its own value, nothing local to tell it).
func TestCreateBinding_ConvergesRemoteAheadMarksLocalRead(t *testing.T) {
	ctx := context.Background()
	env := newConvergeEnv(t, nil)
	seedConvergeConnection(ctx, t, env.client, convergeTrackerID, "acct-token")
	seriesID := seedSeries(ctx, t, env.client, "Remote Ahead Bind", "remote-ahead-bind")
	chapters := seedReadChapters(ctx, t, env.client, seriesID, 70, 10)

	env.trk.getEntryFn = func(_ context.Context, _, remoteID string) (*tracker.TrackEntry, error) {
		return &tracker.TrackEntry{RemoteID: remoteID, Progress: 60}, nil
	}

	body := fmt.Sprintf(`{"trackerId":%d,"remoteId":"r2"}`, convergeTrackerID)
	rec := env.do(http.MethodPost, "/api/series/"+seriesID.String()+"/tracking", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", rec.Code, rec.Body.String())
	}
	var out handler.TrackBindingDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.LastChapterRead != 60 {
		t.Fatalf("LastChapterRead = %v, want 60", out.LastChapterRead)
	}
	if env.trk.updateEntryCalls != 0 {
		t.Fatalf("UpdateEntry calls = %d, want 0 (remote already ahead of local; nothing to push down)", env.trk.updateEntryCalls)
	}

	for i, ch := range chapters {
		n := i + 1
		fresh := reloadConvergeChapter(ctx, t, env.client, ch.ID)
		wantRead := n <= 60
		if fresh.Read != wantRead {
			t.Fatalf("chapter %d read = %v, want %v", n, fresh.Read, wantRead)
		}
	}
}

// TestCreateBinding_ConvergesEvenWithAutoUpdateTrackOff proves
// convergence-on-bind is NOT gated by auto_update_track: that toggle gates
// only the reading-triggered PushProgress path (see syncsvc.Service's
// package doc comment); an explicit Bind must still converge local↔remote
// even when the setting is off, mirroring the explicit Sync-now endpoint.
func TestCreateBinding_ConvergesEvenWithAutoUpdateTrackOff(t *testing.T) {
	ctx := context.Background()
	env := newConvergeEnv(t, fakeConvergeAutoUpdate{enabled: false})
	seedConvergeConnection(ctx, t, env.client, convergeTrackerID, "acct-token")
	seriesID := seedSeries(ctx, t, env.client, "Auto Update Off Bind", "auto-update-off-bind")
	seedReadChapters(ctx, t, env.client, seriesID, 70, 60)

	env.trk.getEntryFn = func(_ context.Context, _, remoteID string) (*tracker.TrackEntry, error) {
		return &tracker.TrackEntry{RemoteID: remoteID, Progress: 10}, nil
	}

	body := fmt.Sprintf(`{"trackerId":%d,"remoteId":"r3"}`, convergeTrackerID)
	rec := env.do(http.MethodPost, "/api/series/"+seriesID.String()+"/tracking", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", rec.Code, rec.Body.String())
	}
	var out handler.TrackBindingDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.LastChapterRead != 60 {
		t.Fatalf("LastChapterRead = %v, want 60 — convergence must run even with auto_update_track disabled", out.LastChapterRead)
	}
	if env.trk.updateEntryCalls != 1 || env.trk.lastUpdateEntry.Progress != 60 {
		t.Fatalf("push-back = calls=%d entry=%+v, want exactly 1 call pushing Progress=60 even with auto_update_track off", env.trk.updateEntryCalls, env.trk.lastUpdateEntry)
	}
}
