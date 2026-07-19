package library_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entcategory "github.com/technobecet/tsundoku/internal/ent/category"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/ingest"
	"github.com/technobecet/tsundoku/internal/library"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sse"
)

// ---- consolidation test fixtures -------------------------------------------
//
// These reuse the shared library_test helpers (writeCBZ, chapterKeyOf,
// disk.GenerateCBZFilename, newFakeClientWithChapters, assertNoUpgradesFlagged,
// waitForMergeEvent) so the consolidation tests never re-invent disk/DB setup.

// consolidateMaxChapter drives filename zero-padding across a fixture; it MUST be
// the series-wide max so a post-fold relabel pads identically to the disk files.
const consolidateMaxChapter = 6.0

// newConsolidateSeries creates a fresh series linked to the testdb-seeded "Manga"
// category so its on-disk folder is <storage>/Manga/<title> (where the fixtures
// write CBZs and where the fold relabels them).
func newConsolidateSeries(t *testing.T, client *ent.Client, title string) *ent.Series {
	t.Helper()
	ctx := context.Background()
	mangaID := client.Category.Query().Where(entcategory.Name("Manga")).OnlyX(ctx).ID
	// Slug MUST equal disk.Slugify(title): the match-to-source arm re-ingests by
	// title, and ingest find-or-creates the Series by that slug — a mismatch would
	// attach the source to a DIFFERENT series and orphan the fixture.
	return client.Series.Create().
		SetTitle(title).
		SetSlug(disk.Slugify(title)).
		SetCategoryID(mangaID).
		SaveX(ctx)
}

// diskChapterMeta builds the on-disk RenderMeta for one chapter of a disk-origin
// provider named providerName (a display NAME, so IsLinkedProvider is false), so
// the fixture filename matches what a fold will rename FROM.
func diskChapterMeta(title, providerName string, number float64) disk.RenderMeta {
	n, mc := number, consolidateMaxChapter
	return disk.RenderMeta{
		Provider:      providerName,
		ProviderLabel: providerName,
		Scanlator:     "",
		Language:      "en",
		SeriesTitle:   title,
		Category:      disk.CategoryManga,
		Number:        &n,
		MaxChapter:    &mc,
		ChapterKey:    chapterKeyOf(number),
	}
}

// createDiskProvider creates one UNLINKED disk-origin SeriesProvider (provider = a
// display NAME, importance 1, NO ProviderChapter feed) with a downloaded Chapter +
// real CBZ on disk for each (key, number) pair. This is the exact shape
// disk.Reconcile produces for a Kaizoku-imported group.
func createDiskProvider(t *testing.T, client *ent.Client, storage, title, providerName string, chapters map[string]float64) *ent.SeriesProvider {
	t.Helper()
	ctx := context.Background()
	ser := client.Series.Query().Where(entseries.Title(title)).OnlyX(ctx)

	sp := client.SeriesProvider.Create().
		SetSeriesID(ser.ID).
		SetProvider(providerName). // non-numeric ⇒ unlinked disk-origin
		SetScanlator("").
		SetLanguage("en").
		SetImportance(1).
		SaveX(ctx)

	for key, num := range chapters {
		filename := disk.GenerateCBZFilename(diskChapterMeta(title, providerName, num))
		writeCBZ(t, storage, title, filename)
		n := num
		client.Chapter.Create().
			SetSeriesID(ser.ID).
			SetChapterKey(key).
			SetNumber(n).
			SetState(entchapter.StateDownloaded).
			SetFilename(filename).
			SetSatisfiedByProviderID(sp.ID).
			SetSatisfiedImportance(1).
			SaveX(ctx)
	}
	return sp
}

