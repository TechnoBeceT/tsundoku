// Package bind_test exercises the tracker bind service against an ephemeral
// PostgreSQL instance (testdb) with a FAKE tracker.Tracker double — no
// network calls, so it runs in the default gate (unlike the shape-tagged
// live tests each real tracker client carries). Tests require Docker.
package bind_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	enttrackbinding "github.com/technobecet/tsundoku/internal/ent/trackbinding"
	enttrackerconnection "github.com/technobecet/tsundoku/internal/ent/trackerconnection"
	"github.com/technobecet/tsundoku/internal/tracker"
	"github.com/technobecet/tsundoku/internal/tracker/bind"
)

// fakeTracker is a tracker.Tracker test double whose GetEntry/SaveEntry/
// DeleteEntry/Search behavior is configurable per test via the *Fn fields,
// with a deterministic default when unset. Call counts let a test assert
// e.g. "GetEntry ran exactly once" or "Reconcile made zero tracker calls".
type fakeTracker struct {
	id int

	getEntryFn    func(ctx context.Context, token, remoteID string) (*tracker.TrackEntry, error)
	saveEntryFn   func(ctx context.Context, token string, entry tracker.TrackEntry) (tracker.TrackEntry, error)
	deleteEntryFn func(ctx context.Context, token string, entry tracker.TrackEntry) error
	searchFn      func(ctx context.Context, token, query string) ([]tracker.TrackSearchResult, error)

	getEntryCalls    int
	saveEntryCalls   int
	deleteEntryCalls int
	searchCalls      int
	lastDeleteEntry  tracker.TrackEntry
	lastSaveEntry    tracker.TrackEntry
	lastSearchToken  string
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

func (f *fakeTracker) Search(ctx context.Context, token, query string) ([]tracker.TrackSearchResult, error) {
	f.searchCalls++
	f.lastSearchToken = token
	if f.searchFn != nil {
		return f.searchFn(ctx, token, query)
	}
	return nil, nil
}

func (f *fakeTracker) GetEntry(ctx context.Context, token, remoteID string) (*tracker.TrackEntry, error) {
	f.getEntryCalls++
	if f.getEntryFn != nil {
		return f.getEntryFn(ctx, token, remoteID)
	}
	return nil, nil
}

func (f *fakeTracker) SaveEntry(ctx context.Context, token string, entry tracker.TrackEntry) (tracker.TrackEntry, error) {
	f.saveEntryCalls++
	f.lastSaveEntry = entry
	if f.saveEntryFn != nil {
		return f.saveEntryFn(ctx, token, entry)
	}
	return entry, nil
}

func (f *fakeTracker) UpdateEntry(_ context.Context, _ string, entry tracker.TrackEntry) (tracker.TrackEntry, error) {
	return entry, nil
}

func (f *fakeTracker) DeleteEntry(ctx context.Context, token string, entry tracker.TrackEntry) error {
	f.deleteEntryCalls++
	f.lastDeleteEntry = entry
	if f.deleteEntryFn != nil {
		return f.deleteEntryFn(ctx, token, entry)
	}
	return nil
}

var _ tracker.Tracker = (*fakeTracker)(nil)

// seedConnection creates a TrackerConnection row for trackerID — the
// "owner is logged in" precondition every bind.Service method needs.
func seedConnection(ctx context.Context, t *testing.T, client *ent.Client, trackerID int, accessToken string) {
	t.Helper()
	if _, err := client.TrackerConnection.Create().
		SetTrackerID(trackerID).
		SetAccessToken(accessToken).
		Save(ctx); err != nil {
		t.Fatalf("seed tracker connection: %v", err)
	}
}

// seedBoundSeries creates a Series row (category "Manga") PLUS a real
// on-disk folder carrying a sidecar with one chapter — the minimum a
// series needs to (a) be Bind-able and (b) be visible to disk.ScanLibrary
// (a folder with zero chapter facts is invisible — see scanSeriesDir's
// cover-only-directory guard), which the round-trip test below depends on.
func seedBoundSeries(ctx context.Context, t *testing.T, client *ent.Client, storage, title string) *ent.Series {
	t.Helper()

	catID, err := category.IDByName(ctx, client, "Manga")
	if err != nil {
		t.Fatalf("resolve Manga category id: %v", err)
	}
	row, err := client.Series.Create().
		SetTitle(title).
		SetSlug(disk.Slugify(title)).
		SetCategoryID(catID).
		Save(ctx)
	if err != nil {
		t.Fatalf("create series: %v", err)
	}

	dir := disk.SeriesDir(storage, "Manga", title)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir series dir: %v", err)
	}
	num := 1.0
	sidecar := disk.Sidecar{
		Title:    title,
		Category: "Manga",
		Chapters: []disk.ChapterProvenance{{
			ChapterKey: "1",
			Number:     &num,
			Provider:   "mangadex",
			Importance: 1,
			Filename:   "chapter-001.cbz",
			PageCount:  10,
		}},
	}
	if err := disk.WriteSidecar(dir, sidecar); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}

	return row
}

