// Package chapter_test — integration tests for the chapter service helpers.
// Tests require Docker (via testcontainers) for an ephemeral PostgreSQL instance.
package chapter_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/fetcher"
)

// TestWantedChapters_wanted_included verifies that a chapter in state wanted is
// returned by WantedChapters.
func TestWantedChapters_wanted_included(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Wanted Test").SetSlug("wanted-test").SaveX(ctx)
	client.Chapter.Create().SetSeries(s).SetChapterKey("1").SetState(entchapter.StateWanted).SaveX(ctx)

	got, err := chapter.WantedChapters(ctx, client, 100)
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

// TestWantedChapters_failed_included verifies that a failed chapter is returned
// by WantedChapters regardless of any legacy per-CHAPTER retry fields. In the
// multi-source engine the per-source retry gating (which source is a live
// candidate) lives in RankedLiveCandidates and is applied per chapter by the
// dispatcher — WantedChapters just surfaces the work list. A failed chapter whose
// sources happen to be all on cooldown is still returned (the dispatcher no-ops
// it that cycle); it is not filtered out at the query level.
func TestWantedChapters_failed_included(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Failed Included Test").SetSlug("failed-included-test").SaveX(ctx)
	// Legacy chapter-level retry fields are deliberately set to non-default values
	// to prove they no longer gate WantedChapters.
	future := time.Now().Add(1 * time.Hour)
	client.Chapter.Create().
		SetSeries(s).
		SetChapterKey("2").
		SetState(entchapter.StateFailed).
		SetRetries(9).
		SetNextAttemptAt(future).
		SaveX(ctx)

	got, err := chapter.WantedChapters(ctx, client, 100)
	if err != nil {
		t.Fatalf("WantedChapters: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 chapter (failed is actionable regardless of legacy fields), got %d", len(got))
	}
	if got[0].ChapterKey != "2" {
		t.Errorf("chapter_key: want %q, got %q", "2", got[0].ChapterKey)
	}
}

func TestWantedChapters_ReturnsAttachedEntities(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	s := client.Series.Create().SetTitle("Attached Wanted").SetSlug("attached-wanted").SaveX(ctx)
	ch := client.Chapter.Create().
		SetSeries(s).
		SetChapterKey("attached").
		SetState(entchapter.StateWanted).
		SaveX(ctx)

	got, err := chapter.WantedChapters(ctx, client, 100)
	if err != nil {
		t.Fatalf("WantedChapters: %v", err)
	}
	if len(got) != 1 || got[0].ID != ch.ID {
		t.Fatalf("WantedChapters = %+v, want chapter %s", got, ch.ID)
	}
	if _, err := got[0].Update().SetLastError("attached update").Save(ctx); err != nil {
		t.Fatalf("update returned wanted chapter: %v", err)
	}
	if got := client.Chapter.GetX(ctx, ch.ID).LastError; got != "attached update" {
		t.Fatalf("last_error = %q, want attached update", got)
	}
}

func TestWantedSelections_GenerationChangesAcrossFailedABA(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	s := client.Series.Create().SetTitle("Work Generation").SetSlug("work-generation").SaveX(ctx)
	ch := client.Chapter.Create().
		SetSeries(s).
		SetChapterKey("generation").
		SetState(entchapter.StateFailed).
		SaveX(ctx)

	first, err := chapter.WantedSelections(ctx, client, 100)
	if err != nil {
		t.Fatalf("first WantedSelections: %v", err)
	}
	if len(first) != 1 || first[0].Chapter.ID != ch.ID || first[0].Generation == "" {
		t.Fatalf("first selection = %+v, want chapter %s with a generation", first, ch.ID)
	}
	if err := chapter.SetState(ctx, client, ch.ID, entchapter.StateDownloading); err != nil {
		t.Fatalf("failed→downloading: %v", err)
	}
	if err := chapter.SetState(ctx, client, ch.ID, entchapter.StateFailed); err != nil {
		t.Fatalf("downloading→failed: %v", err)
	}

	second, err := chapter.WantedSelections(ctx, client, 100)
	if err != nil {
		t.Fatalf("second WantedSelections: %v", err)
	}
	if len(second) != 1 {
		t.Fatalf("second selection count = %d, want 1", len(second))
	}
	if second[0].Generation == first[0].Generation {
		t.Fatalf("failed ABA reused generation %q", first[0].Generation)
	}
}

func TestRankedLiveCandidatesForMany_MatchesSingleCandidateSnapshot(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	now := time.Now()
	past := now.Add(-time.Hour)
	s := client.Series.Create().SetTitle("Candidate Snapshot").SetSlug("candidate-snapshot").SaveX(ctx)
	sp := client.SeriesProvider.Create().
		SetSeries(s).
		SetSuwayomiID(11).
		SetProvider("77").
		SetProviderName("candidate source").
		SetScanlator("candidate scans").
		SetLanguage("en").
		SetURL("https://example.test/series").
		SetWebURL("https://example.test/series-web").
		SetTitle("Candidate Snapshot Source").
		SetMetadata(true).
		SetStatus("ongoing").
		SetFlags(3).
		SetImportance(42).
		SetCoverURL("https://example.test/cover.jpg").
		SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProvider(sp).
		SetChapterKey("1.5").
		SetNumber(1.5).
		SetName("Chapter 1.5").
		SetURL("https://example.test/chapter").
		SetWebURL("https://example.test/chapter-web").
		SetProviderUploadDate(past).
		SetProviderIndex(7).
		SetPageCount(12).
		SetSuwayomiChapterID(99).
		SetAttempts(1).
		SetLastError("prior chapter failure").
		SetNextAttemptAt(past).
		SetPageLinks([]fetcher.PageLink{{URL: "https://example.test/page", ImageURL: "https://example.test/image"}}).
		SaveX(ctx)
	ch := client.Chapter.Create().
		SetSeries(s).
		SetChapterKey("1.5").
		SetNumber(1.5).
		SaveX(ctx)

	single, err := chapter.RankedLiveCandidates(ctx, client, ch.ID, 3, now, nil)
	if err != nil {
		t.Fatalf("RankedLiveCandidates: %v", err)
	}
	batched, err := chapter.RankedLiveCandidatesForMany(ctx, client, []*ent.Chapter{ch}, 3, now, nil)
	if err != nil {
		t.Fatalf("RankedLiveCandidatesForMany: %v", err)
	}
	assertCandidateSnapshotsEqual(t, single, batched[ch.ID])
}

func assertCandidateSnapshotsEqual(t *testing.T, single, bulk []chapter.Candidate) {
	t.Helper()
	if len(single) != 1 || len(bulk) != 1 {
		t.Fatalf("candidate counts: single=%d bulk=%d, want 1 each", len(single), len(bulk))
	}
	if single[0].Generation == "" || bulk[0].Generation == "" {
		t.Fatalf("candidate generations: single=%q bulk=%q, want both non-empty", single[0].Generation, bulk[0].Generation)
	}
	if bulk[0].Generation != single[0].Generation {
		t.Fatalf("candidate generations differ: single=%q bulk=%q", single[0].Generation, bulk[0].Generation)
	}
	singleJSON, err := json.Marshal(single[0])
	if err != nil {
		t.Fatalf("marshal single candidate: %v", err)
	}
	bulkJSON, err := json.Marshal(bulk[0])
	if err != nil {
		t.Fatalf("marshal bulk candidate: %v", err)
	}
	if !bytes.Equal(bulkJSON, singleJSON) {
		t.Fatalf("candidate snapshots differ:\nsingle: %s\nbulk:   %s", singleJSON, bulkJSON)
	}
}

// TestWantedChapters_terminal_states_excluded verifies that downloaded,
// downloading, and permanently_failed chapters are NOT returned by WantedChapters
// — only wanted and failed are actionable.
func TestWantedChapters_terminal_states_excluded(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Terminal Test").SetSlug("terminal-test").SaveX(ctx)
	for i, st := range []entchapter.State{
		entchapter.StateDownloaded,
		entchapter.StateDownloading,
		entchapter.StatePermanentlyFailed,
	} {
		client.Chapter.Create().
			SetSeries(s).
			SetChapterKey(fmt.Sprintf("t-%d", i)).
			SetState(st).
			SaveX(ctx)
	}

	got, err := chapter.WantedChapters(ctx, client, 100)
	if err != nil {
		t.Fatalf("WantedChapters: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want 0 chapters (downloaded/downloading/permanently_failed excluded), got %d", len(got))
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

	got, err := chapter.WantedChapters(ctx, client, 100)
	if err != nil {
		t.Fatalf("WantedChapters: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want 0 chapters (upgrade_available excluded), got %d", len(got))
	}
}

// TestWantedChapters_ordered_by_number_ascending verifies that WantedChapters
// returns actionable chapters ordered by their numeric number ascending —
// independent of insert order. This pins the fix for the random-order bug: the
// Chapter id is a UUIDv4, so ordering by id scrambled the download sequence; the
// owner expects chapters to download 1, 2, 10, 20, … in number order.
func TestWantedChapters_ordered_by_number_ascending(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Order Test").SetSlug("order-test").SaveX(ctx)

	// Insert in scrambled order; the returned slice must still be number-ascending.
	for _, n := range []float64{20, 1, 10, 2} {
		client.Chapter.Create().
			SetSeries(s).
			SetChapterKey(fmt.Sprintf("%g", n)).
			SetNumber(n).
			SetState(entchapter.StateWanted).
			SaveX(ctx)
	}

	got, err := chapter.WantedChapters(ctx, client, 100)
	if err != nil {
		t.Fatalf("WantedChapters: %v", err)
	}

	wantOrder := []float64{1, 2, 10, 20}
	if len(got) != len(wantOrder) {
		t.Fatalf("want %d chapters, got %d", len(wantOrder), len(got))
	}
	for i, ch := range got {
		if ch.Number == nil {
			t.Fatalf("chapter %d: number is nil, want %g", i, wantOrder[i])
		}
		if *ch.Number != wantOrder[i] {
			t.Errorf("position %d: want number %g, got %g", i, wantOrder[i], *ch.Number)
		}
	}
}

// TestWantedChapters_null_number_sorts_last verifies that a chapter with no
// parsed number stays reachable but sorts after every numbered chapter (nulls
// last), so a missing number never jumps the download queue.
func TestWantedChapters_null_number_sorts_last(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Null Number Test").SetSlug("null-number-test").SaveX(ctx)

	// A numbered chapter and one with no number (nil), inserted null-first.
	client.Chapter.Create().SetSeries(s).SetChapterKey("no-number").SetState(entchapter.StateWanted).SaveX(ctx)
	client.Chapter.Create().SetSeries(s).SetChapterKey("5").SetNumber(5).SetState(entchapter.StateWanted).SaveX(ctx)

	got, err := chapter.WantedChapters(ctx, client, 100)
	if err != nil {
		t.Fatalf("WantedChapters: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 chapters, got %d", len(got))
	}
	if got[0].Number == nil || *got[0].Number != 5 {
		t.Errorf("position 0: want numbered chapter 5 first, got %v", got[0].Number)
	}
	if got[1].Number != nil {
		t.Errorf("position 1: want null-number chapter last, got %v", got[1].Number)
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
