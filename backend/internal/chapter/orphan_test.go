package chapter_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
)

// newOrphanTestSeries creates a bare series for the orphan-sweep tests. Each
// test gets its own client (testdb.New is one ephemeral Postgres per test), so
// the slug only needs to be unique within that client.
func newOrphanTestSeries(ctx context.Context, t *testing.T, client *ent.Client, slug string) *ent.Series {
	t.Helper()
	return client.Series.Create().SetTitle(slug).SetSlug(slug).SaveX(ctx)
}

// TestResetOrphanedChapters_downloading_to_wanted verifies the crash-recovery
// edge the FSM itself forbids: a chapter stranded mid-download (state
// downloading, owned by a process that died) is re-queued to wanted so the
// dispatcher's WantedChapters picks it up again next cycle.
func TestResetOrphanedChapters_downloading_to_wanted(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	s := newOrphanTestSeries(ctx, t, client, "downloading-orphan")
	ch := client.Chapter.Create().
		SetSeries(s).
		SetChapterKey("c1").
		SetState(entchapter.StateDownloading).
		SaveX(ctx)

	result, err := chapter.ResetOrphanedChapters(ctx, client)
	if err != nil {
		t.Fatalf("ResetOrphanedChapters: %v", err)
	}
	if result.Requeued != 1 {
		t.Errorf("want Requeued=1, got %d", result.Requeued)
	}
	if result.UpgradesReset != 0 {
		t.Errorf("want UpgradesReset=0, got %d", result.UpgradesReset)
	}

	got := client.Chapter.GetX(ctx, ch.ID)
	if got.State != entchapter.StateWanted {
		t.Errorf("want state wanted, got %s", got.State)
	}
}

// TestResetOrphanedChapters_upgrading_to_downloaded verifies a chapter
// stranded mid-upgrade (state upgrading) resets to downloaded — NOT wanted —
// because the pre-upgrade CBZ is still on disk and must not be re-fetched
// from scratch; DetectUpgrades will re-flag upgrade_available next cycle if a
// better source still exists. File provenance (filename, satisfied_by,
// page_count) must survive the reset untouched.
func TestResetOrphanedChapters_upgrading_to_downloaded(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	s := newOrphanTestSeries(ctx, t, client, "upgrading-orphan")
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("p").SetImportance(5).SaveX(ctx)
	ch := client.Chapter.Create().
		SetSeries(s).
		SetChapterKey("c1").
		SetState(entchapter.StateUpgrading).
		SetFilename("[p] Series 001.cbz").
		SetSatisfiedByID(sp.ID).
		SetSatisfiedImportance(5).
		SetPageCount(20).
		SaveX(ctx)

	result, err := chapter.ResetOrphanedChapters(ctx, client)
	if err != nil {
		t.Fatalf("ResetOrphanedChapters: %v", err)
	}
	if result.UpgradesReset != 1 {
		t.Errorf("want UpgradesReset=1, got %d", result.UpgradesReset)
	}
	if result.Requeued != 0 {
		t.Errorf("want Requeued=0, got %d", result.Requeued)
	}

	got := client.Chapter.GetX(ctx, ch.ID)
	if got.State != entchapter.StateDownloaded {
		t.Errorf("want state downloaded, got %s", got.State)
	}
	if got.Filename != "[p] Series 001.cbz" {
		t.Errorf("filename must be preserved, got %q", got.Filename)
	}
	if got.SatisfiedByProviderID == nil || *got.SatisfiedByProviderID != sp.ID {
		t.Errorf("satisfied_by_provider_id must be preserved, got %v", got.SatisfiedByProviderID)
	}
	if got.PageCount == nil || *got.PageCount != 20 {
		t.Errorf("page_count must be preserved, got %v", got.PageCount)
	}
}

