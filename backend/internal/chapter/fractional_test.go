package chapter_test

import (
	"context"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
)

// seedNumberedChapter creates a series plus a wanted chapter carrying a parsed
// NUMBER. The ignore-fractional gate keys off Chapter.Number (fractional-ness is
// a number property, never part of chapter_key), so these tests cannot reuse
// candidates_test.go's seedChapter, which leaves Number nil.
func seedNumberedChapter(ctx context.Context, t *testing.T, slug, key string, number float64) (*ent.Client, *ent.Series, *ent.Chapter) {
	t.Helper()
	client := testdb.New(t)
	s := client.Series.Create().SetTitle(slug).SetSlug(slug).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey(key).SetNumber(number).SaveX(ctx)
	return client, s, ch
}

// addFractionalSource creates a SeriesProvider carrying the given ignore_fractional
// flag, plus its ProviderChapter for chapterKey (fresh retry state, so the source
// is a live candidate unless the gate excludes it).
func addFractionalSource(
	ctx context.Context,
	t *testing.T,
	client *ent.Client,
	series *ent.Series,
	provider, chapterKey string,
	importance int,
	ignoreFractional bool,
) *ent.SeriesProvider {
	t.Helper()
	sp := client.SeriesProvider.Create().
		SetSeries(series).
		SetProvider(provider).
		SetImportance(importance).
		SetIgnoreFractional(ignoreFractional).
		SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).
		SetChapterKey(chapterKey).
		SetURL("https://" + provider + ".example.com/" + chapterKey).
		SetProviderIndex(0).
		SaveX(ctx)
	return sp
}

