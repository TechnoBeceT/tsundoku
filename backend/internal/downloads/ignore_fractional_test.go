// Package downloads_test — the ignore-fractional surface of the activity list: a
// source the owner has flagged as a fractional re-uploader
// (SeriesProvider.ignore_fractional) offers NO fractional chapters, so the read
// model must never name it as the source of one. These tests pin the read model
// against the engine's own rule (chapter.dropIgnoredFractionalSources): the same
// exclusion, the same fail-open on an unjudgeable number.
package downloads_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/downloads"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
)

// fractionalStates spans every state the ignore-fractional seed uses, so ONE List
// call surfaces all of its rows.
var fractionalStates = []entchapter.State{
	entchapter.StateWanted,
	entchapter.StateDownloading,
}

// seedIgnoredFractional reproduces the owner's real shape: "Comic Asura" is a
// fractional re-uploader (flagged, and deliberately given the HIGHER importance so
// a broken fix would name it first), "Asura Scans" is a normal source.
//
//   - 10.1 — fractional, carried ONLY by the flagged source ⇒ SOURCELESS: the engine
//     drops the flagged source from candidacy, emits download.skip and leaves the
//     chapter wanted. Nothing is fetching it, so no source may be named.
//   - 11.1 — fractional, carried by BOTH: the flagged (higher) source is excluded, so
//     the row must name the NON-flagged one — the source the engine will really use.
//   - 12.5 — a genuine `.5` omake on the NON-flagged source only: untouched (the
//     825-omake guard, restated at the read-model layer).
//   - 13   — a WHOLE chapter on the flagged source: untouched (the flag suppresses
//     fractionals, it does not disable the source).
//   - x-1  — a feed row with NO parsed number on the flagged source: unjudgeable ⇒
//     fail-open, still named (mirrors the engine's nil-number carve-out).
func seedIgnoredFractional(ctx context.Context, t *testing.T, client *ent.Client) {
	t.Helper()

	s := client.Series.Create().SetTitle("Solo Leveling").SetSlug("solo-leveling-ign").
		SetCategoryID(catID(ctx, client, "Manhwa")).SaveX(ctx)

	comic := client.SeriesProvider.Create().SetSeries(s).
		SetProvider("comic-asura").SetProviderName("Comic Asura").SetImportance(60).
		SetIgnoreFractional(true).SaveX(ctx)
	asura := client.SeriesProvider.Create().SetSeries(s).
		SetProvider("asura").SetProviderName("Asura Scans").SetImportance(40).SaveX(ctx)

	// Feed rows: (provider, key, number) — number nil means "unparseable".
	addFeed := func(sp *ent.SeriesProvider, key string, number *float64) {
		client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey(key).
			SetNillableNumber(number).SaveX(ctx)
	}
	num := func(n float64) *float64 { return &n }

	addFeed(comic, "10.1", num(10.1))
	addFeed(comic, "11.1", num(11.1))
	addFeed(asura, "11.1", num(11.1))
	addFeed(asura, "12.5", num(12.5))
	addFeed(comic, "13", num(13))
	addFeed(comic, "x-1", nil)

	chapter := func(key string, number *float64, state entchapter.State) {
		client.Chapter.Create().SetSeries(s).SetChapterKey(key).
			SetNillableNumber(number).SetState(state).SaveX(ctx)
	}
	chapter("10.1", num(10.1), entchapter.StateWanted)
	chapter("11.1", num(11.1), entchapter.StateDownloading)
	chapter("12.5", num(12.5), entchapter.StateDownloading)
	chapter("13", num(13), entchapter.StateDownloading)
	chapter("x-1", nil, entchapter.StateWanted)
}

// listIgnoredFractional seeds the ignore-fractional shape and returns the page.
func listIgnoredFractional(ctx context.Context, t *testing.T, client *ent.Client) []downloads.DownloadChapterDTO {
	t.Helper()
	seedIgnoredFractional(ctx, t, client)
	res, err := downloads.NewService(client).List(ctx, downloads.ListFilter{States: fractionalStates, Limit: 50})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return res.Items
}

