package series_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entpredicate "github.com/technobecet/tsundoku/internal/ent/predicate"
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
	"github.com/technobecet/tsundoku/internal/series"
)

// chapterKey is the "the chapter with this key" predicate, spelled once.
func chapterKey(key string) entpredicate.Chapter { return entchapter.ChapterKeyEQ(key) }

// chapterKeyOf renders a chapter number as its chapter_key (the same formatting
// chapter.NormalizeChapterKey uses).
func chapterKeyOf(n float64) string { return chapter.FormatChapterNumber(n) }

// cleanupFixture is one seeded series ready for a fractional-cleanup assertion:
// the storage root (so a test can look at the CBZ files on disk), the series row,
// and its providers by name.
type cleanupFixture struct {
	storage   string
	series    *ent.Series
	providers map[string]*ent.SeriesProvider
}

// seedDownloadedChapter creates a DOWNLOADED chapter with a real CBZ file on disk,
// satisfied by the given provider. The file is what a removal must delete.
func seedDownloadedChapter(ctx context.Context, t *testing.T, db *ent.Client, fx cleanupFixture, key string, number float64, pageCount int, satisfiedBy *ent.SeriesProvider) *ent.Chapter {
	t.Helper()
	filename := key + ".cbz"
	dir := filepath.Join(fx.storage, "Manga", fx.series.Title)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), []byte("cbz"), 0o600); err != nil {
		t.Fatalf("write cbz: %v", err)
	}
	create := db.Chapter.Create().
		SetSeriesID(fx.series.ID).
		SetChapterKey(key).
		SetNumber(number).
		SetPageCount(pageCount).
		SetState("downloaded").
		SetFilename(filename)
	if satisfiedBy != nil {
		create = create.SetSatisfiedByProviderID(satisfiedBy.ID)
	}
	return create.SaveX(ctx)
}

// seedReturnersMagic reproduces the LIVE-PROD shape this feature was specified
// from ("A Returner's Magic Should Be Special"): three sources, two of them
// flagged ignore_fractional (KaliScan, Comic Asura) and one NOT (Comix), plus the
// downloaded fractionals and the whole chapters that give the median its meaning.
//
// The load-bearing detail is 268.1 "Creator's Note": KaliScan (ignored) satisfies
// it, but COMIX — which the owner did NOT ignore — also carries the key. It must
// never be offered for removal, or the next refresh sweep would re-ingest the key
// and download it straight back (the resurrection trap).
func seedReturnersMagic(ctx context.Context, t *testing.T, db *ent.Client) cleanupFixture {
	t.Helper()
	s := db.Series.Create().
		SetTitle("A Returner's Magic Should Be Special").
		SetSlug("a-returners-magic-should-be-special").
		SetCategoryID(catID(ctx, db, "Manga")).
		SaveX(ctx)

	fx := cleanupFixture{storage: t.TempDir(), series: s, providers: map[string]*ent.SeriesProvider{}}

	// Comix carries the wholes AND 268.1 — NOT ignored (the resurrection guard).
	fx.providers["comix"] = seedFeed(ctx, t, db, s.ID, "comix", 60, 181, 190, 221, 224, 268, 268.1)
	// KaliScan: an ignored re-uploader carrying the notice pages + the two
	// full-size ".5" chapters, plus 268.1.
	fx.providers["kaliscan"] = seedFeed(ctx, t, db, s.ID, "kaliscan", 40, 181.5, 190.5, 221.5, 224.5, 268.1)
	// Comic Asura: an ignored re-uploader carrying 3.1.
	fx.providers["comic-asura"] = seedFeed(ctx, t, db, s.ID, "comic-asura", 20, 3.1)

	for _, name := range []string{"kaliscan", "comic-asura"} {
		db.SeriesProvider.UpdateOneID(fx.providers[name].ID).SetIgnoreFractional(true).ExecX(ctx)
		fx.providers[name] = db.SeriesProvider.GetX(ctx, fx.providers[name].ID)
	}

	// Whole downloaded chapters — the yardstick (median = 96).
	seedDownloadedChapter(ctx, t, db, fx, "181", 181, 94, fx.providers["comix"])
	seedDownloadedChapter(ctx, t, db, fx, "190", 190, 96, fx.providers["comix"])
	seedDownloadedChapter(ctx, t, db, fx, "221", 221, 114, fx.providers["comix"])

	// Downloaded fractionals carried ONLY by ignored sources → removable.
	seedDownloadedChapter(ctx, t, db, fx, "181.5", 181.5, 1, fx.providers["kaliscan"])
	seedDownloadedChapter(ctx, t, db, fx, "190.5", 190.5, 1, fx.providers["kaliscan"])
	seedDownloadedChapter(ctx, t, db, fx, "221.5", 221.5, 132, fx.providers["kaliscan"])
	seedDownloadedChapter(ctx, t, db, fx, "224.5", 224.5, 16, fx.providers["kaliscan"])
	seedDownloadedChapter(ctx, t, db, fx, "3.1", 3.1, 5, fx.providers["comic-asura"])

	// 268.1 — downloaded from the ignored KaliScan, but ALSO carried by Comix,
	// which is NOT ignored. NOT removable.
	seedDownloadedChapter(ctx, t, db, fx, "268.1", 268.1, 3, fx.providers["kaliscan"])

	return fx
}