// TestResetOrphanedChapters_upgrade_available_to_downloaded verifies fix ⑨: a
// chapter stranded in upgrade_available (DetectUpgrades flagged it, but UpgradeAll
// never converged it — e.g. its better source was down) is reset to downloaded at
// boot, so it does not survive a restart still flagged. The pre-upgrade CBZ +
// provenance are intact and must be preserved; DetectUpgrades re-flags it next
// cycle if a strictly-better source is reachable again.
func TestResetOrphanedChapters_upgrade_available_to_downloaded(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	s := newOrphanTestSeries(ctx, t, client, "upgrade-available-orphan")
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("p").SetImportance(5).SaveX(ctx)
	ch := client.Chapter.Create().
		SetSeries(s).
		SetChapterKey("c1").
		SetState(entchapter.StateUpgradeAvailable).
		SetFilename("[p] Series 001.cbz").
		SetSatisfiedByID(sp.ID).
		SetSatisfiedImportance(5).
		SetPageCount(20).
		SaveX(ctx)

	result, err := chapter.ResetOrphanedChapters(ctx, client)
	if err != nil {
		t.Fatalf("ResetOrphanedChapters: %v", err)
	}
	if result.UpgradesUnflagged != 1 {
		t.Errorf("want UpgradesUnflagged=1, got %d", result.UpgradesUnflagged)
	}
	if result.Requeued != 0 || result.UpgradesReset != 0 {
		t.Errorf("want Requeued=0 UpgradesReset=0, got %+v", result)
	}

	got := client.Chapter.GetX(ctx, ch.ID)
	if got.State != entchapter.StateDownloaded {
		t.Errorf("want state downloaded, got %s", got.State)
	}
	if got.Filename != "[p] Series 001.cbz" {
		t.Errorf("filename must be preserved, got %q", got.Filename)
	}
	if got.SatisfiedByProviderID == nil || *got.SatisfiedByProviderID != sp.ID {
		t.Errorf("satisfied_by_provider_id must be preserved, got %v", got.SatisfiedByProviderID)
	}
	// page_count preservation on a bulk SetState is already proven by the
	// sibling upgrading→downloaded test (identical mechanism); not re-asserted
	// here.
}

// TestResetOrphanedChapters_resets_all_states_in_one_call verifies the three
// bulk updates compose in a single call: a downloading, an upgrading, and an
// upgrade_available chapter seeded in the same DB are ALL swept in one
// invocation, each to its own target state, with every count reported.
func TestResetOrphanedChapters_resets_all_states_in_one_call(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	s := newOrphanTestSeries(ctx, t, client, "all-states")
	downloading := client.Chapter.Create().
		SetSeries(s).
		SetChapterKey("c1").
		SetState(entchapter.StateDownloading).
		SaveX(ctx)
	upgrading := client.Chapter.Create().
		SetSeries(s).
		SetChapterKey("c2").
		SetState(entchapter.StateUpgrading).
		SaveX(ctx)
	upgradeAvailable := client.Chapter.Create().
		SetSeries(s).
		SetChapterKey("c3").
		SetState(entchapter.StateUpgradeAvailable).
		SaveX(ctx)

	result, err := chapter.ResetOrphanedChapters(ctx, client)
	if err != nil {
		t.Fatalf("ResetOrphanedChapters: %v", err)
	}
	if result.Requeued != 1 || result.UpgradesReset != 1 || result.UpgradesUnflagged != 1 {
		t.Fatalf("want {Requeued:1, UpgradesReset:1, UpgradesUnflagged:1}, got %+v", result)
	}

	if got := client.Chapter.GetX(ctx, downloading.ID); got.State != entchapter.StateWanted {
		t.Errorf("downloading chapter: want state wanted, got %s", got.State)
	}
	if got := client.Chapter.GetX(ctx, upgrading.ID); got.State != entchapter.StateDownloaded {
		t.Errorf("upgrading chapter: want state downloaded, got %s", got.State)
	}
	if got := client.Chapter.GetX(ctx, upgradeAvailable.ID); got.State != entchapter.StateDownloaded {
		t.Errorf("upgrade_available chapter: want state downloaded, got %s", got.State)
	}
}