// TestListIgnoredFractionalSourceIsNeverNamed is the feature-interaction bug: with
// the toggle ticked, chapter 10.1's only carrier is excluded from candidacy, so the
// engine skips it and it sits wanted forever. The row must say so (no source) — not
// keep naming the very source the owner told it to ignore, which is exactly the lie
// the source-truth fix exists to kill.
func TestListIgnoredFractionalSourceIsNeverNamed(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	items := listIgnoredFractional(ctx, t, client)

	row, ok := itemByKey(items, "10.1")
	if !ok {
		t.Fatal("missing chapter 10.1")
	}
	if row.Provider != "" || row.ProviderName != "" {
		t.Errorf("10.1 provider = %q/%q, want empty — its only carrier ignores fractionals, so nothing is fetching it",
			row.Provider, row.ProviderName)
	}
}

// TestListFractionalFallsThroughToTheNonIgnoredSource: when a flagged source and a
// normal one both carry a fractional chapter, the engine drops the flagged one from
// candidacy — even though it outranks. The row must name the source that will
// actually fetch it.
func TestListFractionalFallsThroughToTheNonIgnoredSource(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	items := listIgnoredFractional(ctx, t, client)

	row, ok := itemByKey(items, "11.1")
	if !ok {
		t.Fatal("missing chapter 11.1")
	}
	if row.Provider != "asura" || row.ProviderName != "Asura Scans" {
		t.Errorf("11.1 provider = %q/%q, want asura/Asura Scans — the flagged higher source offers no fractionals",
			row.Provider, row.ProviderName)
	}
}

// TestListOmakeOnNonIgnoredSourceIsUnaffected is the 825-omake guard restated at the
// read-model layer: `.5` side-chapters are the most common fractional in the real
// library (825 across 44 series) and are GENUINE. The exclusion is keyed on the
// SOURCE's flag, never on fractional-ness alone, so an omake from a normal source is
// named exactly as before.
func TestListOmakeOnNonIgnoredSourceIsUnaffected(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	items := listIgnoredFractional(ctx, t, client)

	row, ok := itemByKey(items, "12.5")
	if !ok {
		t.Fatal("missing chapter 12.5")
	}
	if row.Provider != "asura" || row.ProviderName != "Asura Scans" {
		t.Errorf("12.5 provider = %q/%q, want asura/Asura Scans — a `.5` omake on a non-flagged source is untouched",
			row.Provider, row.ProviderName)
	}
}

// TestListIgnoredSourceStillNamedForWholeChapters: the toggle suppresses a source's
// fractional re-uploads, it does not disable the source. Its whole chapters are
// still fetched from it, so the row still names it.
func TestListIgnoredSourceStillNamedForWholeChapters(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	items := listIgnoredFractional(ctx, t, client)

	row, ok := itemByKey(items, "13")
	if !ok {
		t.Fatal("missing chapter 13")
	}
	if row.Provider != "comic-asura" || row.ProviderName != "Comic Asura" {
		t.Errorf("13 provider = %q/%q, want comic-asura/Comic Asura — the flag suppresses fractionals only",
			row.Provider, row.ProviderName)
	}
}

// TestListUnnumberedChapterOnIgnoredSourceStillNamed pins the deliberate FAIL-OPEN
// the engine uses (chapter.dropIgnoredFractionalSources returns early on a nil
// number): a chapter with no parsed number cannot be judged fractional, so the
// source is left alone rather than silently orphaning the chapter. The read model
// must fail open the same way, or it would report "no source" for a chapter the
// engine is happily downloading.
func TestListUnnumberedChapterOnIgnoredSourceStillNamed(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	items := listIgnoredFractional(ctx, t, client)

	row, ok := itemByKey(items, "x-1")
	if !ok {
		t.Fatal("missing chapter x-1")
	}
	if row.Provider != "comic-asura" || row.ProviderName != "Comic Asura" {
		t.Errorf("x-1 provider = %q/%q, want comic-asura/Comic Asura — an unnumbered chapter cannot be judged fractional",
			row.Provider, row.ProviderName)
	}
}