// createLiveTarget creates a LINKED live SeriesProvider (provider = numeric source
// id, so IsLinkedProvider is true) with a ProviderChapter feed covering keys — the
// existing-provider consolidation target (Ranker's real QiScans). It creates NO
// Chapter rows (those come from the disk providers being folded in).
func createLiveTarget(t *testing.T, client *ent.Client, title, sourceID, displayName string, importance int, keyed map[string]float64) *ent.SeriesProvider {
	t.Helper()
	ctx := context.Background()
	ser := client.Series.Query().Where(entseries.Title(title)).OnlyX(ctx)

	sp := client.SeriesProvider.Create().
		SetSeriesID(ser.ID).
		SetProvider(sourceID). // numeric ⇒ linked live source
		SetProviderName(displayName).
		SetScanlator("").
		SetLanguage("en").
		SetImportance(importance).
		SaveX(ctx)

	for key, num := range keyed {
		n := num
		client.ProviderChapter.Create().
			SetSeriesProviderID(sp.ID).
			SetChapterKey(key).
			SetNumber(n).
			SetName("").
			SaveX(ctx)
	}
	return sp
}

// assertChaptersSatisfiedBy fails unless every key's chapter is re-pointed onto
// survivor and its (relabeled) CBZ exists on disk. wantLabel, when non-empty, must
// appear in the filename; forbidLabel, when non-empty, must NOT. Extracted so each
// consolidation test stays within the fleet cyclop budget.
func assertChaptersSatisfiedBy(t *testing.T, client *ent.Client, storage, title string, survivor uuid.UUID, keys []string, wantLabel, forbidLabel string) {
	t.Helper()
	ctx := context.Background()
	ser := client.Series.Query().Where(entseries.Title(title)).OnlyX(ctx)
	for _, key := range keys {
		ch := client.Chapter.Query().Where(entchapter.SeriesID(ser.ID), entchapter.ChapterKey(key)).OnlyX(ctx)
		if ch.SatisfiedByProviderID == nil || *ch.SatisfiedByProviderID != survivor {
			t.Errorf("chapter %s satisfied_by = %v, want survivor %s", key, ch.SatisfiedByProviderID, survivor)
		}
		if wantLabel != "" && !strings.Contains(ch.Filename, wantLabel) {
			t.Errorf("chapter %s filename = %q, want it to contain %q", key, ch.Filename, wantLabel)
		}
		if forbidLabel != "" && strings.Contains(ch.Filename, forbidLabel) {
			t.Errorf("chapter %s filename = %q, still carries %q", key, ch.Filename, forbidLabel)
		}
		if _, err := os.Stat(filepath.Join(storage, "Manga", title, ch.Filename)); err != nil {
			t.Errorf("chapter %s CBZ missing after fold: %v", key, err)
		}
	}
}

// countCBZ returns how many .cbz files exist in the series dir — the anti-loss
// yardstick (a silent overwrite drops the count below the chapter count).
func countCBZ(t *testing.T, storage, title string) int {
	t.Helper()
	dir := filepath.Join(storage, "Manga", title)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read series dir: %v", err)
	}
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".cbz") {
			n++
		}
	}
	return n
}

// TestConsolidateProviders_FoldIntoExistingProvider is the Ranker acceptance
// proof: fold three disk providers (disjoint chapters) into the ONE real linked
// provider already on the series. Result: one survivor, every chapter re-pointed
// onto it, every CBZ relabeled to its identity, the disk rows drained + deleted,
// no CBZ lost, and NO upgrade flagged (no re-download).
func TestConsolidateProviders_FoldIntoExistingProvider(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()
	const title = "Ranker"

	newConsolidateSeries(t, client, title)
	target := createLiveTarget(t, client, title, "1", "QiScans", 30, map[string]float64{
		"1": 1, "2": 2, "3": 3, "4": 4, "5": 5, "6": 6,
	})
	d1 := createDiskProvider(t, client, storage, title, "QiScans.old1", map[string]float64{"1": 1, "2": 2})
	d2 := createDiskProvider(t, client, storage, title, "QiScans.old2", map[string]float64{"3": 3, "4": 4})
	d3 := createDiskProvider(t, client, storage, title, "QiScans.old3", map[string]float64{"5": 5, "6": 6})

	svc := library.NewService(client, nil, nil, series.NewService(client, storage, 14), func() {}, storage, sse.NewHub())
	ser := client.Series.Query().Where(entseries.Title(title)).OnlyX(ctx)

	res, err := svc.ConsolidateProviders(ctx, ser.ID, []uuid.UUID{d1.ID, d2.ID, d3.ID}, library.ConsolidateTarget{
		ExistingProviderID: &target.ID,
	})
	if err != nil {
		t.Fatalf("ConsolidateProviders: %v", err)
	}
	if res.Merged != 3 || len(res.Skipped) != 0 {
		t.Fatalf("result = merged %d skipped %d, want merged 3 skipped 0 (%+v)", res.Merged, len(res.Skipped), res.Skipped)
	}

	// One provider row left — the survivor (target).
	rows := client.SeriesProvider.Query().Where(entseriesprovider.SeriesID(ser.ID)).AllX(ctx)
	if len(rows) != 1 || rows[0].ID != target.ID {
		t.Fatalf("providers = %d, want 1 (the target survivor)", len(rows))
	}

	// Every chapter re-pointed onto the survivor + relabeled to [QiScans].
	assertChaptersSatisfiedBy(t, client, storage, title, target.ID, []string{"1", "2", "3", "4", "5", "6"}, "[QiScans]", "")

	// No CBZ lost: exactly one file per chapter (never-auto-delete — relabeled, not deleted).
	if n := countCBZ(t, storage, title); n != 6 {
		t.Errorf("CBZ file count = %d, want 6 (relabeled, none lost)", n)
	}
	assertNoUpgradesFlagged(t, ctx, client)
}

