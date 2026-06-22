// Package chapter_test contains integration tests for the chapter ingest service.
// Tests require Docker (via testcontainers) for an ephemeral PostgreSQL instance.
package chapter_test

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
)

// TestIngestDedupAcrossProviders verifies the core dedup invariant:
// ingesting the same chapter_key from two different SeriesProviders of one
// Series produces exactly ONE Chapter row and TWO ProviderChapter rows.
// The dedup is non-vacuous — it is the (series_id, chapter_key) unique index
// doing the work, not application-level filtering.
func TestIngestDedupAcrossProviders(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Dedup Test").SetSlug("dedup-test").SaveX(ctx)
	sp1 := client.SeriesProvider.Create().SetSeries(s).SetProvider("provider-a").SetImportance(1).SaveX(ctx)
	sp2 := client.SeriesProvider.Create().SetSeries(s).SetProvider("provider-b").SetImportance(2).SaveX(ctx)

	fc := chapter.FetchedChapter{
		Number:        ptr(12.0),
		Name:          "Chapter 12",
		URL:           "https://example.com/ch12",
		ProviderIndex: 0,
	}

	res1, err := chapter.IngestProviderChapters(ctx, client, sp1.ID, []chapter.FetchedChapter{fc})
	if err != nil {
		t.Fatalf("ingest sp1: %v", err)
	}
	if res1.NewChapters != 1 {
		t.Errorf("sp1 ingest: want 1 new chapter, got %d", res1.NewChapters)
	}
	if res1.NewProviderChapters != 1 {
		t.Errorf("sp1 ingest: want 1 new provider chapter, got %d", res1.NewProviderChapters)
	}

	res2, err := chapter.IngestProviderChapters(ctx, client, sp2.ID, []chapter.FetchedChapter{fc})
	if err != nil {
		t.Fatalf("ingest sp2: %v", err)
	}
	// The Chapter for key "12" already exists — must NOT create a second one.
	if res2.NewChapters != 0 {
		t.Errorf("sp2 ingest: want 0 new chapters (dedup), got %d", res2.NewChapters)
	}
	if res2.NewProviderChapters != 1 {
		t.Errorf("sp2 ingest: want 1 new provider chapter, got %d", res2.NewProviderChapters)
	}

	chapterCount := client.Chapter.Query().CountX(ctx)
	if chapterCount != 1 {
		t.Errorf("want exactly 1 chapter row, got %d", chapterCount)
	}

	pcCount := client.ProviderChapter.Query().
		Where(entproviderchapter.SeriesProviderID(sp1.ID)).
		CountX(ctx)
	pcCount += client.ProviderChapter.Query().
		Where(entproviderchapter.SeriesProviderID(sp2.ID)).
		CountX(ctx)
	if pcCount != 2 {
		t.Errorf("want exactly 2 provider chapter rows, got %d", pcCount)
	}
}

