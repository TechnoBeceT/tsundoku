// Package download_test — proof that the convergence-upgrade pass is bounded
// PER SOURCE (not by one global pool): chapters whose upgrade targets are
// DIFFERENT sources progress in parallel, while no single source ever runs more
// than DownloadConcurrency upgrades at once (the anti-ban invariant).
package download_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sse"
)

// upgradeTargetPrefix marks the high-importance providers an upgrade targets, so
// the fetcher can treat the initial (low-provider) downloads as instant and only
// instrument the upgrade fetches.
const upgradeTargetPrefix = "high-"

// concurrencyFetcher instruments upgrade fetches: it records the maximum number of
// simultaneous in-flight fetches PER SOURCE, and holds every upgrade fetch open
// until `want` DISTINCT sources are in flight AT THE SAME TIME (the barrier) — or
// until a timeout releases them.
//
// The barrier is the cross-source-parallelism proof: under the old GLOBAL limit,
// concurrency slots were consumed by whichever source got there first, so fewer
// than `want` distinct sources could ever be in flight together and the barrier
// would never fire. maxPerSource is the politeness proof: it must never exceed the
// per-source cap.
type concurrencyFetcher struct {
	mu           sync.Mutex
	inFlight     map[string]int
	maxPerSource map[string]int
	want         int
	barrier      chan struct{}
	barrierOnce  sync.Once
	hold         time.Duration
}

// newConcurrencyFetcher builds a fetcher whose barrier fires once `want` distinct
// sources are fetching simultaneously. hold bounds how long a fetch waits on the
// barrier before giving up, so a test that fails the parallelism property still
// terminates.
func newConcurrencyFetcher(want int, hold time.Duration) *concurrencyFetcher {
	return &concurrencyFetcher{
		inFlight:     map[string]int{},
		maxPerSource: map[string]int{},
		want:         want,
		barrier:      make(chan struct{}),
		hold:         hold,
	}
}

// Fetch returns a one-page chapter. Fetches from a low (non-upgrade-target)
// provider return instantly — they only exist to seed the downloaded state.
// Fetches from an upgrade-target provider are instrumented and parked on the
// barrier so the test can observe the true simultaneous concurrency.
func (f *concurrencyFetcher) Fetch(ctx context.Context, ref fetcher.FetchRef) (fetcher.ChapterPages, error) {
	page := fetcher.ChapterPages{
		Pages:     []fetcher.PageImage{{Data: []byte{0x01}, Ext: "jpg"}},
		PageCount: 1,
	}
	if !strings.HasPrefix(ref.Provider, upgradeTargetPrefix) {
		return page, nil
	}

	f.enter(ref.Provider)
	defer f.leave(ref.Provider)

	select {
	case <-f.barrier:
	case <-time.After(f.hold):
	case <-ctx.Done():
		return fetcher.ChapterPages{}, ctx.Err()
	}
	return page, nil
}

// enter records the start of an upgrade fetch: it bumps the source's in-flight
// count (tracking its high-water mark) and releases the barrier the moment `want`
// distinct sources are fetching at the same time.
func (f *concurrencyFetcher) enter(source string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inFlight[source]++
	if f.inFlight[source] > f.maxPerSource[source] {
		f.maxPerSource[source] = f.inFlight[source]
	}
	if len(f.inFlight) >= f.want {
		f.barrierOnce.Do(func() { close(f.barrier) })
	}
}

// leave records the end of an upgrade fetch. The source's key is REMOVED at zero so
// len(inFlight) means "sources fetching right now" — the barrier must therefore be
// satisfied by genuinely SIMULTANEOUS sources, not by sources that took turns.
func (f *concurrencyFetcher) leave(source string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inFlight[source]--
	if f.inFlight[source] == 0 {
		delete(f.inFlight, source)
	}
}

// parallelismReached reports whether `want` distinct sources were ever fetching at
// the same instant.
func (f *concurrencyFetcher) parallelismReached() bool {
	select {
	case <-f.barrier:
		return true
	default:
		return false
	}
}

// maxima returns a snapshot of the per-source high-water concurrency marks.
func (f *concurrencyFetcher) maxima() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]int, len(f.maxPerSource))
	for k, v := range f.maxPerSource {
		out[k] = v
	}
	return out
}

