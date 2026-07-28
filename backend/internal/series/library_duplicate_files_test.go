package series_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/series"
)

// dupRowByID finds a library-duplicate-files row for a series id (nil when absent).
func dupRowByID(dto series.LibraryDuplicateFilesDTO, id uuid.UUID) *series.SeriesDuplicateFilesDTO {
	for i := range dto.Series {
		if dto.Series[i].SeriesID == id.String() {
			return &dto.Series[i]
		}
	}
	return nil
}

// writeExtraCBZ drops a loose CBZ of the given byte size into a series folder —
// a leftover file the owner's per-series "Remove duplicate files" would delete.
// Returns the byte size written so a test can assert reclaimable bytes exactly.
func writeExtraCBZ(t *testing.T, storage, title, name string, size int) int64 {
	t.Helper()
	dir := filepath.Join(storage, "Manga", title)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), make([]byte, size), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return int64(size)
}

// seedSeriesWithChapters creates a series with `count` downloaded whole chapters
// (numbers 1..count), each with its own winning CBZ on disk, satisfied by one
// source. The fixture is the baseline every duplicate-file case builds on.
func seedSeriesWithChapters(ctx context.Context, t *testing.T, db *ent.Client, storage, title, slug string, count int) cleanupFixture {
	t.Helper()
	s := db.Series.Create().SetTitle(title).SetSlug(slug).
		SetCategoryID(catID(ctx, db, "Manga")).SaveX(ctx)
	fx := cleanupFixture{storage: storage, series: s, providers: map[string]*ent.SeriesProvider{}}

	nums := make([]float64, 0, count)
	for i := 1; i <= count; i++ {
		nums = append(nums, float64(i))
	}
	fx.providers["comix"] = seedFeed(ctx, t, db, s.ID, "comix", 60, nums...)
	for _, n := range nums {
		seedDownloadedChapter(ctx, t, db, fx, chapterKeyOf(n), n, 90, fx.providers["comix"])
	}
	return fx
}

// TestLibraryDuplicateFiles_ListsSeriesWithRemovableDuplicates is the core case:
// a series whose folder carries two leftover CBZs of chapters it already has a
// winning file for is listed with the exact file count and the exact reclaimable
// byte total (a stat per PLANNED file, not per file in the library).
func TestLibraryDuplicateFiles_ListsSeriesWithRemovableDuplicates(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()
	fx := seedSeriesWithChapters(ctx, t, db, storage, "Dupes Abound", "dupes-abound", 3)

	// Two leftovers of chapters 1 and 2; the winners ("1.cbz", "2.cbz", "3.cbz")
	// are kept. Distinct sizes so a wrong file being counted changes the total.
	want := writeExtraCBZ(t, storage, fx.series.Title, "[old][en] Dupes Abound 001.cbz", 1024)
	want += writeExtraCBZ(t, storage, fx.series.Title, "[old][en] Dupes Abound 002.cbz", 2048)

	svc := series.NewService(db, storage, 14)
	dto, err := svc.LibraryDuplicateFiles(ctx)
	if err != nil {
		t.Fatalf("LibraryDuplicateFiles: %v", err)
	}
	row := dupRowByID(dto, fx.series.ID)
	if row == nil {
		t.Fatalf("series not listed; got %+v", dto.Series)
	}
	if row.FileCount != 2 {
		t.Errorf("fileCount = %d, want 2", row.FileCount)
	}
	if row.ReclaimableBytes != want {
		t.Errorf("reclaimableBytes = %d, want %d", row.ReclaimableBytes, want)
	}
	if row.Category != "Manga" || row.Title != "Dupes Abound" || row.DisplayName == "" {
		t.Errorf("identity = %q/%q/%q, want the canonical title + Manga populated", row.Title, row.DisplayName, row.Category)
	}
	if dto.TotalFiles != 2 || dto.TotalBytes != want {
		t.Errorf("totals = %d files/%d bytes, want 2/%d", dto.TotalFiles, dto.TotalBytes, want)
	}
}

