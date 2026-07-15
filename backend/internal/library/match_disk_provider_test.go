package library_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/ingest"
	"github.com/technobecet/tsundoku/internal/library"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sse"
)

// setupMatchFixture writes a 2-chapter Kaizoku-style disk series, imports it
// disk-only (satisfied_importance=1, disk provider suwayomi_id=0), and
// returns the series row + the disk-origin SeriesProvider row it created —
// the shared starting point for every MatchDiskProvider test.
func setupMatchFixture(t *testing.T, client *ent.Client, storage string) (*ent.Series, *ent.SeriesProvider) {
	t.Helper()
	writeKaizokuSeries(t, storage, "Manga", "My Series", "mangadex", "Alpha", 2)
	ctx := context.Background()

	facts, err := diskScanFirst(t, storage)
	if err != nil {
		t.Fatalf("diskScanFirst: %v", err)
	}
	importOneFromFacts(t, client, facts)

	ser := client.Series.Query().OnlyX(ctx)
	diskSP := client.SeriesProvider.Query().Where(seriesprovider.SeriesID(ser.ID)).OnlyX(ctx)
	return ser, diskSP
}

// newMatchService builds a library.Service wired for MatchDiskProvider tests:
// a fake engine-host client (via newFakeClientWithFeed/newFakeClientWithChapters)
// backing a real ingest.Ingest, and a real series.Service.
func newMatchService(client *ent.Client, storage string, fake sourceengine.Client) *library.Service {
	ingestSvc := ingest.NewIngest(fake, client)
	seriesSvc := series.NewService(client, storage, 14)
	return library.NewService(client, ingestSvc, nil, seriesSvc, func() {}, storage, sse.NewHub())
}

// TestMatchDiskProvider_RepointsChaptersNoUpgradeFlagged is THE no-redownload
// proof (the whole point of Match): after matching the disk group to a real
// source that offers the SAME two chapter keys, both chapters are re-pointed
// onto the new provider at its importance, and — critically —
// download.DetectUpgrades flags ZERO chapters for them (satisfied_importance
// now equals the new provider's importance, so the strict `>` upgrade gate
// never fires and neither chapter is ever re-downloaded).
func TestMatchDiskProvider_RepointsChaptersNoUpgradeFlagged(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()

	ser, diskSP := setupMatchFixture(t, client, storage)
	fake := newFakeClientWithFeed(t) // 2 chapters keyed "1"/"2", matching the disk fixture
	svc := newMatchService(client, storage, fake)

	dto, err := svc.MatchDiskProvider(ctx, ser.ID, diskSP.ID, "1", "/manga/99", "", 5)
	if err != nil {
		t.Fatalf("MatchDiskProvider: %v", err)
	}
	if len(dto.Providers) != 1 {
		t.Fatalf("providers = %d, want 1 (disk provider deleted, only the new source remains)", len(dto.Providers))
	}

	newSP := client.SeriesProvider.Query().Where(seriesprovider.SeriesID(ser.ID)).OnlyX(ctx)
	if newSP.Provider != "1" || newSP.Importance != 5 {
		t.Fatalf("new provider = %+v, want provider=1 importance=5", newSP)
	}

	for _, key := range []string{"1", "2"} {
		assertChapterSatisfaction(t, client, ctx, ser.ID, key, &newSP.ID, 5)
	}

	assertNoUpgradesFlagged(t, ctx, client)
}

// assertChapterSatisfaction fails the test unless the chapter (series, key) is
// state=downloaded with the given satisfied_by (nil means "want nil") and
// satisfied_importance.
func assertChapterSatisfaction(t *testing.T, client *ent.Client, ctx context.Context, seriesID uuid.UUID, key string, wantSatisfiedBy *uuid.UUID, wantImportance int) {
	t.Helper()
	ch := client.Chapter.Query().Where(chapter.SeriesID(seriesID), chapter.ChapterKey(key)).OnlyX(ctx)
	if ch.State != chapter.StateDownloaded {
		t.Errorf("chapter %s state = %s, want downloaded", key, ch.State)
	}
	switch {
	case wantSatisfiedBy == nil:
		if ch.SatisfiedByProviderID != nil {
			t.Errorf("chapter %s satisfied_by = %v, want nil", key, ch.SatisfiedByProviderID)
		}
	case ch.SatisfiedByProviderID == nil || *ch.SatisfiedByProviderID != *wantSatisfiedBy:
		t.Errorf("chapter %s satisfied_by = %v, want %s", key, ch.SatisfiedByProviderID, *wantSatisfiedBy)
	}
	if ch.SatisfiedImportance == nil || *ch.SatisfiedImportance != wantImportance {
		t.Errorf("chapter %s satisfied_importance = %v, want %d", key, ch.SatisfiedImportance, wantImportance)
	}
}

