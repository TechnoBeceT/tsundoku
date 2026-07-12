// Package downloads_test — the SOURCE-TRUTH surface of the activity list: the row
// must name the source a chapter is ACTUALLY coming from, never the series'
// top-ranked source. These tests reproduce the production bug that motivated the
// fix (see TestListUnsatisfiedChapterNamesTheSourceThatCarriesIt).
package downloads_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/downloads"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
)

// sourceTruth holds the ids the source-truth assertions target.
type sourceTruth struct {
	asuraID uuid.UUID // "Asura Scans", importance 60 — the series' TOP source
	comicID uuid.UUID // "Comic Asura", importance 40 — the source really fetching 5.1
}

// seedSourceTruth reproduces the production shape exactly. A series whose
// TOP-RANKED source (Asura Scans, importance 60) does NOT carry chapter key "5.1",
// and a LOWER source (Comic Asura, importance 40) that does:
//
//   - "5.1" downloading, no satisfier — only Comic Asura's feed carries it. The
//     engine is fetching it from Comic Asura; the row used to say "Asura Scans".
//   - "6"   downloaded, satisfied by Comic Asura even though Asura Scans also
//     carries it — true provenance must still win over the feed ranking.
//   - "9"   wanted, in NO feed at all — nothing is fetching it, so no source may
//     be named (owner-ratified: the row renders an em-dash).
func seedSourceTruth(ctx context.Context, t *testing.T, client *ent.Client) sourceTruth {
	t.Helper()

	s := client.Series.Create().SetTitle("Solo Leveling").SetSlug("solo-leveling").
		SetCategoryID(catID(ctx, client, "Manhwa")).SaveX(ctx)

	asura := client.SeriesProvider.Create().SetSeries(s).
		SetProvider("asura").SetProviderName("Asura Scans").SetImportance(60).SaveX(ctx)
	comic := client.SeriesProvider.Create().SetSeries(s).
		SetProvider("comic-asura").SetProviderName("Comic Asura").SetImportance(40).SaveX(ctx)

	// Asura's feed stops at the whole chapters; only Comic Asura republishes "5.1".
	for _, key := range []string{"5", "6"} {
		client.ProviderChapter.Create().SetSeriesProviderID(asura.ID).SetChapterKey(key).SaveX(ctx)
	}
	for _, key := range []string{"5", "6", "5.1"} {
		client.ProviderChapter.Create().SetSeriesProviderID(comic.ID).SetChapterKey(key).SaveX(ctx)
	}

	five1 := 5.1
	client.Chapter.Create().SetSeries(s).SetChapterKey("5.1").SetNillableNumber(&five1).
		SetState(entchapter.StateDownloading).SaveX(ctx)
	client.Chapter.Create().SetSeries(s).SetChapterKey("6").SetNumber(6).
		SetState(entchapter.StateDownloaded).
		SetSatisfiedByProviderID(comic.ID).SetSatisfiedImportance(comic.Importance).
		SetFilename("[comic-asura][en] Solo Leveling 006.cbz").SaveX(ctx)
	client.Chapter.Create().SetSeries(s).SetChapterKey("9").SetNumber(9).
		SetState(entchapter.StateWanted).SaveX(ctx)

	return sourceTruth{asuraID: asura.ID, comicID: comic.ID}
}

// truthStates is every state the source-truth seed spans, so ONE List call
// surfaces all three rows.
var truthStates = []entchapter.State{
	entchapter.StateWanted,
	entchapter.StateDownloading,
	entchapter.StateDownloaded,
}

// listSourceTruth seeds the production shape and returns the single page of rows.
func listSourceTruth(ctx context.Context, t *testing.T, client *ent.Client) []downloads.DownloadChapterDTO {
	t.Helper()
	seedSourceTruth(ctx, t, client)
	res, err := downloads.NewService(client).List(ctx, downloads.ListFilter{States: truthStates, Limit: 50})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return res.Items
}

// TestListUnsatisfiedChapterNamesTheSourceThatCarriesIt is the production bug,
// verbatim: chapter 5.1 was rendered "Asura Scans" (the series' top source) while
// the engine was really fetching it from Comic Asura — Asura's feed has no such
// key. A chapter with no satisfier must name the highest-importance source whose
// FEED CARRIES its key, which is the scheduler's own primary-source rule.
func TestListUnsatisfiedChapterNamesTheSourceThatCarriesIt(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	items := listSourceTruth(ctx, t, client)

	row, ok := itemByKey(items, "5.1")
	if !ok {
		t.Fatal("missing chapter 5.1")
	}
	if row.ProviderName != "Comic Asura" {
		t.Errorf("5.1 providerName = %q, want the source that actually carries it, %q",
			row.ProviderName, "Comic Asura")
	}
	if row.Provider != "comic-asura" {
		t.Errorf("5.1 provider = %q, want %q", row.Provider, "comic-asura")
	}
}

// TestListSatisfiedChapterStillNamesItsSatisfier pins that true provenance wins:
// a downloaded chapter keeps reporting the source it actually came from, even
// though a HIGHER-importance source also carries the key.
func TestListSatisfiedChapterStillNamesItsSatisfier(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	items := listSourceTruth(ctx, t, client)

	row, ok := itemByKey(items, "6")
	if !ok {
		t.Fatal("missing chapter 6")
	}
	if row.ProviderName != "Comic Asura" {
		t.Errorf("6 providerName = %q, want the satisfying source %q (provenance beats the feed ranking)",
			row.ProviderName, "Comic Asura")
	}
	if row.Provider != "comic-asura" {
		t.Errorf("6 provider = %q, want %q", row.Provider, "comic-asura")
	}
}

