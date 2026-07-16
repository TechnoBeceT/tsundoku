package syncsvc_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/tracker"
)

// TestSyncNow_LocalAheadPushesBack confirms SyncNow's max-wins convergence
// (sync.Converge) adopts the higher LOCAL value and pushes it back to the
// remote when local was strictly ahead of what GetEntry reports — carrying
// the just-fetched remote's OWN Score/Private/Status unchanged (never the
// stale pre-sync local row's) so the push-back can never clobber them, the
// pushBack half of the pre-activation data-corruption fix.
func TestSyncNow_LocalAheadPushesBack(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Local Ahead", "local-ahead")
	seedConnection(ctx, t, client, fakeTrackerID, "acct-token")
	binding := seedBinding(ctx, t, client, seriesID, fakeTrackerID, "r1", 60, 0)

	ft := &fakeTracker{
		id: fakeTrackerID,
		getEntryFn: func(_ context.Context, _, remoteID string) (*tracker.TrackEntry, error) {
			return &tracker.TrackEntry{RemoteID: remoteID, Progress: 50, Score: 8, Private: true, Status: "CURRENT"}, nil
		},
	}
	svc := newService(client, ft, nil, nil)

	out, err := svc.SyncNow(ctx, seriesID)
	if err != nil {
		t.Fatalf("SyncNow: %v", err)
	}
	if len(out) != 1 || out[0].LastChapterRead != 60 {
		t.Fatalf("SyncNow result = %+v, want converged to local's 60", out)
	}
	if ft.updateEntryCalls != 1 || ft.lastUpdateEntry.Progress != 60 {
		t.Fatalf("push-back = calls=%d entry=%+v, want 1 call with Progress=60", ft.updateEntryCalls, ft.lastUpdateEntry)
	}
	if ft.lastUpdateEntry.Score != 8 || !ft.lastUpdateEntry.Private || ft.lastUpdateEntry.Status != "CURRENT" {
		t.Fatalf("push-back entry = %+v, want the just-fetched remote's Score=8/Private=true/Status=CURRENT carried through unchanged (NOT zero/false/empty)", ft.lastUpdateEntry)
	}

	fresh := reloadBinding(ctx, t, client, binding.ID)
	if fresh.LastChapterRead != 60 {
		t.Fatalf("LastChapterRead = %v, want 60", fresh.LastChapterRead)
	}
}

// TestSyncNow_RemoteAheadAdoptsRemoteWithoutPushing confirms the reverse
// direction: when the remote is ahead, the local row adopts the remote's
// value and NO push is sent back (there is nothing local to tell the
// remote — it already knows).
func TestSyncNow_RemoteAheadAdoptsRemoteWithoutPushing(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Remote Ahead", "remote-ahead")
	seedConnection(ctx, t, client, fakeTrackerID, "acct-token")
	binding := seedBinding(ctx, t, client, seriesID, fakeTrackerID, "r2", 10, 0)

	ft := &fakeTracker{
		id: fakeTrackerID,
		getEntryFn: func(_ context.Context, _, remoteID string) (*tracker.TrackEntry, error) {
			return &tracker.TrackEntry{RemoteID: remoteID, Progress: 80, Status: "current", TotalChapters: 100, Score: 7}, nil
		},
	}
	svc := newService(client, ft, nil, nil)

	out, err := svc.SyncNow(ctx, seriesID)
	if err != nil {
		t.Fatalf("SyncNow: %v", err)
	}
	if len(out) != 1 || out[0].LastChapterRead != 80 {
		t.Fatalf("SyncNow result = %+v, want converged to remote's 80", out)
	}
	if ft.updateEntryCalls != 0 {
		t.Fatalf("UpdateEntry calls = %d, want 0 (remote was already ahead, nothing to push)", ft.updateEntryCalls)
	}

	fresh := reloadBinding(ctx, t, client, binding.ID)
	if fresh.LastChapterRead != 80 || fresh.Status != "current" || fresh.TotalChapters != 100 || fresh.Score != 7 {
		t.Fatalf("binding after SyncNow = %+v, want the remote snapshot adopted", fresh)
	}
}

