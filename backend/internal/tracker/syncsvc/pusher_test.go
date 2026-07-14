package syncsvc_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestPush_LoadsBindingAndCallsUpdateEntry confirms the retry.Pusher
// implementation (Push) loads the binding fresh by id and pushes chapter
// through the SAME core logic PushProgress uses (truncated Progress, the
// account's own token).
func TestPush_LoadsBindingAndCallsUpdateEntry(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Retry Push", "retry-push")
	seedConnection(ctx, t, client, fakeTrackerID, "acct-token")
	binding := seedBinding(ctx, t, client, seriesID, fakeTrackerID, "r1", 3, 0)

	ft := &fakeTracker{id: fakeTrackerID}
	svc := newService(client, ft, nil, nil)

	if err := svc.Push(ctx, binding.ID, 9); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if ft.updateEntryCalls != 1 {
		t.Fatalf("UpdateEntry calls = %d, want 1", ft.updateEntryCalls)
	}
	if ft.lastUpdateEntry.Progress != 9 || ft.lastToken != "acct-token" {
		t.Fatalf("pushed entry = %+v token = %q, want Progress=9 token=acct-token", ft.lastUpdateEntry, ft.lastToken)
	}

	fresh := reloadBinding(ctx, t, client, binding.ID)
	if fresh.LastChapterRead != 9 {
		t.Fatalf("LastChapterRead = %v, want 9", fresh.LastChapterRead)
	}
}

// TestPush_UnknownBindingIsANoOp confirms a since-deleted binding (the owner
// unbound between the failed push and this retry) reports success rather
// than erroring, so retry.Queue.RunOnce deletes the orphaned pending row
// instead of retrying it forever.
func TestPush_UnknownBindingIsANoOp(t *testing.T) {
	client := newTestDB(t)
	svc := newService(client, &fakeTracker{id: fakeTrackerID}, nil, nil)

	if err := svc.Push(context.Background(), uuid.New(), 5); err != nil {
		t.Fatalf("Push for an unknown binding: err = %v, want nil", err)
	}
}
