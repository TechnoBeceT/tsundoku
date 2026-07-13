// Package disk_test — integration tests for the DB-loss reconciler.
// Tests require Docker (via testcontainers) for an ephemeral PostgreSQL instance.
package disk_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/fetcher"
)

// chSnapshot records the fields used to verify lossless rebuild for one chapter.
type chSnapshot struct {
	Provider   string
	Filename   string
	Number     *float64
	Importance int
	PageCount  int
}

// takeChapterSnapshot captures a key→chSnapshot map from the current DB state.
func takeChapterSnapshot(ctx context.Context, t *testing.T, client *ent.Client) map[string]chSnapshot {
	t.Helper()
	allChapters := client.Chapter.Query().AllX(ctx)
	allSPs := client.SeriesProvider.Query().AllX(ctx)

	spNames := make(map[uuid.UUID]string, len(allSPs))
	for _, sp := range allSPs {
		spNames[sp.ID] = sp.Provider
	}

	out := make(map[string]chSnapshot, len(allChapters))
	for _, ch := range allChapters {
		s := chSnapshot{Filename: ch.Filename, Number: ch.Number}
		if ch.SatisfiedImportance != nil {
			s.Importance = *ch.SatisfiedImportance
		}
		if ch.PageCount != nil {
			s.PageCount = *ch.PageCount
		}
		if ch.SatisfiedByProviderID != nil {
			s.Provider = spNames[*ch.SatisfiedByProviderID]
		}
		out[ch.ChapterKey] = s
	}
	return out
}

// assertChapterRebuildMatch verifies one rebuilt chapter against its pre-drop snapshot.
func assertChapterRebuildMatch(t *testing.T, ch *ent.Chapter, snap chSnapshot, spNames map[uuid.UUID]string) {
	t.Helper()
	key := ch.ChapterKey
	if ch.State != entchapter.StateDownloaded {
		t.Errorf("chapter %q: state = %s, want downloaded", key, ch.State)
	}
	if ch.Filename != snap.Filename {
		t.Errorf("chapter %q: filename = %q, want %q", key, ch.Filename, snap.Filename)
	}
	assertFloat64Ptr(t, key, "number", ch.Number, snap.Number)
	if ch.SatisfiedImportance != nil && *ch.SatisfiedImportance != snap.Importance {
		t.Errorf("chapter %q: importance = %d, want %d", key, *ch.SatisfiedImportance, snap.Importance)
	}
	if ch.PageCount != nil && *ch.PageCount != snap.PageCount {
		t.Errorf("chapter %q: page_count = %d, want %d", key, *ch.PageCount, snap.PageCount)
	}
	if ch.SatisfiedByProviderID != nil {
		if got := spNames[*ch.SatisfiedByProviderID]; got != snap.Provider {
			t.Errorf("chapter %q: provider = %q, want %q", key, got, snap.Provider)
		}
	}
}

// assertFloat64Ptr verifies two *float64 values are equal.
func assertFloat64Ptr(t *testing.T, key, field string, got, want *float64) {
	t.Helper()
	switch {
	case got == nil && want != nil:
		t.Errorf("chapter %q: %s = nil, want %v", key, field, *want)
	case got != nil && want == nil:
		t.Errorf("chapter %q: %s = %v, want nil", key, field, *got)
	case got != nil && want != nil && *got != *want:
		t.Errorf("chapter %q: %s = %v, want %v", key, field, *got, *want)
	}
}