// entryFn returns a getEntryFn that always answers with entry (RemoteID
// overridden to the requested remoteID, mirroring every real tracker
// client's own "RemoteID always comes from the response, not a
// caller-supplied fallback" contract).
func entryFn(entry tracker.TrackEntry) func(context.Context, string, string) (*tracker.TrackEntry, error) {
	return func(_ context.Context, _, remoteID string) (*tracker.TrackEntry, error) {
		e := entry
		e.RemoteID = remoteID
		return &e, nil
	}
}

const fakeTrackerID = 900

// TestBind_CreatesBindingAndWritesSidecar drives the common path: GetEntry
// finds an existing remote entry, and Bind upserts a TrackBinding row from
// it PLUS mirrors the series' full binding set into its sidecar.
func TestBind_CreatesBindingAndWritesSidecar(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()
	row := seedBoundSeries(ctx, t, client, storage, "Solo Leveling")

	ft := &fakeTracker{
		id: fakeTrackerID,
		getEntryFn: entryFn(tracker.TrackEntry{
			LibraryID: "lib-1", Status: "current", Progress: 12, TotalChapters: 179, Score: 8,
		}),
	}
	seedConnection(ctx, t, client, ft.id, "acct-token")
	svc := bind.NewService(client, tracker.NewRegistry(ft), storage)

	binding, err := svc.Bind(ctx, row.ID, ft.id, "remote-7224")
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	assertBindingFields(t, binding, "remote-7224", "lib-1", "current", 12, 179, 8)
	if ft.getEntryCalls != 1 {
		t.Fatalf("GetEntry calls = %d, want 1", ft.getEntryCalls)
	}
	if ft.saveEntryCalls != 0 {
		t.Fatalf("SaveEntry calls = %d, want 0 (GetEntry already found an entry)", ft.saveEntryCalls)
	}

	seriesDir := disk.SeriesDir(storage, "Manga", "Solo Leveling")
	assertSidecarBinding(t, seriesDir, ft.id, "remote-7224", "current", 12, 8)
}

// assertBindingFields fails the test unless binding carries exactly the
// given field values — extracted so the tests driving it stay under the
// fleet's per-function cyclomatic-complexity budget.
func assertBindingFields(t *testing.T, binding *ent.TrackBinding, remoteID, libraryID, status string, lastChapterRead float64, totalChapters int, score float64) {
	t.Helper()
	if binding.RemoteID != remoteID || binding.LibraryID != libraryID || binding.Status != status ||
		binding.LastChapterRead != lastChapterRead || binding.TotalChapters != totalChapters || binding.Score != score {
		t.Fatalf("binding = %+v, want remoteID=%s libraryID=%s status=%s lastChapterRead=%v totalChapters=%d score=%v",
			binding, remoteID, libraryID, status, lastChapterRead, totalChapters, score)
	}
}

// assertSidecarBinding reads seriesDir's sidecar and fails the test unless
// it carries exactly one TrackBinding entry matching the given fields.
func assertSidecarBinding(t *testing.T, seriesDir string, trackerID int, remoteID, status string, lastChapterRead, score float64) {
	t.Helper()
	sc, err := disk.ReadSidecar(seriesDir)
	if err != nil {
		t.Fatalf("ReadSidecar: %v", err)
	}
	if sc == nil || len(sc.TrackBindings) != 1 {
		t.Fatalf("sidecar TrackBindings = %+v, want exactly one entry", sc)
	}
	got := sc.TrackBindings[0]
	if got.TrackerID != trackerID || got.RemoteID != remoteID || got.Status != status ||
		got.LastChapterRead != lastChapterRead || got.Score != score {
		t.Fatalf("sidecar binding = %+v, want trackerID=%d remoteID=%s status=%s lastChapterRead=%v score=%v",
			got, trackerID, remoteID, status, lastChapterRead, score)
	}
}