// TestSyncNow_EqualNeverPushes confirms exactly-equal local/remote progress
// converges to the same value without an UpdateEntry call.
func TestSyncNow_EqualNeverPushes(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Equal", "equal")
	seedConnection(ctx, t, client, fakeTrackerID, "acct-token")
	seedBinding(ctx, t, client, seriesID, fakeTrackerID, "r3", 30, 0)

	ft := &fakeTracker{
		id: fakeTrackerID,
		getEntryFn: func(_ context.Context, _, remoteID string) (*tracker.TrackEntry, error) {
			return &tracker.TrackEntry{RemoteID: remoteID, Progress: 30}, nil
		},
	}
	svc := newService(client, ft, nil, nil)

	if _, err := svc.SyncNow(ctx, seriesID); err != nil {
		t.Fatalf("SyncNow: %v", err)
	}
	if ft.updateEntryCalls != 0 {
		t.Fatalf("UpdateEntry calls = %d, want 0 (already converged)", ft.updateEntryCalls)
	}
}

// TestSyncNow_RemoteGoneLeavesBindingUnchanged confirms a nil GetEntry
// result (the manga vanished from the account's list) leaves the existing
// row untouched rather than erroring or zeroing it.
func TestSyncNow_RemoteGoneLeavesBindingUnchanged(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Gone", "gone")
	seedConnection(ctx, t, client, fakeTrackerID, "acct-token")
	seedBinding(ctx, t, client, seriesID, fakeTrackerID, "r4", 15, 0)

	ft := &fakeTracker{id: fakeTrackerID} // getEntryFn nil ⇒ (nil, nil)
	svc := newService(client, ft, nil, nil)

	out, err := svc.SyncNow(ctx, seriesID)
	if err != nil {
		t.Fatalf("SyncNow: %v", err)
	}
	if len(out) != 1 || out[0].LastChapterRead != 15 {
		t.Fatalf("SyncNow result = %+v, want the row unchanged at 15", out)
	}
}

// TestSyncNow_NoBindingsReturnsEmpty confirms a series with zero bindings
// yields an empty (non-nil-checked-by-len) result and no error.
func TestSyncNow_NoBindingsReturnsEmpty(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "No Bindings", "no-bindings")
	svc := newService(client, &fakeTracker{id: fakeTrackerID}, nil, nil)

	out, err := svc.SyncNow(ctx, seriesID)
	if err != nil {
		t.Fatalf("SyncNow: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("SyncNow result = %+v, want empty", out)
	}
}

// TestSyncNow_LocalLibraryReadCountConverges proves the THREE-WAY
// convergence's new leg: the local library is read far ahead of BOTH the
// binding's stored value and what the remote currently reports, so the
// series' own read-count (seriesLocalFurthest) — not just the binding row —
// must win and get pushed back to the remote. This is the "converge on add"
// behavior the reference apps (Suwayomi/Komikku) have and Tsundoku's old
// two-way Converge(b.LastChapterRead, remote.Progress) could never reach,
// since neither of those two inputs ever reflected the local read-state.
func TestSyncNow_LocalLibraryReadCountConverges(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Local Library Ahead", "local-library-ahead")
	seedConnection(ctx, t, client, fakeTrackerID, "acct-token")
	binding := seedBinding(ctx, t, client, seriesID, fakeTrackerID, "r1", 5, 0)

	for i := 1; i <= 70; i++ {
		ch := seedChapter(ctx, t, client, seriesID, chKey(i), float64(i))
		if i <= 60 {
			markChapterRead(ctx, t, client, ch.ID, time.Now().UTC())
		}
	}

	ft := &fakeTracker{
		id: fakeTrackerID,
		getEntryFn: func(_ context.Context, _, remoteID string) (*tracker.TrackEntry, error) {
			return &tracker.TrackEntry{RemoteID: remoteID, Progress: 10, Score: 8, Private: true, Status: "CURRENT"}, nil
		},
	}
	svc := newService(client, ft, nil, nil)

	out, err := svc.SyncNow(ctx, seriesID)
	if err != nil {
		t.Fatalf("SyncNow: %v", err)
	}
	assertLocalLibraryReadCountConverged(t, out, ft, binding.ID)

	fresh := reloadBinding(ctx, t, client, binding.ID)
	if fresh.LastChapterRead != 60 {
		t.Fatalf("LastChapterRead = %v, want 60", fresh.LastChapterRead)
	}
}