// TestReconcile_lossless_rebuild is the milestone's third regression proof.
//
// It renders real CBZs to a temp storage directory (producing tsundoku.json
// sidecar + CBZ archives), seeds the DB via Reconcile, snapshots the chapter
// rows, drops all rows to simulate a total DB loss, re-runs Reconcile, and
// asserts the chapters table is rebuilt identically: same chapter_key,
// state=downloaded, provider, importance, filename, page_count, number.
func TestReconcile_lossless_rebuild(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()
	max := 10.0
	num1, num2 := 1.0, 2.0

	fn1 := renderForRebuild(t, storage, &num1, "1", "mangadex", 2, &max)
	fn2 := renderForRebuild(t, storage, &num2, "2", "comick", 1, &max)

	seedAndDrop(t, ctx, client, storage)
	snapshots := takeChapterSnapshot(ctx, t, client)
	dropAllRows(ctx, client)

	result, err := disk.Reconcile(ctx, client, storage)
	if err != nil {
		t.Fatalf("Reconcile after DB loss: %v", err)
	}
	assertReconcileResultNonZero(t, result)
	assertRebuiltChapters(t, ctx, client, snapshots, fn1, fn2)
}

// seedAndDrop seeds the DB from disk and verifies 2 chapters exist.
func seedAndDrop(t *testing.T, ctx context.Context, client *ent.Client, storage string) {
	t.Helper()
	if _, err := disk.Reconcile(ctx, client, storage); err != nil {
		t.Fatalf("initial Reconcile: %v", err)
	}
	if n := client.Chapter.Query().CountX(ctx); n != 2 {
		t.Fatalf("want 2 chapter rows after initial Reconcile, got %d", n)
	}
}

// dropAllRows deletes all Series/SeriesProvider/Chapter rows to simulate DB loss.
func dropAllRows(ctx context.Context, client *ent.Client) {
	client.Chapter.Delete().ExecX(ctx)
	client.SeriesProvider.Delete().ExecX(ctx)
	client.Series.Delete().ExecX(ctx)
}

// assertReconcileResultNonZero asserts that at least one row of each type was upserted.
func assertReconcileResultNonZero(t *testing.T, r disk.ReconcileResult) {
	t.Helper()
	if r.SeriesUpserted == 0 {
		t.Error("SeriesUpserted = 0, want > 0")
	}
	if r.ProvidersUpserted == 0 {
		t.Error("ProvidersUpserted = 0, want > 0")
	}
	if r.ChaptersUpserted == 0 {
		t.Error("ChaptersUpserted = 0, want > 0")
	}
}

// assertRebuiltChapters verifies the rebuilt chapter table against pre-drop snapshots
// and checks that the expected filenames are present.
func assertRebuiltChapters(t *testing.T, ctx context.Context, client *ent.Client, snapshots map[string]chSnapshot, fn1, fn2 string) {
	t.Helper()
	rebuilt := client.Chapter.Query().AllX(ctx)
	if len(rebuilt) != 2 {
		t.Fatalf("want 2 chapters after rebuild, got %d", len(rebuilt))
	}

	newSPs := client.SeriesProvider.Query().AllX(ctx)
	spNames := make(map[uuid.UUID]string, len(newSPs))
	for _, sp := range newSPs {
		spNames[sp.ID] = sp.Provider
	}

	fileMap := make(map[string]string, len(rebuilt))
	for _, ch := range rebuilt {
		fileMap[ch.ChapterKey] = ch.Filename
		snap, ok := snapshots[ch.ChapterKey]
		if !ok {
			t.Errorf("rebuilt chapter_key %q not in original snapshot", ch.ChapterKey)
			continue
		}
		assertChapterRebuildMatch(t, ch, snap, spNames)
	}
	assertEqual(t, "chapter 1 filename after rebuild", fn1, fileMap["1"])
	assertEqual(t, "chapter 2 filename after rebuild", fn2, fileMap["2"])
}

// renderForRebuild renders a single chapter to storage and returns its filename.
func renderForRebuild(t *testing.T, storage string, num *float64, key, provider string, importance int, max *float64) string {
	t.Helper()
	req := disk.RenderRequest{
		Storage: storage,
		Meta: disk.RenderMeta{
			Provider:    provider,
			Language:    "en",
			SeriesTitle: "Rebuild Test",
			Category:    disk.CategoryManga,
			Number:      num,
			MaxChapter:  max,
			ChapterKey:  key,
			Importance:  importance,
		},
		Pages: []fetcher.PageImage{
			{Data: []byte{0xFF, 0xD8}, Ext: "jpg"},
			{Data: []byte{0xFF, 0xD9}, Ext: "jpg"},
		},
	}
	fn, err := disk.RenderChapter(req)
	if err != nil {
		t.Fatalf("RenderChapter(%q): %v", key, err)
	}
	return fn
}

