package library_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/library"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sse"
)

// TestDedupAllProviders_AggregatesAcrossSeries proves the sweep visits every
// series, sums merged/skipped from the per-series DedupProviders, and reports
// how many series it processed. A clean series (disk-origin provider only, no
// matching linked twin) contributes 0/0; a drifted series (disk-origin
// provider + an already-attached linked twin sharing the same provider name +
// scanlator, feed present) contributes its one merge; the sweep never aborts
// on one series.
func TestDedupAllProviders_AggregatesAcrossSeries(t *testing.T) {
	ctx := context.Background()
	storage := t.TempDir()

	// Drifted series: disk-origin provider ("mangadex"/"Alpha") that will get a
	// matching linked twin attached below (the source-identity drift dedup
	// exists to clean up).
	writeKaizokuSeries(t, storage, "Manga", "Drifted Series", "mangadex", "Alpha", 2)
	// Clean series: disk-origin provider only, no matching twin — a no-op pass.
	writeKaizokuSeries(t, storage, "Manga", "Clean Series", "weebcentral", "Beta", 2)

	client := testdb.New(t)

	facts, err := disk.ScanLibrary(storage)
	if err != nil {
		t.Fatalf("disk.ScanLibrary: %v", err)
	}
	if len(facts) != 2 {
		t.Fatalf("facts = %d, want 2", len(facts))
	}
	for _, sf := range facts {
		if _, err := disk.ReconcileOne(ctx, client, sf); err != nil {
			t.Fatalf("disk.ReconcileOne(%s): %v", sf.Title, err)
		}
	}

	drifted := findSeriesByTitle(t, client, ctx, "Drifted Series")

	// Attach the linked twin: same provider name + scanlator as the disk row,
	// with a non-empty ProviderChapter feed — an already-drifted pair.
	attachDriftedTwin(t, client, ctx, drifted.ID)

	seriesSvc := series.NewService(client, storage, 14)
	svc := library.NewService(client, nil, nil, seriesSvc, func() {}, storage, sse.NewHub())

	processed, merged, skipped, err := svc.DedupAllProviders(ctx)
	if err != nil {
		t.Fatalf("DedupAllProviders: %v", err)
	}
	if processed != 2 {
		t.Errorf("processed = %d, want 2", processed)
	}
	if merged != 1 {
		t.Errorf("merged = %d, want 1 (the drifted series' one pair)", merged)
	}
	if skipped != 0 {
		t.Errorf("skipped = %d, want 0", skipped)
	}
}

// findSeriesByTitle returns the series matching title, failing the test if
// none is found.
func findSeriesByTitle(t *testing.T, client *ent.Client, ctx context.Context, title string) *ent.Series {
	t.Helper()
	for _, s := range client.Series.Query().AllX(ctx) {
		if s.Title == title {
			return s
		}
	}
	t.Fatalf("series %q not found", title)
	return nil
}

// attachDriftedTwin creates a linked SeriesProvider ("99"/"mangadex"/
// "Alpha") on seriesID carrying a non-empty ProviderChapter feed for keys
// "1"/"2" — the same provider name + scanlator as the disk-origin row written
// by writeKaizokuSeries, i.e. an already-drifted (disk, live) pair. Provider
// is a numeric source id string — the live-provider marker under the new
// identity model (see series.IsLinkedProvider).
func attachDriftedTwin(t *testing.T, client *ent.Client, ctx context.Context, seriesID uuid.UUID) {
	t.Helper()
	live := client.SeriesProvider.Create().
		SetSeriesID(seriesID).
		SetProvider("99").
		SetProviderName("mangadex").
		SetScanlator("Alpha").
		SetImportance(5).
		SaveX(ctx)
	one, two := 1.0, 2.0
	client.ProviderChapter.Create().SetSeriesProviderID(live.ID).SetChapterKey("1").SetNumber(one).SaveX(ctx)
	client.ProviderChapter.Create().SetSeriesProviderID(live.ID).SetChapterKey("2").SetNumber(two).SaveX(ctx)
}