// assertLocalLibraryReadCountConverged fails the test unless out/ft carry
// exactly the values TestSyncNow_LocalLibraryReadCountConverges expects —
// extracted so the driving test stays under the fleet's per-function
// cyclomatic-complexity budget (mirrors handler/trackers'
// assertCreatedBindingFields).
func assertLocalLibraryReadCountConverged(t *testing.T, out []*ent.TrackBinding, ft *fakeTracker, bindingID uuid.UUID) {
	t.Helper()
	if len(out) != 1 || out[0].ID != bindingID || out[0].LastChapterRead != 60 {
		t.Fatalf("SyncNow result = %+v, want converged to the local library's read-count (60), not the stored 5 or remote's 10", out)
	}
	if ft.updateEntryCalls != 1 || ft.lastUpdateEntry.Progress != 60 {
		t.Fatalf("push-back = calls=%d entry=%+v, want 1 call pushing Progress=60", ft.updateEntryCalls, ft.lastUpdateEntry)
	}
	if ft.lastUpdateEntry.Score != 8 || !ft.lastUpdateEntry.Private || ft.lastUpdateEntry.Status != "CURRENT" {
		t.Fatalf("push-back entry = %+v, want the just-fetched remote's Score/Private/Status carried through unchanged", ft.lastUpdateEntry)
	}
}

// TestSyncNow_NeverRegressWhenRemoteRegressesAndLocalHasNothingRead is the
// never-regress proof for the three-way chain: a prior sync already
// converged the binding to 60; the remote's report has since REGRESSED to
// 10 (e.g. the owner's account got reset/corrupted on the tracker's side),
// and the local library currently has ZERO chapters marked read (e.g. a
// fresh reconcile). Without b.LastChapterRead staying in the convergence
// chain as a floor, max(localFurthest=0, remote.Progress=10) would silently
// drag the binding DOWN to 10 — this proves it stays at 60 instead, and the
// regressed remote gets the correct value of 60 pushed back to restore it.
func TestSyncNow_NeverRegressWhenRemoteRegressesAndLocalHasNothingRead(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Remote Regressed", "remote-regressed")
	seedConnection(ctx, t, client, fakeTrackerID, "acct-token")
	binding := seedBinding(ctx, t, client, seriesID, fakeTrackerID, "r1", 60, 0)

	// No chapters seeded at all: seriesLocalFurthest must return 0, not error.

	ft := &fakeTracker{
		id: fakeTrackerID,
		getEntryFn: func(_ context.Context, _, remoteID string) (*tracker.TrackEntry, error) {
			return &tracker.TrackEntry{RemoteID: remoteID, Progress: 10}, nil
		},
	}
	svc := newService(client, ft, nil, nil)

	out, err := svc.SyncNow(ctx, seriesID)
	if err != nil {
		t.Fatalf("SyncNow: %v", err)
	}
	if len(out) != 1 || out[0].LastChapterRead != 60 {
		t.Fatalf("SyncNow result = %+v, want the binding to STAY at 60 (never regress), not drop to the regressed remote's 10", out)
	}
	if ft.updateEntryCalls != 1 || ft.lastUpdateEntry.Progress != 60 {
		t.Fatalf("push-back = calls=%d entry=%+v, want 1 call RESTORING the regressed remote to Progress=60", ft.updateEntryCalls, ft.lastUpdateEntry)
	}

	fresh := reloadBinding(ctx, t, client, binding.ID)
	if fresh.LastChapterRead != 60 {
		t.Fatalf("LastChapterRead = %v, want 60 (no local regression)", fresh.LastChapterRead)
	}
}