// numbersOf projects a preview onto its chapter numbers, for readable assertions.
func numbersOf(p series.FractionalCleanupDTO) []float64 {
	out := make([]float64, len(p.Chapters))
	for i, c := range p.Chapters {
		out[i] = c.Number
	}
	return out
}

// chapterByNumber finds the preview entry for a chapter number.
func chapterByNumber(p series.FractionalCleanupDTO, number float64) (series.FractionalCleanupChapterDTO, bool) {
	for _, c := range p.Chapters {
		if c.Number == number {
			return c, true
		}
	}
	return series.FractionalCleanupChapterDTO{}, false
}

// contains reports whether the preview offers a chapter with the given number.
func contains(p series.FractionalCleanupDTO, number float64) bool {
	_, ok := chapterByNumber(p, number)
	return ok
}

// pageCountOf renders a preview entry's nullable page count as an int (-1 = null),
// so an assertion on it reads as a single comparison.
func pageCountOf(c series.FractionalCleanupChapterDTO) int {
	if c.PageCount == nil {
		return -1
	}
	return *c.PageCount
}

// selectAllBut is the owner's dialog selection: every offered chapter except the
// ones he un-ticked.
func selectAllBut(p series.FractionalCleanupDTO, skip ...float64) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(p.Chapters))
	for _, c := range p.Chapters {
		if slices.Contains(skip, c.Number) {
			continue
		}
		ids = append(ids, uuid.MustParse(c.ChapterID))
	}
	return ids
}

// assertFilesGone fails if any of the named CBZs is still in the series folder.
func assertFilesGone(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s still on disk (stat err = %v) — the CBZ must be deleted", name, err)
		}
	}
}

// TestFractionalCleanupPreview_ResurrectionGuard is THE guard: a fractional chapter
// carried by ANY non-ignored source is never offered for removal — even when the
// source that SATISFIES it is ignored. Deleting 268.1 (satisfied by the ignored
// KaliScan, but also carried by Comix, which is NOT ignored) would let the next
// refresh sweep re-ingest the key and download it straight back.
//
// NON-VACUOUS by construction: the same fixture DOES offer five other fractionals
// satisfied by the very same ignored KaliScan/Comic Asura sources — so the test
// fails on a rule that merely looks at satisfied_by (it would offer 268.1 too), and
// it fails on a rule that offers nothing at all.
func TestFractionalCleanupPreview_ResurrectionGuard(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	fx := seedReturnersMagic(ctx, t, db)
	svc := series.NewService(db, fx.storage, 14)

	preview, err := svc.FractionalCleanupPreview(ctx, fx.series.ID)
	if err != nil {
		t.Fatalf("FractionalCleanupPreview: %v", err)
	}

	if contains(preview, 268.1) {
		t.Errorf("268.1 is OFFERED for removal but Comix (not ignored) carries it — removing it would resurrect on the next sweep; preview = %v", numbersOf(preview))
	}
	// Non-vacuity: the ignored-only fractionals ARE offered (so the guard is not
	// simply "offer nothing").
	for _, n := range []float64{3.1, 181.5, 190.5, 221.5, 224.5} {
		if !contains(preview, n) {
			t.Errorf("%v is NOT offered but every source carrying it is ignored; preview = %v", n, numbersOf(preview))
		}
	}
	if len(preview.Chapters) != 5 {
		t.Errorf("preview offers %d chapters (%v), want exactly the 5 ignored-only fractionals", len(preview.Chapters), numbersOf(preview))
	}
}