// TestBind_RegistersFreshEntryWhenNotYetTracked confirms Bind calls
// SaveEntry (registering the series on the tracker's own account) when
// GetEntry reports the manga is not yet tracked at all.
func TestBind_RegistersFreshEntryWhenNotYetTracked(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()
	row := seedBoundSeries(ctx, t, client, storage, "New Bind")

	ft := &fakeTracker{
		id: fakeTrackerID,
		saveEntryFn: func(_ context.Context, _ string, entry tracker.TrackEntry) (tracker.TrackEntry, error) {
			entry.LibraryID = "new-lib"
			entry.Status = "current"
			return entry, nil
		},
	}
	seedConnection(ctx, t, client, ft.id, "acct-token")
	svc := bind.NewService(client, tracker.NewRegistry(ft), storage)

	binding, err := svc.Bind(ctx, row.ID, ft.id, "remote-1")
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if ft.saveEntryCalls != 1 {
		t.Fatalf("SaveEntry calls = %d, want 1", ft.saveEntryCalls)
	}
	if binding.LibraryID != "new-lib" || binding.RemoteID != "remote-1" {
		t.Fatalf("binding = %+v", binding)
	}
}

// TestBind_RegistersFreshEntryWithDefaultStatus confirms Bind's fresh
// SaveEntry call (the not-yet-tracked path) seeds a NON-EMPTY, tracker-native
// "currently reading" status for AniList/MAL/Kitsu — the bind-path half of
// the pre-activation data-corruption fix. An empty Status here would make
// AniList's MediaListStatus GraphQL enum REJECT the create outright (the
// common bind case failing), and Kitsu's status field has no sane empty
// default either.
func TestBind_RegistersFreshEntryWithDefaultStatus(t *testing.T) {
	cases := []struct {
		name       string
		trackerID  int
		wantStatus string
	}{
		{"AniList", tracker.IDAniList, "CURRENT"},
		{"MAL", tracker.IDMAL, "reading"},
		{"Kitsu", tracker.IDKitsu, "current"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := testdb.New(t)
			storage := t.TempDir()
			row := seedBoundSeries(ctx, t, client, storage, "Fresh "+tc.name)

			ft := &fakeTracker{id: tc.trackerID}
			seedConnection(ctx, t, client, ft.id, "acct-token")
			svc := bind.NewService(client, tracker.NewRegistry(ft), storage)

			if _, err := svc.Bind(ctx, row.ID, ft.id, "remote-1"); err != nil {
				t.Fatalf("Bind: %v", err)
			}
			if ft.saveEntryCalls != 1 {
				t.Fatalf("SaveEntry calls = %d, want 1", ft.saveEntryCalls)
			}
			if ft.lastSaveEntry.Status != tc.wantStatus {
				t.Fatalf("SaveEntry Status = %q, want %q (NOT empty)", ft.lastSaveEntry.Status, tc.wantStatus)
			}
		})
	}
}

// TestBind_TrackerNotFound confirms Bind fails closed for a trackerID the
// registry doesn't know.
func TestBind_TrackerNotFound(t *testing.T) {
	client := testdb.New(t)
	svc := bind.NewService(client, tracker.NewRegistry(), t.TempDir())

	if _, err := svc.Bind(context.Background(), uuid.New(), 9999, "r"); err != bind.ErrTrackerNotFound {
		t.Fatalf("Bind: err = %v, want bind.ErrTrackerNotFound", err)
	}
}

// TestBind_TrackerNotConnected confirms Bind fails closed when the tracker
// is registered but the owner has never logged in.
func TestBind_TrackerNotConnected(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()
	row := seedBoundSeries(ctx, t, client, storage, "Unconnected")

	ft := &fakeTracker{id: fakeTrackerID}
	svc := bind.NewService(client, tracker.NewRegistry(ft), storage)

	if _, err := svc.Bind(ctx, row.ID, ft.id, "r"); err != bind.ErrTrackerNotConnected {
		t.Fatalf("Bind: err = %v, want bind.ErrTrackerNotConnected", err)
	}
}

