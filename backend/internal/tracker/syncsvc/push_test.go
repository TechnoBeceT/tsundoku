package syncsvc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entpendingtrackpush "github.com/technobecet/tsundoku/internal/ent/pendingtrackpush"
	enttrackbinding "github.com/technobecet/tsundoku/internal/ent/trackbinding"
	"github.com/technobecet/tsundoku/internal/tracker"
)

// TestPushProgress_NeverRegress confirms a local furthest chapter that is
// NOT strictly greater than the binding's already-stored last_chapter_read
// never calls UpdateEntry and never enqueues a retry row (sync.NextPush's
// never-regress rule, reused verbatim).
func TestPushProgress_NeverRegress(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Never Regress", "never-regress")
	seedConnection(ctx, t, client, fakeTrackerID, "acct-token")
	binding := seedBinding(ctx, t, client, seriesID, fakeTrackerID, "r1", 50, 0)

	ft := &fakeTracker{id: fakeTrackerID}
	sidecar := &fakeSidecar{}
	svc := newService(client, ft, sidecar, nil)

	for _, local := range []float64{50, 40} {
		if err := svc.PushProgress(ctx, seriesID, local); err != nil {
			t.Fatalf("PushProgress(%v): %v", local, err)
		}
	}

	if ft.updateEntryCalls != 0 {
		t.Fatalf("UpdateEntry calls = %d, want 0 (local never ahead of remote)", ft.updateEntryCalls)
	}
	assertPendingCount(ctx, t, client, binding.ID, 0)
	fresh := reloadBinding(ctx, t, client, binding.ID)
	if fresh.LastChapterRead != 50 {
		t.Fatalf("LastChapterRead = %v, want unchanged 50", fresh.LastChapterRead)
	}
}

// TestPushProgress_LocalAheadPushesTruncatedValue confirms a local furthest
// chapter strictly ahead of the binding's remote progress calls UpdateEntry
// with the truncated (sync.TruncateForInteger) value, persists it locally,
// and mirrors the sidecar.
func TestPushProgress_LocalAheadPushesTruncatedValue(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Ahead", "ahead")
	seedConnection(ctx, t, client, fakeTrackerID, "acct-token")
	binding := seedBinding(ctx, t, client, seriesID, fakeTrackerID, "r2", 10, 0)

	ft := &fakeTracker{id: fakeTrackerID}
	sidecar := &fakeSidecar{}
	svc := newService(client, ft, sidecar, nil)

	if err := svc.PushProgress(ctx, seriesID, 12.7); err != nil {
		t.Fatalf("PushProgress: %v", err)
	}

	if ft.updateEntryCalls != 1 {
		t.Fatalf("UpdateEntry calls = %d, want 1", ft.updateEntryCalls)
	}
	if ft.lastUpdateEntry.Progress != 12 {
		t.Fatalf("pushed Progress = %v, want 12 (floor of 12.7)", ft.lastUpdateEntry.Progress)
	}
	if ft.lastToken != "acct-token" {
		t.Fatalf("pushed token = %q, want acct-token", ft.lastToken)
	}

	fresh := reloadBinding(ctx, t, client, binding.ID)
	if fresh.LastChapterRead != 12 {
		t.Fatalf("LastChapterRead = %v, want 12", fresh.LastChapterRead)
	}
	if sidecar.calls != 1 || sidecar.lastSeriesID != seriesID {
		t.Fatalf("sidecar sync calls = %d lastSeriesID = %v, want 1 call for series %v", sidecar.calls, sidecar.lastSeriesID, seriesID)
	}
}