// TestUpgradeAll_PerSourceParallelism is the throughput + politeness proof.
//
// Setup: three series, each downloaded from its own low-importance source, each
// then offered a DIFFERENT strictly-higher source (high-A / high-B / high-C) that
// carries every chapter — so the flagged upgrades target three distinct sources,
// chaptersPerSource each. With DownloadConcurrency=2 this asserts:
//
//   - CROSS-SOURCE PARALLELISM: all three target sources are fetching AT THE SAME
//     INSTANT (the fetcher's barrier fires). The pre-fix global limit of 2 could
//     never have more than two sources in flight, so this assertion is what the bug
//     fails.
//   - PER-SOURCE CEILING: no source ever exceeds 2 concurrent upgrade fetches, i.e.
//     no single source is hit harder than it was before the change.
//   - The upgrades still complete: every chapter ends downloaded, satisfied by its
//     high source, and the count returned equals the number of flagged chapters.
func TestUpgradeAll_PerSourceParallelism(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		concurrency       = 2
		sources           = 3
		chaptersPerSource = 3
	)

	f := newConcurrencyFetcher(sources, 5*time.Second)
	d := download.New(client, f, sse.NewHub(), download.Config{Storage: mustTempDir(t)},
		settings.Static{Retries: 3, Backoff: time.Hour, DownloadConc: concurrency}, nil)

	// Seed three series, each downloaded from its own low source.
	var chapterIDs []uuid.UUID
	seriesRows := make([]*ent.Series, 0, sources)
	for _, name := range []string{"A", "B", "C"} {
		s, ids := seedDownloadableSeries(ctx, t, client, "upg-"+name, "low-"+name, chaptersPerSource)
		seriesRows = append(seriesRows, s)
		chapterIDs = append(chapterIDs, ids...)
	}
	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("initial RunOnce: %v", err)
	}
	assertAllInState(ctx, t, client, chapterIDs, entchapter.StateDownloaded)

	// Attach a DIFFERENT strictly-higher source to each series and flag the upgrades.
	for i, s := range seriesRows {
		attachHigherProvider(ctx, t, client, s, upgradeTargetPrefix+string(rune('A'+i)), chaptersPerSource)
	}
	flagged, err := d.DetectUpgrades(ctx, d.MaxRetries(ctx))
	if err != nil {
		t.Fatalf("DetectUpgrades: %v", err)
	}
	if flagged != sources*chaptersPerSource {
		t.Fatalf("DetectUpgrades flagged %d, want %d", flagged, sources*chaptersPerSource)
	}

	// nil downloadsConsumed: no downloads ran this cycle, so every target source
	// gets its full per-cycle budget (batchPerSource(2)=4 >= 3 chapters/source),
	// leaving this test's per-source-parallelism assertions unchanged.
	upgraded, err := d.UpgradeAll(ctx, nil, nil)
	if err != nil {
		t.Fatalf("UpgradeAll: %v", err)
	}

	assertPerSourceScheduling(t, f, sources, concurrency)
	if upgraded != sources*chaptersPerSource {
		t.Errorf("UpgradeAll upgraded %d chapters, want %d", upgraded, sources*chaptersPerSource)
	}
	assertAllInState(ctx, t, client, chapterIDs, entchapter.StateDownloaded)
	assertSatisfiedByPrefix(ctx, t, client, chapterIDs, upgradeTargetPrefix)
}

type gatedNumericUpgradeFetcher struct {
	mu       sync.Mutex
	started  chan string
	release  chan struct{}
	inFlight map[string]int
	maxima   map[string]int
}

func newGatedNumericUpgradeFetcher() *gatedNumericUpgradeFetcher {
	return &gatedNumericUpgradeFetcher{started: make(chan string, 16), release: make(chan struct{}), inFlight: map[string]int{}, maxima: map[string]int{}}
}

func (f *gatedNumericUpgradeFetcher) Fetch(ctx context.Context, ref fetcher.FetchRef) (fetcher.ChapterPages, error) {
	page := fetcher.ChapterPages{Pages: []fetcher.PageImage{{Data: []byte{1}, Ext: "jpg"}}, PageCount: 1}
	if ref.Provider != "101" && ref.Provider != "202" {
		return page, nil
	}
	f.mu.Lock()
	f.inFlight[ref.Provider]++
	f.maxima[ref.Provider] = max(f.maxima[ref.Provider], f.inFlight[ref.Provider])
	f.mu.Unlock()
	f.started <- ref.Provider
	select {
	case <-f.release:
	case <-ctx.Done():
		return fetcher.ChapterPages{}, ctx.Err()
	}
	f.mu.Lock()
	f.inFlight[ref.Provider]--
	f.mu.Unlock()
	return page, nil
}

func TestUpgradeAll_SourceOverrideControlsSchedulingAndAdmission(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	const chapters = 4
	f := newGatedNumericUpgradeFetcher()
	one := 1
	d := download.New(client, f, sse.NewHub(), download.Config{Storage: mustTempDir(t)}, settings.Static{Retries: 3, Backoff: time.Hour, DownloadConc: 3}, nil).
		WithSourceThroughputPolicies(staticThroughputOverrides{101: {DownloadConcurrency: &one}})

	for i, target := range []string{"101", "202"} {
		seedSourceOverrideUpgrade(t, ctx, client, d, target, i, chapters)
	}

	done := make(chan error, 1)
	go func() { _, err := d.UpgradeAll(ctx, nil, nil); done <- err }()
	waitForUpgradeStarts(t, f.started, 4) // source 101 override=1 plus source 202 inherited=3
	f.mu.Lock()
	if f.maxima["101"] != 1 || f.maxima["202"] != 3 {
		t.Errorf("upgrade maxima = %v, want 101=1 and 202=3", f.maxima)
	}
	f.mu.Unlock()
	close(f.release)
	if err := <-done; err != nil {
		t.Fatalf("UpgradeAll: %v", err)
	}
}

