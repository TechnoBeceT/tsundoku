package schema_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/ent/chapter"
)

// TestChapterKeyIsUnique verifies that the (series_id, chapter_key) unique index fires
// and prevents duplicate chapter keys within the same series.
func TestChapterKeyIsUnique(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Test Series").SetSlug("test-series").SaveX(ctx)

	client.Chapter.Create().SetSeries(s).SetChapterKey("12").SetState(chapter.StateWanted).SaveX(ctx)

	_, err := client.Chapter.Create().SetSeries(s).SetChapterKey("12").SetState(chapter.StateWanted).Save(ctx)
	if err == nil || !ent.IsConstraintError(err) {
		t.Fatalf("expected unique constraint violation, got %v", err)
	}

	// A different key in the same series must succeed.
	client.Chapter.Create().SetSeries(s).SetChapterKey("12.5").SetState(chapter.StateWanted).SaveX(ctx)
}

// TestProviderChapterKeyIsUnique verifies that the (series_provider_id, chapter_key) unique
// index fires and prevents duplicate chapter keys within the same provider.
func TestProviderChapterKeyIsUnique(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Test Series 2").SetSlug("test-series-2").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("test-provider").SetImportance(1).SaveX(ctx)

	client.ProviderChapter.Create().SetSeriesProvider(sp).SetChapterKey("1").SaveX(ctx)

	_, err := client.ProviderChapter.Create().SetSeriesProvider(sp).SetChapterKey("1").Save(ctx)
	if err == nil || !ent.IsConstraintError(err) {
		t.Fatalf("expected unique constraint violation for provider chapter, got %v", err)
	}

	// A different key for the same provider must succeed.
	client.ProviderChapter.Create().SetSeriesProvider(sp).SetChapterKey("2").SaveX(ctx)
}
