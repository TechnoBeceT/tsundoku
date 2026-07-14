package syncsvc_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/tracker"
)

// TestSyncOnView_DelegatesToConverge confirms SyncOnView performs the SAME
// max-wins convergence SyncNow does — proven the same way TestSyncNow_
// LocalAheadPushesBack proves SyncNow's push-back: a local value strictly
// ahead of the remote's report gets adopted AND pushed back.
func TestSyncOnView_DelegatesToConverge(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "View Sync Delegates", "view-sync-delegates")
	seedConnection(ctx, t, client, fakeTrackerID, "acct-token")
	binding := seedBinding(ctx, t, client, seriesID, fakeTrackerID, "r1", 60, 0)

	ft := &fakeTracker{
		id: fakeTrackerID,
		getEntryFn: func(_ context.Context, _, remoteID string) (*tracker.TrackEntry, error) {
			return &tracker.TrackEntry{RemoteID: remoteID, Progress: 50}, nil
		},
	}
	svc := newService(client, ft, nil, nil)

	if err := svc.SyncOnView(ctx, seriesID); err != nil {
		t.Fatalf("SyncOnView: %v", err)
	}

	fresh := reloadBinding(ctx, t, client, binding.ID)
	if fresh.LastChapterRead != 60 {
		t.Fatalf("LastChapterRead = %v, want 60 (converged to the local-ahead value)", fresh.LastChapterRead)
	}
	if ft.updateEntryCalls != 1 || ft.lastUpdateEntry.Progress != 60 {
		t.Fatalf("push-back = calls=%d entry=%+v, want 1 call with Progress=60", ft.updateEntryCalls, ft.lastUpdateEntry)
	}
}

// TestSyncOnView_UngatedByAutoUpdateTrack proves SyncOnView is NOT gated by
// auto_update_track — unlike PushProgress (the reading-triggered push, which
// consults AutoUpdateTracker and silent-no-ops when the toggle is off),
// SyncOnView is the series-detail-open trigger: a deliberate view action, so
// it converges regardless of the toggle. With autoUpdate disabled, a
// PushProgress call on the same binding would no-op (see push.go's GATING
// doc comment); SyncOnView still performs the full converge + push-back.
func TestSyncOnView_UngatedByAutoUpdateTrack(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "View Sync Ungated", "view-sync-ungated")
	seedConnection(ctx, t, client, fakeTrackerID, "acct-token")
	binding := seedBinding(ctx, t, client, seriesID, fakeTrackerID, "r1", 60, 0)

	ft := &fakeTracker{
		id: fakeTrackerID,
		getEntryFn: func(_ context.Context, _, remoteID string) (*tracker.TrackEntry, error) {
			return &tracker.TrackEntry{RemoteID: remoteID, Progress: 50}, nil
		},
	}
	// autoUpdate is explicitly DISABLED — the exact opposite of PushProgress's
	// gate, proving SyncOnView doesn't consult it at all.
	svc := newService(client, ft, nil, fakeAutoUpdate{enabled: false})

	// Sanity: with the toggle off, PushProgress itself would no-op on this
	// same binding (distinguishes SyncOnView's ungated behavior from it).
	if err := svc.PushProgress(ctx, seriesID, 70); err != nil {
		t.Fatalf("PushProgress (sanity, toggle off): %v", err)
	}
	if ft.updateEntryCalls != 0 {
		t.Fatalf("PushProgress with auto_update_track disabled should no-op; got %d UpdateEntry calls", ft.updateEntryCalls)
	}

	if err := svc.SyncOnView(ctx, seriesID); err != nil {
		t.Fatalf("SyncOnView: %v", err)
	}

	fresh := reloadBinding(ctx, t, client, binding.ID)
	if fresh.LastChapterRead != 60 {
		t.Fatalf("LastChapterRead = %v, want 60 — SyncOnView must converge even with auto_update_track disabled", fresh.LastChapterRead)
	}
	if ft.updateEntryCalls != 1 || ft.lastUpdateEntry.Progress != 60 {
		t.Fatalf("push-back = calls=%d entry=%+v, want 1 call with Progress=60 despite the toggle being off", ft.updateEntryCalls, ft.lastUpdateEntry)
	}
}

// TestSyncOnView_NoBindingsReturnsNil confirms a series with zero bindings
// is a clean no-op (mirrors TestSyncNow_NoBindingsReturnsEmpty).
func TestSyncOnView_NoBindingsReturnsNil(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "View Sync No Bindings", "view-sync-no-bindings")
	svc := newService(client, &fakeTracker{id: fakeTrackerID}, nil, nil)

	if err := svc.SyncOnView(ctx, seriesID); err != nil {
		t.Fatalf("SyncOnView: %v", err)
	}
}