func seedSourceOverrideUpgrade(t *testing.T, ctx context.Context, client *ent.Client, d *download.Dispatcher, target string, sourceIndex, chapters int) {
	t.Helper()
	s, ids := seedDownloadableSeries(ctx, t, client, "override-upg-"+target, "low-"+target, chapters)
	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("initial RunOnce source %s: %v", target, err)
	}
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider(target).SetProviderName("target-" + target).SetImportance(10).SaveX(ctx)
	for n := range chapters {
		number := float64(n + 1)
		client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey(s.Slug + "-" + itoa(n+1)).SetNillableNumber(&number).SetURL("https://target/" + target).SetProviderIndex(n).SaveX(ctx)
	}
	if flagged, err := d.DetectUpgrades(ctx, d.MaxRetries(ctx)); err != nil || flagged != len(ids) {
		t.Fatalf("DetectUpgrades source %d = %d, %v; want %d", sourceIndex, flagged, err, len(ids))
	}
}

func waitForUpgradeStarts(t *testing.T, started <-chan string, count int) {
	t.Helper()
	for range count {
		select {
		case <-started:
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for upgrade admission")
		}
	}
}

// assertPerSourceScheduling asserts the two halves of the per-source contract:
// sources make progress CONCURRENTLY (the barrier fired — the throughput fix), and
// no single source exceeded the per-source concurrency cap (the anti-ban invariant).
func assertPerSourceScheduling(t *testing.T, f *concurrencyFetcher, sources, concurrency int) {
	t.Helper()
	if !f.parallelismReached() {
		t.Errorf("BUG: the %d upgrade-target sources never fetched simultaneously — upgrades are still serialised behind one global pool (per-source maxima: %v)",
			sources, f.maxima())
	}
	for source, got := range f.maxima() {
		if got > concurrency {
			t.Errorf("source %s ran %d concurrent upgrade fetches, per-source cap is %d — a single source got MORE aggressive", source, got, concurrency)
		}
	}
}

// seedDownloadableSeries creates a series with ONE low-importance provider offering
// n numbered chapters (all wanted), and returns the series plus the chapter ids.
func seedDownloadableSeries(ctx context.Context, t *testing.T, client *ent.Client, slug, provider string, n int) (*ent.Series, []uuid.UUID) {
	t.Helper()
	s := client.Series.Create().SetTitle(slug).SetSlug(slug).SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider(provider).SetImportance(2).SaveX(ctx)
	return s, seedProviderChapters(ctx, t, client, s, sp, slug, provider, n)
}

// attachHigherProvider adds a strictly-higher-importance provider to an existing
// series and gives it a feed for the same n chapter keys, so every one of the
// series' chapters becomes upgrade-eligible against that ONE source.
func attachHigherProvider(ctx context.Context, t *testing.T, client *ent.Client, s *ent.Series, provider string, n int) {
	t.Helper()
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider(provider).SetImportance(10).SaveX(ctx)
	for i := range n {
		num := float64(i + 1)
		client.ProviderChapter.Create().
			SetSeriesProviderID(sp.ID).
			SetChapterKey(s.Slug + "-" + itoa(i+1)).
			SetNillableNumber(&num).
			SetURL("https://" + provider + "/" + s.Slug + "/" + itoa(i+1)).
			SetProviderIndex(i).
			SaveX(ctx)
	}
}

// assertAllInState fails the test unless every given chapter is in want.
func assertAllInState(ctx context.Context, t *testing.T, client *ent.Client, ids []uuid.UUID, want entchapter.State) {
	t.Helper()
	for _, id := range ids {
		if got := client.Chapter.GetX(ctx, id).State; got != want {
			t.Fatalf("chapter %s state = %s, want %s", id, got, want)
		}
	}
}

// assertSatisfiedByPrefix asserts every chapter is now satisfied by a source whose
// provider string carries the given prefix — i.e. the upgrade really swapped the
// provenance to the high source, not just flipped the state back.
func assertSatisfiedByPrefix(ctx context.Context, t *testing.T, client *ent.Client, ids []uuid.UUID, prefix string) {
	t.Helper()
	for _, id := range ids {
		ch := client.Chapter.GetX(ctx, id)
		if ch.SatisfiedByProviderID == nil {
			t.Fatalf("chapter %s has no satisfied_by after upgrade", id)
		}
		sp := client.SeriesProvider.GetX(ctx, *ch.SatisfiedByProviderID)
		if !strings.HasPrefix(sp.Provider, prefix) {
			t.Errorf("chapter %s satisfied by %q, want an upgrade target (prefix %q)", id, sp.Provider, prefix)
		}
	}
}