// TestIngestConcurrentRaceNoDuplicate verifies that a concurrent double-ingest of
// the same chapter_key produces one Chapter row and no error surfaced to callers.
// Both goroutines must complete without error even though only one can win the
// INSERT race; the loser must re-fetch the existing row instead of propagating
// the constraint error.
func TestIngestConcurrentRaceNoDuplicate(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Race Test").SetSlug("race-test").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("provider-race").SetImportance(1).SaveX(ctx)

	fc := chapter.FetchedChapter{
		Number:        ptr(5.0),
		ProviderIndex: 0,
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	for i := range 2 {
		go func() {
			defer wg.Done()
			<-start
			_, err := chapter.IngestProviderChapters(ctx, client, sp.ID, []chapter.FetchedChapter{fc})
			errs[i] = err
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d got error: %v", i, err)
		}
	}

	chapterCount := client.Chapter.Query().CountX(ctx)
	if chapterCount != 1 {
		t.Errorf("concurrent ingest: want 1 chapter row, got %d", chapterCount)
	}
}

// TestIngestKeyNormalizationDedup verifies that ingesting the same numeric chapter
// via two providers normalises to the same chapter_key and produces exactly one
// Chapter row. This exercises Task 1's NormalizeChapterKey via the ingest path.
func TestIngestKeyNormalizationDedup(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Norm Test").SetSlug("norm-test").SaveX(ctx)
	sp1 := client.SeriesProvider.Create().SetSeries(s).SetProvider("prov-norm-a").SetImportance(1).SaveX(ctx)
	sp2 := client.SeriesProvider.Create().SetSeries(s).SetProvider("prov-norm-b").SetImportance(2).SaveX(ctx)

	// Both 12.0 (float literal) and 12 (integer-like literal) normalise to
	// chapter_key "12" via NormalizeChapterKey — trailing zero is stripped.
	_, err := chapter.IngestProviderChapters(ctx, client, sp1.ID, []chapter.FetchedChapter{
		{Number: ptr(12.0), ProviderIndex: 0},
	})
	if err != nil {
		t.Fatalf("ingest 12.0: %v", err)
	}

	_, err = chapter.IngestProviderChapters(ctx, client, sp2.ID, []chapter.FetchedChapter{
		{Number: ptr(12), ProviderIndex: 0},
	})
	if err != nil {
		t.Fatalf("ingest 12 via sp2: %v", err)
	}

	chapterCount := client.Chapter.Query().CountX(ctx)
	if chapterCount != 1 {
		t.Errorf("normalisation dedup: want 1 chapter row, got %d", chapterCount)
	}

	// Confirm the normaliser produced key "12", not "12.0".
	ch := client.Chapter.Query().OnlyX(ctx)
	if ch.ChapterKey != "12" {
		t.Errorf("normalised chapter_key: want %q, got %q", "12", ch.ChapterKey)
	}
}

// TestSetStateIllegalTransitionRejected verifies that SetState rejects an illegal
// transition (downloaded → wanted is not in the state graph) with an error and
// leaves the chapter state unchanged.
func TestSetStateIllegalTransitionRejected(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("State Test").SetSlug("state-test").SaveX(ctx)
	ch := client.Chapter.Create().
		SetSeries(s).
		SetChapterKey("1").
		SetState(entchapter.StateDownloaded).
		SaveX(ctx)

	err := chapter.SetState(ctx, client, ch.ID, entchapter.StateWanted)
	if err == nil {
		t.Fatal("expected error for illegal transition downloaded→wanted, got nil")
	}

	// State must be unchanged.
	refreshed := client.Chapter.GetX(ctx, ch.ID)
	if refreshed.State != entchapter.StateDownloaded {
		t.Errorf("state changed unexpectedly: got %s", refreshed.State)
	}
}

// TestSetStateLegalTransitionSucceeds verifies that SetState accepts a legal
// transition (wanted → downloading) and persists the new state.
func TestSetStateLegalTransitionSucceeds(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("State OK Test").SetSlug("state-ok-test").SaveX(ctx)
	ch := client.Chapter.Create().
		SetSeries(s).
		SetChapterKey("2").
		SetState(entchapter.StateWanted).
		SaveX(ctx)

	err := chapter.SetState(ctx, client, ch.ID, entchapter.StateDownloading)
	if err != nil {
		t.Fatalf("expected no error for wanted→downloading, got: %v", err)
	}

	refreshed := client.Chapter.GetX(ctx, ch.ID)
	if refreshed.State != entchapter.StateDownloading {
		t.Errorf("state not updated: want %s, got %s", entchapter.StateDownloading, refreshed.State)
	}
}

// TestIngestResultCounts verifies that IngestResult.NewChapters and
// IngestResult.NewProviderChapters count genuinely new rows only.
// A second ingest of the same list must report 0 new of each.
func TestIngestResultCounts(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Count Test").SetSlug("count-test").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("prov-count").SetImportance(1).SaveX(ctx)

	chapters := []chapter.FetchedChapter{
		{Number: ptr(1.0), ProviderIndex: 0},
		{Number: ptr(2.0), ProviderIndex: 1},
		{Number: ptr(3.0), ProviderIndex: 2},
	}

	res, err := chapter.IngestProviderChapters(ctx, client, sp.ID, chapters)
	if err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	if res.NewChapters != 3 {
		t.Errorf("first ingest: want NewChapters=3, got %d", res.NewChapters)
	}
	if res.NewProviderChapters != 3 {
		t.Errorf("first ingest: want NewProviderChapters=3, got %d", res.NewProviderChapters)
	}

	// Second ingest of the same list: no new rows.
	res2, err := chapter.IngestProviderChapters(ctx, client, sp.ID, chapters)
	if err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if res2.NewChapters != 0 {
		t.Errorf("second ingest: want NewChapters=0, got %d", res2.NewChapters)
	}
	if res2.NewProviderChapters != 0 {
		t.Errorf("second ingest: want NewProviderChapters=0, got %d", res2.NewProviderChapters)
	}
}

// TestSetStateChapterNotFound verifies that SetState returns a non-nil error
// when the given chapter ID does not exist.
func TestSetStateChapterNotFound(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	err := chapter.SetState(ctx, client, uuid.New(), entchapter.StateDownloading)
	if err == nil {
		t.Fatal("expected error for nonexistent chapter ID, got nil")
	}
}

// TestIngestProviderChaptersDBError verifies that IngestProviderChapters returns
// a non-nil error when the database is unavailable (cancelled context).
func TestIngestProviderChaptersDBError(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	// Create a real SeriesProvider before cancelling so the cancellation exercises
	// the chapter ingest path, not the SeriesProvider load path.
	s := client.Series.Create().SetTitle("Error Test").SetSlug("error-test").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("prov-error").SetImportance(1).SaveX(ctx)

	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately to simulate a dead DB connection

	chapters := []chapter.FetchedChapter{
		{Number: ptr(1.0), ProviderIndex: 0},
	}
	_, err := chapter.IngestProviderChapters(cancelCtx, client, sp.ID, chapters)
	if err == nil {
		t.Fatal("expected error with cancelled context, got nil")
	}
}

// TestAbsorbProviderChapterRace verifies absorbProviderChapterRace deterministically:
// given an existing ProviderChapter row, calling AbsorbProviderChapterRace with new
// values must re-fetch the row, update all mutable fields, and return nil error.
// This exercises the concurrent-INSERT loser path without relying on a real race.
func TestAbsorbProviderChapterRace(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Race Absorb Test").SetSlug("race-absorb-test").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("prov-absorb").SetImportance(1).SaveX(ctx)

	const key = "7"

	// Seed an existing ProviderChapter row — this is the "winner" of the INSERT race.
	initialNum := ptr(7.0)
	client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).
		SetChapterKey(key).
		SetNillableNumber(initialNum).
		SetName("Chapter 7 initial").
		SetURL("https://example.com/ch7-initial").
		SetProviderIndex(0).
		SaveX(ctx)

	// The "loser" goroutine calls AbsorbProviderChapterRace with updated values.
	newNum := ptr(7.0)
	newFC := chapter.FetchedChapter{
		Number:        newNum,
		Name:          "Chapter 7 updated",
		URL:           "https://example.com/ch7-updated",
		ProviderIndex: 99,
	}

	err := chapter.AbsorbProviderChapterRace(ctx, client, sp.ID, key, newFC)
	if err != nil {
		t.Fatalf("AbsorbProviderChapterRace returned unexpected error: %v", err)
	}

	// Verify the existing row was updated to the new values.
	rows := client.ProviderChapter.Query().AllX(ctx)
	if len(rows) != 1 {
		t.Fatalf("want exactly 1 ProviderChapter row, got %d", len(rows))
	}
	got := rows[0]
	if got.Name != newFC.Name {
		t.Errorf("Name: want %q, got %q", newFC.Name, got.Name)
	}
	if got.URL != newFC.URL {
		t.Errorf("URL: want %q, got %q", newFC.URL, got.URL)
	}
	if got.ProviderIndex != newFC.ProviderIndex {
		t.Errorf("ProviderIndex: want %d, got %d", newFC.ProviderIndex, got.ProviderIndex)
	}
}