// TestListSourcelessChapterNamesNoSource: a wanted chapter whose key NO feed
// carries reports "" — nothing is fetching it, and the engine skips it every cycle
// (handleNoCandidates → download.skip, stays wanted). Naming the series' top source
// would repeat the very lie this fix removes; the empty value renders as an
// em-dash and usefully surfaces the sourceless-stuck chapter.
func TestListSourcelessChapterNamesNoSource(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	items := listSourceTruth(ctx, t, client)

	row, ok := itemByKey(items, "9")
	if !ok {
		t.Fatal("missing chapter 9")
	}
	if row.Provider != "" || row.ProviderName != "" {
		t.Errorf("9 provider = %q/%q, want empty — no feed carries this chapter",
			row.Provider, row.ProviderName)
	}
}

// TestListDownloadedChapterWithClearedSatisfierNamesNoSource pins the honest
// consequence of the cleared-satisfier case: series.RemoveProvider nulls
// satisfied_by BY DESIGN (keeping the watermark and the CBZ), so a DOWNLOADED
// chapter can have no stored provenance. The row then answers "who supplies this
// chapter now" — and when no remaining feed carries the key, that is nobody: the
// a-3 fixture is downloaded, has no satisfier, and no provider feed lists "a-3",
// so both source fields are empty (the UI renders an em-dash). It must NOT invent
// the series' top source, which is exactly what it used to do.
func TestListDownloadedChapterWithClearedSatisfierNamesNoSource(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seedLibrary(ctx, t, client)

	res, err := downloads.NewService(client).List(ctx, downloads.ListFilter{
		States: []entchapter.State{entchapter.StateDownloaded}, Limit: 50,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	row, ok := itemByKey(res.Items, "a-3")
	if !ok {
		t.Fatal("missing chapter a-3")
	}
	if row.Provider != "" || row.ProviderName != "" {
		t.Errorf("a-3 provider = %q/%q, want empty — its satisfier is cleared and no feed carries the key",
			row.Provider, row.ProviderName)
	}
}

// tiedFeedIDs are the two ProviderChapter ids the tie-break test pins. They are
// EXPLICIT (not the random default) so the ordering under test is deterministic:
// the FIRST-created provider gets the HIGH id, the second the LOW one. The engine's
// tiebreak is ProviderChapter.ID.String() ASC, so the SECOND provider must win —
// the opposite of the insertion order an unsorted tie would fall back to.
var (
	tiedHighFeedID = uuid.MustParse("ffffffff-ffff-4fff-8fff-ffffffffffff")
	tiedLowFeedID  = uuid.MustParse("00000000-0000-4000-8000-000000000000")
)

// TestListEqualImportanceTieBreaksLikeTheEngine pins that the row's source matches
// the engine's pick when two sources carry the same key at the SAME importance —
// which is routine (disk.Reconcile gives every disk-origin provider importance 1).
// chapter.RankedLiveCandidates orders candidates importance DESC, then
// ProviderChapter.ID.String() ASC, and the scheduler takes cands[0]; the read model
// must order identically. Without the ProviderChapter.ID tiebreak the index would
// fall back to the order Postgres returned the providers in (the batch load has no
// ORDER BY) — here that is the first-created provider, "Tied High", so this test
// fails the moment the secondary key is dropped. It also asserts STABILITY: the same
// source is named on every List call.
func TestListEqualImportanceTieBreaksLikeTheEngine(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()

	s := client.Series.Create().SetTitle("Tie Saga").SetSlug("tie-saga").
		SetCategoryID(catID(ctx, client, "Manga")).SaveX(ctx)

	// Both sources rank EQUALLY — only the feed-row id can break the tie.
	high := client.SeriesProvider.Create().SetSeries(s).
		SetProvider("tied-high").SetProviderName("Tied High").SetImportance(50).SaveX(ctx)
	low := client.SeriesProvider.Create().SetSeries(s).
		SetProvider("tied-low").SetProviderName("Tied Low").SetImportance(50).SaveX(ctx)

	client.ProviderChapter.Create().SetID(tiedHighFeedID).
		SetSeriesProviderID(high.ID).SetChapterKey("t-1").SaveX(ctx)
	client.ProviderChapter.Create().SetID(tiedLowFeedID).
		SetSeriesProviderID(low.ID).SetChapterKey("t-1").SaveX(ctx)

	client.Chapter.Create().SetSeries(s).SetChapterKey("t-1").SetNumber(1).
		SetState(entchapter.StateDownloading).SaveX(ctx)

	svc := downloads.NewService(client)
	for i := range 3 { // repeated calls must name the SAME source — no request-to-request drift
		res, err := svc.List(ctx, downloads.ListFilter{
			States: []entchapter.State{entchapter.StateDownloading}, Limit: 50,
		})
		if err != nil {
			t.Fatalf("List (call %d): %v", i+1, err)
		}
		row, ok := itemByKey(res.Items, "t-1")
		if !ok {
			t.Fatalf("call %d: missing chapter t-1", i+1)
		}
		if row.Provider != "tied-low" || row.ProviderName != "Tied Low" {
			t.Errorf("call %d: t-1 provider = %q/%q, want tied-low/Tied Low — the engine breaks an "+
				"importance tie by ProviderChapter.ID ASC, and tied-low holds the lower feed-row id",
				i+1, row.Provider, row.ProviderName)
		}
	}
}
