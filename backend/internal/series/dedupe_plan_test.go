package series_test

import (
	"context"
	"path/filepath"
	"slices"
	"sort"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/series"
)

// seedDedupePlanFixture seeds ONE series that exercises ALL THREE DedupeFiles removal
// sources at once, so a single parity assertion covers the whole plan:
//
//   - pass 0 (epilogue-merge): a negative-numeric "-1" chapter + its name-keyed twin,
//     provably one physical chapter (shared source URL). The "-1" row + CBZ go.
//   - pass 0b (ignored-fractional): a downloaded 5.5 whose ONLY carrier ignores
//     fractionals. Its row + CBZ go.
//   - passes 1+2 (orphan-superseded, file-only): an orphan duplicate CBZ of a
//     downloaded whole chapter 7, and a superseded fractional 9.1's leftover CBZ.
//
// It returns the series id, the storage root, and the series directory. Every CBZ the
// plan should touch — and every CBZ it must keep — is written to disk.
func seedDedupePlanFixture(ctx context.Context, t *testing.T, db *ent.Client) (seriesID uuid.UUID, storage, seriesDir string) {
	t.Helper()
	storage = t.TempDir()

	sr := db.Series.Create().
		SetTitle("Parity Series").SetSlug("parity-series").
		SetCategoryID(catID(ctx, db, "Manga")).SaveX(ctx)

	// pass 0 — one source carrying the SAME chapter under two engine keys (shared URL).
	epi := db.SeriesProvider.Create().
		SetSeriesID(sr.ID).SetProvider("101").SetProviderName("Toonily").SetImportance(10).SaveX(ctx)
	db.ProviderChapter.Create().SetSeriesProviderID(epi.ID).SetChapterKey("-1").SetURL("/u/ep").SaveX(ctx)
	db.ProviderChapter.Create().SetSeriesProviderID(epi.ID).SetChapterKey("name:epilogue").SetURL("/u/ep").SaveX(ctx)

	negNumber := -1.0
	db.Chapter.Create().SetSeriesID(sr.ID).SetChapterKey("-1").SetNumber(negNumber).
		SetState(entchapter.StateDownloaded).SetFilename("neg-epilogue.cbz").SaveX(ctx)
	db.Chapter.Create().SetSeriesID(sr.ID).SetChapterKey("name:epilogue").
		SetState(entchapter.StateDownloaded).SetFilename("named-epilogue.cbz").SaveX(ctx)

	// pass 0b — an ignored re-uploader is 5.5's ONLY carrier ⇒ removable.
	kali := seedFeed(ctx, t, db, sr.ID, "kaliscan", 40, 5.5)
	db.SeriesProvider.UpdateOneID(kali.ID).SetIgnoreFractional(true).ExecX(ctx)
	db.Chapter.Create().SetSeriesID(sr.ID).SetChapterKey("5.5").SetNumber(5.5).
		SetState(entchapter.StateDownloaded).SetFilename("5.5.cbz").
		SetSatisfiedByProviderID(kali.ID).SaveX(ctx)

	// passes 1+2 — a downloaded whole chapter with an orphan duplicate, and a
	// superseded fractional whose CBZ was orphaned on disk.
	num7 := 7.0
	db.Chapter.Create().SetSeriesID(sr.ID).SetChapterKey("7").SetNumber(num7).
		SetState(entchapter.StateDownloaded).SetFilename("[X] Parity Series 7.cbz").SaveX(ctx)
	num91 := 9.1
	db.Chapter.Create().SetSeriesID(sr.ID).SetChapterKey("9.1").SetNumber(num91).
		SetState(entchapter.StateSuperseded).SetFilename("").SaveX(ctx)

	seriesDir = filepath.Join(storage, "Manga", "Parity Series")
	for _, name := range []string{
		"neg-epilogue.cbz",              // pass 0 — removed
		"named-epilogue.cbz",            // pass 0 — kept (the canonical)
		"5.5.cbz",                       // pass 0b — removed
		"[X] Parity Series 7.cbz",       // pass 1 — winner, kept
		"[old] Parity Series 7.cbz",     // pass 1 — orphan, removed
		"[stray] Parity Series 9.1.cbz", // pass 2 — superseded orphan, removed
	} {
		writeCBZ(t, seriesDir, name)
	}

	return sr.ID, storage, seriesDir
}

