// Package series_test exercises the library read service against an ephemeral
// PostgreSQL instance (testdb). Tests require Docker.
package series_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	"github.com/technobecet/tsundoku/internal/series"
)

// seededLibrary holds the ids of the fixture series so tests can target them.
type seededLibrary struct {
	mangaID  uuid.UUID
	manhwaID uuid.UUID
}

// seedLibrary creates two series in different categories:
//   - "Alpha Saga" (Manga): 1 downloaded + 1 wanted chapter, one provider.
//   - "Beta Quest" (Manhwa): 1 downloaded + 1 failed + 1 wanted chapter, two providers.
//
// The non-trivial state mix makes the chapter-count rollup assertions non-vacuous.
func seedLibrary(ctx context.Context, t *testing.T, client *ent.Client) seededLibrary {
	t.Helper()

	manga := client.Series.Create().
		SetTitle("Alpha Saga").
		SetSlug("alpha-saga").
		SetCoverURL("https://example.test/alpha.jpg").
		SetCategory(entseries.CategoryManga).
		SaveX(ctx)

	num1, num2 := 1.0, 2.0
	pages := 20
	client.Chapter.Create().
		SetSeriesID(manga.ID).
		SetChapterKey("alpha-1").
		SetNumber(num1).
		SetState(entchapter.StateDownloaded).
		SetFilename("[mangadex][en] Alpha Saga 001.cbz").
		SetPageCount(pages).
		SaveX(ctx)
	client.Chapter.Create().
		SetSeriesID(manga.ID).
		SetChapterKey("alpha-2").
		SetNumber(num2).
		SetState(entchapter.StateWanted).
		SaveX(ctx)

	client.SeriesProvider.Create().
		SetSeriesID(manga.ID).
		SetProvider("mangadex").
		SetScanlator("ScanGroup").
		SetLanguage("en").
		SetImportance(10).
		SaveX(ctx)

	manhwa := client.Series.Create().
		SetTitle("Beta Quest").
		SetSlug("beta-quest").
		SetCategory(entseries.CategoryManhwa).
		SaveX(ctx)

	bnum1, bnum2, bnum3 := 1.0, 2.0, 3.0
	client.Chapter.Create().
		SetSeriesID(manhwa.ID).
		SetChapterKey("beta-1").
		SetNumber(bnum1).
		SetState(entchapter.StateDownloaded).
		SaveX(ctx)
	client.Chapter.Create().
		SetSeriesID(manhwa.ID).
		SetChapterKey("beta-2").
		SetNumber(bnum2).
		SetState(entchapter.StateFailed).
		SaveX(ctx)
	client.Chapter.Create().
		SetSeriesID(manhwa.ID).
		SetChapterKey("beta-3").
		SetNumber(bnum3).
		SetState(entchapter.StateWanted).
		SaveX(ctx)

	client.SeriesProvider.Create().
		SetSeriesID(manhwa.ID).
		SetProvider("asura").
		SetLanguage("en").
		SetImportance(5).
		SaveX(ctx)
	client.SeriesProvider.Create().
		SetSeriesID(manhwa.ID).
		SetProvider("flame").
		SetLanguage("en").
		SetImportance(8).
		SaveX(ctx)

	return seededLibrary{mangaID: manga.ID, manhwaID: manhwa.ID}
}