// TestSyncNow_ThreeWayConvergePicksMaxAcrossAllThreeSources exercises three
// different "which leg wins" shapes in one table, proving the three-way
// chain always adopts the single highest of (local library read-count,
// binding's stored value, remote's reported progress) regardless of WHICH
// of the three happens to be ahead.
func TestSyncNow_ThreeWayConvergePicksMaxAcrossAllThreeSources(t *testing.T) {
	cases := []struct {
		name                      string
		localRead, stored, remote float64
		wantConverged             float64
	}{
		{name: "local wins", localRead: 45, stored: 30, remote: 20, wantConverged: 45},
		{name: "stored wins", localRead: 10, stored: 45, remote: 30, wantConverged: 45},
		{name: "remote wins", localRead: 10, stored: 20, remote: 45, wantConverged: 45},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := newTestDB(t)
			seriesID := seedSeries(ctx, t, client, "Three Way "+tc.name, "three-way-"+tc.name)
			seedConnection(ctx, t, client, fakeTrackerID, "acct-token")
			seedBinding(ctx, t, client, seriesID, fakeTrackerID, "r1", tc.stored, 0)

			localReadInt := int(tc.localRead)
			for i := 1; i <= localReadInt; i++ {
				ch := seedChapter(ctx, t, client, seriesID, chKey(i), float64(i))
				markChapterRead(ctx, t, client, ch.ID, time.Now().UTC())
			}

			remoteProgress := tc.remote
			ft := &fakeTracker{
				id: fakeTrackerID,
				getEntryFn: func(_ context.Context, _, remoteID string) (*tracker.TrackEntry, error) {
					return &tracker.TrackEntry{RemoteID: remoteID, Progress: remoteProgress}, nil
				},
			}
			svc := newService(client, ft, nil, nil)

			out, err := svc.SyncNow(ctx, seriesID)
			if err != nil {
				t.Fatalf("SyncNow: %v", err)
			}
			if len(out) != 1 || out[0].LastChapterRead != tc.wantConverged {
				t.Fatalf("SyncNow result = %+v, want converged=%v", out, tc.wantConverged)
			}
		})
	}
}

// TestSyncNow_CrossTrackerCompletionPropagatesFromPull is the STEP-3 core
// proof: a PULL lands read-completion on one binding (AniList pulled as
// COMPLETED at 100/100) and the cross-tracker fan-out must move the OTHER
// binding — MangaUpdates, which reports no total and could never
// auto-complete on a push — to its own completed ("complete") status, WITHOUT
// the owner touching the push side at all. Before this fix CompleteSeries
// fired only from PushProgress/UpdateTrack/retry, so MangaUpdates stayed stuck
// on Reading whenever completion arrived via a pull.
func TestSyncNow_CrossTrackerCompletionPropagatesFromPull(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Pulled Complete", "pulled-complete")

	seedConnection(ctx, t, client, tracker.IDAniList, "acct-anilist")
	seedConnection(ctx, t, client, tracker.IDMangaUpdates, "acct-mu")
	aniBinding := seedBinding(ctx, t, client, seriesID, tracker.IDAniList, "a1", 50, 100)
	muBinding := seedBinding(ctx, t, client, seriesID, tracker.IDMangaUpdates, "m1", 50, 0)

	ani := &fakeTracker{
		id: tracker.IDAniList,
		getEntryFn: func(_ context.Context, _, remoteID string) (*tracker.TrackEntry, error) {
			// The owner completed it on AniList — the pull reports COMPLETED at total.
			return &tracker.TrackEntry{RemoteID: remoteID, Progress: 100, Status: "COMPLETED", TotalChapters: 100}, nil
		},
	}
	mu := &fakeTracker{
		id: tracker.IDMangaUpdates,
		getEntryFn: func(_ context.Context, _, remoteID string) (*tracker.TrackEntry, error) {
			return &tracker.TrackEntry{RemoteID: remoteID, Progress: 50, Status: "reading"}, nil
		},
	}
	svc := newServiceMulti(client, []tracker.Tracker{ani, mu}, nil, nil)

	if _, err := svc.SyncNow(ctx, seriesID); err != nil {
		t.Fatalf("SyncNow: %v", err)
	}

	gotAni := reloadBinding(ctx, t, client, aniBinding.ID)
	if gotAni.Status != "COMPLETED" || gotAni.LastChapterRead != 100 {
		t.Fatalf("AniList binding = status %q / read %v, want COMPLETED / 100", gotAni.Status, gotAni.LastChapterRead)
	}
	gotMU := reloadBinding(ctx, t, client, muBinding.ID)
	if gotMU.Status != "complete" {
		t.Fatalf("MangaUpdates binding status = %q, want complete (a pulled sibling completion must fan out to it)", gotMU.Status)
	}
}