// TestDedupeFilesPlan_PreviewMatchesExecuteAndIsPure is THE parity proof: the DRY-RUN
// (DedupeFilesPreview) lists EXACTLY the CBZ files DedupeFiles then deletes — the same
// plan drives both — and the preview itself mutates NOTHING (all files + all rows
// survive a preview call). One fixture exercises all three removal sources
// (epilogue-merge, ignored-fractional, orphan-superseded) so the parity holds across
// every pass.
func TestDedupeFilesPlan_PreviewMatchesExecuteAndIsPure(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	id, storage, seriesDir := seedDedupePlanFixture(ctx, t, db)
	svc := series.NewService(db, storage, 14)

	filesBefore := listCBZ(t, seriesDir)
	wantPlanned := []string{"5.5.cbz", "[old] Parity Series 7.cbz", "[stray] Parity Series 9.1.cbz", "neg-epilogue.cbz"}

	// 1. Preview — the plan lists EXACTLY the files (and reasons) execute will remove.
	preview, err := svc.DedupeFilesPreview(ctx, id)
	if err != nil {
		t.Fatalf("DedupeFilesPreview: %v", err)
	}
	plannedFiles := plannedFilenames(preview)
	if !equalStrings(plannedFiles, wantPlanned) {
		t.Fatalf("preview files = %v, want %v", plannedFiles, wantPlanned)
	}
	assertReasonBreakdown(t, preview)

	// 2. The preview is PURE — every file and every row is still there.
	if got := listCBZ(t, seriesDir); !equalStrings(got, filesBefore) {
		t.Fatalf("preview mutated the disk: before %v, after %v", filesBefore, got)
	}
	if n := db.Chapter.Query().CountX(ctx); n != 5 {
		t.Fatalf("preview mutated the DB: chapter count = %d, want 5", n)
	}

	// 3. Execute — the removed count matches the plan, and EXACTLY the planned files
	//    are gone (parity: plan == what execute deletes).
	removed, err := svc.DedupeFiles(ctx, id)
	if err != nil {
		t.Fatalf("DedupeFiles: %v", err)
	}
	if removed != preview.Total {
		t.Fatalf("execute removed %d, preview planned %d — plan and execute disagree", removed, preview.Total)
	}
	gone := diffStrings(filesBefore, listCBZ(t, seriesDir))
	sort.Strings(gone)
	if !equalStrings(gone, wantPlanned) {
		t.Fatalf("execute removed files %v, plan listed %v — NOT identical", gone, wantPlanned)
	}

	// 4. The row-bearing passes deleted their rows; everything else survives.
	assertChapterRows(t, ctx, db, []string{"-1", "5.5"}, []string{"name:epilogue", "7", "9.1"})
}

// seedDoubleClaimFixture seeds the pathological shape F1 fixes: a DOWNLOADED
// negative-FRACTIONAL merge twin ("-1.5") that is ALSO an ignored-downloaded-fractional
// removable, so the SAME chapter satisfies both the pass-0 merge rule AND the pass-0b
// ignored-fractional rule off the ONE pre-deletion snapshot. Its carrier feed row
// ignores fractionals (so removableFractionals claims it) and shares a source URL with a
// name-keyed twin (so detectEngineSwitchDuplicates claims it too). Returns the series
// id, storage root, and series dir.
func seedDoubleClaimFixture(ctx context.Context, t *testing.T, db *ent.Client) (seriesID uuid.UUID, storage, seriesDir string) {
	t.Helper()
	storage = t.TempDir()

	sr := db.Series.Create().
		SetTitle("Double Claim Series").SetSlug("double-claim-series").
		SetCategoryID(catID(ctx, db, "Manga")).SaveX(ctx)

	// One IGNORE-FRACTIONAL source carrying the SAME epilogue under two engine keys
	// (shared URL): the negative-fractional "-1.5" and its name-keyed twin. The
	// ignore-fractional flag is what makes the "-1.5" chapter ALSO look removable to
	// pass 0b, while the shared URL makes it a pass-0 merge twin — the double claim.
	sp := db.SeriesProvider.Create().
		SetSeriesID(sr.ID).SetProvider("202").SetProviderName("Comix").SetImportance(10).
		SetIgnoreFractional(true).SaveX(ctx)
	db.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey("-1.5").SetURL("/u/ep15").SaveX(ctx)
	db.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey("name:epi15").SetURL("/u/ep15").SaveX(ctx)

	negNumber := -1.5
	db.Chapter.Create().SetSeriesID(sr.ID).SetChapterKey("-1.5").SetNumber(negNumber).
		SetState(entchapter.StateDownloaded).SetFilename("neg-1.5.cbz").
		SetSatisfiedByProviderID(sp.ID).SaveX(ctx)
	db.Chapter.Create().SetSeriesID(sr.ID).SetChapterKey("name:epi15").
		SetState(entchapter.StateDownloaded).SetFilename("named-epi15.cbz").SaveX(ctx)

	seriesDir = filepath.Join(storage, "Manga", "Double Claim Series")
	for _, name := range []string{"neg-1.5.cbz", "named-epi15.cbz"} {
		writeCBZ(t, seriesDir, name)
	}
	return sr.ID, storage, seriesDir
}