// TestConsolidateProviders_MatchToSourceTarget is the KaliScan acceptance proof:
// fold three domain-named disk providers into a NEW match-to-real-source target
// (the live KaliScan). The source is attached, becomes the survivor, and every
// disk chapter is relabeled onto it — no re-download.
func TestConsolidateProviders_MatchToSourceTarget(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()
	const title = "KaliScan Series"

	newConsolidateSeries(t, client, title)
	io := createDiskProvider(t, client, storage, title, "kaliscan.io", map[string]float64{"1": 1, "2": 2})
	me := createDiskProvider(t, client, storage, title, "kaliscan.me", map[string]float64{"3": 3, "4": 4})
	com := createDiskProvider(t, client, storage, title, "kaliscan.com", map[string]float64{"5": 5, "6": 6})

	// The fake source offers all six keys, so attachRealSource ingests a feed that
	// covers every disk chapter (so each gets re-pointed, not left behind).
	fake := newFakeClientWithChapters(t, sixChapterFeed())
	ingestSvc := ingest.NewIngest(fake, client)
	svc := library.NewService(client, ingestSvc, nil, series.NewService(client, storage, 14), func() {}, storage, sse.NewHub())
	ser := client.Series.Query().Where(entseries.Title(title)).OnlyX(ctx)

	res, err := svc.ConsolidateProviders(ctx, ser.ID, []uuid.UUID{io.ID, me.ID, com.ID}, library.ConsolidateTarget{
		Source:     "1",
		URL:        "/manga/9",
		Scanlator:  "",
		Importance: 20,
	})
	if err != nil {
		t.Fatalf("ConsolidateProviders: %v", err)
	}
	if res.Merged != 3 || len(res.Skipped) != 0 {
		t.Fatalf("result = merged %d skipped %d, want merged 3 skipped 0 (%+v)", res.Merged, len(res.Skipped), res.Skipped)
	}

	// Exactly one provider remains — the live source ("1"), and every disk row is gone.
	rows := client.SeriesProvider.Query().Where(entseriesprovider.SeriesID(ser.ID)).AllX(ctx)
	if len(rows) != 1 {
		t.Fatalf("providers = %d, want 1 (the live KaliScan survivor)", len(rows))
	}
	survivor := rows[0]
	if !series.IsLinkedProvider(survivor) {
		t.Fatalf("survivor provider = %q, want a linked live source", survivor.Provider)
	}

	// Every chapter is re-pointed onto the live survivor; no [kaliscan.*] file remains.
	assertChaptersSatisfiedBy(t, client, storage, title, survivor.ID, []string{"1", "2", "3", "4", "5", "6"}, "", "kaliscan.")
	if n := countCBZ(t, storage, title); n != 6 {
		t.Errorf("CBZ file count = %d, want 6 (relabeled, none lost)", n)
	}
	assertNoUpgradesFlagged(t, ctx, client)
}