// TestBind_SeriesNotFound confirms Bind fails closed for an unknown
// seriesID even with a fully connected tracker.
func TestBind_SeriesNotFound(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()

	ft := &fakeTracker{id: fakeTrackerID}
	seedConnection(ctx, t, client, ft.id, "acct-token")
	svc := bind.NewService(client, tracker.NewRegistry(ft), storage)

	if _, err := svc.Bind(ctx, uuid.New(), ft.id, "r"); err != bind.ErrSeriesNotFound {
		t.Fatalf("Bind: err = %v, want bind.ErrSeriesNotFound", err)
	}
}

// bindOne is the shared setup for the Unbind/FetchTrack/SearchTracker
// tests below: a bound series + a live TrackBinding row.
func bindOne(ctx context.Context, t *testing.T, client *ent.Client, storage string, ft *fakeTracker, remoteID string) *ent.TrackBinding {
	t.Helper()
	row := seedBoundSeries(ctx, t, client, storage, fmt.Sprintf("Series %s", remoteID))
	seedConnection(ctx, t, client, ft.id, "acct-token")
	svc := bind.NewService(client, tracker.NewRegistry(ft), storage)
	binding, err := svc.Bind(ctx, row.ID, ft.id, remoteID)
	if err != nil {
		t.Fatalf("bindOne: Bind: %v", err)
	}
	return binding
}

// TestUnbind_RemovesRowAndRewritesSidecar confirms a plain Unbind
// (deleteRemote=false) removes the local row, leaves the remote entry
// untouched (DeleteEntry never called), and rewrites the sidecar to an
// empty binding set.
func TestUnbind_RemovesRowAndRewritesSidecar(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()
	ft := &fakeTracker{id: fakeTrackerID}
	binding := bindOne(ctx, t, client, storage, ft, "r1")
	svc := bind.NewService(client, tracker.NewRegistry(ft), storage)

	if err := svc.Unbind(ctx, binding.ID, false); err != nil {
		t.Fatalf("Unbind: %v", err)
	}
	if ft.deleteEntryCalls != 0 {
		t.Fatalf("DeleteEntry calls = %d, want 0 (deleteRemote=false)", ft.deleteEntryCalls)
	}

	count, err := client.TrackBinding.Query().Where(enttrackbinding.IDEQ(binding.ID)).Count(ctx)
	if err != nil {
		t.Fatalf("count TrackBinding: %v", err)
	}
	if count != 0 {
		t.Fatalf("row count after Unbind = %d, want 0", count)
	}

	sc, err := disk.ReadSidecar(disk.SeriesDir(storage, "Manga", "Series r1"))
	if err != nil {
		t.Fatalf("ReadSidecar: %v", err)
	}
	if sc == nil || len(sc.TrackBindings) != 0 {
		t.Fatalf("sidecar TrackBindings after Unbind = %+v, want empty", sc)
	}
}

// TestUnbind_DeleteRemoteCallsDeleteEntry confirms deleteRemote=true calls
// DeleteEntry with the binding's own remote/library id before removing the
// local row.
func TestUnbind_DeleteRemoteCallsDeleteEntry(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()
	ft := &fakeTracker{
		id: fakeTrackerID,
		getEntryFn: entryFn(tracker.TrackEntry{
			LibraryID: "lib-9", Status: "current",
		}),
	}
	binding := bindOne(ctx, t, client, storage, ft, "r2")
	svc := bind.NewService(client, tracker.NewRegistry(ft), storage)

	if err := svc.Unbind(ctx, binding.ID, true); err != nil {
		t.Fatalf("Unbind: %v", err)
	}
	if ft.deleteEntryCalls != 1 {
		t.Fatalf("DeleteEntry calls = %d, want 1", ft.deleteEntryCalls)
	}
	if ft.lastDeleteEntry.RemoteID != "r2" || ft.lastDeleteEntry.LibraryID != "lib-9" {
		t.Fatalf("DeleteEntry called with %+v", ft.lastDeleteEntry)
	}
}