// TestFractionalCleanupPreview_Evidence pins the fields the owner judges from: the
// median page count of the WHOLE downloaded chapters (the yardstick that makes "1p"
// vs "132p" legible), the satisfying source's display name, and the filename.
func TestFractionalCleanupPreview_Evidence(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	fx := seedReturnersMagic(ctx, t, db)
	svc := series.NewService(db, fx.storage, 14)

	preview, err := svc.FractionalCleanupPreview(ctx, fx.series.ID)
	if err != nil {
		t.Fatalf("FractionalCleanupPreview: %v", err)
	}

	// Wholes downloaded: 94, 96, 114 → median 96.
	if preview.TypicalPageCount != 96 {
		t.Errorf("TypicalPageCount = %d, want 96 (median of the whole downloaded chapters 94/96/114)", preview.TypicalPageCount)
	}

	c, ok := chapterByNumber(preview, 181.5)
	if !ok {
		t.Fatalf("181.5 missing from the preview: %v", numbersOf(preview))
	}
	want := series.FractionalCleanupChapterDTO{
		ChapterID: db.Chapter.Query().Where(chapterKey("181.5")).OnlyX(ctx).ID.String(),
		Number:    181.5,
		PageCount: c.PageCount, // compared separately (nullable)
		Provider:  "kaliscan",  // the SATISFYING source's label
		Filename:  "181.5.cbz",
	}
	if c != want {
		t.Errorf("181.5 evidence = %+v, want %+v", c, want)
	}
	if got := pageCountOf(c); got != 1 {
		t.Errorf("181.5 PageCount = %d, want 1 (the evidence that it is a notice page)", got)
	}
	if preview.Chapters == nil {
		t.Error("Chapters is nil — it must be a non-nil slice so the JSON renders [] not null")
	}
}

// TestFractionalCleanupPreview_OmakeGuard is the 825-omake regression guard at this
// layer: `.5` is the MOST COMMON fractional in the owner's real library (825 genuine
// side-chapters across 44 series). A `.5` chapter carried by a source the owner did
// NOT flag must never be offered — no heuristic, ever.
func TestFractionalCleanupPreview_OmakeGuard(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	s := db.Series.Create().SetTitle("Omake Series").SetSlug("omake-series").
		SetCategoryID(catID(ctx, db, "Manga")).SaveX(ctx)
	fx := cleanupFixture{storage: t.TempDir(), series: s, providers: map[string]*ent.SeriesProvider{}}
	// One honest source, NOT ignored, carrying a genuine 5.5 omake.
	fx.providers["asura"] = seedFeed(ctx, t, db, s.ID, "asura", 60, 5, 5.5, 6)
	seedDownloadedChapter(ctx, t, db, fx, "5", 5, 90, fx.providers["asura"])
	omake := seedDownloadedChapter(ctx, t, db, fx, "5.5", 5.5, 42, fx.providers["asura"])

	svc := series.NewService(db, fx.storage, 14)
	preview, err := svc.FractionalCleanupPreview(ctx, s.ID)
	if err != nil {
		t.Fatalf("FractionalCleanupPreview: %v", err)
	}
	if len(preview.Chapters) != 0 {
		t.Fatalf("preview offers %v — a .5 side-chapter on a NON-ignored source must never be removable", numbersOf(preview))
	}

	// And the removal endpoint refuses it even if a client names it directly.
	if _, err := svc.RemoveFractionalChapters(ctx, s.ID, []uuid.UUID{omake.ID}); !errors.Is(err, series.ErrChapterNotRemovable) {
		t.Errorf("RemoveFractionalChapters(5.5) err = %v, want ErrChapterNotRemovable", err)
	}
	if db.Chapter.Query().CountX(ctx) != 2 {
		t.Error("a chapter was deleted despite the rejection")
	}
}