// TestPushProgress_PreservesScorePrivateStatusOnAdvance confirms a NORMAL
// advance push (local strictly ahead, not completing) carries the binding's
// EXISTING Score/Private/Status to UpdateEntry UNCHANGED — the
// pre-activation data-corruption bug this fix closes: every concrete
// Tracker client full-field-writes, so a sparse entry (only RemoteID/
// LibraryID/Progress) used to silently clobber the remote's score to 0,
// flip private to public, and send an empty (AniList-rejected) status on
// every single advance.
func TestPushProgress_PreservesScorePrivateStatusOnAdvance(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Preserve Fields", "preserve-fields")
	seedConnection(ctx, t, client, fakeTrackerID, "acct-token")
	binding := seedBinding(ctx, t, client, seriesID, fakeTrackerID, "r8", 10, 0)
	setBindingScorePrivateStatus(ctx, t, client, binding.ID, 9, true, "reading")

	ft := &fakeTracker{id: fakeTrackerID}
	svc := newService(client, ft, nil, nil)

	if err := svc.PushProgress(ctx, seriesID, 15); err != nil {
		t.Fatalf("PushProgress: %v", err)
	}

	if ft.updateEntryCalls != 1 {
		t.Fatalf("UpdateEntry calls = %d, want 1", ft.updateEntryCalls)
	}
	pushed := ft.lastUpdateEntry
	if pushed.Score != 9 || !pushed.Private || pushed.Status != "reading" {
		t.Fatalf("pushed entry = %+v, want Score=9 Private=true Status=reading preserved (NOT zero/empty)", pushed)
	}
	if pushed.Progress != 15 {
		t.Fatalf("pushed Progress = %v, want 15", pushed.Progress)
	}
}

// TestPushProgress_FailureEnqueuesForRetry confirms a push failure never
// loses progress: it is durably enqueued in the retry queue (coalescing key
// = the track_binding_id) for a later drain.
func TestPushProgress_FailureEnqueuesForRetry(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Failing Push", "failing-push")
	seedConnection(ctx, t, client, fakeTrackerID, "acct-token")
	binding := seedBinding(ctx, t, client, seriesID, fakeTrackerID, "r3", 1, 0)

	ft := &fakeTracker{
		id: fakeTrackerID,
		updateEntryFn: func(context.Context, string, tracker.TrackEntry) (tracker.TrackEntry, error) {
			return tracker.TrackEntry{}, errors.New("upstream is down")
		},
	}
	svc := newService(client, ft, nil, nil)

	if err := svc.PushProgress(ctx, seriesID, 5); err == nil {
		t.Fatalf("PushProgress with a failing UpdateEntry: want an error, got nil")
	}

	assertPendingCount(ctx, t, client, binding.ID, 1)
	pending, err := client.PendingTrackPush.Query().Where(entpendingtrackpush.TrackBindingID(binding.ID)).Only(ctx)
	if err != nil {
		t.Fatalf("query pending push: %v", err)
	}
	if pending.Chapter != 5 {
		t.Fatalf("pending.Chapter = %v, want 5", pending.Chapter)
	}

	// The local row is untouched — the failed push never landed.
	fresh := reloadBinding(ctx, t, client, binding.ID)
	if fresh.LastChapterRead != 1 {
		t.Fatalf("LastChapterRead after a failed push = %v, want unchanged 1", fresh.LastChapterRead)
	}
}