// TestConsolidateProviders_SerialSameNumberGuardsCollision proves the collision
// guard makes a same-number / distinct-key fold NON-DESTRUCTIVE.
//
// This input is IMPOSSIBLE in production: chapter.NormalizeChapterKey maps a given
// number to ONE key, so a series has exactly one Chapter row (and one target
// filename) per number. It is forged here — two Chapter rows both number 3 with
// distinct keys ("3"/"3-b"), each satisfied by a different disk provider, both in
// the target feed — to model a legacy/hand-edited DB or a future ingest bug.
//
// Folding A relabels chapter "3" → [Real] … 3.cbz. Folding B computes the
// IDENTICAL filename (it derives from the NUMBER, not the key) whose destination
// now exists AND whose source still exists — a genuine collision. WITHOUT the
// guard os.Rename would silently OVERWRITE and destroy A's archive; WITH it,
// disk.ErrRelabelCollision is raised, B's whole fold is skipped (rolled back,
// nothing overwritten), and it is reported in result.Skipped. BOTH archives
// survive — a destructive op must never silently destroy a CBZ.
func TestConsolidateProviders_SerialSameNumberGuardsCollision(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()
	const title = "Same Number"

	newConsolidateSeries(t, client, title)
	// Target feed offers BOTH keys, both number 3 — so both are eligible for fold.
	target := createLiveTarget(t, client, title, "1", "Real", 30, map[string]float64{"3": 3, "3-b": 3})
	a := createDiskProvider(t, client, storage, title, "src.io", map[string]float64{"3": 3})
	b := createDiskProvider(t, client, storage, title, "src.me", map[string]float64{"3-b": 3})
	aFile := chapterFilename(t, client, ctx, title, "3")
	bFile := chapterFilename(t, client, ctx, title, "3-b")

	svc := library.NewService(client, nil, nil, series.NewService(client, storage, 14), func() {}, storage, sse.NewHub())
	ser := client.Series.Query().Where(entseries.Title(title)).OnlyX(ctx)

	res, err := svc.ConsolidateProviders(ctx, ser.ID, []uuid.UUID{a.ID, b.ID}, library.ConsolidateTarget{
		ExistingProviderID: &target.ID,
	})
	if err != nil {
		t.Fatalf("ConsolidateProviders (serial same-number): %v", err)
	}

	// A folded; B's fold was refused by the collision guard and reported skipped.
	if res.Merged != 1 {
		t.Fatalf("merged = %d, want 1 (A folded; B collision-skipped)", res.Merged)
	}
	assertOneSkip(t, res, b.ID, "collision")

	// NO silent destruction: BOTH archives still exist. A's archive is now at the
	// survivor name (relabeled); B's original file is untouched (fold rolled back).
	if n := countCBZ(t, storage, title); n != 2 {
		t.Fatalf("CBZ file count = %d, want 2 (nothing overwritten/destroyed)", n)
	}
	survivorFile := chapterFilename(t, client, ctx, title, "3") // A, now at [Real] … 3.cbz
	dir := filepath.Join(storage, "Manga", title)
	assertFilePresentInDir(t, dir, survivorFile)
	assertFilePresentInDir(t, dir, bFile) // B's original, intact
	if survivorFile == aFile {
		t.Errorf("chapter 3 filename unchanged (%q) — expected it relabeled to the survivor", aFile)
	}

	// Chapter "3" folded onto the survivor; the skipped disk provider survives intact.
	assertChapterSatisfaction(t, client, ctx, ser.ID, "3", &target.ID, 30)
	if n := client.SeriesProvider.Query().Where(entseriesprovider.IDEQ(b.ID)).CountX(ctx); n != 1 {
		t.Errorf("skipped disk provider %s = %d, want 1 (never folded, never deleted)", b.ID, n)
	}
}

// assertOneSkip fails unless res reports exactly one skipped provider == wantID
// whose reason contains reasonSubstr.
func assertOneSkip(t *testing.T, res library.ConsolidateResult, wantID uuid.UUID, reasonSubstr string) {
	t.Helper()
	if len(res.Skipped) != 1 || res.Skipped[0].ProviderID != wantID {
		t.Fatalf("skipped = %+v, want [%s]", res.Skipped, wantID)
	}
	if !strings.Contains(res.Skipped[0].Reason, reasonSubstr) {
		t.Errorf("skip reason = %q, want it to contain %q", res.Skipped[0].Reason, reasonSubstr)
	}
}