// TestFractionalCleanupPreview_Excludes covers the remaining exclusions: a WHOLE
// chapter is never removable (even from an ignored source), and a fractional with
// NO file (never downloaded) is never removable (there is nothing to clean).
func TestFractionalCleanupPreview_Excludes(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	s := db.Series.Create().SetTitle("Excludes").SetSlug("excludes").
		SetCategoryID(catID(ctx, db, "Manga")).SaveX(ctx)
	fx := cleanupFixture{storage: t.TempDir(), series: s, providers: map[string]*ent.SeriesProvider{}}
	sp := seedFeed(ctx, t, db, s.ID, "kaliscan", 40, 7, 7.1, 8.1)
	db.SeriesProvider.UpdateOneID(sp.ID).SetIgnoreFractional(true).ExecX(ctx)
	fx.providers["kaliscan"] = db.SeriesProvider.GetX(ctx, sp.ID)

	// A WHOLE chapter downloaded from the ignored source.
	seedDownloadedChapter(ctx, t, db, fx, "7", 7, 88, fx.providers["kaliscan"])
	// A fractional that is WANTED — no file to remove.
	db.Chapter.Create().SetSeriesID(s.ID).SetChapterKey("8.1").SetNumber(8.1).
		SetState("wanted").SaveX(ctx)
	// A removable one, so the test cannot pass vacuously.
	seedDownloadedChapter(ctx, t, db, fx, "7.1", 7.1, 4, fx.providers["kaliscan"])

	svc := series.NewService(db, fx.storage, 14)
	preview, err := svc.FractionalCleanupPreview(ctx, s.ID)
	if err != nil {
		t.Fatalf("FractionalCleanupPreview: %v", err)
	}
	if contains(preview, 7) {
		t.Error("a WHOLE chapter is offered for removal — the toggle suppresses fractionals, not the source")
	}
	if contains(preview, 8.1) {
		t.Error("a fractional with no file is offered for removal — there is nothing to clean")
	}
	if !contains(preview, 7.1) {
		t.Fatalf("the downloaded ignored-only fractional 7.1 is missing: %v — the test would be vacuous", numbersOf(preview))
	}
}

// TestFractionalCleanupPreview_UnknownSeries: an unknown id is a 404, never an
// empty preview.
func TestFractionalCleanupPreview_UnknownSeries(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	svc := series.NewService(db, t.TempDir(), 14)

	if _, err := svc.FractionalCleanupPreview(ctx, uuid.New()); !errors.Is(err, series.ErrSeriesNotFound) {
		t.Errorf("err = %v, want ErrSeriesNotFound", err)
	}
}

// TestRemoveFractionalChapters_DeletesFileAndRow_KeepsFeed is the removal contract:
// the CBZ and the Chapter row go; every ProviderChapter feed row STAYS. The feed is
// the SOURCE's offering, not the owner's library — keeping it is what makes
// un-ticking the toggle restore the chapter (see the restore test below). Deleting
// it would make the toggle a one-way door.
func TestRemoveFractionalChapters_DeletesFileAndRow_KeepsFeed(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	fx := seedReturnersMagic(ctx, t, db)
	svc := series.NewService(db, fx.storage, 14)

	preview, err := svc.FractionalCleanupPreview(ctx, fx.series.ID)
	if err != nil {
		t.Fatalf("FractionalCleanupPreview: %v", err)
	}
	// The owner un-ticks the full-size 221.5 (132p) and removes the rest.
	ids := selectAllBut(preview, 221.5)

	feedBefore := db.ProviderChapter.Query().CountX(ctx)
	chaptersBefore := db.Chapter.Query().CountX(ctx)

	removed, err := svc.RemoveFractionalChapters(ctx, fx.series.ID, ids)
	if err != nil {
		t.Fatalf("RemoveFractionalChapters: %v", err)
	}
	if removed != len(ids) {
		t.Errorf("removed = %d, want %d", removed, len(ids))
	}

	seriesDir := filepath.Join(fx.storage, "Manga", fx.series.Title)
	assertFilesGone(t, seriesDir, "181.5.cbz", "190.5.cbz", "224.5.cbz", "3.1.cbz")
	// The un-ticked one is untouched, on disk and in the DB.
	if _, err := os.Stat(filepath.Join(seriesDir, "221.5.cbz")); err != nil {
		t.Errorf("221.5.cbz was deleted but the owner did not select it: %v", err)
	}
	if db.Chapter.Query().CountX(ctx) != chaptersBefore-len(ids) {
		t.Errorf("chapter rows = %d, want %d", db.Chapter.Query().CountX(ctx), chaptersBefore-len(ids))
	}
	if got := db.ProviderChapter.Query().CountX(ctx); got != feedBefore {
		t.Errorf("ProviderChapter feed rows %d → %d — the feed MUST survive a removal (un-ticking the toggle restores the chapter from it)", feedBefore, got)
	}
	if db.ProviderChapter.Query().Where(entproviderchapter.ChapterKeyEQ("181.5")).CountX(ctx) != 1 {
		t.Error("the removed chapter's feed row is gone — the toggle would become a one-way door")
	}
}