// TestLibraryDuplicateFiles_ExcludesSeriesWithNothingRemovable: a tidy series —
// one CBZ per downloaded chapter — is never listed, and the envelope is a
// non-nil [] so the JSON never renders null.
func TestLibraryDuplicateFiles_ExcludesSeriesWithNothingRemovable(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()
	fx := seedSeriesWithChapters(ctx, t, db, storage, "All Tidy", "all-tidy", 3)

	svc := series.NewService(db, storage, 14)
	dto, err := svc.LibraryDuplicateFiles(ctx)
	if err != nil {
		t.Fatalf("LibraryDuplicateFiles: %v", err)
	}
	if dto.Series == nil {
		t.Fatal("Series must be a non-nil slice so the JSON renders [] not null")
	}
	if dupRowByID(dto, fx.series.ID) != nil {
		t.Errorf("a series with nothing removable was listed; got %+v", dto.Series)
	}
	if dto.TotalFiles != 0 || dto.TotalBytes != 0 {
		t.Errorf("totals = %d/%d, want 0/0", dto.TotalFiles, dto.TotalBytes)
	}
}

// TestLibraryDuplicateFiles_ExcludesUnparsableFilenames is the SAFETY case, and
// the one the owner will ask about. A series whose leftover CBZs end in a
// non-numeric token — "… - 000 (Chapter 0 - Prologue).cbz" — is ABSENT, because
// the strict full-token number rule refuses to read "Prologue)" as chapter 0.
// The page lists what is actually removable, not everything that looks odd on
// disk; a looser parse here would offer 300+ files for deletion on a coerced
// number match.
//
// Non-vacuous: relax the match to a loose float parse and this series appears.
func TestLibraryDuplicateFiles_ExcludesUnparsableFilenames(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	const title = "Omniscient Reader"
	s := db.Series.Create().SetTitle(title).SetSlug("omniscient-reader").
		SetCategoryID(catID(ctx, db, "Manga")).SaveX(ctx)
	fx := cleanupFixture{storage: storage, series: s, providers: map[string]*ent.SeriesProvider{}}
	fx.providers["comix"] = seedFeed(ctx, t, db, s.ID, "comix", 60, 0)
	seedDownloadedChapter(ctx, t, db, fx, chapterKeyOf(0), 0, 90, fx.providers["comix"])

	// Leftovers whose final token is "Prologue)" / "Epilogue)" — never a number.
	writeExtraCBZ(t, storage, title, "[Asura Scans][en] Omniscient Reader - 000 (Chapter 0 - Prologue).cbz", 4096)
	writeExtraCBZ(t, storage, title, "[Asura Scans][en] Omniscient Reader - 000 (Chapter 0 - Epilogue).cbz", 4096)

	svc := series.NewService(db, storage, 14)
	dto, err := svc.LibraryDuplicateFiles(ctx)
	if err != nil {
		t.Fatalf("LibraryDuplicateFiles: %v", err)
	}
	if row := dupRowByID(dto, s.ID); row != nil {
		t.Errorf("a series whose files fail the strict number parse was listed (%d files); "+
			"the strict rule must refuse them", row.FileCount)
	}
}