// assertNoUpgradesFlagged is the shared no-redownload proof: DetectUpgrades
// must flag zero chapters and zero rows may sit in upgrade_available.
func assertNoUpgradesFlagged(t *testing.T, ctx context.Context, client *ent.Client) {
	t.Helper()
	n, err := download.DetectUpgrades(ctx, client, 3)
	if err != nil {
		t.Fatalf("DetectUpgrades: %v", err)
	}
	if n != 0 {
		t.Fatalf("DetectUpgrades flagged %d chapters, want 0 (Match must never trigger a re-download)", n)
	}
	up := client.Chapter.Query().Where(chapter.StateEQ(chapter.StateUpgradeAvailable)).CountX(ctx)
	if up != 0 {
		t.Fatalf("upgrade_available count = %d, want 0", up)
	}
}

// TestMatchDiskProvider_ParkedProviderNoUpgradeDuringWindow directly proves
// the crux of the fix, independent of the Match orchestration: a real provider
// carrying a FULL chapter feed but PARKED at importance 0 (the state Match
// holds throughout its disk-relabel window, and the state it leaves on
// rollback) must NOT trip DetectUpgrades against disk chapters at
// satisfied_importance 1. The test is non-vacuous: bumping that same provider
// to importance 5 immediately makes DetectUpgrades flag both chapters, proving
// the feed is genuinely upgrade-capable and it is ONLY the importance-0 parking
// that holds the re-download off.
func TestMatchDiskProvider_ParkedProviderNoUpgradeDuringWindow(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()

	ser := client.Series.Create().SetTitle("Park Test").SetSlug("park-test").SaveX(ctx)

	// Disk-origin provider: importance 1, no ProviderChapter feed (mirrors a
	// reconcile-imported group).
	diskSP := client.SeriesProvider.Create().
		SetSeriesID(ser.ID).SetProvider("mangadex").SetScanlator("Alpha").SetImportance(1).SaveX(ctx)

	// New real provider PARKED at importance 0, WITH a full feed for keys 1 & 2.
	newSP := client.SeriesProvider.Create().
		SetSeriesID(ser.ID).SetProvider("weeb").SetSuwayomiID(42).SetImportance(0).SaveX(ctx)
	one, two := 1.0, 2.0
	client.ProviderChapter.Create().SetSeriesProviderID(newSP.ID).SetChapterKey("1").SetNumber(one).SaveX(ctx)
	client.ProviderChapter.Create().SetSeriesProviderID(newSP.ID).SetChapterKey("2").SetNumber(two).SaveX(ctx)

	// Two downloaded chapters satisfied by the disk provider at importance 1.
	for _, key := range []string{"1", "2"} {
		n := one
		if key == "2" {
			n = two
		}
		client.Chapter.Create().SetSeriesID(ser.ID).SetChapterKey(key).SetNumber(n).
			SetState("downloaded").SetSatisfiedByProviderID(diskSP.ID).SetSatisfiedImportance(1).SaveX(ctx)
	}

	// Parked at 0 (0 <= 1) → nothing flagged.
	assertNoUpgradesFlagged(t, ctx, client)

	// Non-vacuous: the SAME feed at importance 5 (5 > 1) flags BOTH chapters,
	// proving the park is what held the re-download off.
	client.SeriesProvider.UpdateOneID(newSP.ID).SetImportance(5).ExecX(ctx)
	n, err := download.DetectUpgrades(ctx, client, 3)
	if err != nil {
		t.Fatalf("DetectUpgrades (bumped): %v", err)
	}
	if n != 2 {
		t.Fatalf("DetectUpgrades after bump to importance 5 = %d, want 2 (feed is upgrade-capable)", n)
	}
}