// TestPushProgress_AutoCompleteOnlyWhenTotalKnown confirms the auto-complete
// status/finish-date transition (sync.ShouldAutoComplete) fires ONLY when
// the binding's total_chapters is non-zero AND the pushed value reaches it —
// an ongoing series (total=0) must never auto-complete no matter how high
// the pushed chapter is.
func TestPushProgress_AutoCompleteOnlyWhenTotalKnown(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)

	t.Run("total zero never auto-completes", func(t *testing.T) {
		seriesID := seedSeries(ctx, t, client, "Ongoing", "ongoing")
		seedConnection(ctx, t, client, fakeTrackerID, "acct-token")
		binding := seedBinding(ctx, t, client, seriesID, fakeTrackerID, "r4", 0, 0)
		ft := &fakeTracker{id: fakeTrackerID}
		svc := newService(client, ft, nil, nil)

		if err := svc.PushProgress(ctx, seriesID, 500); err != nil {
			t.Fatalf("PushProgress: %v", err)
		}
		assertNoAutoComplete(t, ft.lastUpdateEntry, reloadBinding(ctx, t, client, binding.ID))
	})

	t.Run("reaching a known total auto-completes", func(t *testing.T) {
		// A REAL tracker id is required here (not a synthetic fakeTrackerID+1):
		// completedStatus (status.go) deliberately maps only trackers with a
		// known native "completed" vocabulary — a synthetic id would leave
		// entry.Status unset even though the auto-complete FinishDate branch
		// fired, which is exactly what made this subtest fail.
		trackerID := tracker.IDAniList
		seriesID := seedSeries(ctx, t, client, "Finished", "finished")
		seedConnection(ctx, t, client, trackerID, "acct-token-2")
		binding := seedBinding(ctx, t, client, seriesID, trackerID, "r5", 10, 20)
		// Score=6/Private=true pre-exist on the remote (e.g. the owner rated
		// it mid-read) — the auto-complete transition must layer FinishDate +
		// the completed status on TOP of these, never reset them.
		setBindingScorePrivateStatus(ctx, t, client, binding.ID, 6, true, "CURRENT")
		ft := &fakeTracker{id: trackerID}
		svc := newService(client, ft, nil, nil)

		if err := svc.PushProgress(ctx, seriesID, 20); err != nil {
			t.Fatalf("PushProgress: %v", err)
		}
		assertAutoCompleted(t, ft.lastUpdateEntry, reloadBinding(ctx, t, client, binding.ID), "COMPLETED")
		if ft.lastUpdateEntry.Score != 6 || !ft.lastUpdateEntry.Private {
			t.Fatalf("pushed entry = %+v, want Score=6 Private=true preserved through auto-complete", ft.lastUpdateEntry)
		}
	})
}

// assertNoAutoComplete fails t unless neither the just-pushed entry nor the
// persisted binding show any auto-complete signal (native status or finish
// date) — the total-unknown branch of the auto-complete rule.
func assertNoAutoComplete(t *testing.T, pushed tracker.TrackEntry, persisted *ent.TrackBinding) {
	t.Helper()
	if pushed.Status != "" || pushed.FinishDate != nil {
		t.Fatalf("entry = %+v, want no status/finishDate (total unknown)", pushed)
	}
	if persisted.Status != "" || persisted.FinishDate != nil {
		t.Fatalf("binding = %+v, want no auto-complete", persisted)
	}
}

// assertAutoCompleted fails t unless both the just-pushed entry and the
// persisted binding show the auto-complete signal (wantStatus + a finish
// date) — the known-total branch of the auto-complete rule.
func assertAutoCompleted(t *testing.T, pushed tracker.TrackEntry, persisted *ent.TrackBinding, wantStatus string) {
	t.Helper()
	if pushed.Status != wantStatus || pushed.FinishDate == nil {
		t.Fatalf("entry = %+v, want status=%s + a finishDate", pushed, wantStatus)
	}
	if persisted.Status != wantStatus || persisted.FinishDate == nil {
		t.Fatalf("binding = %+v, want auto-completed", persisted)
	}
}

