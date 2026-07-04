package chapter_test

import (
	"context"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
)

// addSource creates a SeriesProvider at the given importance plus its
// ProviderChapter for chapterKey, seeding the per-source retry state
// (attempts + optional next_attempt_at cooldown). It returns the SeriesProvider id.
func addSource(ctx context.Context, t *testing.T, client *ent.Client, series *ent.Series, provider, chapterKey string, importance, attempts int, nextAttempt *time.Time) {
	t.Helper()
	sp := client.SeriesProvider.Create().
		SetSeries(series).
		SetProvider(provider).
		SetImportance(importance).
		SaveX(ctx)
	create := client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).
		SetChapterKey(chapterKey).
		SetURL("https://" + provider + ".example.com/" + chapterKey).
		SetProviderIndex(0).
		SetAttempts(attempts)
	if nextAttempt != nil {
		create = create.SetNextAttemptAt(*nextAttempt)
	}
	create.SaveX(ctx)
}

// seedChapter creates a series + a wanted chapter and returns them.
func seedChapter(ctx context.Context, t *testing.T, slug, key string) (*ent.Client, *ent.Series, *ent.Chapter) {
	t.Helper()
	client := testdb.New(t)
	s := client.Series.Create().SetTitle(slug).SetSlug(slug).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey(key).SaveX(ctx)
	return client, s, ch
}

// TestRankedLiveCandidates_ranks_by_importance_desc verifies that live sources
// are returned best-first (highest importance first).
func TestRankedLiveCandidates_ranks_by_importance_desc(t *testing.T) {
	ctx := context.Background()
	client, s, ch := seedChapter(ctx, t, "rank-desc", "c1")
	addSource(ctx, t, client, s, "low", "c1", 5, 0, nil)
	addSource(ctx, t, client, s, "high", "c1", 10, 0, nil)

	cands, err := chapter.RankedLiveCandidates(ctx, client, ch.ID, 3, time.Now())
	if err != nil {
		t.Fatalf("RankedLiveCandidates: %v", err)
	}
	if len(cands) != 2 {
		t.Fatalf("want 2 live candidates, got %d", len(cands))
	}
	if cands[0].SeriesProvider.Importance != 10 || cands[1].SeriesProvider.Importance != 5 {
		t.Errorf("want importance order [10,5], got [%d,%d]", cands[0].SeriesProvider.Importance, cands[1].SeriesProvider.Importance)
	}
}