// TestReconcile_restores_category is the M3 lossless-round-trip proof: a
// reconcile after a recategorize restores the series' category from disk.
//
// It renders a series under Other/, recategorizes it to Manhwa via
// MoveSeriesCategory (folder + sidecar both flip to Manhwa), wipes all DB rows
// to simulate a total DB loss, then re-runs Reconcile and asserts the rebuilt
// Series.Category is Manhwa — not the column default Other. Before the fix
// upsertSeries never wrote the category, so the restored row defaulted to Other.
func TestReconcile_restores_category(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()

	num := 1.0
	max := 1.0
	const title = "Round Trip Series"
	req := disk.RenderRequest{
		Storage: storage,
		Meta: disk.RenderMeta{
			Provider:    "mangadex",
			Language:    "en",
			SeriesTitle: title,
			Category:    disk.CategoryOther,
			Number:      &num,
			MaxChapter:  &max,
			ChapterKey:  "1",
			Importance:  1,
		},
		Pages: []fetcher.PageImage{{Data: []byte{0x00}, Ext: "jpg"}},
	}
	if _, err := disk.RenderChapter(req); err != nil {
		t.Fatalf("RenderChapter: %v", err)
	}

	// Recategorize Other → Manhwa: moves the folder and rewrites the sidecar.
	if err := disk.MoveSeriesCategory(storage, disk.CategoryOther, disk.CategoryManhwa, title); err != nil {
		t.Fatalf("MoveSeriesCategory: %v", err)
	}

	// Simulate total DB loss: nothing in the DB, only the (recategorized) disk.
	result, err := disk.Reconcile(ctx, client, storage)
	if err != nil {
		t.Fatalf("Reconcile after recategorize: %v", err)
	}
	if result.SeriesUpserted == 0 {
		t.Fatal("SeriesUpserted = 0, want > 0")
	}

	got := client.Series.Query().WithCategory().OnlyX(ctx)
	if name := got.QueryCategory().OnlyX(ctx).Name; name != disk.CategoryManhwa {
		t.Errorf("restored Series category = %q, want %q (round-trip must preserve the recategorize)",
			name, disk.CategoryManhwa)
	}
}

// TestReconcile_user_named_category_round_trips is the dynamic-scanner safety
// proof: a series rendered under a NON-default, user-named category folder
// survives a total DB loss. After wiping the DB, Reconcile treats the folder as a
// category, find-or-creates a Category row by that name, and links the rebuilt
// series to it — so a user-defined category is never lost on a reconcile.
func TestReconcile_user_named_category_round_trips(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()

	const userCategory = "Webtoons I Love"
	num := 1.0
	max := 1.0
	const title = "Custom Category Series"
	req := disk.RenderRequest{
		Storage: storage,
		Meta: disk.RenderMeta{
			Provider:    "mangadex",
			Language:    "en",
			SeriesTitle: title,
			Category:    userCategory,
			Number:      &num,
			MaxChapter:  &max,
			ChapterKey:  "1",
			Importance:  1,
		},
		Pages: []fetcher.PageImage{{Data: []byte{0x00}, Ext: "jpg"}},
	}
	if _, err := disk.RenderChapter(req); err != nil {
		t.Fatalf("RenderChapter: %v", err)
	}

	// No Category row exists for this user-named folder yet beyond the five
	// seeded defaults — Reconcile must create it.
	if _, err := disk.Reconcile(ctx, client, storage); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	got := client.Series.Query().WithCategory().OnlyX(ctx)
	gotCat := got.QueryCategory().OnlyX(ctx)
	if gotCat.Name != userCategory {
		t.Errorf("restored category = %q, want %q (user-named category must round-trip)", gotCat.Name, userCategory)
	}
}