// TestResetOrphanedChapters_leaves_non_orphan_states verifies the sweep never
// touches a chapter outside the three stranded states — wanted, failed,
// downloaded, and permanently_failed rows must all be left exactly as they were
// (no accidental over-broad sweep). upgrade_available is deliberately EXCLUDED
// here: it IS a swept state (→downloaded), proven by its own test below.
func TestResetOrphanedChapters_leaves_non_orphan_states(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	s := newOrphanTestSeries(ctx, t, client, "non-orphan")

	states := []entchapter.State{
		entchapter.StateWanted,
		entchapter.StateFailed,
		entchapter.StateDownloaded,
		entchapter.StatePermanentlyFailed,
	}
	ids := make([]uuid.UUID, len(states))
	for i, st := range states {
		ch := client.Chapter.Create().
			SetSeries(s).
			SetChapterKey("c" + st.String()).
			SetState(st).
			SaveX(ctx)
		ids[i] = ch.ID
	}

	result, err := chapter.ResetOrphanedChapters(ctx, client)
	if err != nil {
		t.Fatalf("ResetOrphanedChapters: %v", err)
	}
	if result.Requeued != 0 || result.UpgradesReset != 0 {
		t.Fatalf("want no rows touched, got %+v", result)
	}

	for i, st := range states {
		got := client.Chapter.GetX(ctx, ids[i])
		if got.State != st {
			t.Errorf("chapter %s: want state %s untouched, got %s", ids[i], st, got.State)
		}
	}
}

// TestResetOrphanedChapters_idempotent_when_empty verifies a sweep with
// nothing to reset (and a second, repeated run) is a safe no-op.
func TestResetOrphanedChapters_idempotent_when_empty(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	s := newOrphanTestSeries(ctx, t, client, "empty")
	client.Chapter.Create().SetSeries(s).SetChapterKey("c1").SetState(entchapter.StateWanted).SaveX(ctx)

	result, err := chapter.ResetOrphanedChapters(ctx, client)
	if err != nil {
		t.Fatalf("ResetOrphanedChapters (first run): %v", err)
	}
	if result.Requeued != 0 || result.UpgradesReset != 0 {
		t.Fatalf("want {0,0} with no orphans, got %+v", result)
	}

	// Running again must be equally harmless.
	result, err = chapter.ResetOrphanedChapters(ctx, client)
	if err != nil {
		t.Fatalf("ResetOrphanedChapters (second run): %v", err)
	}
	if result.Requeued != 0 || result.UpgradesReset != 0 {
		t.Fatalf("want {0,0} on repeated run, got %+v", result)
	}
}

// TestResetOrphanedChapters_leaves_provider_chapter_retry_state verifies the
// sweep only touches Chapter.state — a downloading chapter's per-source
// ProviderChapter retry state (attempts / next_attempt_at / last_error) must
// be completely unaffected, since a Chapter-state reset is not a retry reset
// (that is owner-initiated, via downloads.RetryChapter/RetryAll).
func TestResetOrphanedChapters_leaves_provider_chapter_retry_state(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	s := newOrphanTestSeries(ctx, t, client, "retry-state")
	ch := client.Chapter.Create().
		SetSeries(s).
		SetChapterKey("c1").
		SetState(entchapter.StateDownloading).
		SaveX(ctx)

	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("p").SetImportance(5).SaveX(ctx)
	pc := client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).
		SetChapterKey(ch.ChapterKey).
		SetProviderIndex(0).
		SetAttempts(2).
		SetNextAttemptAt(time.Now().Add(1 * time.Hour)).
		SetLastError("boom").
		SaveX(ctx)

	// Baseline the cooldown from the DB-stored value (Postgres truncates to
	// microseconds), so the comparison proves the sweep left it unchanged
	// rather than tripping on time-precision noise.
	before := client.ProviderChapter.GetX(ctx, pc.ID)

	if _, err := chapter.ResetOrphanedChapters(ctx, client); err != nil {
		t.Fatalf("ResetOrphanedChapters: %v", err)
	}

	got := client.ProviderChapter.GetX(ctx, pc.ID)
	if got.Attempts != 2 {
		t.Errorf("attempts must be untouched, want 2, got %d", got.Attempts)
	}
	if got.LastError != "boom" {
		t.Errorf("last_error must be untouched, want %q, got %q", "boom", got.LastError)
	}
	if got.NextAttemptAt == nil || !got.NextAttemptAt.Equal(*before.NextAttemptAt) {
		t.Errorf("next_attempt_at must be untouched, want %v, got %v", before.NextAttemptAt, got.NextAttemptAt)
	}
}
