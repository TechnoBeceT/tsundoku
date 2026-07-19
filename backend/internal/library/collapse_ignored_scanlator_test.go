package library_test

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entcategory "github.com/technobecet/tsundoku/internal/ent/category"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/library"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sse"
)

// collapseTestSource is the numeric engine-host source id used across the Slice-B
// collapse tests (a "Hive Scans"-style uploader-in-scanlator source).
const collapseTestSource int64 = 1

// newCollapseService builds a library.Service for the collapse migration tests.
// The migration needs only the Ent client + storage root (it reuses the
// provider-merge/CBZ-relabel machinery, not the engine client), so ingest is nil.
func newCollapseService(client *ent.Client, storage string) *library.Service {
	return library.NewService(client, nil, nil, series.NewService(client, storage, 14), func() {}, storage, sse.NewHub())
}

// uploaderMeta builds the disk.RenderMeta for one chapter of a per-uploader
// provider ("[Hive Scans-<scanlator>] …") of the shared collapse-test source, so
// the on-disk filename matches exactly what the migration will rename FROM.
func uploaderMeta(title, scanlator string, number, maxChapter float64) disk.RenderMeta {
	n, mc := number, maxChapter
	return disk.RenderMeta{
		Provider:      "1",
		ProviderLabel: "Hive Scans",
		Scanlator:     scanlator,
		Language:      "en",
		SeriesTitle:   title,
		Category:      disk.CategoryManga,
		Number:        &n,
		MaxChapter:    &mc,
		ChapterKey:    chapterKeyOf(number),
	}
}

// collapsedFilename returns the filename a chapter must carry AFTER the collapse:
// the same identity with the scanlator dropped ("[Hive Scans] …").
func collapsedFilename(title string, number, maxChapter float64) string {
	m := uploaderMeta(title, "", number, maxChapter)
	return disk.GenerateCBZFilename(m)
}