// chapterFilename returns the current DB filename of one (title, key) chapter.
func chapterFilename(t *testing.T, client *ent.Client, ctx context.Context, title, key string) string {
	t.Helper()
	ser := client.Series.Query().Where(entseries.Title(title)).OnlyX(ctx)
	return client.Chapter.Query().Where(entchapter.SeriesID(ser.ID), entchapter.ChapterKey(key)).OnlyX(ctx).Filename
}

// TestConsolidateProviders_FaultIsolation proves a bad provider in the set is
// reported skipped and does not abort the rest: a non-existent id and a linked
// live source (not the target) are both skipped, while the one real disk provider
// is folded successfully.
func TestConsolidateProviders_FaultIsolation(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()
	const title = "Fault Isolation"

	newConsolidateSeries(t, client, title)
	target := createLiveTarget(t, client, title, "1", "Real", 30, map[string]float64{"1": 1, "2": 2})
	good := createDiskProvider(t, client, storage, title, "old.disk", map[string]float64{"1": 1, "2": 2})
	// A second LINKED provider (numeric) — putting it in the merge set must be
	// skipped ("not a disk-origin provider"), never folded.
	linkedOther := client.SeriesProvider.Create().
		SetSeriesID(client.Series.Query().Where(entseries.Title(title)).OnlyX(ctx).ID).
		SetProvider("7").SetProviderName("Other").SetImportance(20).SaveX(ctx)
	bogus := uuid.New() // not a provider on this series

	svc := library.NewService(client, nil, nil, series.NewService(client, storage, 14), func() {}, storage, sse.NewHub())
	ser := client.Series.Query().Where(entseries.Title(title)).OnlyX(ctx)

	res, err := svc.ConsolidateProviders(ctx, ser.ID, []uuid.UUID{good.ID, bogus, linkedOther.ID}, library.ConsolidateTarget{
		ExistingProviderID: &target.ID,
	})
	if err != nil {
		t.Fatalf("ConsolidateProviders: %v", err)
	}
	if res.Merged != 1 {
		t.Fatalf("merged = %d, want 1 (only the good disk provider)", res.Merged)
	}
	if len(res.Skipped) != 2 {
		t.Fatalf("skipped = %d, want 2 (bogus id + linked provider) — %+v", len(res.Skipped), res.Skipped)
	}
	// The good disk provider is gone; the linked "other" survives (never folded).
	if n := client.SeriesProvider.Query().Where(entseriesprovider.IDEQ(good.ID)).CountX(ctx); n != 0 {
		t.Errorf("good disk provider still present, want folded away")
	}
	if n := client.SeriesProvider.Query().Where(entseriesprovider.IDEQ(linkedOther.ID)).CountX(ctx); n != 1 {
		t.Errorf("linked 'other' provider = %d, want 1 (skipped, not folded)", n)
	}
}

// TestConsolidateProviders_TargetNoFeedRejected proves the guard: an existing
// target with an EMPTY feed is rejected (ErrTargetNoFeed) — merging into it would
// orphan the disk chapters.
func TestConsolidateProviders_TargetNoFeedRejected(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()
	const title = "No Feed Target"

	newConsolidateSeries(t, client, title)
	// A linked target WITHOUT any ProviderChapter feed.
	emptyTarget := client.SeriesProvider.Create().
		SetSeriesID(client.Series.Query().Where(entseries.Title(title)).OnlyX(ctx).ID).
		SetProvider("1").SetProviderName("Empty").SetImportance(30).SaveX(ctx)
	d1 := createDiskProvider(t, client, storage, title, "old.disk", map[string]float64{"1": 1})

	svc := library.NewService(client, nil, nil, series.NewService(client, storage, 14), func() {}, storage, sse.NewHub())
	ser := client.Series.Query().Where(entseries.Title(title)).OnlyX(ctx)

	_, err := svc.ConsolidateProviders(ctx, ser.ID, []uuid.UUID{d1.ID}, library.ConsolidateTarget{
		ExistingProviderID: &emptyTarget.ID,
	})
	if err == nil || !isErr(err, library.ErrTargetNoFeed) {
		t.Fatalf("want ErrTargetNoFeed, got %v", err)
	}
	// Nothing changed — the disk provider is untouched.
	if n := client.SeriesProvider.Query().Where(entseriesprovider.IDEQ(d1.ID)).CountX(ctx); n != 1 {
		t.Errorf("disk provider = %d, want 1 (unchanged — feedless target rejected)", n)
	}
}