func TestListSeriesReturnsAllWithRollup(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seedLibrary(ctx, t, client)

	svc := series.NewService(client, t.TempDir())
	got, err := svc.ListSeries(ctx, series.ListFilter{})
	if err != nil {
		t.Fatalf("ListSeries: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("ListSeries: want 2 series, got %d", len(got))
	}

	// Deterministic title-ASC order: "Alpha Saga" then "Beta Quest".
	if got[0].Title != "Alpha Saga" || got[1].Title != "Beta Quest" {
		t.Fatalf("ListSeries: want title-ASC order [Alpha Saga, Beta Quest], got [%s, %s]", got[0].Title, got[1].Title)
	}

	alpha := got[0]
	if alpha.Slug != "alpha-saga" || alpha.Category != "Manga" || alpha.CoverURL != "https://example.test/alpha.jpg" {
		t.Fatalf("ListSeries: alpha summary mismatch: %+v", alpha)
	}
	// Non-vacuous rollup: 1 downloaded + 1 wanted = total 2.
	wantAlpha := series.ChapterCounts{Total: 2, Downloaded: 1, Wanted: 1, Failed: 0}
	if alpha.ChapterCounts != wantAlpha {
		t.Fatalf("ListSeries: alpha counts: want %+v, got %+v", wantAlpha, alpha.ChapterCounts)
	}

	beta := got[1]
	wantBeta := series.ChapterCounts{Total: 3, Downloaded: 1, Wanted: 1, Failed: 1}
	if beta.ChapterCounts != wantBeta {
		t.Fatalf("ListSeries: beta counts: want %+v, got %+v", wantBeta, beta.ChapterCounts)
	}
}

func TestListSeriesFiltersByCategory(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seedLibrary(ctx, t, client)

	svc := series.NewService(client, t.TempDir())
	cat := "Manhwa"
	got, err := svc.ListSeries(ctx, series.ListFilter{Category: &cat})
	if err != nil {
		t.Fatalf("ListSeries: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("ListSeries(Manhwa): want 1 series, got %d", len(got))
	}
	if got[0].Title != "Beta Quest" || got[0].Category != "Manhwa" {
		t.Fatalf("ListSeries(Manhwa): wrong series: %+v", got[0])
	}
}

func TestListSeriesPaginates(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seedLibrary(ctx, t, client)

	svc := series.NewService(client, t.TempDir())

	page1, err := svc.ListSeries(ctx, series.ListFilter{Limit: 1, Offset: 0})
	if err != nil {
		t.Fatalf("ListSeries page1: %v", err)
	}
	page2, err := svc.ListSeries(ctx, series.ListFilter{Limit: 1, Offset: 1})
	if err != nil {
		t.Fatalf("ListSeries page2: %v", err)
	}

	if len(page1) != 1 || len(page2) != 1 {
		t.Fatalf("pagination: want 1 per page, got %d and %d", len(page1), len(page2))
	}
	if page1[0].Title != "Alpha Saga" {
		t.Fatalf("pagination: page1 want Alpha Saga, got %s", page1[0].Title)
	}
	if page2[0].Title != "Beta Quest" {
		t.Fatalf("pagination: page2 want Beta Quest, got %s", page2[0].Title)
	}
}

func TestGetSeriesReturnsDetail(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	lib := seedLibrary(ctx, t, client)

	svc := series.NewService(client, t.TempDir())
	got, err := svc.GetSeries(ctx, lib.manhwaID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}

	if got.Title != "Beta Quest" || got.Category != "Manhwa" {
		t.Fatalf("GetSeries: summary mismatch: %+v", got)
	}
	if got.ChapterCounts != (series.ChapterCounts{Total: 3, Downloaded: 1, Wanted: 1, Failed: 1}) {
		t.Fatalf("GetSeries: counts: %+v", got.ChapterCounts)
	}

	if len(got.Chapters) != 3 {
		t.Fatalf("GetSeries: want 3 chapters, got %d", len(got.Chapters))
	}
	// Ordered by number then chapter_key.
	if got.Chapters[0].ChapterKey != "beta-1" || got.Chapters[2].ChapterKey != "beta-3" {
		t.Fatalf("GetSeries: chapter order wrong: %+v", got.Chapters)
	}
	if got.Chapters[1].State != "failed" {
		t.Fatalf("GetSeries: beta-2 state want failed, got %s", got.Chapters[1].State)
	}

	if len(got.Providers) != 2 {
		t.Fatalf("GetSeries: want 2 providers, got %d", len(got.Providers))
	}
}

func TestGetSeriesNotFound(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seedLibrary(ctx, t, client)

	svc := series.NewService(client, t.TempDir())
	_, err := svc.GetSeries(ctx, uuid.New())
	if !errors.Is(err, series.ErrSeriesNotFound) {
		t.Fatalf("GetSeries(random): want ErrSeriesNotFound, got %v", err)
	}
}