// TestRankedLiveCandidates_excludes_exhausted verifies that a source whose
// attempts have reached maxRetries is not a live candidate, while a source with
// budget remaining is.
func TestRankedLiveCandidates_excludes_exhausted(t *testing.T) {
	ctx := context.Background()
	client, s, ch := seedChapter(ctx, t, "excl-exhausted", "c1")
	addSource(ctx, t, client, s, "spent", "c1", 10, 3, nil) // attempts == maxRetries
	addSource(ctx, t, client, s, "fresh", "c1", 5, 0, nil)  // budget left

	cands, err := chapter.RankedLiveCandidates(ctx, client, ch.ID, 3, time.Now())
	if err != nil {
		t.Fatalf("RankedLiveCandidates: %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("want 1 live candidate (exhausted excluded), got %d", len(cands))
	}
	if cands[0].SeriesProvider.Provider != "fresh" {
		t.Errorf("want the non-exhausted source, got %q", cands[0].SeriesProvider.Provider)
	}
}

// TestRankedLiveCandidates_excludes_cooldown verifies that a source whose
// next_attempt_at is in the future is gated out until its cooldown elapses, while
// a source with a past (or nil) cooldown is live.
func TestRankedLiveCandidates_excludes_cooldown(t *testing.T) {
	ctx := context.Background()
	client, s, ch := seedChapter(ctx, t, "excl-cooldown", "c1")
	now := time.Now()
	future := now.Add(1 * time.Hour)
	past := now.Add(-1 * time.Hour)
	addSource(ctx, t, client, s, "cooling", "c1", 10, 1, &future) // on cooldown
	addSource(ctx, t, client, s, "ready", "c1", 5, 1, &past)      // cooldown elapsed

	cands, err := chapter.RankedLiveCandidates(ctx, client, ch.ID, 3, now)
	if err != nil {
		t.Fatalf("RankedLiveCandidates: %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("want 1 live candidate (cooldown gated out), got %d", len(cands))
	}
	if cands[0].SeriesProvider.Provider != "ready" {
		t.Errorf("want the past-cooldown source, got %q", cands[0].SeriesProvider.Provider)
	}
}

// TestRankedLiveCandidates_empty_when_no_source verifies an empty slice (no error)
// when no source offers the chapter's key.
func TestRankedLiveCandidates_empty_when_no_source(t *testing.T) {
	ctx := context.Background()
	client, _, ch := seedChapter(ctx, t, "no-source", "c1")

	cands, err := chapter.RankedLiveCandidates(ctx, client, ch.ID, 3, time.Now())
	if err != nil {
		t.Fatalf("RankedLiveCandidates: %v", err)
	}
	if len(cands) != 0 {
		t.Fatalf("want 0 candidates, got %d", len(cands))
	}
}

// TestRankedLiveCandidates_migration_default verifies that a freshly-ingested
// source (attempts defaulting to 0, no cooldown) is IMMEDIATELY a live candidate —
// the zero-data-migration property (existing rows are live on upgrade).
func TestRankedLiveCandidates_migration_default(t *testing.T) {
	ctx := context.Background()
	client, s, ch := seedChapter(ctx, t, "migration-default", "c1")
	// A ProviderChapter created WITHOUT touching attempts/next_attempt_at.
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("p").SetImportance(7).SaveX(ctx)
	pc := client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey("c1").SetProviderIndex(0).SaveX(ctx)
	if pc.Attempts != 0 {
		t.Fatalf("attempts default: want 0, got %d", pc.Attempts)
	}
	if pc.NextAttemptAt != nil {
		t.Fatalf("next_attempt_at default: want nil, got %v", pc.NextAttemptAt)
	}

	cands, err := chapter.RankedLiveCandidates(ctx, client, ch.ID, 3, time.Now())
	if err != nil {
		t.Fatalf("RankedLiveCandidates: %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("want 1 live candidate (attempts=0 default is immediately live), got %d", len(cands))
	}
}

// TestHasAnyProviderChapter verifies the has-any predicate distinguishes "a source
// offers this chapter" from "no source has it yet".
func TestHasAnyProviderChapter(t *testing.T) {
	ctx := context.Background()
	client, s, ch := seedChapter(ctx, t, "has-any", "c1")

	has, err := chapter.HasAnyProviderChapter(ctx, client, ch.ID)
	if err != nil {
		t.Fatalf("HasAnyProviderChapter: %v", err)
	}
	if has {
		t.Fatal("want false before any source offers the chapter")
	}

	addSource(ctx, t, client, s, "p", "c1", 5, 0, nil)
	has, err = chapter.HasAnyProviderChapter(ctx, client, ch.ID)
	if err != nil {
		t.Fatalf("HasAnyProviderChapter: %v", err)
	}
	if !has {
		t.Fatal("want true once a source offers the chapter")
	}
}

// TestAllProvidersExhausted verifies the perma-fail condition: true only when at
// least one source offers the chapter AND every such source is exhausted.
func TestAllProvidersExhausted(t *testing.T) {
	ctx := context.Background()
	client, s, ch := seedChapter(ctx, t, "all-exhausted", "c1")

	// No source at all → not exhausted (awaiting a source, not permanently failed).
	got, err := chapter.AllProvidersExhausted(ctx, client, ch.ID, 3)
	if err != nil {
		t.Fatalf("AllProvidersExhausted (no source): %v", err)
	}
	if got {
		t.Fatal("want false with no source (chapter is awaiting a source, not exhausted)")
	}

	// One exhausted + one with budget → not all exhausted.
	addSource(ctx, t, client, s, "spent", "c1", 10, 3, nil)
	addSource(ctx, t, client, s, "fresh", "c1", 5, 1, nil)
	got, err = chapter.AllProvidersExhausted(ctx, client, ch.ID, 3)
	if err != nil {
		t.Fatalf("AllProvidersExhausted (partial): %v", err)
	}
	if got {
		t.Fatal("want false while any source still has retry budget")
	}

	// Exhaust the second source too → all exhausted.
	client.ProviderChapter.Update().
		Where().
		SetAttempts(3).
		SaveX(ctx)
	got, err = chapter.AllProvidersExhausted(ctx, client, ch.ID, 3)
	if err != nil {
		t.Fatalf("AllProvidersExhausted (all): %v", err)
	}
	if !got {
		t.Fatal("want true once every source is exhausted")
	}
}