// writeCBZ writes a minimal but valid CBZ (one page + a ComicInfo.xml) at
// <storage>/Manga/<title>/<filename>. The bytes never matter to the migration
// (it only renames + rewrites ComicInfo), only that the file exists and is a
// readable zip.
func writeCBZ(t *testing.T, storage, title, filename string) {
	t.Helper()
	dir := filepath.Join(storage, "Manga", title)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	page, _ := zw.Create("001.jpg")
	_, _ = page.Write([]byte{0xFF, 0xD8, 0xFF, 0xD9})
	ci, _ := zw.Create("ComicInfo.xml")
	_, _ = ci.Write([]byte(`<?xml version="1.0"?><ComicInfo><Series>` + title + `</Series></ComicInfo>`))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// createUploaderProvider creates one linked per-uploader SeriesProvider of the
// shared collapse-test source (provider="1", provider_name "Hive Scans",
// scanlator, importance), with a ProviderChapter feed AND downloaded Chapter rows
// (satisfied_by this provider) for each number, and writes each chapter's real
// CBZ under the uploader identity. maxChapter drives filename zero-padding and
// MUST be the series-wide max so the post-collapse relabel pads identically.
func createUploaderProvider(t *testing.T, client *ent.Client, storage, title, scanlator string, importance int, numbers []float64, maxChapter float64) *ent.SeriesProvider {
	t.Helper()
	ctx := context.Background()
	ser := client.Series.Query().Where(entseries.Title(title)).OnlyX(ctx)

	sp := client.SeriesProvider.Create().
		SetSeriesID(ser.ID).
		SetProvider("1").
		SetProviderName("Hive Scans").
		SetScanlator(scanlator).
		SetLanguage("en").
		SetImportance(importance).
		SaveX(ctx)

	for _, num := range numbers {
		key := chapterKeyOf(num)
		n := num
		client.ProviderChapter.Create().
			SetSeriesProviderID(sp.ID).
			SetChapterKey(key).
			SetNumber(n).
			SetName("").
			SaveX(ctx)

		filename := disk.GenerateCBZFilename(uploaderMeta(title, scanlator, num, maxChapter))
		writeCBZ(t, storage, title, filename)

		client.Chapter.Create().
			SetSeriesID(ser.ID).
			SetChapterKey(key).
			SetNumber(n).
			SetState(entchapter.StateDownloaded).
			SetFilename(filename).
			SetSatisfiedByProviderID(sp.ID).
			SetSatisfiedImportance(importance).
			SaveX(ctx)
	}
	return sp
}

// chapterKeyOf is the canonical chapter_key for a numbered chapter — exactly what
// chapter.NormalizeChapterKey derives, reused so the fixture keys match ingest.
func chapterKeyOf(n float64) string {
	return chapter.FormatChapterNumber(n)
}

// newIgnoreFlaggedSeries creates a fresh series titled title, linked to the
// testdb-seeded "Manga" category so its on-disk folder is <storage>/Manga/<title>
// (matching where createUploaderProvider writes the CBZs, and where the migration
// relabels them). No providers yet — createUploaderProvider adds them.
func newIgnoreFlaggedSeries(t *testing.T, client *ent.Client, title string) *ent.Series {
	t.Helper()
	ctx := context.Background()
	mangaID := client.Category.Query().Where(entcategory.Name("Manga")).OnlyX(ctx).ID
	return client.Series.Create().
		SetTitle(title).
		SetSlug("collapse-" + title).
		SetCategoryID(mangaID).
		SaveX(ctx)
}

// TestCollapseIgnoredScanlator_MergesUploadersAndRelabels is the core Slice-B
// proof: a series adopted with TWO per-uploader providers of one flagged source
// ([Hive Scans-Admin] ch1/2, [Hive Scans-Aero] ch3/4) collapses to ONE
// scanlator="" provider carrying all four chapters, every CBZ is renamed
// [Hive Scans-Uploader] → [Hive Scans] on disk, the drained rows are gone,
// importances land on the survivor, and NO upgrade is flagged (no re-download).
func TestCollapseIgnoredScanlator_MergesUploadersAndRelabels(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()
	const title = "Flagged Series"

	newIgnoreFlaggedSeries(t, client, title)
	const maxCh = 4.0
	createUploaderProvider(t, client, storage, title, "Admin", 20, []float64{1, 2}, maxCh)
	createUploaderProvider(t, client, storage, title, "Aero", 30, []float64{3, 4}, maxCh)
	ser := client.Series.Query().OnlyX(ctx)

	svc := newCollapseService(client, storage)
	sp, merged, skipped, err := svc.CollapseIgnoredScanlatorSource(ctx, collapseTestSource)
	if err != nil {
		t.Fatalf("CollapseIgnoredScanlatorSource: %v", err)
	}
	if sp != 1 || merged != 2 || skipped != 0 {
		t.Fatalf("summary = (seriesProcessed=%d, merged=%d, skipped=%d), want (1, 2, 0)", sp, merged, skipped)
	}

	// Exactly one provider row for the source, scanlator "" at the max importance.
	rows := client.SeriesProvider.Query().Where(entseriesprovider.Provider("1")).AllX(ctx)
	if len(rows) != 1 {
		t.Fatalf("source provider rows = %d, want 1 (collapsed)", len(rows))
	}
	survivor := rows[0]
	if survivor.Scanlator != "" {
		t.Errorf("survivor scanlator = %q, want \"\"", survivor.Scanlator)
	}
	if survivor.Importance != 30 {
		t.Errorf("survivor importance = %d, want 30 (max of folded rows)", survivor.Importance)
	}

	// Every chapter is re-pointed onto the survivor at importance 30, downloaded,
	// and its CBZ renamed to the [Hive Scans] identity on disk (old name gone).
	assertCollapsedChapters(t, client, storage, ser.ID, survivor.ID, title, []float64{1, 2, 3, 4}, maxCh, 30)

	// The survivor's feed is the union of all four chapter keys.
	feed := client.ProviderChapter.Query().Where(entproviderchapter.SeriesProviderID(survivor.ID)).CountX(ctx)
	if feed != 4 {
		t.Errorf("survivor feed rows = %d, want 4 (union)", feed)
	}

	assertNoUpgradesFlagged(t, ctx, client)
}

// assertCollapsedChapters checks that every numbered chapter is downloaded,
// satisfied by the survivor at wantImportance, carries the [Source] (no-scanlator)
// filename, and that file exists on disk under the old per-uploader name is gone.
func assertCollapsedChapters(t *testing.T, client *ent.Client, storage string, seriesID, survivorID uuid.UUID, title string, numbers []float64, maxCh float64, wantImportance int) {
	t.Helper()
	ctx := context.Background()
	for _, num := range numbers {
		key := chapterKeyOf(num)
		assertChapterSatisfaction(t, client, ctx, seriesID, key, &survivorID, wantImportance)
		wantFile := collapsedFilename(title, num, maxCh)
		ch := client.Chapter.Query().Where(entchapter.SeriesID(seriesID), entchapter.ChapterKey(key)).OnlyX(ctx)
		if ch.Filename != wantFile {
			t.Errorf("chapter %s filename = %q, want %q", key, ch.Filename, wantFile)
		}
		if !fileExists(t, storage, title, wantFile) {
			t.Errorf("collapsed CBZ %q missing on disk", wantFile)
		}
	}
}

// TestCollapseIgnoredScanlator_Idempotent proves a second run over an
// already-collapsed source is a no-op: merged=0, skipped=0, still one provider.
func TestCollapseIgnoredScanlator_Idempotent(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()
	const title = "Flagged Series"

	newIgnoreFlaggedSeries(t, client, title)
	const maxCh = 2.0
	createUploaderProvider(t, client, storage, title, "Admin", 20, []float64{1, 2}, maxCh)

	svc := newCollapseService(client, storage)
	if _, merged, _, err := svc.CollapseIgnoredScanlatorSource(ctx, collapseTestSource); err != nil || merged != 1 {
		t.Fatalf("first run: merged=%d err=%v, want merged=1 err=nil", merged, err)
	}

	sp, merged, skipped, err := svc.CollapseIgnoredScanlatorSource(ctx, collapseTestSource)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if sp != 0 || merged != 0 || skipped != 0 {
		t.Fatalf("second run summary = (%d, %d, %d), want (0, 0, 0) (idempotent no-op)", sp, merged, skipped)
	}
	if n := client.SeriesProvider.Query().Where(entseriesprovider.Provider("1")).CountX(ctx); n != 1 {
		t.Fatalf("source provider rows = %d, want 1 (still collapsed)", n)
	}
}

// TestCollapseIgnoredScanlator_SingleScanlatorRowCollapsesToBlank proves a source
// with a lone per-uploader row (no second uploader) still collapses: the single
// [Hive Scans-Admin] provider becomes one [Hive Scans] provider and its CBZ is
// relabeled — post-flag, even one uploader row is "wrong" and must be fixed.
func TestCollapseIgnoredScanlator_SingleScanlatorRowCollapsesToBlank(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()
	const title = "Flagged Series"

	newIgnoreFlaggedSeries(t, client, title)
	const maxCh = 2.0
	createUploaderProvider(t, client, storage, title, "Admin", 20, []float64{1, 2}, maxCh)
	ser := client.Series.Query().OnlyX(ctx)

	svc := newCollapseService(client, storage)
	_, merged, _, err := svc.CollapseIgnoredScanlatorSource(ctx, collapseTestSource)
	if err != nil {
		t.Fatalf("CollapseIgnoredScanlatorSource: %v", err)
	}
	if merged != 1 {
		t.Fatalf("merged = %d, want 1", merged)
	}
	rows := client.SeriesProvider.Query().Where(entseriesprovider.Provider("1")).AllX(ctx)
	if len(rows) != 1 || rows[0].Scanlator != "" {
		t.Fatalf("want one collapsed \"\" provider, got %d rows (scanlator %q)", len(rows), rows[0].Scanlator)
	}
	for _, num := range []float64{1, 2} {
		if !fileExists(t, storage, title, collapsedFilename(title, num, maxCh)) {
			t.Errorf("collapsed CBZ for %v missing", num)
		}
	}
	assertChapterSatisfaction(t, client, ctx, ser.ID, chapterKeyOf(1), &rows[0].ID, 20)
}

// TestCollapseIgnoredScanlator_AlreadyBlankIsNoOp proves a source already at
// scanlator="" (nothing to collapse) is untouched: merged=0.
func TestCollapseIgnoredScanlator_AlreadyBlankIsNoOp(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()
	const title = "Flagged Series"

	newIgnoreFlaggedSeries(t, client, title)
	// A single scanlator="" provider (already collapsed shape).
	createUploaderProvider(t, client, storage, title, "", 20, []float64{1, 2}, 2.0)

	svc := newCollapseService(client, storage)
	sp, merged, skipped, err := svc.CollapseIgnoredScanlatorSource(ctx, collapseTestSource)
	if err != nil {
		t.Fatalf("CollapseIgnoredScanlatorSource: %v", err)
	}
	if sp != 0 || merged != 0 || skipped != 0 {
		t.Fatalf("summary = (%d, %d, %d), want (0, 0, 0)", sp, merged, skipped)
	}
	if n := client.SeriesProvider.Query().Where(entseriesprovider.Provider("1")).CountX(ctx); n != 1 {
		t.Fatalf("provider rows = %d, want 1 (untouched)", n)
	}
}

// TestCollapseIgnoredScanlator_RollbackOnRelabelFailure is the destructive-safety
// proof: with a single uploader whose CBZ is corrupted (so RelabelChapterFile
// fails), the collapse of that series fails and is SKIPPED, and the series is
// left byte-for-byte unchanged — the fresh survivor is cleaned up, the uploader
// row and its chapters survive under their ORIGINAL identity, and no upgrade is
// flagged.
func TestCollapseIgnoredScanlator_RollbackOnRelabelFailure(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()
	const title = "Flagged Series"

	newIgnoreFlaggedSeries(t, client, title)
	const maxCh = 2.0
	up := createUploaderProvider(t, client, storage, title, "Admin", 20, []float64{1, 2}, maxCh)
	ser := client.Series.Query().OnlyX(ctx)

	// Corrupt chapter 1's CBZ so RelabelChapterFile fails during the fold.
	ch1 := client.Chapter.Query().Where(entchapter.SeriesID(ser.ID), entchapter.ChapterKey(chapterKeyOf(1))).OnlyX(ctx)
	corruptCBZ(t, storage, title, ch1.Filename)

	svc := newCollapseService(client, storage)
	sp, merged, skipped, err := svc.CollapseIgnoredScanlatorSource(ctx, collapseTestSource)
	if err != nil {
		t.Fatalf("CollapseIgnoredScanlatorSource returned a hard error, want per-series skip: %v", err)
	}
	if sp != 0 || merged != 0 || skipped != 1 {
		t.Fatalf("summary = (%d, %d, %d), want (0, 0, 1) (series skipped)", sp, merged, skipped)
	}

	// The series is unchanged: the uploader row survives, no orphan "" survivor
	// was left behind, and the chapters are still satisfied by the uploader.
	rows := client.SeriesProvider.Query().Where(entseriesprovider.Provider("1")).AllX(ctx)
	if len(rows) != 1 || rows[0].ID != up.ID || rows[0].Scanlator != "Admin" {
		t.Fatalf("want the ORIGINAL uploader row to survive, got %d rows: %+v", len(rows), rows)
	}
	assertChapterSatisfaction(t, client, ctx, ser.ID, chapterKeyOf(2), &up.ID, 20)
	assertNoUpgradesFlagged(t, ctx, client)
}

// TestCollapseIgnoredScanlator_SkipsUnaffectedSeries proves the sweep is scoped:
// a series that carries a DIFFERENT source is untouched, and a series with the
// flagged source is collapsed — the summary reflects only the affected series.
func TestCollapseIgnoredScanlator_SkipsUnaffectedSeries(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()

	// Series A carries the flagged source (id "1") with two uploaders.
	newIgnoreFlaggedSeries(t, client, "Flagged Series")
	const maxCh = 2.0
	createUploaderProvider(t, client, storage, "Flagged Series", "Admin", 20, []float64{1}, maxCh)
	createUploaderProvider(t, client, storage, "Flagged Series", "Aero", 30, []float64{2}, maxCh)

	// Series B carries an UNRELATED source (id "9") — must be left alone.
	serB := client.Series.Create().SetTitle("Other Series").SetSlug("other").SaveX(ctx)
	client.SeriesProvider.Create().SetSeriesID(serB.ID).SetProvider("9").SetProviderName("Other").SetScanlator("Grp").SetImportance(10).SaveX(ctx)

	svc := newCollapseService(client, storage)
	sp, merged, skipped, err := svc.CollapseIgnoredScanlatorSource(ctx, collapseTestSource)
	if err != nil {
		t.Fatalf("CollapseIgnoredScanlatorSource: %v", err)
	}
	if sp != 1 || merged != 2 || skipped != 0 {
		t.Fatalf("summary = (%d, %d, %d), want (1, 2, 0)", sp, merged, skipped)
	}
	// Series B's unrelated provider is untouched (still scanlator "Grp").
	other := client.SeriesProvider.Query().Where(entseriesprovider.Provider("9")).OnlyX(ctx)
	if other.Scanlator != "Grp" {
		t.Errorf("unrelated source scanlator = %q, want \"Grp\" (untouched)", other.Scanlator)
	}
}

// fileExists reports whether <storage>/Manga/<title>/<filename> exists.
func fileExists(t *testing.T, storage, title, filename string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(storage, "Manga", title, filename))
	return err == nil
}

// corruptCBZ overwrites a chapter's CBZ with non-zip bytes so RelabelChapterFile
// (which rewrites the embedded ComicInfo) fails.
func corruptCBZ(t *testing.T, storage, title, filename string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(storage, "Manga", title, filename), []byte("not a zip"), 0o600); err != nil {
		t.Fatal(err)
	}
}