// TestMatchDiskProvider_RenamesCBZAndDeletesDiskProvider proves the on-disk
// side effects: each matched chapter's CBZ is renamed to the new source's
// clean identity (and its ComicInfo rewritten to match), and the disk-origin
// SeriesProvider row is gone afterwards.
func TestMatchDiskProvider_RenamesCBZAndDeletesDiskProvider(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()

	ser, diskSP := setupMatchFixture(t, client, storage)
	oldFilenames := map[string]string{}
	for _, key := range []string{"1", "2"} {
		ch := client.Chapter.Query().Where(chapter.SeriesID(ser.ID), chapter.ChapterKey(key)).OnlyX(ctx)
		oldFilenames[key] = ch.Filename
	}

	fake := newFakeClientWithFeed(t)
	svc := newMatchService(client, storage, fake)

	if _, err := svc.MatchDiskProvider(ctx, ser.ID, diskSP.ID, "1", "/manga/99", "", 5); err != nil {
		t.Fatalf("MatchDiskProvider: %v", err)
	}

	seriesDir := filepath.Join(storage, "Manga", "My Series")
	for key, oldName := range oldFilenames {
		ch := client.Chapter.Query().Where(chapter.SeriesID(ser.ID), chapter.ChapterKey(key)).OnlyX(ctx)
		if ch.Filename == oldName {
			t.Errorf("chapter %s filename unchanged (%q) — expected a rename to the new source's identity", key, oldName)
		}
		if _, err := os.Stat(filepath.Join(seriesDir, oldName)); !os.IsNotExist(err) {
			t.Errorf("old file %q still present on disk after Match", oldName)
		}
		if _, err := os.Stat(filepath.Join(seriesDir, ch.Filename)); err != nil {
			t.Errorf("new file %q missing on disk after Match: %v", ch.Filename, err)
		}
	}

	if n := client.SeriesProvider.Query().Where(seriesprovider.IDEQ(diskSP.ID)).CountX(ctx); n != 0 {
		t.Errorf("disk provider row count = %d, want 0 (deleted after Match)", n)
	}
}

// TestMatchDiskProvider_PartialOverlapKeepsLeftoverChapterSafe covers the
// "source lacks a disk chapter" case: the new source only offers chapter key
// "1", so chapter "2" is NOT re-pointed — it keeps satisfied_importance=1 but
// has its satisfied_by cleared (the disk provider it pointed at is deleted),
// and DetectUpgrades must still flag ZERO chapters (no live source is better
// than importance 1 for a chapter no live source even offers... but here
// chapter 2 now has NO ProviderChapter feed at all after the disk provider is
// removed, so it simply has no upgrade candidate — safe).
func TestMatchDiskProvider_PartialOverlapKeepsLeftoverChapterSafe(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()

	ser, diskSP := setupMatchFixture(t, client, storage)

	fake := newFakeClientWithChapters(t, []sourceengine.Chapter{
		{URL: "/ch/1", Name: "Chapter 1", Number: 1},
	})
	svc := newMatchService(client, storage, fake)

	dto, err := svc.MatchDiskProvider(ctx, ser.ID, diskSP.ID, "1", "/manga/99", "", 5)
	if err != nil {
		t.Fatalf("MatchDiskProvider: %v", err)
	}
	if len(dto.Providers) != 1 {
		t.Fatalf("providers = %d, want 1 (disk provider deleted)", len(dto.Providers))
	}

	newSP := client.SeriesProvider.Query().Where(seriesprovider.SeriesID(ser.ID)).OnlyX(ctx)

	// Chapter 1 (the overlap) is re-pointed onto the new source.
	assertChapterSatisfaction(t, client, ctx, ser.ID, "1", &newSP.ID, 5)
	// Chapter 2 (outside the overlap) keeps its importance-1 watermark but its
	// satisfied_by is cleared (the disk provider it pointed at is deleted) —
	// never left dangling.
	assertChapterSatisfaction(t, client, ctx, ser.ID, "2", nil, 1)

	assertNoUpgradesFlagged(t, ctx, client)
}

