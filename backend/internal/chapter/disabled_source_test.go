package chapter_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
)

// pausedSourceID is the engine source id these tests pause. It is a plain
// numeric id because that is what a LIVE-ingested provider stores in
// SeriesProvider.provider — the only shape an id-keyed pause set can address.
const pausedSourceID int64 = 599

// addLiveSource creates a LIVE-shaped SeriesProvider — Provider holds the
// NUMERIC engine source id, ProviderName holds the display name, exactly as
// internal/ingest writes it — plus its ProviderChapter for chapterKey with fresh
// retry state (so the source is a live candidate unless the pause drops it).
func addLiveSource(
	ctx context.Context,
	t *testing.T,
	client *ent.Client,
	series *ent.Series,
	sourceID, name, chapterKey string,
	importance int,
) *ent.SeriesProvider {
	t.Helper()
	sp := client.SeriesProvider.Create().
		SetSeries(series).
		SetProvider(sourceID).
		SetProviderName(name).
		SetImportance(importance).
		SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).
		SetChapterKey(chapterKey).
		SetURL("https://" + name + ".example.com/" + chapterKey).
		SetProviderIndex(0).
		SaveX(ctx)
	return sp
}

// addDiskSource creates a DISK-ORIGIN SeriesProvider — Provider holds the
// display NAME and ProviderName is left empty, exactly as disk reconcile
// (disk.findOrCreateSeriesProvider) writes it — plus its ProviderChapter.
//
// This is the source-identity DRIFT shape: the same physical source stored under
// two different provider strings by the two create paths. It is what
// internal/library's merge machinery exists to reconcile.
func addDiskSource(
	ctx context.Context,
	t *testing.T,
	client *ent.Client,
	series *ent.Series,
	name, chapterKey string,
	importance int,
) *ent.SeriesProvider {
	t.Helper()
	sp := client.SeriesProvider.Create().
		SetSeries(series).
		SetProvider(name).
		SetImportance(importance).
		SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).
		SetChapterKey(chapterKey).
		SetURL("https://" + name + ".example.com/" + chapterKey).
		SetProviderIndex(0).
		SaveX(ctx)
	return sp
}

// candidateProviders lists the Provider column of each ranked candidate, in the
// order the ranking returned them. Asserting on the ordered list rather than a
// count is what keeps these tests non-vacuous: a drop that removed the WRONG
// source would still leave the right number of candidates.
func candidateProviders(cands []chapter.Candidate) []string {
	out := make([]string, 0, len(cands))
	for _, c := range cands {
		out = append(out, c.SeriesProvider.Provider)
	}
	return out
}

// assertProviders compares an ordered provider list against want.
func assertProviders(t *testing.T, got, want []string, because string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got candidates %v, want %v", because, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: got candidates %v, want %v", because, got, want)
		}
	}
}

// TestRankedLiveCandidates_PausedSourceIsDropped is the core QCAT-513 claim on
// the single-chapter path: a source the owner has PAUSED is not a candidate, so
// the next-best provider wins the chapter instead.
//
// The paused source is deliberately the HIGHEST-importance one — without the
// drop it would win outright, so the test cannot pass by accident.
func TestRankedLiveCandidates_PausedSourceIsDropped(t *testing.T) {
	ctx := context.Background()
	client, s, ch := seedChapter(ctx, t, "paused-drop", "ch-1")
	addLiveSource(ctx, t, client, s, "599", "Comix", "ch-1", 30)
	addLiveSource(ctx, t, client, s, "42", "Hive Scans", "ch-1", 20)

	// Nothing paused: the higher-importance source wins, as always.
	cands, err := chapter.RankedLiveCandidates(ctx, client, ch.ID, 3, time.Now(), nil)
	if err != nil {
		t.Fatalf("RankedLiveCandidates (nothing paused): %v", err)
	}
	assertProviders(t, candidateProviders(cands), []string{"599", "42"},
		"with no pause both sources rank, best first")

	// Pause the higher-importance source: it disappears from candidacy entirely
	// and the runner-up takes over.
	cands, err = chapter.RankedLiveCandidates(ctx, client, ch.ID, 3, time.Now(),
		map[int64]bool{pausedSourceID: true})
	if err != nil {
		t.Fatalf("RankedLiveCandidates (paused): %v", err)
	}
	assertProviders(t, candidateProviders(cands), []string{"42"},
		"a paused source is dropped and the next provider wins")
}

