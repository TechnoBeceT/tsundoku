package chapter_test

import (
	"context"
	"github.com/google/uuid"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
)

func TestCandidateResolutionSeparatesWaitingFromExhaustion(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	now := time.Now()
	row := client.Series.Create().SetTitle("resolution").SetSlug("resolution").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeriesID(row.ID).SetProvider("77").SetImportance(10).SaveX(ctx)
	ignored := client.SeriesProvider.Create().SetSeriesID(row.ID).SetProvider("88").SetImportance(20).SetIgnoreFractional(true).SaveX(ctx)
	chapters := createResolutionChapters(ctx, client, row.ID, sp.ID, ignored.ID, now)
	got, err := chapter.ResolveCandidatesForMany(ctx, client, chapters, 3, now, map[int64]bool{77: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range chapters {
		resolution := got[ch.ID]
		wantHas := ch.ChapterKey != "missing" && ch.ChapterKey != "fractional"
		wantExhausted := ch.ChapterKey == "exhausted"
		if resolution.HasProviders != wantHas || resolution.AllExhausted != wantExhausted || len(resolution.Candidates) != 0 {
			t.Errorf("%s: got %+v; want has=%v exhausted=%v no candidates", ch.ChapterKey, resolution, wantHas, wantExhausted)
		}
	}
}

func createResolutionChapters(ctx context.Context, client *ent.Client, seriesID, providerID, ignoredID uuid.UUID, now time.Time) []*ent.Chapter {
	var chapters []*ent.Chapter
	for _, key := range []string{"missing", "backoff", "exhausted", "paused", "fractional"} {
		ch := client.Chapter.Create().SetSeriesID(seriesID).SetChapterKey(key).SetNumber(1.5).SaveX(ctx)
		chapters = append(chapters, ch)
		if key == "missing" {
			continue
		}
		create := client.ProviderChapter.Create().SetSeriesProviderID(providerID).SetChapterKey(key).SetNumber(1.5).SetURL("https://example.org/" + key).SetProviderIndex(1)
		switch key {
		case "backoff":
			create.SetNextAttemptAt(now.Add(time.Hour))
		case "exhausted":
			create.SetAttempts(3)
		case "fractional":
			create.SetSeriesProviderID(ignoredID).SetAttempts(3)
		}
		create.SaveX(ctx)
	}
	return chapters
}