// TestUnbind_DeleteRemoteFailureLeavesRowIntact confirms a failed remote
// deletion fails the whole Unbind and leaves the local row (and its
// sidecar entry) untouched — never a partial unbind that hides a still-live
// remote entry.
func TestUnbind_DeleteRemoteFailureLeavesRowIntact(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()
	ft := &fakeTracker{
		id:            fakeTrackerID,
		deleteEntryFn: func(context.Context, string, tracker.TrackEntry) error { return fmt.Errorf("remote is down") },
	}
	binding := bindOne(ctx, t, client, storage, ft, "r3")
	svc := bind.NewService(client, tracker.NewRegistry(ft), storage)

	if err := svc.Unbind(ctx, binding.ID, true); err == nil {
		t.Fatalf("Unbind with a failing DeleteEntry: want an error, got nil")
	}

	count, err := client.TrackBinding.Query().Where(enttrackbinding.IDEQ(binding.ID)).Count(ctx)
	if err != nil {
		t.Fatalf("count TrackBinding: %v", err)
	}
	if count != 1 {
		t.Fatalf("row count after a failed Unbind = %d, want 1 (unchanged)", count)
	}
}

// TestUnbind_BindingNotFound confirms Unbind fails closed for an unknown
// recordID.
func TestUnbind_BindingNotFound(t *testing.T) {
	client := testdb.New(t)
	svc := bind.NewService(client, tracker.NewRegistry(), t.TempDir())

	if err := svc.Unbind(context.Background(), uuid.New(), false); err != bind.ErrBindingNotFound {
		t.Fatalf("Unbind: err = %v, want bind.ErrBindingNotFound", err)
	}
}

// TestFetchTrack_UpdatesRowAndSidecar confirms FetchTrack re-pulls the
// remote entry and writes the fresh snapshot onto the row + sidecar.
func TestFetchTrack_UpdatesRowAndSidecar(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()
	ft := &fakeTracker{
		id:         fakeTrackerID,
		getEntryFn: entryFn(tracker.TrackEntry{Status: "current", Progress: 5, Score: 6}),
	}
	binding := bindOne(ctx, t, client, storage, ft, "r4")
	svc := bind.NewService(client, tracker.NewRegistry(ft), storage)

	ft.getEntryFn = entryFn(tracker.TrackEntry{Status: "completed", Progress: 20, Score: 9, TotalChapters: 20})

	updated, err := svc.FetchTrack(ctx, binding.ID)
	if err != nil {
		t.Fatalf("FetchTrack: %v", err)
	}
	if updated.Status != "completed" || updated.LastChapterRead != 20 || updated.Score != 9 || updated.TotalChapters != 20 {
		t.Fatalf("FetchTrack result = %+v", updated)
	}

	sc, err := disk.ReadSidecar(disk.SeriesDir(storage, "Manga", "Series r4"))
	if err != nil {
		t.Fatalf("ReadSidecar: %v", err)
	}
	if len(sc.TrackBindings) != 1 || sc.TrackBindings[0].Status != "completed" || sc.TrackBindings[0].LastChapterRead != 20 {
		t.Fatalf("sidecar after FetchTrack = %+v", sc.TrackBindings)
	}
}

// TestFetchTrack_RemoteGoneLeavesRowUnchanged confirms a GetEntry (nil,
// nil) — the remote no longer carries this entry — leaves the existing row
// as-is rather than zeroing it out.
func TestFetchTrack_RemoteGoneLeavesRowUnchanged(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()
	ft := &fakeTracker{
		id:         fakeTrackerID,
		getEntryFn: entryFn(tracker.TrackEntry{Status: "current", Progress: 5}),
	}
	binding := bindOne(ctx, t, client, storage, ft, "r5")
	svc := bind.NewService(client, tracker.NewRegistry(ft), storage)

	ft.getEntryFn = func(context.Context, string, string) (*tracker.TrackEntry, error) { return nil, nil }

	got, err := svc.FetchTrack(ctx, binding.ID)
	if err != nil {
		t.Fatalf("FetchTrack: %v", err)
	}
	if got.LastChapterRead != 5 || got.Status != "current" {
		t.Fatalf("FetchTrack after remote-gone = %+v, want the row unchanged", got)
	}
}

// TestFetchTrack_BindingNotFound confirms FetchTrack fails closed for an
// unknown recordID.
func TestFetchTrack_BindingNotFound(t *testing.T) {
	client := testdb.New(t)
	svc := bind.NewService(client, tracker.NewRegistry(), t.TempDir())

	if _, err := svc.FetchTrack(context.Background(), uuid.New()); err != bind.ErrBindingNotFound {
		t.Fatalf("FetchTrack: err = %v, want bind.ErrBindingNotFound", err)
	}
}