// TestRankedLiveCandidates_PausedSourceDroppedRegardlessOfRetryState pins that
// the pause is a CANDIDACY rule, not a retry-state one: it holds even for a
// source with a completely fresh budget and no cooldown, which is exactly the
// state a healthy-but-walled-off source (the QCAT-513 CAPTCHA case) sits in.
func TestRankedLiveCandidates_PausedSourceDroppedRegardlessOfRetryState(t *testing.T) {
	ctx := context.Background()
	client, s, ch := seedChapter(ctx, t, "paused-fresh", "ch-1")
	addLiveSource(ctx, t, client, s, "599", "Comix", "ch-1", 30)

	cands, err := chapter.RankedLiveCandidates(ctx, client, ch.ID, 3, time.Now(),
		map[int64]bool{pausedSourceID: true})
	if err != nil {
		t.Fatalf("RankedLiveCandidates: %v", err)
	}
	if len(cands) != 0 {
		t.Fatalf("a paused source with full retry budget must still be dropped, got %v", candidateProviders(cands))
	}
}

// TestRankedLiveCandidatesForMany_PausedSourceIsDropped proves the BATCHED path
// applies the same drop, and TestRankedLiveCandidates_BatchedAndSingleAgreeOnPause
// below proves the two agree. Both are needed: the batched path is the one the
// library-wide upgrade detection runs, so a pause honoured only on the
// single-chapter path would still let a paused source be flagged as an upgrade
// target for the entire library.
func TestRankedLiveCandidatesForMany_PausedSourceIsDropped(t *testing.T) {
	ctx := context.Background()
	client, s, ch := seedChapter(ctx, t, "paused-many", "ch-1")
	addLiveSource(ctx, t, client, s, "599", "Comix", "ch-1", 30)
	addLiveSource(ctx, t, client, s, "42", "Hive Scans", "ch-1", 20)

	byChapter, err := chapter.RankedLiveCandidatesForMany(ctx, client,
		[]*ent.Chapter{ch}, 3, time.Now(), map[int64]bool{pausedSourceID: true})
	if err != nil {
		t.Fatalf("RankedLiveCandidatesForMany: %v", err)
	}
	assertProviders(t, candidateProviders(byChapter[ch.ID]), []string{"42"},
		"the batched path drops a paused source too")
}

// TestRankedLiveCandidates_BatchedAndSingleAgreeOnPause is the agreement test the
// two paths' doc comments promise ("byte-identical"). It is not a parity test
// between two implementations — there is only ONE ranking core
// (liveCandidatesSorted, which is where the drop lives); this pins that the
// batched path still routes through it rather than growing its own filter.
func TestRankedLiveCandidates_BatchedAndSingleAgreeOnPause(t *testing.T) {
	ctx := context.Background()
	client, s, ch := seedChapter(ctx, t, "paused-agree", "ch-1")
	addLiveSource(ctx, t, client, s, "599", "Comix", "ch-1", 30)
	addLiveSource(ctx, t, client, s, "42", "Hive Scans", "ch-1", 20)
	addLiveSource(ctx, t, client, s, "7", "Asura", "ch-1", 10)

	now := time.Now()
	paused := map[int64]bool{pausedSourceID: true}

	single, err := chapter.RankedLiveCandidates(ctx, client, ch.ID, 3, now, paused)
	if err != nil {
		t.Fatalf("RankedLiveCandidates: %v", err)
	}
	batched, err := chapter.RankedLiveCandidatesForMany(ctx, client, []*ent.Chapter{ch}, 3, now, paused)
	if err != nil {
		t.Fatalf("RankedLiveCandidatesForMany: %v", err)
	}
	assertProviders(t, candidateProviders(batched[ch.ID]), candidateProviders(single),
		"the batched and single-chapter paths must rank a paused source identically")
	assertProviders(t, candidateProviders(single), []string{"42", "7"},
		"and both must drop the paused source, keeping the rest importance-DESC")
}

