// Package chapter_test — integration tests for the chapter service helpers.
// Tests require Docker (via testcontainers) for an ephemeral PostgreSQL instance.
package chapter_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
)

// TestWantedChapters_wanted_included verifies that a chapter in state wanted is
// returned by WantedChapters.
func TestWantedChapters_wanted_included(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Wanted Test").SetSlug("wanted-test").SaveX(ctx)
	client.Chapter.Create().SetSeries(s).SetChapterKey("1").SetState(entchapter.StateWanted).SaveX(ctx)

	got, err := chapter.WantedChapters(ctx, client, 100, 3)
	if err != nil {
		t.Fatalf("WantedChapters: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 chapter, got %d", len(got))
	}
	if got[0].ChapterKey != "1" {
		t.Errorf("chapter_key: want %q, got %q", "1", got[0].ChapterKey)
	}
}

// TestWantedChapters_failed_due_included verifies that a failed chapter whose
// next_attempt_at is in the past (or nil) and whose retries < maxRetries is
// returned by WantedChapters.
func TestWantedChapters_failed_due_included(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Failed Due Test").SetSlug("failed-due-test").SaveX(ctx)
	past := time.Now().Add(-1 * time.Hour)
	client.Chapter.Create().
		SetSeries(s).
		SetChapterKey("2").
		SetState(entchapter.StateFailed).
		SetRetries(1).
		SetNextAttemptAt(past).
		SaveX(ctx)

	got, err := chapter.WantedChapters(ctx, client, 100, 3)
	if err != nil {
		t.Fatalf("WantedChapters: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 chapter, got %d", len(got))
	}
	if got[0].ChapterKey != "2" {
		t.Errorf("chapter_key: want %q, got %q", "2", got[0].ChapterKey)
	}
}

// TestWantedChapters_failed_not_due_excluded verifies that a failed chapter
// whose next_attempt_at is in the future is excluded from WantedChapters.
func TestWantedChapters_failed_not_due_excluded(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Failed Future Test").SetSlug("failed-future-test").SaveX(ctx)
	future := time.Now().Add(1 * time.Hour)
	client.Chapter.Create().
		SetSeries(s).
		SetChapterKey("3").
		SetState(entchapter.StateFailed).
		SetRetries(1).
		SetNextAttemptAt(future).
		SaveX(ctx)

	got, err := chapter.WantedChapters(ctx, client, 100, 3)
	if err != nil {
		t.Fatalf("WantedChapters: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want 0 chapters (not due yet), got %d", len(got))
	}
}

// TestWantedChapters_failed_max_retries_excluded verifies that a failed chapter
// that has exhausted its retry budget (retries >= maxRetries) is excluded.
func TestWantedChapters_failed_max_retries_excluded(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Max Retries Test").SetSlug("max-retries-test").SaveX(ctx)
	client.Chapter.Create().
		SetSeries(s).
		SetChapterKey("4").
		SetState(entchapter.StateFailed).
		SetRetries(3). // == maxRetries
		SaveX(ctx)

	got, err := chapter.WantedChapters(ctx, client, 100, 3)
	if err != nil {
		t.Fatalf("WantedChapters: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want 0 chapters (retries exhausted), got %d", len(got))
	}
}

// TestWantedChapters_upgrade_available_excluded verifies that a chapter in
// state upgrade_available is not returned by WantedChapters (upgrade engine
// handles that path).
func TestWantedChapters_upgrade_available_excluded(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Upgrade Excl Test").SetSlug("upgrade-excl-test").SaveX(ctx)
	client.Chapter.Create().
		SetSeries(s).
		SetChapterKey("5").
		SetState(entchapter.StateUpgradeAvailable).
		SaveX(ctx)

	got, err := chapter.WantedChapters(ctx, client, 100, 3)
	if err != nil {
		t.Fatalf("WantedChapters: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want 0 chapters (upgrade_available excluded), got %d", len(got))
	}
}

// TestBestProviderChapter_picks_highest_importance verifies that
// BestProviderChapter selects the ProviderChapter belonging to the
// SeriesProvider with the highest importance when multiple providers offer the
// same chapter key.
func TestBestProviderChapter_picks_highest_importance(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Best Provider Test").SetSlug("best-provider-test").SaveX(ctx)
	spLow := client.SeriesProvider.Create().SetSeries(s).SetProvider("prov-low").SetImportance(5).SaveX(ctx)
	spHigh := client.SeriesProvider.Create().SetSeries(s).SetProvider("prov-high").SetImportance(10).SaveX(ctx)

	const key = "ch-best"
	client.ProviderChapter.Create().SetSeriesProviderID(spLow.ID).SetChapterKey(key).SetURL("https://low.example.com/ch").SetProviderIndex(0).SaveX(ctx)
	pcHigh := client.ProviderChapter.Create().SetSeriesProviderID(spHigh.ID).SetChapterKey(key).SetURL("https://high.example.com/ch").SetProviderIndex(0).SaveX(ctx)

	ch := client.Chapter.Create().SetSeries(s).SetChapterKey(key).SaveX(ctx)

	got, importance, err := chapter.BestProviderChapter(ctx, client, ch.ID)
	if err != nil {
		t.Fatalf("BestProviderChapter: %v", err)
	}
	if got.ID != pcHigh.ID {
		t.Errorf("best provider chapter ID: want %s (high-importance), got %s", pcHigh.ID, got.ID)
	}
	if importance != 10 {
		t.Errorf("importance: want 10, got %d", importance)
	}
}

// TestBestProviderChapter_none_found verifies that BestProviderChapter returns
// an error when no ProviderChapter offers the chapter's key for its series.
func TestBestProviderChapter_none_found(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("None Found Test").SetSlug("none-found-test").SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("missing-key").SaveX(ctx)

	_, _, err := chapter.BestProviderChapter(ctx, client, ch.ID)
	if err == nil {
		t.Fatal("expected error when no provider chapter exists, got nil")
	}
}

// TestBestProviderChapter_chapter_not_found verifies that BestProviderChapter
// returns an error when the given chapter ID does not exist.
func TestBestProviderChapter_chapter_not_found(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	_, _, err := chapter.BestProviderChapter(ctx, client, uuid.New())
	if err == nil {
		t.Fatal("expected error for nonexistent chapter ID, got nil")
	}
}