// TestDedupAllProviders_SkipsASeriesWhoseMergeLatchIsHeld is the GAP-120 proof
// for the library-wide sweep. Two merges running over ONE series' CBZs corrupt
// it: relabelMoveIntoPlace is idempotent, so the loser does not fail fast on the
// already-moved file — it proceeds, its commitMatch then fails on the
// already-deleted disk row, and its rollback renames every CBZ BACK while the
// winner's committed rows name the new file. The sweep must therefore take the
// same per-series latch every other merge path takes, and YIELD a series that is
// already held.
//
// Yielding must be surgical: the busy series is left byte-identical (same
// provider rows, same chapter state, same files on disk), fires no trigger, and
// the sweep walks straight on to the next series instead of aborting.
//
// FAILS on the unfixed code: DedupProviders took NO latch, so the sweep merged
// the held series regardless — the disk provider row is gone, both chapters are
// re-pointed and both CBZs renamed, exactly the concurrent-merge corruption this
// closes. (It also cannot compile there: DedupSweepEvent / the busy count did
// not exist.)
func TestDedupAllProviders_SkipsASeriesWhoseMergeLatchIsHeld(t *testing.T) {
	ctx := context.Background()
	storage := t.TempDir()
	client := testdb.New(t)
	hub := sse.NewHub()
	events, unsub := hub.Subscribe()
	t.Cleanup(unsub)

	busySer := importedDiskSeries(t, client, storage, "Busy Series", "mangadex", "Alpha")
	attachLinkedTwin(t, client, ctx, busySer.ID, "mangadex", "Alpha", 5, true)
	otherSer := importedDiskSeries(t, client, storage, "Other Series", "weebcentral", "Beta")
	attachLinkedTwin(t, client, ctx, otherSer.ID, "weebcentral", "Beta", 5, true)

	triggered := 0
	svc := healSvcWithHub(client, storage, &triggered, hub)

	// Stand in for an owner merge already in flight on the busy series.
	if !svc.AcquireMerge(busySer.ID) {
		t.Fatal("could not take the merge latch")
	}
	before := snapshotSeries(t, client, ctx, storage, busySer)

	// The other series is fully processed — one busy series never short-circuits
	// the sweep.
	if got, want := runSweep(t, svc, ctx), (sweepCounts{processed: 1, merged: 1}); got != want {
		t.Fatalf("sweep = %+v, want %+v (only the un-held series)", got, want)
	}
	assertProviderCount(t, client, ctx, otherSer.ID, 1)
	if triggered != 1 {
		t.Errorf("trigger fired %d time(s), want exactly 1 — the skipped series must not fire one", triggered)
	}

	// The busy series is untouched, down to the bytes on disk.
	if after := snapshotSeries(t, client, ctx, storage, busySer); after != before {
		t.Errorf("the busy series changed:\n before = %+v\n after  = %+v", before, after)
	}

	sweep := awaitDedupSweep(t, events)
	if sweep.Busy != 1 {
		t.Errorf("library.dedup.done busy = %d, want 1 — a skipped-busy series must be reported so the owner can re-run", sweep.Busy)
	}

	// The skip is a YIELD, not an abandonment: re-running catches it.
	svc.ReleaseMerge(busySer.ID)
	if got, want := runSweep(t, svc, ctx), (sweepCounts{processed: 2, merged: 1}); got != want {
		t.Fatalf("re-run sweep = %+v, want %+v (the previously busy series folded)", got, want)
	}
	assertProviderCount(t, client, ctx, busySer.ID, 1)
}

// TestDedupAllProviders_ThreeCountsStayIndependent pins the owner-facing meaning
// of every count the sweep reports, so a future change cannot silently redefine
// one by folding another into it. Four series drive all four numbers apart:
//
//   - a drifted pair with a fed twin  → merged, and counts as PROCESSED
//   - a drifted pair with an EMPTY-feed twin → skipped (the orphan guard), and
//     still counts as PROCESSED (the series was examined)
//   - a fully-linked series with nothing to fold → counts as PROCESSED. This is
//     the "how many series I looked at" contract: the sweep deliberately walks
//     EVERY series rather than only the drifted ones (unlike the recurring
//     self-heal, which targets), so narrowing its input would change this number.
//   - a drifted series whose merge latch is held → BUSY, and counts as NEITHER
//     processed nor skipped
//
// FAILS on the unfixed code: with no latch the held series is merged like any
// other, so processed = 4 and merged = 2; and busy has nowhere to be reported.
func TestDedupAllProviders_ThreeCountsStayIndependent(t *testing.T) {
	ctx := context.Background()
	storage := t.TempDir()
	client := testdb.New(t)
	hub := sse.NewHub()
	events, unsub := hub.Subscribe()
	t.Cleanup(unsub)

	mergeable := importedDiskSeries(t, client, storage, "Mergeable", "mangadex", "Alpha")
	attachLinkedTwin(t, client, ctx, mergeable.ID, "mangadex", "Alpha", 5, true)

	orphanGuarded := importedDiskSeries(t, client, storage, "No Feed Yet", "weebcentral", "Beta")
	attachLinkedTwin(t, client, ctx, orphanGuarded.ID, "weebcentral", "Beta", 5, false)

	// Nothing to fold at all — but still a series the sweep looked at.
	linkedOnly := client.Series.Create().SetTitle("All Linked").SetSlug("all-linked").SaveX(ctx)
	client.SeriesProvider.Create().SetSeriesID(linkedOnly.ID).SetProvider("7").SetImportance(10).SaveX(ctx)

	held := importedDiskSeries(t, client, storage, "Held", "comick", "Gamma")
	attachLinkedTwin(t, client, ctx, held.ID, "comick", "Gamma", 5, true)

	triggered := 0
	svc := healSvcWithHub(client, storage, &triggered, hub)
	if !svc.AcquireMerge(held.ID) {
		t.Fatal("could not take the merge latch")
	}
	t.Cleanup(func() { svc.ReleaseMerge(held.ID) })

	// processed counts the merged series, the orphan-guarded one AND the
	// nothing-to-do one — but never the busy one.
	got := runSweep(t, svc, ctx)
	if want := (sweepCounts{processed: 3, merged: 1, skipped: 1}); got != want {
		t.Errorf("sweep = %+v, want %+v (skipped = empty-feed PAIRS, never series skipped for being busy)", got, want)
	}

	sweep := awaitDedupSweep(t, events)
	if sweep.Busy != 1 {
		t.Errorf("busy = %d, want 1 — series skipped because a merge held their latch", sweep.Busy)
	}
	if pushed := (sweepCounts{sweep.SeriesProcessed, sweep.Merged, sweep.Skipped}); pushed != got {
		t.Errorf("the pushed summary %+v disagrees with the returned counts %+v", pushed, got)
	}
	if sweep.Error != "" {
		t.Errorf("a successful sweep pushed error %q", sweep.Error)
	}
}

