package schema_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
)

// Tracker ids from the fixed registry (MAL=1, AniList=2, Kitsu=3, MangaUpdates=7).
const (
	trackerMAL     = 1
	trackerAniList = 2
)

// TestTrackerConnectionTrackerIDIsUnique verifies the tracker_id unique
// constraint fires: at most one connected account per tracker.
func TestTrackerConnectionTrackerIDIsUnique(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	client.TrackerConnection.Create().
		SetTrackerID(trackerAniList).
		SetAccessToken("tok-a").
		SetUsername("owner").
		SaveX(ctx)

	_, err := client.TrackerConnection.Create().
		SetTrackerID(trackerAniList).
		SetAccessToken("tok-b").
		Save(ctx)
	if err == nil || !ent.IsConstraintError(err) {
		t.Fatalf("expected unique constraint violation on duplicate tracker_id, got %v", err)
	}

	// A different tracker must succeed.
	client.TrackerConnection.Create().
		SetTrackerID(trackerMAL).
		SetAccessToken("tok-c").
		SaveX(ctx)
}

// TestTrackBindingSeriesTrackerIsUnique verifies the (series_id, tracker_id)
// unique index fires: a series may be bound to several different trackers but
// never twice on the same one.
func TestTrackBindingSeriesTrackerIsUnique(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Tracked Series").SetSlug("tracked-series").SaveX(ctx)

	// Two bindings on DIFFERENT trackers for the same series both succeed.
	client.TrackBinding.Create().
		SetSeries(s).
		SetTrackerID(trackerAniList).
		SetRemoteID("111").
		SaveX(ctx)
	client.TrackBinding.Create().
		SetSeries(s).
		SetTrackerID(trackerMAL).
		SetRemoteID("222").
		SaveX(ctx)

	// A second binding on the SAME tracker for the same series must fail.
	_, err := client.TrackBinding.Create().
		SetSeries(s).
		SetTrackerID(trackerAniList).
		SetRemoteID("333").
		Save(ctx)
	if err == nil || !ent.IsConstraintError(err) {
		t.Fatalf("expected unique constraint violation on duplicate (series_id, tracker_id), got %v", err)
	}
}

// TestTrackBindingCascadesOnSeriesDelete verifies the DB-level ON DELETE CASCADE:
// deleting a Series row removes its TrackBindings automatically.
func TestTrackBindingCascadesOnSeriesDelete(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Doomed Series").SetSlug("doomed-series").SaveX(ctx)
	tb := client.TrackBinding.Create().
		SetSeries(s).
		SetTrackerID(trackerAniList).
		SetRemoteID("999").
		SaveX(ctx)

	// Sanity: the binding exists before the delete.
	if _, err := client.TrackBinding.Get(ctx, tb.ID); err != nil {
		t.Fatalf("expected TrackBinding to exist before series delete, got %v", err)
	}

	client.Series.DeleteOne(s).ExecX(ctx)

	if _, err := client.TrackBinding.Get(ctx, tb.ID); !ent.IsNotFound(err) {
		t.Fatalf("expected TrackBinding to be cascade-deleted when its series was deleted, got %v", err)
	}
}