// TestLibraryDuplicateFiles_MatchesThePerSeriesFileOnlyPlan is the PARITY pin:
// the library-wide count for a series must equal the file-only removals the
// existing per-series dedupe preview lists for it — the same set, reached by a
// different read path. If the two ever diverge, the page would send the owner to
// a series whose button removes a different number of files.
func TestLibraryDuplicateFiles_MatchesThePerSeriesFileOnlyPlan(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()
	fx := seedSeriesWithChapters(ctx, t, db, storage, "Parity Series", "parity-series", 4)

	writeExtraCBZ(t, storage, fx.series.Title, "[a][en] Parity Series 001.cbz", 10)
	writeExtraCBZ(t, storage, fx.series.Title, "[b][en] Parity Series 001.cbz", 20)
	writeExtraCBZ(t, storage, fx.series.Title, "[c][en] Parity Series 004.cbz", 30)
	writeExtraCBZ(t, storage, fx.series.Title, "junk token.cbz", 40) // never matched

	svc := series.NewService(db, storage, 14)

	plan, err := svc.DedupeFilesPreview(ctx, fx.series.ID)
	if err != nil {
		t.Fatalf("DedupeFilesPreview: %v", err)
	}
	fileOnly := 0
	for _, it := range plan.Items {
		if it.Reason == string(series.DedupeReasonOrphanSuperseded) {
			fileOnly++
		}
	}

	dto, err := svc.LibraryDuplicateFiles(ctx)
	if err != nil {
		t.Fatalf("LibraryDuplicateFiles: %v", err)
	}
	row := dupRowByID(dto, fx.series.ID)
	if row == nil {
		t.Fatalf("series not listed; got %+v", dto.Series)
	}
	if fileOnly != 3 {
		t.Fatalf("per-series preview listed %d file-only removals, want 3 (fixture)", fileOnly)
	}
	if row.FileCount != fileOnly {
		t.Errorf("library-wide fileCount = %d, per-series file-only plan = %d — they must agree",
			row.FileCount, fileOnly)
	}
}

// TestLibraryDuplicateFiles_MatchesPerSeriesWithTwoChaptersOnOneNumber pins the
// hardest parity shape: TWO downloaded chapters carrying the SAME number, each
// with its own winning file, plus a third loose copy.
//
// It is here because the library-wide load reads chapters UNORDERED while the
// per-series load orders them (number, chapter_key), and a walk-order difference
// is only ever observable when two chapters share a number. It is not observable
// in the RESULT: each chapter claims "every file of this number except mine", the
// passes union those claims, so with two distinct keepers every file is claimed by
// someone and the set is the same either way. This test proves that equality
// rather than asserting it — the rows are even inserted in the reverse of the
// per-series order so the two loads genuinely walk them differently.
func TestLibraryDuplicateFiles_MatchesPerSeriesWithTwoChaptersOnOneNumber(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	const title = "Shared Number"
	s := db.Series.Create().SetTitle(title).SetSlug("shared-number").
		SetCategoryID(catID(ctx, db, "Manga")).SaveX(ctx)
	sp := seedFeed(ctx, t, db, s.ID, "comix", 60, 1)

	// Inserted name-keyed FIRST — the reverse of the (number, chapter_key) order
	// the per-series load applies, so the unordered load walks them the other way.
	var want int64
	for _, ch := range []struct {
		key, filename string
		size          int
	}{
		{"name:extra", "[b][en] Shared Number 001.cbz", 200},
		{"1", "[a][en] Shared Number 001.cbz", 100},
	} {
		want += writeExtraCBZ(t, storage, title, ch.filename, ch.size)
		db.Chapter.Create().SetSeriesID(s.ID).SetChapterKey(ch.key).SetNumber(1).
			SetState("downloaded").SetFilename(ch.filename).
			SetSatisfiedByProviderID(sp.ID).SaveX(ctx)
	}
	want += writeExtraCBZ(t, storage, title, "[c][en] Shared Number 001.cbz", 300)

	svc := series.NewService(db, storage, 14)

	plan, err := svc.DedupeFilesPreview(ctx, s.ID)
	if err != nil {
		t.Fatalf("DedupeFilesPreview: %v", err)
	}
	fileOnly := 0
	for _, it := range plan.Items {
		if it.Reason == string(series.DedupeReasonOrphanSuperseded) {
			fileOnly++
		}
	}

	dto, err := svc.LibraryDuplicateFiles(ctx)
	if err != nil {
		t.Fatalf("LibraryDuplicateFiles: %v", err)
	}
	row := dupRowByID(dto, s.ID)
	if row == nil {
		t.Fatalf("series not listed; got %+v", dto.Series)
	}
	// Every file of the number is claimed by one chapter or the other, so all
	// three are planned and the byte total is their full size — from either walk.
	if fileOnly != 3 {
		t.Fatalf("per-series preview listed %d file-only removals, want 3 (fixture)", fileOnly)
	}
	if row.FileCount != fileOnly {
		t.Errorf("library-wide fileCount = %d, per-series file-only plan = %d — the two paths must agree",
			row.FileCount, fileOnly)
	}
	if row.ReclaimableBytes != want {
		t.Errorf("reclaimableBytes = %d, want %d (all three files, whichever chapter claimed which)",
			row.ReclaimableBytes, want)
	}
}