// TestSyncNow_CompletedWithTotalDoesNotOscillateAboveOwnTotal is the STEP-3
// oscillation regression proof: when the local library's furthest-read chapter
// number (269) is STRICTLY ABOVE a completed with-total tracker's OWN total
// (MAL 268), consecutive syncs must SETTLE at 268/268 and never re-push. The
// bug: syncOneBinding let seriesLocalFurthest drag the stored value to 269
// (pushBack pushed 269), then completeOne clamped back to 268 — two remote
// writes every sync forever. This runs SyncNow TWICE and asserts the SECOND
// consecutive sync issues ZERO UpdateEntry (remote write) calls. (The existing
// SyncNow tests miss this because they all seed localFurthest=0.)
func TestSyncNow_CompletedWithTotalDoesNotOscillateAboveOwnTotal(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Overshoot Sync", "overshoot-sync")
	seedConnection(ctx, t, client, tracker.IDMAL, "acct-mal")

	binding := seedBinding(ctx, t, client, seriesID, tracker.IDMAL, "m1", 269, 268)
	setBindingScorePrivateStatus(ctx, t, client, binding.ID, 0, false, "completed")

	// Local library read to chapter 269 — ABOVE MAL's own 268-chapter total.
	ch := seedChapter(ctx, t, client, seriesID, chKey(269), 269)
	markChapterRead(ctx, t, client, ch.ID, time.Now().UTC())

	ft := &fakeTracker{
		id: tracker.IDMAL,
		getEntryFn: func(_ context.Context, _, remoteID string) (*tracker.TrackEntry, error) {
			// MAL reports its OWN catalog: completed at 268/268.
			return &tracker.TrackEntry{RemoteID: remoteID, Progress: 268, Status: "completed", TotalChapters: 268}, nil
		},
	}
	svc := newService(client, ft, nil, nil)

	if _, err := svc.SyncNow(ctx, seriesID); err != nil {
		t.Fatalf("SyncNow (1): %v", err)
	}
	writesAfterFirst := ft.updateEntryCalls

	if _, err := svc.SyncNow(ctx, seriesID); err != nil {
		t.Fatalf("SyncNow (2): %v", err)
	}
	if secondSyncWrites := ft.updateEntryCalls - writesAfterFirst; secondSyncWrites != 0 {
		t.Fatalf("second consecutive SyncNow made %d remote write(s), want 0 (a settled completed with-total binding must never re-push)", secondSyncWrites)
	}

	fresh := reloadBinding(ctx, t, client, binding.ID)
	if fresh.LastChapterRead != 268 || fresh.Status != "completed" {
		t.Fatalf("resting binding = read %v / status %q, want 268 / completed (capped to the tracker's own total, not local's 269)", fresh.LastChapterRead, fresh.Status)
	}
}

