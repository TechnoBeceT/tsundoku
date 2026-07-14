package syncsvc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/technobecet/tsundoku/internal/tracker"
	"github.com/technobecet/tsundoku/internal/tracker/syncsvc"
)

// ptr is a tiny generic pointer helper for building UpdatePatch fields.
func ptr[T any](v T) *T { return &v }

// TestCompleteSeries_PropagatesToAllBindings is the BUG-4 core proof: a series
// bound to a tracker WITH a total (AniList, 50/100) and one WITHOUT a total
// (MangaUpdates, 83 ch, no total). CompleteSeries must move EVERY binding to
// its own native completed status — including the totalless one, which could
// never auto-complete on "reached total" — and advance the with-total one's
// progress to its total, while leaving the totalless one's progress untouched.
func TestCompleteSeries_PropagatesToAllBindings(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Piwa Nabi", "piwa-nabi")

	seedConnection(ctx, t, client, tracker.IDAniList, "acct-anilist")
	seedConnection(ctx, t, client, tracker.IDMangaUpdates, "acct-mu")
	aniBinding := seedBinding(ctx, t, client, seriesID, tracker.IDAniList, "a1", 50, 100)
	muBinding := seedBinding(ctx, t, client, seriesID, tracker.IDMangaUpdates, "m1", 83, 0)

	svc := newServiceMulti(client, []tracker.Tracker{
		&fakeTracker{id: tracker.IDAniList},
		&fakeTracker{id: tracker.IDMangaUpdates},
	}, nil, nil)

	if err := svc.CompleteSeries(ctx, seriesID); err != nil {
		t.Fatalf("CompleteSeries: %v", err)
	}

	ani := reloadBinding(ctx, t, client, aniBinding.ID)
	if ani.Status != "COMPLETED" || ani.LastChapterRead != 100 {
		t.Fatalf("AniList binding = status %q / read %v, want COMPLETED / 100 (with-total ⇒ progress→total)", ani.Status, ani.LastChapterRead)
	}
	mu := reloadBinding(ctx, t, client, muBinding.ID)
	if mu.Status != "complete" || mu.LastChapterRead != 83 {
		t.Fatalf("MangaUpdates binding = status %q / read %v, want complete / 83 (no-total ⇒ status only, progress kept)", mu.Status, mu.LastChapterRead)
	}
}

// TestUpdateTrack_OwnerCompletePropagates proves TRIGGER 1: the owner manually
// sets ONE tracker to completed on the edit sheet, and the completion fans out
// to the series' other trackers.
func TestUpdateTrack_OwnerCompletePropagates(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Piwa Nabi", "piwa-nabi")

	seedConnection(ctx, t, client, tracker.IDAniList, "acct-anilist")
	seedConnection(ctx, t, client, tracker.IDMangaUpdates, "acct-mu")
	aniBinding := seedBinding(ctx, t, client, seriesID, tracker.IDAniList, "a1", 50, 100)
	muBinding := seedBinding(ctx, t, client, seriesID, tracker.IDMangaUpdates, "m1", 83, 0)

	svc := newServiceMulti(client, []tracker.Tracker{
		&fakeTracker{id: tracker.IDAniList},
		&fakeTracker{id: tracker.IDMangaUpdates},
	}, nil, nil)

	updated, err := svc.UpdateTrack(ctx, aniBinding.ID, syncsvc.UpdatePatch{Status: ptr("COMPLETED")})
	if err != nil {
		t.Fatalf("UpdateTrack: %v", err)
	}
	// The triggering binding's own progress was advanced to its total by the
	// propagation, and the response reflects it (§16 round-trip).
	if updated.Status != "COMPLETED" || updated.LastChapterRead != 100 {
		t.Fatalf("returned AniList binding = status %q / read %v, want COMPLETED / 100", updated.Status, updated.LastChapterRead)
	}
	mu := reloadBinding(ctx, t, client, muBinding.ID)
	if mu.Status != "complete" {
		t.Fatalf("MangaUpdates binding status = %q, want complete (owner completion must propagate)", mu.Status)
	}
}

// TestPushProgress_AutoCompletePropagates proves TRIGGER 2: a reading push
// that reaches a tracker's OWN total auto-completes it, and that terminal
// status propagates to the series' other trackers.
func TestPushProgress_AutoCompletePropagates(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Piwa Nabi", "piwa-nabi")

	seedConnection(ctx, t, client, tracker.IDAniList, "acct-anilist")
	seedConnection(ctx, t, client, tracker.IDMangaUpdates, "acct-mu")
	aniBinding := seedBinding(ctx, t, client, seriesID, tracker.IDAniList, "a1", 50, 100)
	muBinding := seedBinding(ctx, t, client, seriesID, tracker.IDMangaUpdates, "m1", 50, 0)

	svc := newServiceMulti(client, []tracker.Tracker{
		&fakeTracker{id: tracker.IDAniList},
		&fakeTracker{id: tracker.IDMangaUpdates},
	}, nil, nil)

	if err := svc.PushProgress(ctx, seriesID, 100); err != nil {
		t.Fatalf("PushProgress: %v", err)
	}

	ani := reloadBinding(ctx, t, client, aniBinding.ID)
	if ani.Status != "COMPLETED" {
		t.Fatalf("AniList binding status = %q, want COMPLETED (reached its total)", ani.Status)
	}
	mu := reloadBinding(ctx, t, client, muBinding.ID)
	if mu.Status != "complete" {
		t.Fatalf("MangaUpdates binding status = %q, want complete (auto-complete must propagate)", mu.Status)
	}
}

// TestCompleteSeries_PerBindingIsolation confirms one tracker's remote failure
// does not stop the others from completing, and the aggregated error is
// returned (mirrors SetSeriesProgress's own isolation contract).
func TestCompleteSeries_PerBindingIsolation(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Partial", "partial-complete")

	seedConnection(ctx, t, client, tracker.IDAniList, "acct-ok")
	seedConnection(ctx, t, client, tracker.IDMAL, "acct-fail")
	okBinding := seedBinding(ctx, t, client, seriesID, tracker.IDAniList, "ok", 50, 100)
	failBinding := seedBinding(ctx, t, client, seriesID, tracker.IDMAL, "fail", 50, 100)

	svc := newServiceMulti(client, []tracker.Tracker{
		&fakeTracker{id: tracker.IDAniList},
		&fakeTracker{id: tracker.IDMAL, updateEntryFn: func(context.Context, string, tracker.TrackEntry) (tracker.TrackEntry, error) {
			return tracker.TrackEntry{}, errors.New("upstream rejected the write")
		}},
	}, nil, nil)

	if err := svc.CompleteSeries(ctx, seriesID); err == nil {
		t.Fatal("CompleteSeries: want a non-nil aggregated error, got nil")
	}

	ok := reloadBinding(ctx, t, client, okBinding.ID)
	if ok.Status != "COMPLETED" {
		t.Fatalf("ok binding status = %q, want COMPLETED (isolation must not abort it)", ok.Status)
	}
	fail := reloadBinding(ctx, t, client, failBinding.ID)
	if fail.Status == "completed" {
		t.Fatalf("failing binding status = %q, want it NOT persisted as completed (remote write failed)", fail.Status)
	}
}
