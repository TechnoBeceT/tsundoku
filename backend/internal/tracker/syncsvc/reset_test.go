package syncsvc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/technobecet/tsundoku/internal/tracker"
)

// TestSetSeriesProgress_ForceSetsBelowCurrentValue is the never-regress-
// BYPASS proof (QCAT-242): the binding starts at chapter 50 and the owner
// resets to chapter 10 — a regression sync.NextPush would ordinarily
// decline. SetSeriesProgress must still push it, and persist it, exactly at
// 10 (not the never-regressed 50).
func TestSetSeriesProgress_ForceSetsBelowCurrentValue(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Regress Me", "regress-me")
	seedConnection(ctx, t, client, fakeTrackerID, "acct-token")
	binding := seedBinding(ctx, t, client, seriesID, fakeTrackerID, "r1", 50, 0)

	ft := &fakeTracker{id: fakeTrackerID}
	svc := newService(client, ft, nil, nil)

	if err := svc.SetSeriesProgress(ctx, seriesID, 10); err != nil {
		t.Fatalf("SetSeriesProgress: %v", err)
	}

	if ft.updateEntryCalls != 1 {
		t.Fatalf("UpdateEntry calls = %d, want 1", ft.updateEntryCalls)
	}
	if ft.lastUpdateEntry.Progress != 10 {
		t.Fatalf("pushed progress = %v, want 10 (never-regress must be bypassed)", ft.lastUpdateEntry.Progress)
	}

	fresh := reloadBinding(ctx, t, client, binding.ID)
	if fresh.LastChapterRead != 10 {
		t.Fatalf("persisted LastChapterRead = %v, want 10", fresh.LastChapterRead)
	}
}

// TestSetSeriesProgress_TruncatesForInteger confirms a fractional target is
// floored before being pushed/persisted (every tracker in this registry is
// integer-count — mirrors pushOne's own truncation rule).
func TestSetSeriesProgress_TruncatesForInteger(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Fractional Target", "fractional-target")
	seedConnection(ctx, t, client, fakeTrackerID, "acct-token")
	binding := seedBinding(ctx, t, client, seriesID, fakeTrackerID, "r1", 0, 0)

	ft := &fakeTracker{id: fakeTrackerID}
	svc := newService(client, ft, nil, nil)

	if err := svc.SetSeriesProgress(ctx, seriesID, 10.7); err != nil {
		t.Fatalf("SetSeriesProgress: %v", err)
	}
	if ft.lastUpdateEntry.Progress != 10 {
		t.Fatalf("pushed progress = %v, want floor(10.7) = 10", ft.lastUpdateEntry.Progress)
	}
	fresh := reloadBinding(ctx, t, client, binding.ID)
	if fresh.LastChapterRead != 10 {
		t.Fatalf("persisted LastChapterRead = %v, want 10", fresh.LastChapterRead)
	}
}

// TestSetSeriesProgress_ReopensCompletedBindingOnRegression confirms a
// binding whose status is the tracker's own "completed" string is moved
// back to its native "reading/current" status when the reset regresses it —
// using tracker.IDAniList (not the generic fakeTrackerID) because
// readingStatus/completedStatus are keyed on the real registry ids.
func TestSetSeriesProgress_ReopensCompletedBindingOnRegression(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Reopen Me", "reopen-me")
	seedConnection(ctx, t, client, tracker.IDAniList, "acct-token")
	binding := seedBinding(ctx, t, client, seriesID, tracker.IDAniList, "r1", 100, 100)
	setBindingScorePrivateStatus(ctx, t, client, binding.ID, 0, false, "COMPLETED")

	ft := &fakeTracker{id: tracker.IDAniList}
	svc := newService(client, ft, nil, nil)

	if err := svc.SetSeriesProgress(ctx, seriesID, 10); err != nil {
		t.Fatalf("SetSeriesProgress: %v", err)
	}

	if ft.lastUpdateEntry.Status != "CURRENT" {
		t.Fatalf("pushed status = %q, want AniList's native reading status %q", ft.lastUpdateEntry.Status, "CURRENT")
	}
	fresh := reloadBinding(ctx, t, client, binding.ID)
	if fresh.Status != "CURRENT" {
		t.Fatalf("persisted status = %q, want %q", fresh.Status, "CURRENT")
	}
}

