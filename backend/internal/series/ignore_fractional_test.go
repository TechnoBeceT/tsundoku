package series_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/series"
)

// seedChapterInState creates a Chapter row for the given number in the given
// state, keyed the SAME way seedFeed keys its ProviderChapters (so carriersByKey
// matches). No file, no satisfied_by — these are the UNDOWNLOADED fractionals the
// ignore reconcile parks/restores.
func seedChapterInState(ctx context.Context, t *testing.T, db *ent.Client, seriesID uuid.UUID, number float64, state entchapter.State) *ent.Chapter {
	t.Helper()
	key := strconv.FormatFloat(number, 'f', -1, 64)
	return db.Chapter.Create().
		SetSeriesID(seriesID).
		SetChapterKey(key).
		SetNumber(number).
		SetState(state).
		SaveX(ctx)
}

// chapterState reloads a chapter and returns its current state.
func chapterState(ctx context.Context, t *testing.T, db *ent.Client, id uuid.UUID) entchapter.State {
	t.Helper()
	return db.Chapter.GetX(ctx, id).State
}

// TestSetIgnoreFractional_ParksAllIgnoredWantedFractional proves that ticking
// ignore_fractional on the ONLY source of a wanted fractional parks that chapter
// in the terminal `ignored` state — out of the queue and out of the chapter list —
// while the source's WHOLE chapters stay wanted. The bug: it used to sit "Wanted"
// forever, un-downloadable yet cluttering both views.
func TestSetIgnoreFractional_ParksAllIgnoredWantedFractional(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	s := db.Series.Create().SetTitle("Park Series").SetSlug("park-series").SaveX(ctx)
	// One source carrying whole 1, 2 and the fractional 1.5.
	comix := seedFeed(ctx, t, db, s.ID, "comix", 40, 1, 1.5, 2)

	whole1 := seedChapterInState(ctx, t, db, s.ID, 1, entchapter.StateWanted)
	frac := seedChapterInState(ctx, t, db, s.ID, 1.5, entchapter.StateWanted)
	seedChapterInState(ctx, t, db, s.ID, 2, entchapter.StateWanted)

	svc := series.NewService(db, t.TempDir(), 14)
	if err := svc.SetIgnoreFractional(ctx, s.ID, comix.ID, true); err != nil {
		t.Fatalf("SetIgnoreFractional: %v", err)
	}

	if got := chapterState(ctx, t, db, frac.ID); got != entchapter.StateIgnored {
		t.Errorf("fractional 1.5 state = %s, want ignored", got)
	}
	if got := chapterState(ctx, t, db, whole1.ID); got != entchapter.StateWanted {
		t.Errorf("whole 1 state = %s, want wanted (whole chapters are never ignored)", got)
	}

	// Hidden from the chapter-list counts (Total + Wanted) — the ignored chapter is
	// excluded exactly like a superseded part.
	dto, err := svc.GetSeries(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if dto.ChapterCounts.Total != 2 {
		t.Errorf("counts.Total = %d, want 2 (1 and 2; the ignored 1.5 is excluded)", dto.ChapterCounts.Total)
	}
	if dto.ChapterCounts.Wanted != 2 {
		t.Errorf("counts.Wanted = %d, want 2 (the ignored 1.5 no longer counts as wanted)", dto.ChapterCounts.Wanted)
	}
}

// TestSetIgnoreFractional_KeepsWantedWhenNonIgnoredCarrier is the RESURRECTION
// GUARD: a fractional a NON-ignored source also carries must stay wanted (and
// downloadable) even after another of its sources is flagged ignore_fractional.
func TestSetIgnoreFractional_KeepsWantedWhenNonIgnoredCarrier(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	s := db.Series.Create().SetTitle("Guard Series").SetSlug("guard-series").SaveX(ctx)
	comix := seedFeed(ctx, t, db, s.ID, "comix", 40, 1.5) // will be ignored
	seedFeed(ctx, t, db, s.ID, "asura", 20, 1.5)          // NOT ignored — also carries 1.5

	frac := seedChapterInState(ctx, t, db, s.ID, 1.5, entchapter.StateWanted)

	svc := series.NewService(db, t.TempDir(), 14)
	if err := svc.SetIgnoreFractional(ctx, s.ID, comix.ID, true); err != nil {
		t.Fatalf("SetIgnoreFractional: %v", err)
	}

	if got := chapterState(ctx, t, db, frac.ID); got != entchapter.StateWanted {
		t.Errorf("fractional 1.5 state = %s, want wanted (a non-ignored source still carries it)", got)
	}
}

// TestSetIgnoreFractional_UntickRestores proves un-ticking the toggle is a genuine
// undo: an ignored fractional whose sole carrier stops ignoring fractionals returns
// to wanted (it is fetchable again). Keeping the feed rows is what makes this work.
func TestSetIgnoreFractional_UntickRestores(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	s := db.Series.Create().SetTitle("Undo Series").SetSlug("undo-series").SaveX(ctx)
	comix := seedFeed(ctx, t, db, s.ID, "comix", 40, 1.5)
	frac := seedChapterInState(ctx, t, db, s.ID, 1.5, entchapter.StateWanted)

	svc := series.NewService(db, t.TempDir(), 14)

	// Tick ON — parks it.
	if err := svc.SetIgnoreFractional(ctx, s.ID, comix.ID, true); err != nil {
		t.Fatalf("SetIgnoreFractional(true): %v", err)
	}
	if got := chapterState(ctx, t, db, frac.ID); got != entchapter.StateIgnored {
		t.Fatalf("after tick ON, state = %s, want ignored", got)
	}

	// Tick OFF — restores it.
	if err := svc.SetIgnoreFractional(ctx, s.ID, comix.ID, false); err != nil {
		t.Fatalf("SetIgnoreFractional(false): %v", err)
	}
	if got := chapterState(ctx, t, db, frac.ID); got != entchapter.StateWanted {
		t.Errorf("after tick OFF, state = %s, want wanted (un-tick restores)", got)
	}
}

// TestSetIgnoreFractional_FailedFractionalParked proves a FAILED (not just wanted)
// all-ignored fractional is also parked — it is equally stuck in the queue.
func TestSetIgnoreFractional_FailedFractionalParked(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	s := db.Series.Create().SetTitle("Failed Series").SetSlug("failed-series").SaveX(ctx)
	comix := seedFeed(ctx, t, db, s.ID, "comix", 40, 1.5)
	frac := seedChapterInState(ctx, t, db, s.ID, 1.5, entchapter.StateFailed)

	svc := series.NewService(db, t.TempDir(), 14)
	if err := svc.SetIgnoreFractional(ctx, s.ID, comix.ID, true); err != nil {
		t.Fatalf("SetIgnoreFractional: %v", err)
	}
	if got := chapterState(ctx, t, db, frac.ID); got != entchapter.StateIgnored {
		t.Errorf("failed fractional 1.5 state = %s, want ignored", got)
	}
}