// TestRemoveFractionalChapters_UnTickRestores proves the feed survival is not
// cosmetic: after a removal the owner un-ticks ignore_fractional and the very next
// ingest re-creates the Chapter from the SURVIVING feed row. This is why removal
// must never delete ProviderChapter rows.
func TestRemoveFractionalChapters_UnTickRestores(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	fx := seedReturnersMagic(ctx, t, db)
	svc := series.NewService(db, fx.storage, 14)

	target := db.Chapter.Query().Where(chapterKey("181.5")).OnlyX(ctx)
	if _, err := svc.RemoveFractionalChapters(ctx, fx.series.ID, []uuid.UUID{target.ID}); err != nil {
		t.Fatalf("RemoveFractionalChapters: %v", err)
	}
	if db.Chapter.Query().Where(chapterKey("181.5")).CountX(ctx) != 0 {
		t.Fatal("the chapter row survived the removal")
	}

	// The owner changes his mind and un-ticks the source.
	if err := svc.SetIgnoreFractional(ctx, fx.series.ID, fx.providers["kaliscan"].ID, false); err != nil {
		t.Fatalf("SetIgnoreFractional(false): %v", err)
	}

	// The next ingest sweep re-offers the chapter from the surviving feed row.
	n := 181.5
	if _, err := chapter.IngestProviderChapters(ctx, db, fx.providers["kaliscan"].ID, []chapter.FetchedChapter{
		{Number: &n, Name: "Chapter 181.5"},
	}); err != nil {
		t.Fatalf("IngestProviderChapters: %v", err)
	}
	restored, err := db.Chapter.Query().Where(chapterKey("181.5")).Only(ctx)
	if err != nil {
		t.Fatalf("the chapter was NOT restored after un-ticking — the removal must have destroyed the feed: %v", err)
	}
	if restored.State.String() != "wanted" {
		t.Errorf("restored chapter state = %s, want wanted (it will be re-downloaded)", restored.State)
	}
}

// TestRemoveFractionalChapters_RejectsIdsOutsideTheSet is the authorization heart of
// the endpoint: the removable set is RE-COMPUTED server-side and any id outside it
// is rejected (400) — a whole chapter, a fractional carried by a live source, and a
// chapter of ANOTHER series. Without this the endpoint would be a general-purpose
// "delete any chapter" route wearing a cleanup hat.
func TestRemoveFractionalChapters_RejectsIdsOutsideTheSet(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	fx := seedReturnersMagic(ctx, t, db)
	svc := series.NewService(db, fx.storage, 14)

	other := db.Series.Create().SetTitle("Other Series").SetSlug("other-series").
		SetCategoryID(catID(ctx, db, "Manga")).SaveX(ctx)
	otherFx := cleanupFixture{storage: fx.storage, series: other, providers: map[string]*ent.SeriesProvider{}}
	otherSP := seedFeed(ctx, t, db, other.ID, "kaliscan", 40, 9.1)
	db.SeriesProvider.UpdateOneID(otherSP.ID).SetIgnoreFractional(true).ExecX(ctx)
	otherFx.providers["kaliscan"] = db.SeriesProvider.GetX(ctx, otherSP.ID)
	foreign := seedDownloadedChapter(ctx, t, db, otherFx, "9.1", 9.1, 2, otherFx.providers["kaliscan"])

	cases := map[string]uuid.UUID{
		"whole chapter":                       db.Chapter.Query().Where(chapterKey("181")).OnlyX(ctx).ID,
		"fractional carried by a live source": db.Chapter.Query().Where(chapterKey("268.1")).OnlyX(ctx).ID,
		"removable chapter of ANOTHER series": foreign.ID,
		"unknown chapter id":                  uuid.New(),
	}
	for name, id := range cases {
		before := db.Chapter.Query().CountX(ctx)
		_, err := svc.RemoveFractionalChapters(ctx, fx.series.ID, []uuid.UUID{id})
		if !errors.Is(err, series.ErrChapterNotRemovable) {
			t.Errorf("%s: err = %v, want ErrChapterNotRemovable", name, err)
		}
		if after := db.Chapter.Query().CountX(ctx); after != before {
			t.Errorf("%s: %d chapter rows deleted despite the rejection", name, before-after)
		}
	}
	// All-or-nothing: one bad id in a list with good ones removes NOTHING.
	good := db.Chapter.Query().Where(chapterKey("181.5")).OnlyX(ctx).ID
	before := db.Chapter.Query().CountX(ctx)
	if _, err := svc.RemoveFractionalChapters(ctx, fx.series.ID, []uuid.UUID{good, foreign.ID}); !errors.Is(err, series.ErrChapterNotRemovable) {
		t.Errorf("mixed list: err = %v, want ErrChapterNotRemovable", err)
	}
	if after := db.Chapter.Query().CountX(ctx); after != before {
		t.Errorf("mixed list: %d rows deleted — the removal must be all-or-nothing", before-after)
	}
	if _, err := os.Stat(filepath.Join(fx.storage, "Manga", fx.series.Title, "181.5.cbz")); err != nil {
		t.Errorf("mixed list: the good chapter's CBZ was deleted anyway: %v", err)
	}
}

