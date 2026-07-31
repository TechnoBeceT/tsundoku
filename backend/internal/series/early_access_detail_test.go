package series_test

import (
	"context"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/series"
)

// TestGetSeriesMarksEarlyAccessChapters pins the series-detail read of a chapter a
// source is WITHHOLDING behind a paywall / early-access window (GAP-141). Such a
// chapter rests in `failed` because the fetch did not produce a file, but it is not
// broken and needs no owner action — the source releases it on its own — so the
// detail DTO must say so rather than leaving the row indistinguishable from a real
// failure. The classification is derived from the stored per-source last_error; no
// column and no chapter state is added.
func TestGetSeriesMarksEarlyAccessChapters(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()

	sr := client.Series.Create().
		SetTitle("Coin Gate").
		SetSlug("coin-gate").
		SetCategoryID(catID(ctx, client, "Manhwa")).
		SaveX(ctx)

	locked, broken, fine := 3.0, 4.0, 5.0
	for key, num := range map[string]float64{"cg-3": locked, "cg-4": broken, "cg-5": fine} {
		state := entchapter.StateFailed
		if key == "cg-5" {
			state = entchapter.StateWanted
		}
		client.Chapter.Create().
			SetSeriesID(sr.ID).SetChapterKey(key).SetNumber(num).
			SetState(state).SaveX(ctx)
	}

	sp := client.SeriesProvider.Create().
		SetSeriesID(sr.ID).SetProvider("42").SetProviderName("Hive Scans").
		SetLanguage("en").SetImportance(30).SaveX(ctx)

	// cg-3: withheld — the deferral the engine wrote is still in force, and the
	// per-source budget is deliberately UNSPENT (a paywall never charges attempts).
	// Truncated to Postgres' microsecond resolution so the round-tripped instant is
	// byte-comparable (a nanosecond fixture would come back rounded).
	until := time.Now().UTC().Add(60 * time.Hour).Truncate(time.Microsecond)
	client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).SetChapterKey("cg-3").
		SetLastError("upstream error: Chapter locked, coins required").
		SetNextAttemptAt(until).SaveX(ctx)
	// cg-4: a genuine failure on the same source, also inside a backoff.
	client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).SetChapterKey("cg-4").SetAttempts(2).
		SetLastError("connection reset by peer").
		SetNextAttemptAt(time.Now().UTC().Add(30 * time.Minute)).SaveX(ctx)
	// cg-5: nothing wrong at all.
	client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).SetChapterKey("cg-5").SaveX(ctx)

	svc := series.NewService(client, t.TempDir(), 14)
	got, err := svc.GetSeries(ctx, sr.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}

	byKey := map[string]series.ChapterDTO{}
	for _, ch := range got.Chapters {
		byKey[ch.ChapterKey] = ch
	}

	assertEarlyAccess(t, byKey["cg-3"], until)
	// The state itself is untouched — this is a presentation-level derivation.
	if byKey["cg-3"].State != string(entchapter.StateFailed) {
		t.Errorf("cg-3 State = %q, want %q", byKey["cg-3"].State, entchapter.StateFailed)
	}
	for _, key := range []string{"cg-4", "cg-5"} {
		assertNotEarlyAccess(t, key, byKey[key])
	}
}

// assertEarlyAccess asserts a chapter row reads as WITHHELD until the given instant.
func assertEarlyAccess(t *testing.T, ch series.ChapterDTO, until time.Time) {
	t.Helper()
	if !ch.Locked {
		t.Errorf("%s Locked = false, want true (source is withholding it)", ch.ChapterKey)
	}
	if ch.LockedUntil == nil {
		t.Fatalf("%s LockedUntil = nil, want %v", ch.ChapterKey, until)
	}
	if !ch.LockedUntil.Equal(until) {
		t.Errorf("%s LockedUntil = %v, want %v", ch.ChapterKey, *ch.LockedUntil, until)
	}
}

// assertNotEarlyAccess asserts a chapter row carries no early-access marker at all.
func assertNotEarlyAccess(t *testing.T, key string, ch series.ChapterDTO) {
	t.Helper()
	if ch.Locked {
		t.Errorf("%s Locked = true, want false (not an early-access wait)", key)
	}
	if ch.LockedUntil != nil {
		t.Errorf("%s LockedUntil = %v, want nil", key, *ch.LockedUntil)
	}
}