// TestSyncNow_NoCompletionWhenNoBindingReadComplete guards the negative: a sync
// where NO binding is read-complete must NOT fire the completion fan-out (no
// binding is moved to completed).
func TestSyncNow_NoCompletionWhenNoBindingReadComplete(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Still Reading", "still-reading")

	seedConnection(ctx, t, client, tracker.IDAniList, "acct-anilist")
	seedConnection(ctx, t, client, tracker.IDMangaUpdates, "acct-mu")
	aniBinding := seedBinding(ctx, t, client, seriesID, tracker.IDAniList, "a1", 40, 100)
	muBinding := seedBinding(ctx, t, client, seriesID, tracker.IDMangaUpdates, "m1", 40, 0)

	ani := &fakeTracker{id: tracker.IDAniList, getEntryFn: func(_ context.Context, _, remoteID string) (*tracker.TrackEntry, error) {
		return &tracker.TrackEntry{RemoteID: remoteID, Progress: 40, Status: "CURRENT", TotalChapters: 100}, nil
	}}
	mu := &fakeTracker{id: tracker.IDMangaUpdates, getEntryFn: func(_ context.Context, _, remoteID string) (*tracker.TrackEntry, error) {
		return &tracker.TrackEntry{RemoteID: remoteID, Progress: 40, Status: "reading"}, nil
	}}
	svc := newServiceMulti(client, []tracker.Tracker{ani, mu}, nil, nil)

	if _, err := svc.SyncNow(ctx, seriesID); err != nil {
		t.Fatalf("SyncNow: %v", err)
	}

	if got := reloadBinding(ctx, t, client, aniBinding.ID); got.Status == "COMPLETED" {
		t.Fatalf("AniList binding wrongly completed (status %q); no binding was read-complete", got.Status)
	}
	if got := reloadBinding(ctx, t, client, muBinding.ID); got.Status == "complete" {
		t.Fatalf("MangaUpdates binding wrongly completed (status %q); no binding was read-complete", got.Status)
	}
}

// TestSyncNow_LocalFurthest_UnparseableOnlyReadChapterCountsAsZero proves
// seriesLocalFurthest treats an unparseable chapter (number == -1, the
// chapter normaliser's sentinel) as NOT counting toward the local
// read-count even when it is the series' ONLY read chapter — it must
// resolve to the safe floor of 0 (never -1), exercising kernel.
// SyncableNumbers' filter on the query's single result row (distinct from
// the "no read chapters at all" not-found path exercised elsewhere).
func TestSyncNow_LocalFurthest_UnparseableOnlyReadChapterCountsAsZero(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Unparseable Only Read", "unparseable-only-read")
	seedConnection(ctx, t, client, fakeTrackerID, "acct-token")
	seedBinding(ctx, t, client, seriesID, fakeTrackerID, "r1", 5, 0)

	unparseable := seedChapter(ctx, t, client, seriesID, "unparseable", -1)
	markChapterRead(ctx, t, client, unparseable.ID, time.Now().UTC())

	ft := &fakeTracker{
		id: fakeTrackerID,
		getEntryFn: func(_ context.Context, _, remoteID string) (*tracker.TrackEntry, error) {
			return &tracker.TrackEntry{RemoteID: remoteID, Progress: 3}, nil
		},
	}
	svc := newService(client, ft, nil, nil)

	out, err := svc.SyncNow(ctx, seriesID)
	if err != nil {
		t.Fatalf("SyncNow: %v", err)
	}
	// local read-count must resolve to 0 (never -1), so the stored value (5)
	// — the highest of {0, 5, 3} — wins.
	if len(out) != 1 || out[0].LastChapterRead != 5 {
		t.Fatalf("SyncNow result = %+v, want converged=5 (the unparseable -1 chapter must never count as local progress)", out)
	}
}