// TestReconcile_idempotent verifies that running Reconcile twice on an
// unchanged library produces 0 new series/providers/chapters on the second run.
func TestReconcile_idempotent(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()

	num := 5.0
	max := 10.0
	req := disk.RenderRequest{
		Storage: storage,
		Meta: disk.RenderMeta{
			Provider:    "mangadex",
			Language:    "en",
			SeriesTitle: "Idempotent Test",
			Category:    disk.CategoryManga,
			Number:      &num,
			MaxChapter:  &max,
			ChapterKey:  "5",
			Importance:  1,
		},
		Pages: []fetcher.PageImage{{Data: []byte{0x00}, Ext: "jpg"}},
	}
	if _, err := disk.RenderChapter(req); err != nil {
		t.Fatalf("RenderChapter: %v", err)
	}

	// First Reconcile — must create rows.
	r1, err := disk.Reconcile(ctx, client, storage)
	if err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}
	if r1.SeriesUpserted == 0 {
		t.Error("first Reconcile: expected SeriesUpserted > 0")
	}

	// Capture row counts after first run.
	seriesCount := client.Series.Query().CountX(ctx)
	spCount := client.SeriesProvider.Query().CountX(ctx)
	chCount := client.Chapter.Query().CountX(ctx)

	// Second Reconcile — must report 0 newly created.
	r2, err := disk.Reconcile(ctx, client, storage)
	if err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	if r2.ChaptersAdopted != 0 {
		t.Errorf("second Reconcile: ChaptersAdopted = %d, want 0", r2.ChaptersAdopted)
	}

	// Row counts must not have grown.
	if got := client.Series.Query().CountX(ctx); got != seriesCount {
		t.Errorf("Series count grew: was %d, now %d", seriesCount, got)
	}
	if got := client.SeriesProvider.Query().CountX(ctx); got != spCount {
		t.Errorf("SeriesProvider count grew: was %d, now %d", spCount, got)
	}
	if got := client.Chapter.Query().CountX(ctx); got != chCount {
		t.Errorf("Chapter count grew: was %d, now %d", chCount, got)
	}
}