// TestConsolidateProviders_UnknownSeriesAndTarget covers the hard sentinels: an
// unknown series id → ErrSeriesNotFound; an existing-target id not on the series →
// ErrProviderNotInSeries.
func TestConsolidateProviders_UnknownSeriesAndTarget(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()
	const title = "Unknown Guards"

	newConsolidateSeries(t, client, title)
	target := createLiveTarget(t, client, title, "1", "Real", 30, map[string]float64{"1": 1})
	d1 := createDiskProvider(t, client, storage, title, "old.disk", map[string]float64{"1": 1})
	svc := library.NewService(client, nil, nil, series.NewService(client, storage, 14), func() {}, storage, sse.NewHub())
	ser := client.Series.Query().Where(entseries.Title(title)).OnlyX(ctx)

	if _, err := svc.ConsolidateProviders(ctx, uuid.New(), []uuid.UUID{d1.ID}, library.ConsolidateTarget{ExistingProviderID: &target.ID}); !isErr(err, library.ErrSeriesNotFound) {
		t.Fatalf("unknown series: want ErrSeriesNotFound, got %v", err)
	}
	strayID := uuid.New()
	if _, err := svc.ConsolidateProviders(ctx, ser.ID, []uuid.UUID{d1.ID}, library.ConsolidateTarget{ExistingProviderID: &strayID}); !isErr(err, library.ErrProviderNotInSeries) {
		t.Fatalf("unknown target: want ErrProviderNotInSeries, got %v", err)
	}
}

// TestConsolidateProviders_IdempotentReRun proves a plain re-run after a
// successful consolidation is safe: the already-folded providers are now "not in
// series" (skipped), nothing else changes, no error.
func TestConsolidateProviders_IdempotentReRun(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()
	const title = "Idempotent"

	newConsolidateSeries(t, client, title)
	target := createLiveTarget(t, client, title, "1", "Real", 30, map[string]float64{"1": 1, "2": 2})
	d1 := createDiskProvider(t, client, storage, title, "old.disk", map[string]float64{"1": 1, "2": 2})
	svc := library.NewService(client, nil, nil, series.NewService(client, storage, 14), func() {}, storage, sse.NewHub())
	ser := client.Series.Query().Where(entseries.Title(title)).OnlyX(ctx)

	if _, err := svc.ConsolidateProviders(ctx, ser.ID, []uuid.UUID{d1.ID}, library.ConsolidateTarget{ExistingProviderID: &target.ID}); err != nil {
		t.Fatalf("first ConsolidateProviders: %v", err)
	}
	// Re-run with the SAME (now-deleted) merge id — it must be skipped, not fatal.
	res, err := svc.ConsolidateProviders(ctx, ser.ID, []uuid.UUID{d1.ID}, library.ConsolidateTarget{ExistingProviderID: &target.ID})
	if err != nil {
		t.Fatalf("re-run ConsolidateProviders: %v", err)
	}
	if res.Merged != 0 || len(res.Skipped) != 1 {
		t.Fatalf("re-run = merged %d skipped %d, want merged 0 skipped 1 (already merged away)", res.Merged, len(res.Skipped))
	}
	if n := client.SeriesProvider.Query().Where(entseriesprovider.SeriesID(ser.ID)).CountX(ctx); n != 1 {
		t.Errorf("providers = %d, want 1 (unchanged on re-run)", n)
	}
}

