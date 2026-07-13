package suwayomi_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// seedIgnoredProvider pre-creates the Series + SeriesProvider rows AddSeries will
// find-or-create (series keyed by slug, provider keyed by (series, provider,
// scanlator)), carrying the given ignore_fractional flag. Ingest then upserts
// onto these existing rows, so the flag is in force for the very first sweep.
func seedIgnoredProvider(
	ctx context.Context,
	t *testing.T,
	client *ent.Client,
	title, sourceName string,
	ignoreFractional bool,
) {
	t.Helper()
	s := client.Series.Create().
		SetTitle(title).
		SetSlug(disk.Slugify(title)).
		SaveX(ctx)
	client.SeriesProvider.Create().
		SetSeries(s).
		SetProvider(sourceName).
		SetImportance(40).
		SetIgnoreFractional(ignoreFractional).
		SaveX(ctx)
}

// mixedFeed is Comic Asura's shape, verbatim from prod: every whole chapter is
// re-uploaded as a lone "N.1" under its own URL (179 pages vs the original's 26).
// Numbers: 1, 1.1, 2, 2.1, 3.
func mixedFeed() []suwayomi.Chapter {
	nums := []float64{1, 1.1, 2, 2.1, 3}
	chs := make([]suwayomi.Chapter, len(nums))
	for i, n := range nums {
		num := n
		label := chapter.FormatChapterNumber(num)
		chs[i] = suwayomi.Chapter{
			ID:     200 + i,
			Index:  i,
			Name:   "Chapter " + label,
			Number: &num,
			URL:    "https://suwayomi.test/ch/" + label,
		}
	}
	return chs
}

// TestIngest_AddSeries_IgnoredSourceSkipsFractionalChapters proves the INGEST
// gate: a source the owner has flagged as a fractional re-uploader creates NO
// ProviderChapter rows for its fractional chapters. Only the wholes land.
func TestIngest_AddSeries_IgnoredSourceSkipsFractionalChapters(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		mangaID    = 42
		mangaTitle = "Solo Leveling"
		sourceName = "Comic Asura"
	)
	seedIgnoredProvider(ctx, t, client, mangaTitle, sourceName, true)

	sc := &ingestStubClient{
		chapters:  mixedFeed(),
		mangaMeta: suwayomi.Manga{Title: mangaTitle},
	}
	ing := suwayomi.NewIngest(sc, client)
	if _, err := ing.AddSeries(ctx, sourceName, mangaID, mangaTitle, ""); err != nil {
		t.Fatalf("AddSeries: %v", err)
	}

	pcs := client.ProviderChapter.Query().AllX(ctx)
	if len(pcs) != 3 {
		t.Fatalf("ProviderChapter count: got %d, want 3 (the 3 wholes; the 2 fractional re-uploads are dropped)", len(pcs))
	}
	for _, pc := range pcs {
		if pc.Number != nil && *pc.Number != float64(int(*pc.Number)) {
			t.Errorf("ProviderChapter %q (number %v): a flagged source must ingest NO fractional chapter", pc.ChapterKey, *pc.Number)
		}
	}
}

// TestIngest_AddSeries_NonIgnoredSourceIngestsOmake is the 825-omake regression
// guard AT INGEST: the exact same feed, on a source WITHOUT the toggle, ingests
// in full — fractional chapters included. `.5` side-chapters are the most common
// fractional in a real library; suppression must be reachable ONLY through an
// explicitly-ticked toggle.
func TestIngest_AddSeries_NonIgnoredSourceIngestsOmake(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		mangaID    = 42
		mangaTitle = "Solo Leveling"
		sourceName = "Comic Asura"
	)
	seedIgnoredProvider(ctx, t, client, mangaTitle, sourceName, false)

	sc := &ingestStubClient{
		chapters:  mixedFeed(),
		mangaMeta: suwayomi.Manga{Title: mangaTitle},
	}
	ing := suwayomi.NewIngest(sc, client)
	if _, err := ing.AddSeries(ctx, sourceName, mangaID, mangaTitle, ""); err != nil {
		t.Fatalf("AddSeries: %v", err)
	}

	pcs := client.ProviderChapter.Query().AllX(ctx)
	if len(pcs) != 5 {
		t.Fatalf("ProviderChapter count: got %d, want 5 (the WHOLE feed — a non-flagged source keeps its fractional chapters)", len(pcs))
	}
	fractional := 0
	for _, pc := range pcs {
		if pc.Number != nil && *pc.Number != float64(int(*pc.Number)) {
			fractional++
		}
	}
	if fractional != 2 {
		t.Fatalf("fractional ProviderChapter rows: got %d, want 2 — a source WITHOUT the toggle must keep offering them", fractional)
	}
}

// TestIngest_AddSeries_IgnoredSourceBackfillsSuwayomiIDs pins that the gate is
// applied to the RAW chapter slice, so the ingest mapping AND the downstream
// suwayomi_chapter_id backfill see the SAME set. A gate applied to only one of
// them would leave the surviving whole chapters with a suwayomi_chapter_id of 0,
// which is what the fetcher needs to download them at all.
func TestIngest_AddSeries_IgnoredSourceBackfillsSuwayomiIDs(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		mangaID    = 42
		mangaTitle = "Solo Leveling"
		sourceName = "Comic Asura"
	)
	seedIgnoredProvider(ctx, t, client, mangaTitle, sourceName, true)

	sc := &ingestStubClient{
		chapters:  mixedFeed(),
		mangaMeta: suwayomi.Manga{Title: mangaTitle},
	}
	ing := suwayomi.NewIngest(sc, client)
	if _, err := ing.AddSeries(ctx, sourceName, mangaID, mangaTitle, ""); err != nil {
		t.Fatalf("AddSeries: %v", err)
	}

	for _, pc := range client.ProviderChapter.Query().AllX(ctx) {
		if pc.SuwayomiChapterID == 0 {
			t.Errorf("ProviderChapter %q: suwayomi_chapter_id is 0 — the backfill did not see this row (the gate must be applied to the RAW slice, once)", pc.ChapterKey)
		}
	}
}