// TestRankedLiveCandidates_OmakeOnNonIgnoredSourceIsUntouched is THE regression
// guard for this whole feature. `.5` side-chapters (omakes) are the MOST COMMON
// fractional in a real library — 825 of them across 44 series in the owner's prod
// DB — and a source hosting a 5.5 omake obviously also hosts chapter 5. Any
// heuristic suppression would have destroyed all of them, which is precisely why
// suppression is reachable ONLY through an explicitly-ticked per-source toggle.
//
// So: a source WITHOUT the toggle keeps offering its fractional chapters. The
// assertion is deliberately POSITIVE (the omake is still a live, downloadable
// candidate), not merely "no error" — a vacuous version of this test is worth
// nothing.
func TestRankedLiveCandidates_OmakeOnNonIgnoredSourceIsUntouched(t *testing.T) {
	ctx := context.Background()
	client, s, ch := seedNumberedChapter(ctx, t, "omake-untouched", "5.5", 5.5)
	addFractionalSource(ctx, t, client, s, "omake-source", "5.5", 10, false)

	cands, err := chapter.RankedLiveCandidates(ctx, client, ch.ID, 3, time.Now())
	if err != nil {
		t.Fatalf("RankedLiveCandidates: %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("want 1 live candidate — a .5 omake on a NON-ignored source must stay downloadable, got %d", len(cands))
	}
	if cands[0].SeriesProvider.Provider != "omake-source" {
		t.Errorf("want the omake's source, got %q", cands[0].SeriesProvider.Provider)
	}
}

// TestRankedLiveCandidates_IgnoredSourceOffersNoFractional verifies the gate: a
// source the owner has flagged as a fractional re-uploader offers NO candidate
// for a fractional chapter.
func TestRankedLiveCandidates_IgnoredSourceOffersNoFractional(t *testing.T) {
	ctx := context.Background()
	client, s, ch := seedNumberedChapter(ctx, t, "ignored-fractional", "6.1", 6.1)
	addFractionalSource(ctx, t, client, s, "comic-asura", "6.1", 40, true)

	cands, err := chapter.RankedLiveCandidates(ctx, client, ch.ID, 3, time.Now())
	if err != nil {
		t.Fatalf("RankedLiveCandidates: %v", err)
	}
	if len(cands) != 0 {
		t.Fatalf("want 0 live candidates (the only source is a flagged re-uploader), got %d", len(cands))
	}
}

// TestRankedLiveCandidates_IgnoredSourceStillOffersWholes verifies the gate is
// narrow: the toggle suppresses a source's FRACTIONAL re-uploads, it does not
// disable the source. Its whole chapters remain candidates.
func TestRankedLiveCandidates_IgnoredSourceStillOffersWholes(t *testing.T) {
	ctx := context.Background()
	client, s, ch := seedNumberedChapter(ctx, t, "ignored-whole", "6", 6)
	addFractionalSource(ctx, t, client, s, "comic-asura", "6", 40, true)

	cands, err := chapter.RankedLiveCandidates(ctx, client, ch.ID, 3, time.Now())
	if err != nil {
		t.Fatalf("RankedLiveCandidates: %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("want 1 live candidate — a flagged source still offers WHOLE chapters, got %d", len(cands))
	}
	if cands[0].SeriesProvider.Provider != "comic-asura" {
		t.Errorf("want the flagged source (whole chapter), got %q", cands[0].SeriesProvider.Provider)
	}
}

// TestRankedLiveCandidates_FractionalFallsThroughToNonIgnoredSource verifies that
// excluding a flagged source does not orphan a fractional chapter another source
// legitimately carries: the non-flagged source remains the candidate.
func TestRankedLiveCandidates_FractionalFallsThroughToNonIgnoredSource(t *testing.T) {
	ctx := context.Background()
	client, s, ch := seedNumberedChapter(ctx, t, "fractional-fallthrough", "5.5", 5.5)
	// The flagged re-uploader outranks the clean source, so a broken gate would
	// return it first — the assertion below would then fail loudly.
	addFractionalSource(ctx, t, client, s, "comic-asura", "5.5", 60, true)
	addFractionalSource(ctx, t, client, s, "clean-source", "5.5", 20, false)

	cands, err := chapter.RankedLiveCandidates(ctx, client, ch.ID, 3, time.Now())
	if err != nil {
		t.Fatalf("RankedLiveCandidates: %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("want 1 live candidate (the non-flagged source), got %d", len(cands))
	}
	if cands[0].SeriesProvider.Provider != "clean-source" {
		t.Errorf("want the non-flagged source, got %q", cands[0].SeriesProvider.Provider)
	}
}

// TestAllProvidersExhausted_IgnoredSourceIsSourcelessNotExhausted pins a
// dangerous edge: a fractional chapter whose ONLY sources are flagged
// re-uploaders is SOURCELESS, not exhausted. AllProvidersExhausted must return
// FALSE — permanent failure is reserved for "every source tried and spent its
// budget". Perma-failing a chapter merely because the owner de-duplicated its
// source would be a serious bug (and permanently_failed is the state the
// dispatcher reaches by DELETING nothing but giving up).
func TestAllProvidersExhausted_IgnoredSourceIsSourcelessNotExhausted(t *testing.T) {
	ctx := context.Background()
	client, s, ch := seedNumberedChapter(ctx, t, "ignored-not-exhausted", "7.1", 7.1)
	addFractionalSource(ctx, t, client, s, "comic-asura", "7.1", 40, true)

	exhausted, err := chapter.AllProvidersExhausted(ctx, client, ch.ID, 3)
	if err != nil {
		t.Fatalf("AllProvidersExhausted: %v", err)
	}
	if exhausted {
		t.Fatal("want false — a chapter whose only sources are flagged re-uploaders is SOURCELESS, not exhausted (it must never be perma-failed)")
	}
}

// TestHasAnyProviderChapter_IgnoredSourceReportsNoSource pins the trap:
// HasAnyProviderChapter used to re-write the shared join's predicate inline as a
// Count(), so it would NOT have seen the ignore-fractional exclusion and would
// have reported a fully-suppressed fractional chapter as "has a source". The
// dispatcher branches on exactly this (handleNoCandidates), so it would have
// treated the chapter as merely COOLING DOWN rather than sourceless. Every reader
// of "does a source offer this chapter" must agree.
func TestHasAnyProviderChapter_IgnoredSourceReportsNoSource(t *testing.T) {
	ctx := context.Background()
	client, s, ch := seedNumberedChapter(ctx, t, "ignored-no-source", "8.1", 8.1)
	addFractionalSource(ctx, t, client, s, "comic-asura", "8.1", 40, true)

	has, err := chapter.HasAnyProviderChapter(ctx, client, ch.ID)
	if err != nil {
		t.Fatalf("HasAnyProviderChapter: %v", err)
	}
	if has {
		t.Fatal("want false — the only source offering this fractional chapter is a flagged re-uploader, so nothing offers it")
	}
}

// TestHasAnyProviderChapter_IgnoredSourceStillHasWholes is the counterpart: the
// same flagged source still OFFERS its whole chapters, so HasAnyProviderChapter
// reports true for a whole chapter. Without this, the test above would pass just
// as well against a gate that disabled the source entirely.
func TestHasAnyProviderChapter_IgnoredSourceStillHasWholes(t *testing.T) {
	ctx := context.Background()
	client, s, ch := seedNumberedChapter(ctx, t, "ignored-has-whole", "8", 8)
	addFractionalSource(ctx, t, client, s, "comic-asura", "8", 40, true)

	has, err := chapter.HasAnyProviderChapter(ctx, client, ch.ID)
	if err != nil {
		t.Fatalf("HasAnyProviderChapter: %v", err)
	}
	if !has {
		t.Fatal("want true — a flagged source still offers WHOLE chapters")
	}
}

// TestIgnoreFractional_DeletesNoRows is the never-auto-delete proof: the gate
// EXCLUDES ProviderChapter rows from candidacy, it never removes them. A full
// candidacy pass over a suppressed fractional chapter leaves the row count
// unchanged, so un-ticking the toggle restores the source immediately.
func TestIgnoreFractional_DeletesNoRows(t *testing.T) {
	ctx := context.Background()
	client, s, ch := seedNumberedChapter(ctx, t, "deletes-nothing", "9.1", 9.1)
	sp := addFractionalSource(ctx, t, client, s, "comic-asura", "9.1", 40, true)

	before := client.ProviderChapter.Query().CountX(ctx)

	// A full candidacy pass: every reader of the shared join.
	if _, err := chapter.RankedLiveCandidates(ctx, client, ch.ID, 3, time.Now()); err != nil {
		t.Fatalf("RankedLiveCandidates: %v", err)
	}
	if _, err := chapter.HasAnyProviderChapter(ctx, client, ch.ID); err != nil {
		t.Fatalf("HasAnyProviderChapter: %v", err)
	}
	if _, err := chapter.AllProvidersExhausted(ctx, client, ch.ID, 3); err != nil {
		t.Fatalf("AllProvidersExhausted: %v", err)
	}

	after := client.ProviderChapter.Query().CountX(ctx)
	if after != before {
		t.Fatalf("ProviderChapter rows: want %d (unchanged — the toggle deletes NOTHING), got %d", before, after)
	}

	// And un-ticking restores the source immediately, from the very same rows.
	client.SeriesProvider.UpdateOneID(sp.ID).SetIgnoreFractional(false).ExecX(ctx)
	cands, err := chapter.RankedLiveCandidates(ctx, client, ch.ID, 3, time.Now())
	if err != nil {
		t.Fatalf("RankedLiveCandidates after un-ticking: %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("want 1 live candidate after un-ticking the toggle (the rows were never deleted), got %d", len(cands))
	}
}

// TestRankedLiveCandidates_NumberlessChapterIsUnaffected pins the guard's entry
// condition: a chapter with no parsed number cannot be judged fractional, so the
// gate must not touch it (dropping it would silently orphan a chapter whose only
// crime is an unparseable number).
func TestRankedLiveCandidates_NumberlessChapterIsUnaffected(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	s := client.Series.Create().SetTitle("numberless").SetSlug("numberless").SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("extra").SaveX(ctx)
	addFractionalSource(ctx, t, client, s, "comic-asura", "extra", 40, true)

	cands, err := chapter.RankedLiveCandidates(ctx, client, ch.ID, 3, time.Now())
	if err != nil {
		t.Fatalf("RankedLiveCandidates: %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("want 1 live candidate — a number-less chapter is not fractional, so the gate must not fire, got %d", len(cands))
	}
}