// TestMatchDiskProvider_NewChaptersEnterWanted proves that a chapter_key the
// new source offers but the disk group never had lands as a NEW Chapter row
// in state=wanted (normal ingest behavior — Match does not suppress
// discovery of genuinely new chapters).
func TestMatchDiskProvider_NewChaptersEnterWanted(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()

	ser, diskSP := setupMatchFixture(t, client, storage)

	fake := newFakeClientWithChapters(t, []sourceengine.Chapter{
		{URL: "/ch/1", Name: "Chapter 1", Number: 1},
		{URL: "/ch/2", Name: "Chapter 2", Number: 2},
		{URL: "/ch/3", Name: "Chapter 3", Number: 3},
	})
	svc := newMatchService(client, storage, fake)

	if _, err := svc.MatchDiskProvider(ctx, ser.ID, diskSP.ID, "1", "/manga/99", "", 5); err != nil {
		t.Fatalf("MatchDiskProvider: %v", err)
	}

	ch3 := client.Chapter.Query().Where(chapter.SeriesID(ser.ID), chapter.ChapterKey("3")).OnlyX(ctx)
	if ch3.State != chapter.StateWanted {
		t.Errorf("chapter 3 state = %s, want wanted (a genuinely new chapter)", ch3.State)
	}
}

// TestMatchDiskProvider_NotADiskProviderRejected asserts the guard: matching
// against a SeriesProvider that is already a real, linked source
// (suwayomi_id != 0) is rejected with ErrNotADiskProvider — Match only
// operates on unlinked disk-origin groups.
func TestMatchDiskProvider_NotADiskProviderRejected(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()

	ser, _ := setupMatchFixture(t, client, storage)
	linked := client.SeriesProvider.Create().
		SetSeriesID(ser.ID).
		SetProvider("already-real").
		SetSuwayomiID(7).
		SetImportance(9).
		SaveX(ctx)

	fake := newFakeClientWithFeed(t)
	svc := newMatchService(client, storage, fake)

	_, err := svc.MatchDiskProvider(ctx, ser.ID, linked.ID, "1", "/manga/99", "", 5)
	if !errors.Is(err, library.ErrNotADiskProvider) {
		t.Fatalf("want ErrNotADiskProvider, got %v", err)
	}
}

// TestMatchDiskProvider_UnknownSeriesAndProviderErrors covers the remaining
// sentinel guards: an unknown series id yields ErrSeriesNotFound, and a
// provider id that does not belong to the given series yields
// ErrProviderNotInSeries.
func TestMatchDiskProvider_UnknownSeriesAndProviderErrors(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()

	ser, diskSP := setupMatchFixture(t, client, storage)
	fake := newFakeClientWithFeed(t)
	svc := newMatchService(client, storage, fake)

	if _, err := svc.MatchDiskProvider(ctx, uuid.New(), diskSP.ID, "1", "/manga/99", "", 5); !errors.Is(err, library.ErrSeriesNotFound) {
		t.Fatalf("want ErrSeriesNotFound, got %v", err)
	}
	if _, err := svc.MatchDiskProvider(ctx, ser.ID, uuid.New(), "1", "/manga/99", "", 5); !errors.Is(err, library.ErrProviderNotInSeries) {
		t.Fatalf("want ErrProviderNotInSeries, got %v", err)
	}
}

