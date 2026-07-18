package syncsvc_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/tracker/syncsvc"
)

// strPtr / floatPtr / boolPtr / timePtr are small pointer helpers for
// building UpdatePatch literals in these tests.
func strPtr(s string) *string        { return &s }
func floatPtr(f float64) *float64    { return &f }
func boolPtr(b bool) *bool           { return &b }
func timePtr(t time.Time) *time.Time { return &t }

// TestUpdateTrack_AppliesPatchToRemoteLocalAndSidecar confirms a manual
// tracking-sheet edit pushes every patched field to the tracker (one
// UpdateEntry call) AND persists them locally AND mirrors the sidecar —
// fields the patch did NOT touch are left at their prior value.
func TestUpdateTrack_AppliesPatchToRemoteLocalAndSidecar(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Manual Edit", "manual-edit")
	seedConnection(ctx, t, client, fakeTrackerID, "acct-token")
	binding := seedBinding(ctx, t, client, seriesID, fakeTrackerID, "r1", 5, 0)

	ft := &fakeTracker{id: fakeTrackerID}
	sidecar := &fakeSidecar{}
	svc := newService(client, ft, sidecar, nil)

	finish := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	patch := syncsvc.UpdatePatch{
		Status:          strPtr("COMPLETED"),
		LastChapterRead: floatPtr(42),
		Score:           floatPtr(9.5),
		FinishDate:      timePtr(finish),
		Private:         boolPtr(true),
	}

	updated, err := svc.UpdateTrack(ctx, binding.ID, patch)
	if err != nil {
		t.Fatalf("UpdateTrack: %v", err)
	}

	assertUpdatedBinding(t, updated, finish)
	assertPushedEntry(t, ft)
	assertSidecarSynced(t, sidecar, seriesID)
	assertPersistedBinding(ctx, t, client, binding.ID)
}

// assertUpdatedBinding fails t unless updated carries every field
// TestUpdateTrack_AppliesPatchToRemoteLocalAndSidecar's patch set.
func assertUpdatedBinding(t *testing.T, updated *ent.TrackBinding, finish time.Time) {
	t.Helper()
	if updated.Status != "COMPLETED" || updated.LastChapterRead != 42 || updated.Score != 9.5 ||
		updated.FinishDate == nil || !updated.FinishDate.Equal(finish) || !updated.Private {
		t.Fatalf("updated binding = %+v", updated)
	}
}

// assertPushedEntry fails t unless ft received exactly one UpdateEntry call
// carrying the patched fields.
func assertPushedEntry(t *testing.T, ft *fakeTracker) {
	t.Helper()
	if ft.updateEntryCalls != 1 {
		t.Fatalf("UpdateEntry calls = %d, want 1", ft.updateEntryCalls)
	}
	if ft.lastUpdateEntry.Status != "COMPLETED" || ft.lastUpdateEntry.Progress != 42 ||
		ft.lastUpdateEntry.Score != 9.5 || !ft.lastUpdateEntry.Private {
		t.Fatalf("pushed entry = %+v", ft.lastUpdateEntry)
	}
}

// assertSidecarSynced fails t unless sidecar was mirrored exactly once, for
// wantSeriesID.
func assertSidecarSynced(t *testing.T, sidecar *fakeSidecar, wantSeriesID uuid.UUID) {
	t.Helper()
	if sidecar.calls != 1 || sidecar.lastSeriesID != wantSeriesID {
		t.Fatalf("sidecar sync calls = %d lastSeriesID = %v", sidecar.calls, sidecar.lastSeriesID)
	}
}

// assertPersistedBinding fails t unless bindingID's row was durably
// persisted with the patch's fields.
func assertPersistedBinding(ctx context.Context, t *testing.T, client *ent.Client, bindingID uuid.UUID) {
	t.Helper()
	fresh := reloadBinding(ctx, t, client, bindingID)
	if fresh.Status != "COMPLETED" || fresh.LastChapterRead != 42 {
		t.Fatalf("persisted binding = %+v", fresh)
	}
}

// TestUpdateTrack_FloorsFractionalLastChapterRead confirms even an explicit
// owner tracking-sheet edit reports a WHOLE chapter: a manual lastChapterRead
// of 42.1 is floored to 42 both in the value pushed to the tracker and in the
// value persisted locally (a tracker's progress field is an integer chapter
// COUNT — see sync.TruncateForInteger). Score is left fractional to prove only
// the chapter number is floored.
func TestUpdateTrack_FloorsFractionalLastChapterRead(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Frac Manual Edit", "frac-manual-edit")
	seedConnection(ctx, t, client, fakeTrackerID, "acct-token")
	binding := seedBinding(ctx, t, client, seriesID, fakeTrackerID, "r1", 5, 0)

	ft := &fakeTracker{id: fakeTrackerID}
	svc := newService(client, ft, nil, nil)

	updated, err := svc.UpdateTrack(ctx, binding.ID, syncsvc.UpdatePatch{
		LastChapterRead: floatPtr(42.1),
		Score:           floatPtr(9.5),
	})
	if err != nil {
		t.Fatalf("UpdateTrack: %v", err)
	}

	if ft.updateEntryCalls != 1 || ft.lastUpdateEntry.Progress != 42 {
		t.Fatalf("pushed = calls=%d Progress=%v, want 1 call pushing 42 (floor of 42.1)", ft.updateEntryCalls, ft.lastUpdateEntry.Progress)
	}
	if ft.lastUpdateEntry.Score != 9.5 {
		t.Fatalf("pushed Score = %v, want 9.5 (score must NOT be floored)", ft.lastUpdateEntry.Score)
	}
	if updated.LastChapterRead != 42 {
		t.Fatalf("updated.LastChapterRead = %v, want 42", updated.LastChapterRead)
	}
	if got := reloadBinding(ctx, t, client, binding.ID).LastChapterRead; got != 42 {
		t.Fatalf("persisted LastChapterRead = %v, want 42", got)
	}
}

// TestUpdateTrack_BindingNotFound confirms UpdateTrack fails closed for an
// unknown recordID.
func TestUpdateTrack_BindingNotFound(t *testing.T) {
	client := newTestDB(t)
	svc := newService(client, &fakeTracker{id: fakeTrackerID}, nil, nil)

	_, err := svc.UpdateTrack(context.Background(), uuid.New(), syncsvc.UpdatePatch{Score: floatPtr(1)})
	if err != syncsvc.ErrBindingNotFound {
		t.Fatalf("UpdateTrack: err = %v, want syncsvc.ErrBindingNotFound", err)
	}
}

// TestUpdateTrack_TrackerNotConnected confirms UpdateTrack fails closed
// when the owning tracker's account has since been logged out.
func TestUpdateTrack_TrackerNotConnected(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Disconnected", "disconnected")
	seedConnection(ctx, t, client, fakeTrackerID, "acct-token")
	binding := seedBinding(ctx, t, client, seriesID, fakeTrackerID, "r2", 0, 0)
	deleteConnection(ctx, t, client, fakeTrackerID)

	svc := newService(client, &fakeTracker{id: fakeTrackerID}, nil, nil)
	if _, err := svc.UpdateTrack(ctx, binding.ID, syncsvc.UpdatePatch{Score: floatPtr(1)}); err != syncsvc.ErrTrackerNotConnected {
		t.Fatalf("UpdateTrack: err = %v, want syncsvc.ErrTrackerNotConnected", err)
	}
}