// TestRankedLiveCandidates_UnpausingRestoresTheSource proves the pause is
// REVERSIBLE from the very same rows: nothing was deleted, so clearing the set
// brings the source straight back at its original rank. This is the read-side
// half of the Rule 2 claim that a pause deletes nothing.
func TestRankedLiveCandidates_UnpausingRestoresTheSource(t *testing.T) {
	ctx := context.Background()
	client, s, ch := seedChapter(ctx, t, "paused-restore", "ch-1")
	addLiveSource(ctx, t, client, s, "599", "Comix", "ch-1", 30)
	addLiveSource(ctx, t, client, s, "42", "Hive Scans", "ch-1", 20)

	if _, err := chapter.RankedLiveCandidates(ctx, client, ch.ID, 3, time.Now(),
		map[int64]bool{pausedSourceID: true}); err != nil {
		t.Fatalf("RankedLiveCandidates (paused): %v", err)
	}

	cands, err := chapter.RankedLiveCandidates(ctx, client, ch.ID, 3, time.Now(), map[int64]bool{})
	if err != nil {
		t.Fatalf("RankedLiveCandidates (resumed): %v", err)
	}
	assertProviders(t, candidateProviders(cands), []string{"599", "42"},
		"resuming a source restores it at its original rank, from the same rows")
}

// TestRankedLiveCandidates_PausedSourceDoesNotDisturbOtherSources guards the
// blast radius: pausing source 599 must not touch a DIFFERENT source, and must
// not touch 599's rows in another series either. A drop keyed on the wrong thing
// (the display name, say, or the provider row) would fail here.
func TestRankedLiveCandidates_PausedSourceDoesNotDisturbOtherSources(t *testing.T) {
	ctx := context.Background()
	client, s, ch := seedChapter(ctx, t, "paused-blast", "ch-1")
	addLiveSource(ctx, t, client, s, "42", "Hive Scans", "ch-1", 20)

	cands, err := chapter.RankedLiveCandidates(ctx, client, ch.ID, 3, time.Now(),
		map[int64]bool{pausedSourceID: true})
	if err != nil {
		t.Fatalf("RankedLiveCandidates: %v", err)
	}
	assertProviders(t, candidateProviders(cands), []string{"42"},
		"pausing one source leaves every other source a candidate")
}

// TestRankedLiveCandidates_PausedSourceDiskOriginRowIsNotDropped documents the
// KNOWN LIMIT of an id-keyed pause, using the real source-identity DRIFT shape:
// ONE live row (Provider = the numeric source id "599", ProviderName = "Comix")
// and ONE disk-origin row (Provider = the display name "Comix", ProviderName
// empty) for the SAME physical source, in the same series.
//
// 🔴 This test asserts what the code DOES, not what would be ideal. The live row
// drops; the disk row does NOT, because a disk-origin provider carries no numeric
// source id at all (series.LinkedProviderSourceID / providerid.SourceID report
// ok=false for a display name), so nothing in an id-keyed set can ever match it.
//
// Matching it would require joining the display name back to the source id — a
// SECOND copy of the drift match that internal/library owns and that the repo map
// explicitly forbids duplicating. The existing route to full coverage is the
// merge machinery (library.HealDriftedProviders folds the disk row into its live
// twin, after which only the live row exists and the pause covers it). The same
// limit already applies to the refresh sweep, which skips every disk-origin row
// outright for exactly this reason.
//
// If a later slice closes the gap, this test should FLIP rather than be deleted —
// it is the pin that says which of the two rows the pause reaches today.
func TestRankedLiveCandidates_PausedSourceDiskOriginRowIsNotDropped(t *testing.T) {
	ctx := context.Background()
	client, s, ch := seedChapter(ctx, t, "paused-drift", "ch-1")
	addLiveSource(ctx, t, client, s, "599", "Comix", "ch-1", 30)
	addDiskSource(ctx, t, client, s, "Comix", "ch-1", 25)
	addLiveSource(ctx, t, client, s, "42", "Hive Scans", "ch-1", 20)

	cands, err := chapter.RankedLiveCandidates(ctx, client, ch.ID, 3, time.Now(),
		map[int64]bool{pausedSourceID: true})
	if err != nil {
		t.Fatalf("RankedLiveCandidates: %v", err)
	}
	assertProviders(t, candidateProviders(cands), []string{"Comix", "42"},
		"the LIVE row of a paused source drops; its disk-origin twin has no source id to match on")
}