// TestSetSeriesProgress_NoReopenWhenNotCompleted confirms a binding that
// ISN'T in the tracker's completed status keeps its status untouched on a
// regression — the reopen logic must not fire on every downward reset, only
// one that is actually un-completing something.
func TestSetSeriesProgress_NoReopenWhenNotCompleted(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Not Completed", "not-completed")
	seedConnection(ctx, t, client, tracker.IDAniList, "acct-token")
	binding := seedBinding(ctx, t, client, seriesID, tracker.IDAniList, "r1", 50, 100)
	setBindingScorePrivateStatus(ctx, t, client, binding.ID, 0, false, "CURRENT")

	ft := &fakeTracker{id: tracker.IDAniList}
	svc := newService(client, ft, nil, nil)

	if err := svc.SetSeriesProgress(ctx, seriesID, 10); err != nil {
		t.Fatalf("SetSeriesProgress: %v", err)
	}
	if ft.lastUpdateEntry.Status != "CURRENT" {
		t.Fatalf("pushed status = %q, want unchanged %q", ft.lastUpdateEntry.Status, "CURRENT")
	}
}

// TestSetSeriesProgress_PerBindingIsolation confirms one binding's tracker
// failure does not abort the rest of the series' bindings, and the
// aggregated error is still returned to the caller (unlike PushProgress, no
// retry-queue enqueue happens here — this is a synchronous owner action).
func TestSetSeriesProgress_PerBindingIsolation(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Partial Failure", "partial-failure")

	const failingTrackerID = 901
	seedConnection(ctx, t, client, fakeTrackerID, "acct-token-ok")
	seedConnection(ctx, t, client, failingTrackerID, "acct-token-fail")
	okBinding := seedBinding(ctx, t, client, seriesID, fakeTrackerID, "ok", 50, 0)
	failBinding := seedBinding(ctx, t, client, seriesID, failingTrackerID, "fail", 50, 0)

	okTracker := &fakeTracker{id: fakeTrackerID}
	failTracker := &fakeTracker{
		id: failingTrackerID,
		updateEntryFn: func(context.Context, string, tracker.TrackEntry) (tracker.TrackEntry, error) {
			return tracker.TrackEntry{}, errors.New("upstream rejected the write")
		},
	}
	svc := newServiceMulti(client, []tracker.Tracker{okTracker, failTracker}, nil, nil)

	err := svc.SetSeriesProgress(ctx, seriesID, 10)
	if err == nil {
		t.Fatal("SetSeriesProgress: want a non-nil aggregated error, got nil")
	}

	// The OK binding still converged despite the other's failure.
	fresh := reloadBinding(ctx, t, client, okBinding.ID)
	if fresh.LastChapterRead != 10 {
		t.Fatalf("ok binding LastChapterRead = %v, want 10 (isolation must not abort it)", fresh.LastChapterRead)
	}
	// The failing binding was never persisted at the new value.
	failFresh := reloadBinding(ctx, t, client, failBinding.ID)
	if failFresh.LastChapterRead != 50 {
		t.Fatalf("failing binding LastChapterRead = %v, want unchanged 50", failFresh.LastChapterRead)
	}
}

// TestSetSeriesProgress_NoBindingsIsNoOp confirms a series with zero
// TrackBindings is a plain nil-error no-op (nothing to force-set).
func TestSetSeriesProgress_NoBindingsIsNoOp(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "No Bindings", "no-bindings")

	svc := newService(client, &fakeTracker{id: fakeTrackerID}, nil, nil)
	if err := svc.SetSeriesProgress(ctx, seriesID, 10); err != nil {
		t.Fatalf("SetSeriesProgress: %v", err)
	}
}