// TestMatchDiskProvider_RollsBackOnMidBatchDiskFailure is the rollback proof
// (plan Task 3, failure test (f)): chapters are relabeled in chapter-number
// order (1 then 2). Corrupting chapter 2's on-disk CBZ so its relabel fails
// AFTER chapter 1's relabel already succeeded must roll chapter 1 all the way
// back — original filename, original ComicInfo, original DB row untouched —
// and the DB tx must never run at all (disk provider still present, chapter 2
// untouched). A non-nil error from Match must mean NO net change.
func TestMatchDiskProvider_RollsBackOnMidBatchDiskFailure(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()

	ser, diskSP := setupMatchFixture(t, client, storage)

	ch1Before := client.Chapter.Query().Where(chapter.SeriesID(ser.ID), chapter.ChapterKey("1")).OnlyX(ctx)
	ch2Before := client.Chapter.Query().Where(chapter.SeriesID(ser.ID), chapter.ChapterKey("2")).OnlyX(ctx)
	seriesDir := filepath.Join(storage, "Manga", "My Series")
	ch1Path := filepath.Join(seriesDir, ch1Before.Filename)
	ch2Path := filepath.Join(seriesDir, ch2Before.Filename)

	// Corrupt chapter 2's CBZ (not a valid zip) so RelabelChapterFile fails at
	// the ComicInfo-rewrite step for it, AFTER chapter 1 (lower number,
	// processed first) has already been relabeled successfully.
	if err := os.WriteFile(ch2Path, []byte("not a zip file"), 0o600); err != nil {
		t.Fatalf("corrupt chapter 2 file: %v", err)
	}

	fake := newFakeClientWithFeed(t)
	svc := newMatchService(client, storage, fake)

	_, err := svc.MatchDiskProvider(ctx, ser.ID, diskSP.ID, "1", "/manga/99", "", 5)
	if err == nil {
		t.Fatal("MatchDiskProvider: want an error from the corrupted chapter 2 file, got nil")
	}

	// Chapter 1 must be back exactly where it started: same filename, same
	// page images, same ComicInfo provenance, same DB row (untouched — the DB
	// tx never ran). The embedded ComicInfo.xml is re-serialised on restore
	// (via UpdateCBZComicInfo), so this checks LOGICAL equality (fields +
	// image bytes), not a byte-identical zip container.
	if _, statErr := os.Stat(ch1Path); statErr != nil {
		t.Errorf("chapter 1 original file %q missing after rollback: %v", ch1Path, statErr)
	}
	// The ORIGINAL Kaizoku-written CBZ predates Tsundoku's own render pipeline,
	// so its provenance lives in the standard Publisher/Translator ComicInfo
	// fields (see writeKaizokuSeries / disk.kaizokuProvenance) — NOT the
	// Tsundoku-only Provider/Scanlator/Importance extension fields, which were
	// never set on it. Restoring "the original" correctly reproduces THAT
	// state (Publisher=mangadex/Translator=Alpha, extensions still empty).
	assertRestoredComicInfo(t, ch1Path, "mangadex", "Alpha")

	ch1DB := client.Chapter.Query().Where(chapter.SeriesID(ser.ID), chapter.ChapterKey("1")).OnlyX(ctx)
	if ch1DB.Filename != ch1Before.Filename {
		t.Errorf("chapter 1 DB filename = %q, want unchanged %q", ch1DB.Filename, ch1Before.Filename)
	}
	assertChapterSatisfaction(t, client, ctx, ser.ID, "1", &diskSP.ID, 1)

	// The disk provider must still exist — the DB tx (which deletes it) never
	// ran, since the failure happened in the disk-first phase.
	if n := client.SeriesProvider.Query().Where(seriesprovider.IDEQ(diskSP.ID)).CountX(ctx); n != 1 {
		t.Errorf("disk provider row count = %d, want 1 (DB tx never ran)", n)
	}

	// Chapter 2's ComicInfo/sidecar were never touched by RelabelChapterFile
	// (it failed before writing either) — its DB row is untouched too.
	assertChapterSatisfaction(t, client, ctx, ser.ID, "2", &diskSP.ID, 1)

	// REGRESSION GUARD for the no-redownload-on-rollback bug: the request used
	// importance=5, but the new provider must have been left PARKED at 0 (never
	// elevated outside commitMatch's tx, which never ran). So DetectUpgrades
	// must flag ZERO — a failed Match never re-arms a re-download. With the old
	// code (new provider elevated to 5 up-front, left at 5 on rollback) this
	// returned 2 and would have re-downloaded the whole imported series with no
	// owner action. This same frozen state (new provider parked at 0 + feed
	// present, disk chapters at satisfied_importance 1) is exactly the transient
	// disk-relabel window, so this assertion also proves the window is safe.
	assertNoUpgradesFlagged(t, ctx, client)

	newSP := client.SeriesProvider.Query().Where(seriesprovider.Provider("1")).OnlyX(ctx)
	if newSP.Importance != 0 {
		t.Errorf("new provider importance = %d after rollback, want 0 (parked, never elevated)", newSP.Importance)
	}
}

// assertRestoredComicInfo fails the test unless path's embedded ComicInfo
// carries the given Publisher/Translator — used to prove a rollback restored
// the ORIGINAL (pre-Match) Kaizoku-era provenance (which predates Tsundoku's
// own Provider/Scanlator extension fields).
func assertRestoredComicInfo(t *testing.T, path, wantPublisher, wantTranslator string) {
	t.Helper()
	ci, err := disk.ReadComicInfoFromCBZ(path)
	if err != nil || ci == nil {
		t.Fatalf("ReadComicInfoFromCBZ(%q) after rollback: %v", path, err)
	}
	if ci.Publisher != wantPublisher || ci.Translator != wantTranslator {
		t.Fatalf("ComicInfo after rollback = %+v, want publisher=%q translator=%q", ci, wantPublisher, wantTranslator)
	}
}