// TestFetchTrack_TrackerNotConnected confirms FetchTrack fails closed when
// the owning tracker's account has since been logged out.
func TestFetchTrack_TrackerNotConnected(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()
	ft := &fakeTracker{id: fakeTrackerID}
	binding := bindOne(ctx, t, client, storage, ft, "r6")
	svc := bind.NewService(client, tracker.NewRegistry(ft), storage)

	if _, err := client.TrackerConnection.Delete().Where(enttrackerconnection.TrackerID(ft.id)).Exec(ctx); err != nil {
		t.Fatalf("delete tracker connection: %v", err)
	}

	if _, err := svc.FetchTrack(ctx, binding.ID); err != bind.ErrTrackerNotConnected {
		t.Fatalf("FetchTrack: err = %v, want bind.ErrTrackerNotConnected", err)
	}
}

// TestFetchTrack_TrackerNotFound confirms FetchTrack fails closed when a
// binding names a tracker no longer in the registry.
func TestFetchTrack_TrackerNotFound(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()
	row := seedBoundSeries(ctx, t, client, storage, "Orphaned Tracker")

	binding, err := client.TrackBinding.Create().
		SetSeriesID(row.ID).
		SetTrackerID(12345).
		SetRemoteID("r").
		Save(ctx)
	if err != nil {
		t.Fatalf("create TrackBinding: %v", err)
	}

	svc := bind.NewService(client, tracker.NewRegistry(), storage)
	if _, err := svc.FetchTrack(ctx, binding.ID); err != bind.ErrTrackerNotFound {
		t.Fatalf("FetchTrack: err = %v, want bind.ErrTrackerNotFound", err)
	}
}

// TestSearchTracker_ReturnsResults confirms SearchTracker forwards the
// connected account's token to the tracker's Search.
func TestSearchTracker_ReturnsResults(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	ft := &fakeTracker{
		id: fakeTrackerID,
		searchFn: func(context.Context, string, string) ([]tracker.TrackSearchResult, error) {
			return []tracker.TrackSearchResult{{RemoteID: "1", Title: "Hit"}}, nil
		},
	}
	seedConnection(ctx, t, client, ft.id, "acct-token")
	svc := bind.NewService(client, tracker.NewRegistry(ft), t.TempDir())

	results, err := svc.SearchTracker(ctx, ft.id, "query")
	if err != nil {
		t.Fatalf("SearchTracker: %v", err)
	}
	if len(results) != 1 || results[0].Title != "Hit" {
		t.Fatalf("SearchTracker results = %+v", results)
	}
	if ft.lastSearchToken != "acct-token" {
		t.Fatalf("SearchTracker token = %q, want acct-token", ft.lastSearchToken)
	}
}

// TestSearchTracker_NotConnected confirms SearchTracker fails closed
// without a connected account.
func TestSearchTracker_NotConnected(t *testing.T) {
	client := testdb.New(t)
	ft := &fakeTracker{id: fakeTrackerID}
	svc := bind.NewService(client, tracker.NewRegistry(ft), t.TempDir())

	if _, err := svc.SearchTracker(context.Background(), ft.id, "q"); err != bind.ErrTrackerNotConnected {
		t.Fatalf("SearchTracker: err = %v, want bind.ErrTrackerNotConnected", err)
	}
}

// TestSearchTracker_ReactiveTokenExpiryFlagsConnection confirms an authed
// Search call that itself reports tracker.ErrTokenExpired (the reactive
// 401-exhausted signal — distinct from accountToken's own PROACTIVE
// pre-call expiry check) flags the connection row token_expired=true,
// WITHOUT masking the original error returned to the caller (§16: the flag
// is a side effect, never a substitute for surfacing the real failure).
func TestSearchTracker_ReactiveTokenExpiryFlagsConnection(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	ft := &fakeTracker{
		id: fakeTrackerID,
		searchFn: func(context.Context, string, string) ([]tracker.TrackSearchResult, error) {
			return nil, tracker.ErrTokenExpired
		},
	}
	seedConnection(ctx, t, client, ft.id, "acct-token")
	svc := bind.NewService(client, tracker.NewRegistry(ft), t.TempDir())

	_, err := svc.SearchTracker(ctx, ft.id, "q")
	if !errors.Is(err, tracker.ErrTokenExpired) {
		t.Fatalf("SearchTracker err = %v, want it to wrap tracker.ErrTokenExpired (not masked)", err)
	}

	conn, qerr := client.TrackerConnection.Query().Where(enttrackerconnection.TrackerID(ft.id)).Only(ctx)
	if qerr != nil {
		t.Fatalf("reload tracker connection: %v", qerr)
	}
	if !conn.TokenExpired {
		t.Fatalf("token_expired = false after a reactive ErrTokenExpired, want true")
	}
}