// TestConsolidateProviders_MatchToSourceZeroFoldsNoElevate proves SHOULD-FIX 3:
// a match-to-source consolidation where EVERY selected provider is skipped (here a
// linked provider that isn't disk-origin) attaches the source but must NOT elevate
// it — leaving it PARKED at importance 0 so the existing disk chapters (importance
// 1) are never outranked and re-downloaded. Non-vacuous: elevating to 20 (>1)
// would make DetectUpgrades flag both disk chapters.
func TestConsolidateProviders_MatchToSourceZeroFoldsNoElevate(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()
	const title = "Zero Fold"

	newConsolidateSeries(t, client, title)
	d := createDiskProvider(t, client, storage, title, "old.disk", map[string]float64{"1": 1, "2": 2})
	ser := client.Series.Query().Where(entseries.Title(title)).OnlyX(ctx)
	// A LINKED provider — putting it in the merge set is skipped (not disk-origin),
	// so NO fold succeeds.
	linked := client.SeriesProvider.Create().
		SetSeriesID(ser.ID).SetProvider("7").SetProviderName("Linked").SetImportance(20).SaveX(ctx)

	fake := newFakeClientWithChapters(t, []sourceengine.Chapter{
		{URL: "/ch/1", Name: "Chapter 1", Number: 1},
		{URL: "/ch/2", Name: "Chapter 2", Number: 2},
	})
	ingestSvc := ingest.NewIngest(fake, client)
	svc := library.NewService(client, ingestSvc, nil, series.NewService(client, storage, 14), func() {}, storage, sse.NewHub())

	res, err := svc.ConsolidateProviders(ctx, ser.ID, []uuid.UUID{linked.ID}, library.ConsolidateTarget{
		Source:     "1",
		URL:        "/manga/9",
		Scanlator:  "",
		Importance: 20,
	})
	if err != nil {
		t.Fatalf("ConsolidateProviders: %v", err)
	}
	if res.Merged != 0 || len(res.Skipped) != 1 {
		t.Fatalf("result = merged %d skipped %d, want merged 0 skipped 1", res.Merged, len(res.Skipped))
	}

	// The attached source is left PARKED at 0, never elevated to 20.
	attached := client.SeriesProvider.Query().
		Where(entseriesprovider.SeriesID(ser.ID), entseriesprovider.Provider("1")).OnlyX(ctx)
	if attached.Importance != 0 {
		t.Errorf("attached source importance = %d, want 0 (never elevated on a zero-fold consolidation)", attached.Importance)
	}
	// The disk chapters are untouched — and no upgrade is flagged (no re-download).
	assertChapterSatisfaction(t, client, ctx, ser.ID, "1", &d.ID, 1)
	assertChapterSatisfaction(t, client, ctx, ser.ID, "2", &d.ID, 1)
	assertNoUpgradesFlagged(t, ctx, client)
}

// TestMerge_MatchAndConsolidateMutuallyExclusivePerSeries proves SHOULD-FIX 2: a
// Match and a Consolidation cannot run concurrently on the SAME series (the shared
// per-series merge latch). A match held in flight makes a consolidation start for
// the same series return false (409), and the guard clears once the match completes.
func TestMerge_MatchAndConsolidateMutuallyExclusivePerSeries(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	const title = "Mutual Exclusion"

	newConsolidateSeries(t, client, title)
	d1 := createDiskProvider(t, client, storage, title, "old.disk", map[string]float64{"1": 1, "2": 2})

	hub := sse.NewHub()
	fake := newFakeClientWithFeed(t) // 2-chapter feed keyed "1"/"2", matching d1
	ingestSvc := ingest.NewIngest(fake, client)
	svc := library.NewService(client, ingestSvc, nil, series.NewService(client, storage, 14), func() {}, storage, hub)
	ser := client.Series.Query().Where(entseries.Title(title)).OnlyX(context.Background())

	events, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	// Hold the match in flight so the consolidation deterministically observes the latch.
	block := make(chan struct{})
	restore := library.SetMatchBlock(block)
	defer restore()

	ctx := context.Background()
	if !svc.StartMatchDiskProvider(ctx, ser.ID, d1.ID, "1", "/manga/9", "", 5) {
		t.Fatal("StartMatchDiskProvider = false, want true")
	}
	// A consolidation for the SAME series is refused while the match is in flight.
	if svc.StartConsolidateProviders(ctx, ser.ID, []uuid.UUID{d1.ID}, library.ConsolidateTarget{Source: "1", URL: "/manga/9", Importance: 20}) {
		t.Fatal("StartConsolidateProviders = true, want false (a match is in flight for this series)")
	}

	// Release the match; it completes and clears the shared latch.
	close(block)
	waitForMergeEvent(t, events, ser.ID.String())
}