// TestReconcile_adopt_orphan verifies that a CBZ on disk with ComicInfo provenance
// but no existing DB row is adopted: Reconcile creates the Chapter row.
func TestReconcile_adopt_orphan(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()

	// Create an orphan CBZ with ComicInfo provenance but NO tsundoku.json.
	seriesDir := filepath.Join(storage, "Manga", "Orphan Test")
	if err := os.MkdirAll(seriesDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	ci := disk.ComicInfo{
		Series:     "Orphan Test",
		Number:     "3",
		Provider:   "comick",
		Importance: 1,
		ChapterKey: "3",
		PageCount:  1,
	}
	cbzPath := filepath.Join(seriesDir, "[comick][en] Orphan Test 3.cbz")
	if err := disk.CreateCBZ(cbzPath, []fetcher.PageImage{{Data: []byte{0x00}, Ext: "jpg"}}, ci); err != nil {
		t.Fatalf("CreateCBZ: %v", err)
	}

	// Stamp a KNOWN past mtime on the orphan CBZ so we can assert the seeded
	// first_downloaded_at came from it, not from CreateCBZ's write-time default.
	want := time.Date(2026, 1, 14, 10, 0, 0, 0, time.UTC)
	if err := os.Chtimes(cbzPath, want, want); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	result, err := disk.Reconcile(ctx, client, storage)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if result.ChaptersAdopted < 1 {
		t.Errorf("ChaptersAdopted = %d, want >= 1", result.ChaptersAdopted)
	}

	ch := client.Chapter.Query().OnlyX(ctx)
	if ch.ChapterKey != "3" {
		t.Errorf("adopted chapter_key = %q, want %q", ch.ChapterKey, "3")
	}
	if ch.State != entchapter.StateDownloaded {
		t.Errorf("adopted chapter state = %s, want downloaded", ch.State)
	}
	// The load-bearing assertion this test previously lacked: an orphan CBZ
	// (no sidecar entry — exactly the owner's real Kaizoku library shape) must
	// seed FirstDownloadedAt from the ORPHAN path's ModTime, not the sidecar path's.
	if ch.FirstDownloadedAt == nil {
		t.Fatal("FirstDownloadedAt = nil for orphan-adopted chapter, want set from CBZ mtime")
	}
	if !ch.FirstDownloadedAt.Truncate(time.Second).Equal(want.Truncate(time.Second)) {
		t.Errorf("FirstDownloadedAt = %v, want %v", ch.FirstDownloadedAt, want)
	}
}

// TestReconcile_missing_file_reported verifies that a sidecar entry whose CBZ
// has been deleted is reported in MissingFiles without crashing and without
// forcing an illegal state transition.
func TestReconcile_missing_file_reported(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()

	num := 2.0
	max := 5.0
	req := disk.RenderRequest{
		Storage: storage,
		Meta: disk.RenderMeta{
			Provider:    "mangadex",
			Language:    "en",
			SeriesTitle: "Missing Test",
			Category:    disk.CategoryManga,
			Number:      &num,
			MaxChapter:  &max,
			ChapterKey:  "2",
			Importance:  1,
		},
		Pages: []fetcher.PageImage{{Data: []byte{0x00}, Ext: "jpg"}},
	}
	filename, err := disk.RenderChapter(req)
	if err != nil {
		t.Fatalf("RenderChapter: %v", err)
	}

	// Seed the DB.
	r1, err := disk.Reconcile(ctx, client, storage)
	if err != nil {
		t.Fatalf("initial Reconcile: %v", err)
	}
	if r1.MissingFiles != 0 {
		t.Fatalf("initial Reconcile: MissingFiles = %d, want 0", r1.MissingFiles)
	}

	// Delete the CBZ — sidecar entry remains.
	seriesDir := filepath.Join(storage, "Manga", "Missing Test")
	if err := os.Remove(filepath.Join(seriesDir, filename)); err != nil {
		t.Fatalf("remove cbz: %v", err)
	}

	// Reconcile must report MissingFiles=1 without crashing.
	r2, err := disk.Reconcile(ctx, client, storage)
	if err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	if r2.MissingFiles != 1 {
		t.Errorf("MissingFiles = %d, want 1", r2.MissingFiles)
	}

	// No illegal state transition must have been forced.
	ch := client.Chapter.Query().OnlyX(ctx)
	if ch.State != entchapter.StateDownloaded {
		t.Errorf("chapter state changed to %s; illegal downloaded→wanted transition must not occur", ch.State)
	}
}

// TestReconcile_scanlator_groups is the Task 5 identity proof: a series dir
// with two CBZs from the SAME provider but DIFFERENT scanlators must reconcile
// into TWO SeriesProvider rows (one per scanlator), not one — collapsing them
// would lose the scanlator identity on a DB-loss reconcile.
func TestReconcile_scanlator_groups(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()
	max := 2.0
	num1, num2 := 1.0, 2.0

	renderScanlatorChapter(t, storage, &num1, "1", "Alpha", &max)
	renderScanlatorChapter(t, storage, &num2, "2", "Beta", &max)

	result, err := disk.Reconcile(ctx, client, storage)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if result.ProvidersUpserted != 2 {
		t.Errorf("ProvidersUpserted = %d, want 2 (one per scanlator)", result.ProvidersUpserted)
	}

	sps := client.SeriesProvider.Query().AllX(ctx)
	if len(sps) != 2 {
		t.Fatalf("SeriesProvider rows = %d, want 2", len(sps))
	}
	gotScanlators := make(map[string]bool, 2)
	for _, sp := range sps {
		if sp.Provider != "Comix" {
			t.Errorf("provider = %q, want Comix", sp.Provider)
		}
		gotScanlators[sp.Scanlator] = true
	}
	if !gotScanlators["Alpha"] || !gotScanlators["Beta"] {
		t.Errorf("scanlators = %v, want both Alpha and Beta present", gotScanlators)
	}
}

// renderScanlatorChapter renders a single chapter for provider "Comix" under
// the given scanlator, producing a real "[Comix-<scanlator>]…" CBZ on disk.
func renderScanlatorChapter(t *testing.T, storage string, num *float64, key, scanlator string, max *float64) string {
	t.Helper()
	req := disk.RenderRequest{
		Storage: storage,
		Meta: disk.RenderMeta{
			Provider:    "Comix",
			Scanlator:   scanlator,
			Language:    "en",
			SeriesTitle: "Two Scanlators",
			Category:    disk.CategoryManga,
			Number:      num,
			MaxChapter:  max,
			ChapterKey:  key,
			Importance:  1,
		},
		Pages: []fetcher.PageImage{{Data: []byte{0x00}, Ext: "jpg"}},
	}
	fn, err := disk.RenderChapter(req)
	if err != nil {
		t.Fatalf("RenderChapter(%q): %v", key, err)
	}
	return fn
}

// TestReconcile_restores_cover_index proves the sidecar is the durable SEED of
// the DB cover fast-index: after a total DB loss, Reconcile puts cover_file +
// cover_source_url back on the Series row, so the rebuilt library serves its
// already-downloaded covers straight from disk and never re-fetches them from a
// source. It also asserts idempotency — a second run leaves the same values.
func TestReconcile_restores_cover_index(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()

	const title = "Cover Index Series"
	num, max := 1.0, 1.0
	req := disk.RenderRequest{
		Storage: storage,
		Meta: disk.RenderMeta{
			Provider:    "mangadex",
			Language:    "en",
			SeriesTitle: title,
			Category:    disk.CategoryManga,
			Number:      &num,
			MaxChapter:  &max,
			ChapterKey:  "1",
			Importance:  1,
		},
		Pages: []fetcher.PageImage{{Data: []byte{0x00}, Ext: "jpg"}},
	}
	if _, err := disk.RenderChapter(req); err != nil {
		t.Fatalf("RenderChapter: %v", err)
	}
	if _, err := disk.SaveCover(disk.CoverRequest{
		Storage:   storage,
		Category:  disk.CategoryManga,
		Title:     title,
		Data:      []byte("IMG"),
		Ext:       "webp",
		SourceURL: "/thumb/a",
		Provider:  "mangadex",
	}); err != nil {
		t.Fatalf("SaveCover: %v", err)
	}

	if _, err := disk.Reconcile(ctx, client, storage); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	row := client.Series.Query().OnlyX(ctx)
	if row.CoverFile != "cover.webp" || row.CoverSourceURL != "/thumb/a" {
		t.Fatalf("cover index after reconcile: cover_file=%q cover_source_url=%q, want cover.webp//thumb/a",
			row.CoverFile, row.CoverSourceURL)
	}

	if _, err := disk.Reconcile(ctx, client, storage); err != nil {
		t.Fatalf("Reconcile (second run): %v", err)
	}
	row = client.Series.Query().OnlyX(ctx)
	if row.CoverFile != "cover.webp" || row.CoverSourceURL != "/thumb/a" {
		t.Errorf("cover index after re-run: cover_file=%q cover_source_url=%q, want unchanged",
			row.CoverFile, row.CoverSourceURL)
	}
}

// TestReconcileSeedsFirstDownloadedAtFromCBZMtime verifies that a
// disk-imported chapter (which has no download_date and never will) has its
// first_downloaded_at seeded from the CBZ file's mtime — the only real
// evidence of when it became readable.
func TestReconcileSeedsFirstDownloadedAtFromCBZMtime(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()

	const title = "Mtime Seed Series"
	num, max := 1.0, 1.0
	req := disk.RenderRequest{
		Storage: storage,
		Meta: disk.RenderMeta{
			Provider:    "mangadex",
			Language:    "en",
			SeriesTitle: title,
			Category:    disk.CategoryManga,
			Number:      &num,
			MaxChapter:  &max,
			ChapterKey:  "1",
			Importance:  1,
		},
		Pages: []fetcher.PageImage{{Data: []byte{0x00}, Ext: "jpg"}},
	}
	filename, err := disk.RenderChapter(req)
	if err != nil {
		t.Fatalf("RenderChapter: %v", err)
	}

	want := time.Date(2026, 1, 14, 10, 0, 0, 0, time.UTC)
	cbzPath := disk.ChapterCBZPath(storage, disk.CategoryManga, title, filename)
	if err := os.Chtimes(cbzPath, want, want); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	if _, err := disk.Reconcile(ctx, client, storage); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	ch := client.Chapter.Query().OnlyX(ctx)
	if ch.FirstDownloadedAt == nil {
		t.Fatal("FirstDownloadedAt = nil, want set from CBZ mtime")
	}
	// Truncate to the second — not every filesystem keeps nanoseconds, and
	// Postgres/Go round-trips can shave precision.
	if !ch.FirstDownloadedAt.Truncate(time.Second).Equal(want.Truncate(time.Second)) {
		t.Errorf("FirstDownloadedAt = %v, want %v", ch.FirstDownloadedAt, want)
	}
}

// TestReconcileDoesNotOverwriteExistingFirstDownloadedAt is the important
// half of the proof: reconcile must never clobber a first_downloaded_at value
// already set. A convergence upgrade REWRITES the CBZ (and therefore its
// mtime) when it re-fetches an old chapter from a better source — trusting
// mtime over an existing value here would re-introduce the exact bug
// first_downloaded_at exists to kill.
func TestReconcileDoesNotOverwriteExistingFirstDownloadedAt(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()

	const title = "Write Once Series"
	num, max := 1.0, 1.0
	req := disk.RenderRequest{
		Storage: storage,
		Meta: disk.RenderMeta{
			Provider:    "mangadex",
			Language:    "en",
			SeriesTitle: title,
			Category:    disk.CategoryManga,
			Number:      &num,
			MaxChapter:  &max,
			ChapterKey:  "1",
			Importance:  1,
		},
		Pages: []fetcher.PageImage{{Data: []byte{0x00}, Ext: "jpg"}},
	}
	filename, err := disk.RenderChapter(req)
	if err != nil {
		t.Fatalf("RenderChapter: %v", err)
	}

	// First reconcile seeds first_downloaded_at from an old mtime.
	original := time.Date(2026, 1, 14, 10, 0, 0, 0, time.UTC)
	cbzPath := disk.ChapterCBZPath(storage, disk.CategoryManga, title, filename)
	if err := os.Chtimes(cbzPath, original, original); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	if _, err := disk.Reconcile(ctx, client, storage); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}
	first := client.Chapter.Query().OnlyX(ctx)
	if first.FirstDownloadedAt == nil {
		t.Fatal("FirstDownloadedAt = nil after first Reconcile, want set")
	}

	// Simulate a convergence upgrade rewriting the CBZ: a DIFFERENT, later mtime.
	rewritten := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(cbzPath, rewritten, rewritten); err != nil {
		t.Fatalf("Chtimes (rewrite): %v", err)
	}
	if _, err := disk.Reconcile(ctx, client, storage); err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}

	second := client.Chapter.Query().OnlyX(ctx)
	if second.FirstDownloadedAt == nil {
		t.Fatal("FirstDownloadedAt = nil after second Reconcile, want still set")
	}
	if !second.FirstDownloadedAt.Truncate(time.Second).Equal(original.Truncate(time.Second)) {
		t.Errorf("FirstDownloadedAt after second Reconcile = %v, want unchanged original %v (mtime must never overwrite an existing value)",
			second.FirstDownloadedAt, original)
	}
}