// TestLibraryDuplicateFiles_ExcludesRowDeletingPassesFromTheCount pins the page's
// defining restriction: fileCount and reclaimableBytes cover the FILE-ONLY
// removals and nothing else. The row-deleting passes (0 engine-switch merge, 0b
// ignored fractionals) delete a Chapter ROW as well as a CBZ, and this read-only
// discovery page deliberately does not advertise them.
//
// Without this test the restriction held only BY CONSTRUCTION — no other fixture
// produces a pass-0/0b item at all, so widening the count to
// len(fileOnly)+len(ignoredFractionals) would ship green. The fixture therefore
// fires BOTH kinds at once: one removable ignored fractional (5.5, carried solely
// by a source the owner set to ignore fractionals) alongside two ordinary
// leftovers, with the fractional's CBZ made much larger than both so a byte total
// that wrongly counted it could not coincide with the right answer.
//
// Non-vacuity was proven by execution: counting the ignored fractional
// (len(plan.fileOnly)+len(plan.ignoredFractionals)) failed this test with
// fileCount 3, want 2.
func TestLibraryDuplicateFiles_ExcludesRowDeletingPassesFromTheCount(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	const title = "Ignored Plus Leftovers"
	fx := seedSeriesWithChapters(ctx, t, db, storage, title, "ignored-plus-leftovers", 3)

	// A re-uploader carrying ONLY the fractional 5.5, flagged ignore_fractional —
	// so 5.5 is downloaded, every one of its carriers ignores fractionals, and the
	// pass-0b rule marks its ROW + CBZ removable.
	ignoring := seedFeed(ctx, t, db, fx.series.ID, "kaliscan", 40, 5.5)
	db.SeriesProvider.UpdateOneID(ignoring.ID).SetIgnoreFractional(true).ExecX(ctx)
	fx.providers["kaliscan"] = db.SeriesProvider.GetX(ctx, ignoring.ID)
	seedDownloadedChapter(ctx, t, db, fx, chapterKeyOf(5.5), 5.5, 1, fx.providers["kaliscan"])
	// Overwrite that chapter's CBZ so it dwarfs the file-only removals: if its
	// bytes ever leaked into the total the number could not look merely "close".
	fractionalBytes := writeExtraCBZ(t, storage, title, "5.5.cbz", 8192)

	// Two ordinary file-only leftovers of chapters that already have a winner.
	want := writeExtraCBZ(t, storage, title, "[old][en] Ignored Plus Leftovers 001.cbz", 1024)
	want += writeExtraCBZ(t, storage, title, "[old][en] Ignored Plus Leftovers 002.cbz", 2048)

	svc := series.NewService(db, storage, 14)

	// The fixture only means something if the row-deleting pass actually fired —
	// assert that through the per-series preview before asserting the exclusion.
	const wantFileOnly = 2
	plan, err := svc.DedupeFilesPreview(ctx, fx.series.ID)
	if err != nil {
		t.Fatalf("DedupeFilesPreview: %v", err)
	}
	assertPlanShape(t, plan, 1, wantFileOnly)

	dto, err := svc.LibraryDuplicateFiles(ctx)
	if err != nil {
		t.Fatalf("LibraryDuplicateFiles: %v", err)
	}
	row := dupRowByID(dto, fx.series.ID)
	if row == nil {
		t.Fatalf("series not listed; got %+v", dto.Series)
	}
	if row.FileCount != wantFileOnly {
		t.Errorf("fileCount = %d, want %d — the ignored-fractional row-deleting removal must be EXCLUDED",
			row.FileCount, wantFileOnly)
	}
	if row.ReclaimableBytes != want {
		t.Errorf("reclaimableBytes = %d, want %d — the ignored fractional's %d bytes must be EXCLUDED",
			row.ReclaimableBytes, want, fractionalBytes)
	}
	if dto.TotalFiles != wantFileOnly || dto.TotalBytes != want {
		t.Errorf("totals = %d files/%d bytes, want %d/%d — the library totals must exclude it too",
			dto.TotalFiles, dto.TotalBytes, wantFileOnly, want)
	}
}

