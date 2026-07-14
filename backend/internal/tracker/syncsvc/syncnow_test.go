package syncsvc_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/tracker"
)

// TestSyncNow_LocalAheadPushesBack confirms SyncNow's max-wins convergence
// (sync.Converge) adopts the higher LOCAL value and pushes it back to the
// remote when local was strictly ahead of what GetEntry reports.
func TestSyncNow_LocalAheadPushesBack(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Local Ahead", "local-ahead")
	seedConnection(ctx, t, client, fakeTrackerID, "acct-token")
	binding := seedBinding(ctx, t, client, seriesID, fakeTrackerID, "r1", 60, 0)

	ft := &fakeTracker{
		id: fakeTrackerID,
		getEntryFn: func(_ context.Context, _, remoteID string) (*tracker.TrackEntry, error) {
			return &tracker.TrackEntry{RemoteID: remoteID, Progress: 50}, nil
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