// sweepCounts bundles DedupAllProviders' three returned counts so a test asserts
// all of them in ONE comparison — and so a future signature change cannot
// silently drop one from an assertion.
type sweepCounts struct {
	processed int
	merged    int
	skipped   int
}

// runSweep runs the library-wide sweep and returns its counts, failing the test
// on error.
func runSweep(t *testing.T, svc *library.Service, ctx context.Context) sweepCounts {
	t.Helper()
	processed, merged, skipped, err := svc.DedupAllProviders(ctx)
	if err != nil {
		t.Fatalf("DedupAllProviders: %v", err)
	}
	return sweepCounts{processed: processed, merged: merged, skipped: skipped}
}

// seriesSnapshot is a comparable fingerprint of everything a merge would change
// on one series: its provider rows, its chapters' identity/state, and the actual
// files in its library folder. Comparing two of them proves a skipped series was
// left byte-identical, not merely left with the same row count.
type seriesSnapshot struct {
	providers string
	chapters  string
	files     string
}

// snapshotSeries fingerprints a series' providers, chapter state and on-disk
// CBZs. Everything is sorted, so the result is order-independent.
func snapshotSeries(t *testing.T, client *ent.Client, ctx context.Context, storage string, ser *ent.Series) seriesSnapshot {
	t.Helper()

	var providers []string
	for _, p := range client.SeriesProvider.Query().Where(seriesprovider.SeriesID(ser.ID)).AllX(ctx) {
		providers = append(providers, p.ID.String()+"|"+p.Provider+"|"+p.ProviderName+"|"+p.Scanlator)
	}
	sort.Strings(providers)

	var chapters []string
	for _, ch := range client.Chapter.Query().Where(entchapter.SeriesID(ser.ID)).AllX(ctx) {
		satisfiedBy := "none"
		if ch.SatisfiedByProviderID != nil {
			satisfiedBy = ch.SatisfiedByProviderID.String()
		}
		chapters = append(chapters, ch.ChapterKey+"|"+ch.Filename+"|"+satisfiedBy+"|"+ch.State.String())
	}
	sort.Strings(chapters)

	var files []string
	entries, err := os.ReadDir(filepath.Join(storage, "Manga", ser.Title))
	if err != nil {
		t.Fatalf("read series folder: %v", err)
	}
	for _, e := range entries {
		files = append(files, e.Name())
	}
	sort.Strings(files)

	return seriesSnapshot{
		providers: strings.Join(providers, ","),
		chapters:  strings.Join(chapters, ","),
		files:     strings.Join(files, ","),
	}
}

// awaitDedupSweep drains the SSE stream until the terminal library.dedup.done
// summary arrives and returns its decoded payload. That event is the ONLY
// channel the detached sweep's outcome reaches the owner on — its own HTTP
// response is a bare 202 — so its absence is a failure, not a timing quirk.
func awaitDedupSweep(t *testing.T, events <-chan sse.Event) library.DedupSweepEvent {
	t.Helper()
	return awaitEvent[library.DedupSweepEvent](t, events, "library.dedup.done", 5*time.Second,
		"no library.dedup.done event — the detached sweep must push its summary or the owner never learns the outcome",
		nil)
}