// assertPlanShape fails unless a per-series dedupe preview holds exactly the
// intended mix of ROW-deleting removals (passes 0/0b — a Chapter row plus its CBZ)
// and FILE-only removals (passes 1/2 — a CBZ and nothing else). It guards the
// fixture behind the exclusion assertion: a fixture that quietly stopped producing
// a row-deleting item would make that assertion vacuous.
func assertPlanShape(t *testing.T, plan series.DedupePlanDTO, wantRowDeleting, wantFileOnly int) {
	t.Helper()
	rowDeleting, fileOnly := 0, 0
	for _, it := range plan.Items {
		switch it.Reason {
		case string(series.DedupeReasonEpilogueMerge), string(series.DedupeReasonIgnoredFractional):
			rowDeleting++
		case string(series.DedupeReasonOrphanSuperseded):
			fileOnly++
		}
	}
	if rowDeleting != wantRowDeleting || fileOnly != wantFileOnly {
		t.Fatalf("fixture did not produce the intended plan: %d row-deleting + %d file-only, want %d + %d (%+v)",
			rowDeleting, fileOnly, wantRowDeleting, wantFileOnly, plan.Items)
	}
}

// TestLibraryDuplicateFiles_SortsMostActionableFirst pins the ordering: highest
// file count first, then the biggest reclaim, then title A→Z as a stable
// tiebreak. Three series make every level bite (A and B tie on 2 files; B wins
// on bytes).
func TestLibraryDuplicateFiles_SortsMostActionableFirst(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()

	a := seedSeriesWithChapters(ctx, t, db, storage, "Aaa", "aaa", 3)
	writeExtraCBZ(t, storage, a.series.Title, "[x][en] Aaa 001.cbz", 10)
	writeExtraCBZ(t, storage, a.series.Title, "[x][en] Aaa 002.cbz", 10)

	b := seedSeriesWithChapters(ctx, t, db, storage, "Bbb", "bbb", 3)
	writeExtraCBZ(t, storage, b.series.Title, "[x][en] Bbb 001.cbz", 5000)
	writeExtraCBZ(t, storage, b.series.Title, "[x][en] Bbb 002.cbz", 5000)

	c := seedSeriesWithChapters(ctx, t, db, storage, "Ccc", "ccc", 3)
	writeExtraCBZ(t, storage, c.series.Title, "[x][en] Ccc 001.cbz", 9999)
	writeExtraCBZ(t, storage, c.series.Title, "[y][en] Ccc 001.cbz", 9999)
	writeExtraCBZ(t, storage, c.series.Title, "[z][en] Ccc 002.cbz", 9999)

	svc := series.NewService(db, storage, 14)
	dto, err := svc.LibraryDuplicateFiles(ctx)
	if err != nil {
		t.Fatalf("LibraryDuplicateFiles: %v", err)
	}
	if len(dto.Series) != 3 {
		t.Fatalf("want 3 rows, got %d (%+v)", len(dto.Series), dto.Series)
	}
	for i, want := range []uuid.UUID{c.series.ID, b.series.ID, a.series.ID} {
		if dto.Series[i].SeriesID != want.String() {
			t.Errorf("row %d = %q (files=%d bytes=%d), want series %s", i, dto.Series[i].Title,
				dto.Series[i].FileCount, dto.Series[i].ReclaimableBytes, want)
		}
	}
}