// TestPushProgress_PerBindingIsolation confirms one binding's push failure
// never aborts another binding's own push in the same PushProgress call.
func TestPushProgress_PerBindingIsolation(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Multi Binding", "multi-binding")

	failingID, okID := fakeTrackerID, fakeTrackerID+1
	seedConnection(ctx, t, client, failingID, "token-fail")
	seedConnection(ctx, t, client, okID, "token-ok")
	failBinding := seedBinding(ctx, t, client, seriesID, failingID, "rf", 0, 0)
	okBinding := seedBinding(ctx, t, client, seriesID, okID, "ro", 0, 0)

	failFt := &fakeTracker{
		id: failingID,
		updateEntryFn: func(context.Context, string, tracker.TrackEntry) (tracker.TrackEntry, error) {
			return tracker.TrackEntry{}, errors.New("boom")
		},
	}
	okFt := &fakeTracker{id: okID}
	svc := newServiceMulti(client, []tracker.Tracker{failFt, okFt}, nil, nil)

	if err := svc.PushProgress(ctx, seriesID, 3); err == nil {
		t.Fatalf("PushProgress: want an aggregated error (one binding failed), got nil")
	}

	if okFt.updateEntryCalls != 1 {
		t.Fatalf("ok tracker UpdateEntry calls = %d, want 1 (isolation: the other binding's failure must not skip this one)", okFt.updateEntryCalls)
	}
	assertPendingCount(ctx, t, client, failBinding.ID, 1)
	assertPendingCount(ctx, t, client, okBinding.ID, 0)

	okFresh := reloadBinding(ctx, t, client, okBinding.ID)
	if okFresh.LastChapterRead != 3 {
		t.Fatalf("ok binding LastChapterRead = %v, want 3", okFresh.LastChapterRead)
	}
}

// TestPushProgress_GatedByAutoUpdateTrack confirms an AutoUpdateTracker
// reporting the toggle off makes PushProgress a complete no-op (no tracker
// call, no local write, no enqueue).
func TestPushProgress_GatedByAutoUpdateTrack(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Toggled Off", "toggled-off")
	seedConnection(ctx, t, client, fakeTrackerID, "acct-token")
	binding := seedBinding(ctx, t, client, seriesID, fakeTrackerID, "r6", 0, 0)

	ft := &fakeTracker{id: fakeTrackerID}
	svc := newService(client, ft, nil, fakeAutoUpdate{enabled: false})

	if err := svc.PushProgress(ctx, seriesID, 99); err != nil {
		t.Fatalf("PushProgress: %v", err)
	}
	if ft.updateEntryCalls != 0 {
		t.Fatalf("UpdateEntry calls = %d, want 0 (auto_update_track is off)", ft.updateEntryCalls)
	}
	assertPendingCount(ctx, t, client, binding.ID, 0)
}

// TestPushProgress_FiltersUnparseableChapterNumber confirms the chapter
// normaliser's unparseable sentinel (-1) is filtered out of sync entirely
// (sync.SyncableNumbers) rather than being pushed as a bogus progress value.
func TestPushProgress_FiltersUnparseableChapterNumber(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Unparseable", "unparseable")
	seedConnection(ctx, t, client, fakeTrackerID, "acct-token")
	seedBinding(ctx, t, client, seriesID, fakeTrackerID, "r7", 0, 0)

	ft := &fakeTracker{id: fakeTrackerID}
	svc := newService(client, ft, nil, nil)

	if err := svc.PushProgress(ctx, seriesID, -1); err != nil {
		t.Fatalf("PushProgress: %v", err)
	}
	if ft.updateEntryCalls != 0 {
		t.Fatalf("UpdateEntry calls = %d, want 0 (unparseable sentinel must never sync)", ft.updateEntryCalls)
	}
}

// reloadBinding re-reads bindingID's current row.
func reloadBinding(ctx context.Context, t *testing.T, client *ent.Client, bindingID uuid.UUID) *ent.TrackBinding {
	t.Helper()
	row, err := client.TrackBinding.Query().Where(enttrackbinding.IDEQ(bindingID)).Only(ctx)
	if err != nil {
		t.Fatalf("reload binding %s: %v", bindingID, err)
	}
	return row
}

// assertPendingCount fails the test unless the retry queue holds exactly
// want rows for bindingID.
func assertPendingCount(ctx context.Context, t *testing.T, client *ent.Client, bindingID uuid.UUID, want int) {
	t.Helper()
	count, err := client.PendingTrackPush.Query().Where(entpendingtrackpush.TrackBindingID(bindingID)).Count(ctx)
	if err != nil {
		t.Fatalf("count pending pushes for binding %s: %v", bindingID, err)
	}
	if count != want {
		t.Fatalf("pending push count for binding %s = %d, want %d", bindingID, count, want)
	}
}