// TestStartConsolidateProviders_DetachedCompletesAfterRequestCancel is the
// disconnect-proof proof: the consolidation runs on a context DETACHED from the
// request (context.WithoutCancel), so cancelling the request context the instant
// the 202 is returned must NOT abort it — it still runs to completion.
func TestStartConsolidateProviders_DetachedCompletesAfterRequestCancel(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	const title = "Detached"

	newConsolidateSeries(t, client, title)
	target := createLiveTarget(t, client, title, "1", "Real", 30, map[string]float64{"1": 1, "2": 2})
	d1 := createDiskProvider(t, client, storage, title, "old.disk", map[string]float64{"1": 1, "2": 2})

	hub := sse.NewHub()
	svc := library.NewService(client, nil, nil, series.NewService(client, storage, 14), func() {}, storage, hub)
	ser := client.Series.Query().Where(entseries.Title(title)).OnlyX(context.Background())

	events, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	reqCtx, cancel := context.WithCancel(context.Background())
	if !svc.StartConsolidateProviders(reqCtx, ser.ID, []uuid.UUID{d1.ID}, library.ConsolidateTarget{ExistingProviderID: &target.ID}) {
		t.Fatal("StartConsolidateProviders = false, want true")
	}
	cancel() // client disconnects immediately after the 202.

	waitForMergeEvent(t, events, ser.ID.String())

	bg := context.Background()
	if n := client.SeriesProvider.Query().Where(entseriesprovider.IDEQ(d1.ID)).CountX(bg); n != 0 {
		t.Fatalf("disk provider still present, want folded (consolidation completed despite cancel)")
	}
}

// TestStartConsolidateProviders_SingleFlightGuard proves a second start for the
// SAME series while one is in flight is refused (single-flight guard keyed by
// series), and the guard clears once the first completes.
func TestStartConsolidateProviders_SingleFlightGuard(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	const title = "Single Flight"

	newConsolidateSeries(t, client, title)
	target := createLiveTarget(t, client, title, "1", "Real", 30, map[string]float64{"1": 1})
	d1 := createDiskProvider(t, client, storage, title, "old.disk", map[string]float64{"1": 1})

	hub := sse.NewHub()
	svc := library.NewService(client, nil, nil, series.NewService(client, storage, 14), func() {}, storage, hub)
	ser := client.Series.Query().Where(entseries.Title(title)).OnlyX(context.Background())

	events, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	block := make(chan struct{})
	restore := library.SetConsolidateBlock(block)
	defer restore()

	ctx := context.Background()
	tgt := library.ConsolidateTarget{ExistingProviderID: &target.ID}
	if !svc.StartConsolidateProviders(ctx, ser.ID, []uuid.UUID{d1.ID}, tgt) {
		t.Fatal("first start = false, want true")
	}
	if svc.StartConsolidateProviders(ctx, ser.ID, []uuid.UUID{d1.ID}, tgt) {
		t.Fatal("second concurrent start = true, want false (single-flight guard)")
	}

	close(block)
	waitForMergeEvent(t, events, ser.ID.String())
	if n := client.SeriesProvider.Query().Where(entseriesprovider.IDEQ(d1.ID)).CountX(ctx); n != 0 {
		t.Fatalf("disk provider still present, want folded (first consolidation completed)")
	}
}

// sixChapterFeed is the fake engine-host feed for the match-to-source test: six
// chapters numbered 1..6 (keys "1".."6") so attachRealSource ingests a feed
// covering every disk chapter.
func sixChapterFeed() []sourceengine.Chapter {
	return []sourceengine.Chapter{
		{URL: "/ch/1", Name: "Chapter 1", Number: 1},
		{URL: "/ch/2", Name: "Chapter 2", Number: 2},
		{URL: "/ch/3", Name: "Chapter 3", Number: 3},
		{URL: "/ch/4", Name: "Chapter 4", Number: 4},
		{URL: "/ch/5", Name: "Chapter 5", Number: 5},
		{URL: "/ch/6", Name: "Chapter 6", Number: 6},
	}
}

// isErr is a tiny errors.Is alias keeping the sentinel assertions terse.
func isErr(err, target error) bool { return errors.Is(err, target) }