// TestFractionalCleanupPreview_QueryCountIsFlat is the NO-N+1 proof: the removable
// set and its provider labels are resolved IN MEMORY from one series load (chapters
// + providers + feeds eager-loaded), so the query count must not grow with the size
// of the removable set. A 2-chapter and a 40-chapter removable set must cost the
// SAME reads.
func TestFractionalCleanupPreview_QueryCountIsFlat(t *testing.T) {
	ctx := context.Background()
	seedClient, db := testdb.NewWithSQL(t)

	seed := func(title, slug string, n int) cleanupFixture {
		s := seedClient.Series.Create().SetTitle(title).SetSlug(slug).
			SetCategoryID(catID(ctx, seedClient, "Manga")).SaveX(ctx)
		fx := cleanupFixture{storage: t.TempDir(), series: s, providers: map[string]*ent.SeriesProvider{}}
		nums := make([]float64, 0, 2*n)
		for i := 1; i <= n; i++ {
			nums = append(nums, float64(i), float64(i)+0.1)
		}
		sp := seedFeed(ctx, t, seedClient, s.ID, "kaliscan", 40, nums...)
		seedClient.SeriesProvider.UpdateOneID(sp.ID).SetIgnoreFractional(true).ExecX(ctx)
		fx.providers["kaliscan"] = seedClient.SeriesProvider.GetX(ctx, sp.ID)
		for i := 1; i <= n; i++ {
			seedDownloadedChapter(ctx, t, seedClient, fx, chapterKeyOf(float64(i)), float64(i), 90, fx.providers["kaliscan"])
			seedDownloadedChapter(ctx, t, seedClient, fx, chapterKeyOf(float64(i)+0.1), float64(i)+0.1, 2, fx.providers["kaliscan"])
		}
		return fx
	}

	small := seed("Small Cleanup", "small-cleanup", 2)
	big := seed("Big Cleanup", "big-cleanup", 40)

	client, drv := newCountingClient(db)

	count := func(fx cleanupFixture, want int) int64 {
		svc := series.NewService(client, fx.storage, 14)
		drv.queries.Store(0)
		preview, err := svc.FractionalCleanupPreview(ctx, fx.series.ID)
		if err != nil {
			t.Fatalf("FractionalCleanupPreview: %v", err)
		}
		if len(preview.Chapters) != want {
			t.Fatalf("removable set = %d, want %d", len(preview.Chapters), want)
		}
		return drv.queries.Load()
	}

	smallQ, bigQ := count(small, 2), count(big, 40)
	if smallQ != bigQ {
		t.Errorf("N+1: preview issued %d queries for a 2-chapter removable set but %d for a 40-chapter one — it must be flat", smallQ, bigQ)
	}
	const maxQueries = 6
	if bigQ > maxQueries {
		t.Errorf("preview issued %d queries, want <= %d (one series load + its eager loads)", bigQ, maxQueries)
	}
	t.Logf("queries: removable(2)=%d removable(40)=%d", smallQ, bigQ)
}