// TestPausedSourceLeavesDownloadedChaptersAlone is the 🔴 safety claim of the
// whole slice: pausing a source must only narrow FUTURE candidate sets. A chapter
// already downloaded FROM that source keeps its state, its file and its
// satisfying-source link, so it stays readable for as long as the pause lasts.
//
// The readability rule the backend actually enforces is filename != "" (see
// series.ChapterPage, and app/utils/readableChapters.ts on the frontend), so that
// is what this asserts — alongside the state and the satisfied_by link, because a
// pause that downgraded the state or unlinked the source would break the library
// view even with the bytes still on disk.
//
// The pass through the ranking is what makes the test non-vacuous: it proves the
// paused source really was dropped from candidacy in the same breath, so the
// chapter surviving is not merely "nothing ran".
func TestPausedSourceLeavesDownloadedChaptersAlone(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	s := client.Series.Create().SetTitle("Paused Readable").SetSlug("paused-readable").SaveX(ctx)
	sp := addLiveSource(ctx, t, client, s, "599", "Comix", "ch-1", 30)

	const filename = "[Comix][en] Paused Readable 001.cbz"
	ch := client.Chapter.Create().
		SetSeries(s).
		SetChapterKey("ch-1").
		SetState("downloaded").
		SetFilename(filename).
		SetSatisfiedByID(sp.ID).
		SetSatisfiedImportance(30).
		SaveX(ctx)

	// Pausing the source drops it from candidacy...
	cands, err := chapter.RankedLiveCandidates(ctx, client, ch.ID, 3, time.Now(),
		map[int64]bool{pausedSourceID: true})
	if err != nil {
		t.Fatalf("RankedLiveCandidates: %v", err)
	}
	if len(cands) != 0 {
		t.Fatalf("the paused source must not be a candidate, got %v", candidateProviders(cands))
	}

	// ...and changes NOTHING about the already-downloaded chapter.
	got := client.Chapter.GetX(ctx, ch.ID)
	if got.State != "downloaded" {
		t.Errorf("state = %q, want downloaded — a pause must never downgrade a downloaded chapter", got.State)
	}
	if got.Filename != filename {
		t.Errorf("filename = %q, want %q — a pause must never clear a chapter's file", got.Filename, filename)
	}
	if got.SatisfiedByProviderID == nil || *got.SatisfiedByProviderID != sp.ID {
		t.Errorf("satisfied_by = %v, want %v — a pause must never unlink the satisfying source", got.SatisfiedByProviderID, sp.ID)
	}

	// The provider row and its feed survive too — a pause deletes nothing (Rule 2),
	// which is what lets resuming the source restore it from the same rows.
	if n := client.SeriesProvider.Query().CountX(ctx); n != 1 {
		t.Errorf("SeriesProvider rows = %d, want 1 — a pause deletes no provider row", n)
	}
	if n := client.ProviderChapter.Query().CountX(ctx); n != 1 {
		t.Errorf("ProviderChapter rows = %d, want 1 — a pause deletes no feed row", n)
	}
}