// TestGetSeriesNeverMarksAChapterThatIsOnDisk is the INTEGRATION pin for the
// on-disk suppression (GAP-141). The unit test on EarlyAccessUnlessSettled proves
// the helper; this proves the read model actually calls it — without this, the
// whole suppression can be deleted from GetSeries and the suite stays green,
// which is exactly the blind spot that let the original defect ship.
//
// The reachable shape is a convergence UPGRADE: the chapter is already downloaded
// from a free mirror, a higher-importance source is flagged as an upgrade, that
// upgrade fetch comes back "Chapter locked (coins required)", and deferSource
// parks the BETTER source while the chapter itself rests on disk. The feed row is
// genuinely withheld — but the file is readable now, so presenting it as "waiting
// for early access" (and, in the UI, replacing its state badge) hides it.
func TestGetSeriesNeverMarksAChapterThatIsOnDisk(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	until := time.Now().UTC().Add(60 * time.Hour).Truncate(time.Microsecond)

	s := client.Series.Create().SetTitle("Coin Gate").SetSlug("coin-gate-disk").
		SetCategoryID(catID(ctx, client, "Manhwa")).SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).
		SetProvider("42").SetProviderName("Hive Scans").SetImportance(30).SaveX(ctx)

	num := 7.0
	// The BETTER source is withheld …
	client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey("cg-7").
		SetNillableNumber(&num).SetURL("https://hive/cg-7").SetProviderIndex(0).
		SetLastError("upstream error: Chapter locked, coins required").
		SetNextAttemptAt(until).SaveX(ctx)
	// … while the chapter itself is already on disk from a free mirror.
	client.Chapter.Create().SetSeries(s).SetChapterKey("cg-7").SetNillableNumber(&num).
		SetState(entchapter.StateDownloaded).
		SetFilename("[Comix][en] Coin Gate 007.cbz").SaveX(ctx)

	got, err := series.NewService(client, t.TempDir(), 14).GetSeries(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if len(got.Chapters) != 1 {
		t.Fatalf("chapters = %d, want 1", len(got.Chapters))
	}
	if ch := got.Chapters[0]; ch.Locked || ch.LockedUntil != nil {
		t.Errorf("locked=%v lockedUntil=%v for a chapter on disk (%q), want false/nil — "+
			"a readable file must keep its own state badge", ch.Locked, ch.LockedUntil, ch.Filename)
	}
}

// TestGetSeriesNeverMarksASupersededChapter is the INTEGRATION pin for the
// PARKED-state half of the suppression (GAP-141). A superseded split part has NO
// file — download.supersedeOnePart deletes its CBZ and clears Chapter.filename —
// so the file test alone cannot see it, and the row rendered "Early access · free
// ~3d" INSTEAD of its Superseded state badge while nothing was ever going to
// fetch it.
//
// The reachable shape: the part arrives from a free mirror, a higher-importance
// source is flagged as an upgrade, that upgrade fetch comes back "Chapter locked
// (coins required)" so deferSource parks the better source, and the whole chapter
// then lands — DetectSupersededParts supersedes the downloaded part, removes its
// CBZ and clears the filename, leaving the withheld feed row behind.
func TestGetSeriesNeverMarksASupersededChapter(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	until := time.Now().UTC().Add(60 * time.Hour).Truncate(time.Microsecond)

	s := client.Series.Create().SetTitle("Coin Gate").SetSlug("coin-gate-superseded").
		SetCategoryID(catID(ctx, client, "Manhwa")).SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).
		SetProvider("42").SetProviderName("Hive Scans").SetImportance(30).SaveX(ctx)

	num := 8.1
	// The source is still withholding the part …
	client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey("cg-8-1").
		SetNillableNumber(&num).SetURL("https://hive/cg-8-1").SetProviderIndex(0).
		SetLastError("upstream error: Chapter locked, coins required").
		SetNextAttemptAt(until).SaveX(ctx)
	// … while the part itself was superseded by its whole: no state badge but its
	// own, and NO filename, because supersedeOnePart cleared it.
	client.Chapter.Create().SetSeries(s).SetChapterKey("cg-8-1").SetNillableNumber(&num).
		SetState(entchapter.StateSuperseded).SaveX(ctx)

	got, err := series.NewService(client, t.TempDir(), 14).GetSeries(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if len(got.Chapters) != 1 {
		t.Fatalf("chapters = %d, want 1", len(got.Chapters))
	}
	if ch := got.Chapters[0]; ch.Locked || ch.LockedUntil != nil {
		t.Errorf("locked=%v lockedUntil=%v for a superseded chapter, want false/nil — "+
			"nothing will fetch a parked chapter, so it must keep its own state badge",
			ch.Locked, ch.LockedUntil)
	}
}