// TestDedupeFilesPlan_MergeTwinAlsoIgnoredFractional pins F1: a DOWNLOADED negative-
// FRACTIONAL merge twin ("-1.5") whose carrier ignores fractionals satisfies BOTH the
// pass-0 merge rule and the pass-0b ignored-fractional rule off the ONE pre-deletion
// snapshot. Before the disjoint-set fix the preview listed it TWICE (Total inflated,
// dry-run != execute) and execute ERRORED with ErrChapterNotRemovable — the merge pass
// had already committed the twin's row deletion, so the ignored-fractional pass's
// re-delete tripped deleted != len(targets). The fix makes MERGE WIN: the twin is listed
// exactly once (epilogue-merge), Total is correct, and execute succeeds — the twin's row
// + CBZ gone, the name-keyed canonical kept.
func TestDedupeFilesPlan_MergeTwinAlsoIgnoredFractional(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	id, storage, seriesDir := seedDoubleClaimFixture(ctx, t, db)
	svc := series.NewService(db, storage, 14)

	filesBefore := listCBZ(t, seriesDir)

	// 1. Preview — the twin appears EXACTLY ONCE, as an epilogue-merge, never doubled.
	preview, err := svc.DedupeFilesPreview(ctx, id)
	if err != nil {
		t.Fatalf("DedupeFilesPreview: %v", err)
	}
	if preview.Total != 1 {
		t.Fatalf("preview Total = %d, want 1 (the twin listed once, not double-claimed)", preview.Total)
	}
	if got := plannedFilenames(preview); !equalStrings(got, []string{"neg-1.5.cbz"}) {
		t.Fatalf("preview files = %v, want [neg-1.5.cbz]", got)
	}
	if r := preview.Items[0].Reason; r != string(series.DedupeReasonEpilogueMerge) {
		t.Fatalf("item reason = %q, want %q (merge wins over ignored-fractional)", r, series.DedupeReasonEpilogueMerge)
	}

	// 2. Execute — no ErrChapterNotRemovable second-delete; the count matches the preview.
	removed, err := svc.DedupeFiles(ctx, id)
	if err != nil {
		t.Fatalf("DedupeFiles: %v (F1 regression: merge + ignored-fractional double-delete)", err)
	}
	if removed != preview.Total {
		t.Fatalf("execute removed %d, preview planned %d — plan and execute disagree", removed, preview.Total)
	}

	// 3. Exactly the twin's CBZ is gone; the canonical is kept.
	gone := diffStrings(filesBefore, listCBZ(t, seriesDir))
	sort.Strings(gone)
	if !equalStrings(gone, []string{"neg-1.5.cbz"}) {
		t.Fatalf("execute removed files %v, want [neg-1.5.cbz]", gone)
	}

	// 4. The twin's row is gone; the name-keyed canonical survives.
	assertChapterRows(t, ctx, db, []string{"-1.5"}, []string{"name:epi15"})
}

// plannedFilenames projects a plan onto its sorted filenames and asserts Total is
// consistent with the item count.
func plannedFilenames(preview series.DedupePlanDTO) []string {
	files := make([]string, 0, len(preview.Items))
	for _, it := range preview.Items {
		files = append(files, it.Filename)
	}
	sort.Strings(files)
	return files
}

// assertReasonBreakdown fails unless the plan carries exactly one epilogue-merge, one
// ignored-fractional, and two orphan-superseded (pass 1 + pass 2) items.
func assertReasonBreakdown(t *testing.T, preview series.DedupePlanDTO) {
	t.Helper()
	reasons := map[string]int{}
	for _, it := range preview.Items {
		reasons[it.Reason]++
	}
	want := map[series.DedupeReason]int{
		series.DedupeReasonEpilogueMerge:     1,
		series.DedupeReasonIgnoredFractional: 1,
		series.DedupeReasonOrphanSuperseded:  2,
	}
	for reason, n := range want {
		if reasons[string(reason)] != n {
			t.Errorf("reason %q count = %d, want %d", reason, reasons[string(reason)], n)
		}
	}
}

// assertChapterRows fails unless every gone key has no chapter row and every kept key
// has exactly one.
func assertChapterRows(t *testing.T, ctx context.Context, db *ent.Client, gone, kept []string) {
	t.Helper()
	for _, key := range gone {
		if n := db.Chapter.Query().Where(chapterKey(key)).CountX(ctx); n != 0 {
			t.Errorf("chapter %q row survived execute, want deleted", key)
		}
	}
	for _, key := range kept {
		if n := db.Chapter.Query().Where(chapterKey(key)).CountX(ctx); n != 1 {
			t.Errorf("chapter %q row missing after execute, want kept", key)
		}
	}
}

// equalStrings reports whether two string slices are element-wise equal.
func equalStrings(a, b []string) bool {
	return slices.Equal(a, b)
}

// diffStrings returns the elements of before that are absent from after (the files a
// sweep removed).
func diffStrings(before, after []string) []string {
	present := make(map[string]struct{}, len(after))
	for _, s := range after {
		present[s] = struct{}{}
	}
	var gone []string
	for _, s := range before {
		if _, ok := present[s]; !ok {
			gone = append(gone, s)
		}
	}
	return gone
}