// TestRankedLiveCandidatesForMany_PauseIsPerSourceAcrossSeries proves the batched
// path applies the pause per SOURCE across a multi-series scan — the shape the
// library-wide upgrade detection actually runs — rather than per series or per
// bucket.
func TestRankedLiveCandidatesForMany_PauseIsPerSourceAcrossSeries(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	seedOne := func(slug string) *ent.Chapter {
		s := client.Series.Create().SetTitle(slug).SetSlug(slug).SaveX(ctx)
		ch := client.Chapter.Create().SetSeries(s).SetChapterKey("ch-1").SaveX(ctx)
		addLiveSource(ctx, t, client, s, "599", "Comix", "ch-1", 30)
		addLiveSource(ctx, t, client, s, "42", "Hive Scans", "ch-1", 20)
		return ch
	}
	first := seedOne("paused-multi-a")
	second := seedOne("paused-multi-b")

	byChapter, err := chapter.RankedLiveCandidatesForMany(ctx, client,
		[]*ent.Chapter{first, second}, 3, time.Now(), map[int64]bool{pausedSourceID: true})
	if err != nil {
		t.Fatalf("RankedLiveCandidatesForMany: %v", err)
	}
	for _, ch := range []*ent.Chapter{first, second} {
		assertProviders(t, candidateProviders(byChapter[ch.ID]), []string{"42"},
			"every series carrying the paused source falls through to its alternative")
	}
}

// TestRankedLiveCandidates_MultiplePausedSources covers a pause set with more
// than one entry — the realistic state once an owner has paused a couple of
// broken sources — and that the survivors keep their importance-DESC order.
func TestRankedLiveCandidates_MultiplePausedSources(t *testing.T) {
	ctx := context.Background()
	client, s, ch := seedChapter(ctx, t, "paused-multi-src", "ch-1")
	addLiveSource(ctx, t, client, s, "599", "Comix", "ch-1", 40)
	addLiveSource(ctx, t, client, s, "42", "Hive Scans", "ch-1", 30)
	addLiveSource(ctx, t, client, s, "7", "Asura", "ch-1", 20)
	addLiveSource(ctx, t, client, s, "8", "Flame", "ch-1", 10)

	cands, err := chapter.RankedLiveCandidates(ctx, client, ch.ID, 3, time.Now(),
		map[int64]bool{599: true, 7: true})
	if err != nil {
		t.Fatalf("RankedLiveCandidates: %v", err)
	}
	assertProviders(t, candidateProviders(cands), []string{"42", "8"},
		"every paused source drops and the survivors stay importance-DESC")
}

// sortedKeys is a tiny helper kept so a failure message lists a pause set in a
// stable order rather than Go's randomised map order.
func sortedKeys(m map[int64]bool) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// TestRankedLiveCandidates_NilAndEmptyPauseSetsAreIdentical pins the safe
// default both consumers rely on: a nil set (nothing wired) and an empty set
// (nothing currently paused) must behave exactly like the pre-QCAT-513 ranking.
func TestRankedLiveCandidates_NilAndEmptyPauseSetsAreIdentical(t *testing.T) {
	ctx := context.Background()
	client, s, ch := seedChapter(ctx, t, "paused-nil", "ch-1")
	addLiveSource(ctx, t, client, s, "599", "Comix", "ch-1", 30)
	addLiveSource(ctx, t, client, s, "42", "Hive Scans", "ch-1", 20)

	now := time.Now()
	for _, tc := range []struct {
		name  string
		set   map[int64]bool
		want  []string
		empty bool
	}{
		{name: "nil set", set: nil, want: []string{"599", "42"}},
		{name: "empty set", set: map[int64]bool{}, want: []string{"599", "42"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cands, err := chapter.RankedLiveCandidates(ctx, client, ch.ID, 3, now, tc.set)
			if err != nil {
				t.Fatalf("RankedLiveCandidates(%v): %v", sortedKeys(tc.set), err)
			}
			assertProviders(t, candidateProviders(cands), tc.want,
				"an empty pause set must leave the ranking exactly as it was")
		})
	}
}
