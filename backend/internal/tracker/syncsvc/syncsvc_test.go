// Package syncsvc_test exercises the Phase-4c tracker sync service against
// an ephemeral PostgreSQL instance (testdb) with a FAKE tracker.Tracker
// double and a fake SidecarSyncer — no network calls, no real disk I/O.
// Tests require Docker.
package syncsvc_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	enttrackerconnection "github.com/technobecet/tsundoku/internal/ent/trackerconnection"
	"github.com/technobecet/tsundoku/internal/tracker"
	"github.com/technobecet/tsundoku/internal/tracker/retry"
	"github.com/technobecet/tsundoku/internal/tracker/syncsvc"
)

// fakeTracker is a tracker.Tracker test double whose GetEntry/UpdateEntry
// behavior is configurable per test via the *Fn fields, with call counts +
// the last entry passed to UpdateEntry captured for assertions.
type fakeTracker struct {
	id int

	getEntryFn    func(ctx context.Context, token, remoteID string) (*tracker.TrackEntry, error)
	updateEntryFn func(ctx context.Context, token string, entry tracker.TrackEntry) (tracker.TrackEntry, error)

	getEntryCalls    int
	updateEntryCalls int
	lastUpdateEntry  tracker.TrackEntry
	lastToken        string
}

func (f *fakeTracker) Key() string      { return "fake" }
func (f *fakeTracker) ID() int          { return f.id }
func (f *fakeTracker) Name() string     { return "Fake Tracker" }
func (f *fakeTracker) NeedsOAuth() bool { return false }

func (f *fakeTracker) AuthURL(string, string) (string, string, error) {
	return "", "", tracker.ErrOAuthNotSupported
}
func (f *fakeTracker) ExchangeCode(context.Context, string, string, string) (tracker.TokenSet, error) {
	return tracker.TokenSet{}, tracker.ErrOAuthNotSupported
}
func (f *fakeTracker) Refresh(context.Context, string) (tracker.TokenSet, error) {
	return tracker.TokenSet{}, tracker.ErrNoRefresh
}
func (f *fakeTracker) Search(context.Context, string, string) ([]tracker.TrackSearchResult, error) {
	return nil, nil
}

func (f *fakeTracker) GetEntry(ctx context.Context, token, remoteID string) (*tracker.TrackEntry, error) {
	f.getEntryCalls++
	if f.getEntryFn != nil {
		return f.getEntryFn(ctx, token, remoteID)
	}
	return nil, nil
}

func (f *fakeTracker) SaveEntry(_ context.Context, _ string, entry tracker.TrackEntry) (tracker.TrackEntry, error) {
	return entry, nil
}

func (f *fakeTracker) UpdateEntry(ctx context.Context, token string, entry tracker.TrackEntry) (tracker.TrackEntry, error) {
	f.updateEntryCalls++
	f.lastUpdateEntry = entry
	f.lastToken = token
	if f.updateEntryFn != nil {
		return f.updateEntryFn(ctx, token, entry)
	}
	return entry, nil
}

func (f *fakeTracker) DeleteEntry(context.Context, string, tracker.TrackEntry) error { return nil }

var _ tracker.Tracker = (*fakeTracker)(nil)

// fakeSidecar is a syncsvc.SidecarSyncer test double that just counts calls
// and remembers the last seriesID it was called with.
type fakeSidecar struct {
	calls        int
	lastSeriesID uuid.UUID
}

func (f *fakeSidecar) SyncSidecar(_ context.Context, seriesID uuid.UUID) {
	f.calls++
	f.lastSeriesID = seriesID
}

// fakeAutoUpdate is a syncsvc.AutoUpdateTracker test double with a fixed
// enabled/disabled value.
type fakeAutoUpdate struct{ enabled bool }

func (f fakeAutoUpdate) AutoUpdateTrack(context.Context) bool { return f.enabled }

const fakeTrackerID = 900

// seedConnection creates a TrackerConnection row for trackerID.
func seedConnection(ctx context.Context, t *testing.T, client *ent.Client, trackerID int, accessToken string) {
	t.Helper()
	if _, err := client.TrackerConnection.Create().
		SetTrackerID(trackerID).
		SetAccessToken(accessToken).
		Save(ctx); err != nil {
		t.Fatalf("seed tracker connection: %v", err)
	}
}

// seedSeries creates a minimal categorized series with no providers.
func seedSeries(ctx context.Context, t *testing.T, client *ent.Client, title, slug string) uuid.UUID {
	t.Helper()
	catID, err := category.IDByName(ctx, client, "Manga")
	if err != nil {
		t.Fatalf("resolve Manga category id: %v", err)
	}
	s, err := client.Series.Create().SetTitle(title).SetSlug(slug).SetCategoryID(catID).Save(ctx)
	if err != nil {
		t.Fatalf("create series: %v", err)
	}
	return s.ID
}

// seedBinding creates a TrackBinding row directly (no network round-trip —
// syncsvc's own methods are what's under test, not bind.Service.Bind).
func seedBinding(ctx context.Context, t *testing.T, client *ent.Client, seriesID uuid.UUID, trackerID int, remoteID string, lastChapterRead float64, totalChapters int) *ent.TrackBinding {
	t.Helper()
	b, err := client.TrackBinding.Create().
		SetSeriesID(seriesID).
		SetTrackerID(trackerID).
		SetRemoteID(remoteID).
		SetLastChapterRead(lastChapterRead).
		SetTotalChapters(totalChapters).
		Save(ctx)
	if err != nil {
		t.Fatalf("seed track binding: %v", err)
	}
	return b
}

// newService builds a syncsvc.Service wired over ft, a fresh retry.Queue on
// the same client, sidecar (nil-safe), and an always-enabled AutoUpdateTracker
// unless autoUpdate is overridden by the caller.
func newService(client *ent.Client, ft tracker.Tracker, sidecar syncsvc.SidecarSyncer, autoUpdate syncsvc.AutoUpdateTracker) *syncsvc.Service {
	return newServiceMulti(client, []tracker.Tracker{ft}, sidecar, autoUpdate)
}

// newServiceMulti is newService's multi-tracker form, for tests that need
// more than one registered tracker (e.g. per-binding isolation).
func newServiceMulti(client *ent.Client, trackers []tracker.Tracker, sidecar syncsvc.SidecarSyncer, autoUpdate syncsvc.AutoUpdateTracker) *syncsvc.Service {
	registry := tracker.NewRegistry(trackers...)
	queue := retry.NewQueue(client)
	if autoUpdate == nil {
		autoUpdate = fakeAutoUpdate{enabled: true}
	}
	return syncsvc.NewService(client, registry, queue, sidecar, autoUpdate)
}

// deleteConnection removes trackerID's TrackerConnection row, simulating a
// since-disconnected tracker.
func deleteConnection(ctx context.Context, t *testing.T, client *ent.Client, trackerID int) {
	t.Helper()
	if _, err := client.TrackerConnection.Delete().Where(enttrackerconnection.TrackerID(trackerID)).Exec(ctx); err != nil {
		t.Fatalf("delete tracker connection: %v", err)
	}
}

// newTestDB is a thin alias kept local to this package's tests so every test
// file reads the same way at a glance.
func newTestDB(t *testing.T) *ent.Client { return testdb.New(t) }