// TestSearchTracker_NotFound confirms SearchTracker fails closed for an
// unregistered trackerID.
func TestSearchTracker_NotFound(t *testing.T) {
	client := testdb.New(t)
	svc := bind.NewService(client, tracker.NewRegistry(), t.TempDir())

	if _, err := svc.SearchTracker(context.Background(), 9999, "q"); err != bind.ErrTrackerNotFound {
		t.Fatalf("SearchTracker: err = %v, want bind.ErrTrackerNotFound", err)
	}
}

// TestBind_SidecarRoundTripSurvivesDBWipe is THE load-bearing durability
// proof (spec/trackers-oauth-phase3 §3/§5): Bind writes a TrackBinding row
// AND its sidecar block; wiping the DB row and running disk.Reconcile over
// the SAME storage root restores the row from the sidecar snapshot alone —
// with ZERO tracker calls (disk.Reconcile takes no tracker parameter at
// all, so a provider/tracker call during reconcile is structurally
// impossible — the call-count assertion below just re-proves it
// concretely, mirroring disk.restoreMetadataIndex's own "zero calls" proof
// for the Phase-1 metadata block).
func TestBind_SidecarRoundTripSurvivesDBWipe(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()
	row := seedBoundSeries(ctx, t, client, storage, "Round Trip Series")

	ft := &fakeTracker{
		id:         901,
		getEntryFn: entryFn(tracker.TrackEntry{Status: "current", Progress: 30, Score: 7}),
	}
	seedConnection(ctx, t, client, ft.id, "acct-token")
	svc := bind.NewService(client, tracker.NewRegistry(ft), storage)

	binding, err := svc.Bind(ctx, row.ID, ft.id, "remote-abc")
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	getCallsBeforeWipe, saveCallsBeforeWipe := ft.getEntryCalls, ft.saveEntryCalls

	wipeTrackBinding(ctx, t, client, binding.ID, row.ID)

	if _, err := disk.Reconcile(ctx, client, storage); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if ft.getEntryCalls != getCallsBeforeWipe || ft.saveEntryCalls != saveCallsBeforeWipe {
		t.Fatalf("Reconcile made tracker calls: getEntry %d->%d, saveEntry %d->%d",
			getCallsBeforeWipe, ft.getEntryCalls, saveCallsBeforeWipe, ft.saveEntryCalls)
	}

	restored, err := client.TrackBinding.Query().
		Where(enttrackbinding.SeriesID(row.ID), enttrackbinding.TrackerID(ft.id)).
		Only(ctx)
	if err != nil {
		t.Fatalf("query restored TrackBinding: %v", err)
	}
	if restored.RemoteID != "remote-abc" || restored.Status != "current" ||
		restored.LastChapterRead != 30 || restored.Score != 7 {
		t.Fatalf("restored binding = %+v, want the pre-wipe sidecar snapshot", restored)
	}
}

// wipeTrackBinding deletes bindingID and confirms the deletion landed —
// the "total DB loss" simulation TestBind_SidecarRoundTripSurvivesDBWipe
// recovers from via disk.Reconcile. Extracted so the test itself stays
// under the fleet's per-function cyclomatic-complexity budget.
func wipeTrackBinding(ctx context.Context, t *testing.T, client *ent.Client, bindingID, seriesID uuid.UUID) {
	t.Helper()
	if _, err := client.TrackBinding.Delete().Where(enttrackbinding.IDEQ(bindingID)).Exec(ctx); err != nil {
		t.Fatalf("wipe TrackBinding: %v", err)
	}
	if count, _ := client.TrackBinding.Query().Where(enttrackbinding.SeriesID(seriesID)).Count(ctx); count != 0 {
		t.Fatalf("wipe did not remove the row (count=%d)", count)
	}
}
