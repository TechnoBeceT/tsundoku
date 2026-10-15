package job_test

import (
	"context"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/technobecet/tsundoku/internal/ent"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/downloads"
	"github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/ent/providerchapter"
	"github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/job"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sse"
)

func TestRunner_SourcePriorityUnlaunchedOrdinaryYields(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := testdb.New(t)
	seedMidCycleSourceBacklog(ctx, t, client, "priority-ordinary", "slow", 4)
	f := &refillFetcher{started: make(chan string, 100), refs: make(chan fetcher.FetchRef, 100), release: make(chan struct{})}
	storage := t.TempDir()
	hub := sse.NewHub()
	d := download.New(client, f, hub, download.Config{Storage: storage}, settings.Static{Retries: 3, DownloadConc: 1, MaxConcurrentDl: 3}, nil)
	r := job.NewRunner(d, client, hub, storage, settings.Static{Download: 90 * time.Second})
	joinRefillRunner(t, r, cancel, f)
	r.Start(ctx)
	r.Trigger()
	awaitRefillSource(t, f, "slow")
	first := <-f.refs
	seedMidCycleSourceBacklog(ctx, t, client, "another-ordinary", "slow", 1)
	selected := seedMidCycleSourceBacklog(ctx, t, client, "priority-selected", "slow", 2)
	client.Chapter.Update().Where(chapter.SeriesIDEQ(selected.ID)).SetNumber(100).ExecX(ctx)
	sp := client.SeriesProvider.Query().Where(seriesprovider.SeriesIDEQ(selected.ID)).OnlyX(ctx)
	if _, err := downloads.NewService(client).SetPriority(ctx, selected.ID, true); err != nil {
		t.Fatal(err)
	}
	r.Trigger()
	time.Sleep(100 * time.Millisecond)
	select {
	case <-f.refs:
		t.Fatal("priority overlapped active same-source fetch")
	default:
	}
	f.open()
	awaitRefillSource(t, f, "slow")
	second := <-f.refs
	if second.SeriesProviderID != sp.ID {
		t.Fatalf("ordinary reservation ran before prioritized series: first %s, next %s, priority %s", first.SeriesProviderID, second.SeriesProviderID, sp.ID)
	}
}

func TestRunner_SourcePriorityUpgradeBeforeOrdinaryDownloadBeyondPrefix(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	seedMidCycleSourceBacklog(ctx, t, client, "ordinary-prefix", "fast", 1001)
	selected := seedMidCycleSourceBacklog(ctx, t, client, "marked-upgrade", "fast", 1)
	low := client.SeriesProvider.Create().SetSeries(selected).SetProvider("low").SetImportance(1).SaveX(ctx)
	client.Chapter.Update().Where(chapter.SeriesIDEQ(selected.ID)).SetNumber(2000).SetState(chapter.StateUpgradeAvailable).SetFilename("existing.cbz").SetSatisfiedImportance(1).SetSatisfiedByProviderID(low.ID).SetDownloadPriority(true).ExecX(ctx)
	sp := client.SeriesProvider.Query().Where(seriesprovider.SeriesIDEQ(selected.ID), seriesprovider.ProviderEQ("fast")).OnlyX(ctx)
	f := &refillFetcher{started: make(chan string, 100), refs: make(chan fetcher.FetchRef, 100), release: make(chan struct{})}
	storage := t.TempDir()
	hub := sse.NewHub()
	d := download.New(client, f, hub, download.Config{Storage: storage}, settings.Static{Retries: 3, DownloadConc: 1, MaxConcurrentDl: 3}, nil)
	r := job.NewRunner(d, client, hub, storage, settings.Static{})
	if err := r.RunDownloadCycle(ctx); err != nil {
		t.Fatal(err)
	}
	first := <-f.refs
	if first.SeriesProviderID != sp.ID {
		t.Fatalf("ordinary download preceded marked high-number upgrade: %s", first.SeriesProviderID)
	}
}

func TestRunner_SourcePriorityBeyondMarkedOtherSourcePrefix(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := testdb.New(t)
	prefix := seedMidCycleSourceBacklog(ctx, t, client, "marked-prefix", "slow", 1000)
	low := client.SeriesProvider.Create().SetSeries(prefix).SetProvider("low-prefix").SetImportance(1).SaveX(ctx)
	client.Chapter.Update().Where(chapter.SeriesIDEQ(prefix.ID)).SetState(chapter.StateUpgradeAvailable).SetFilename("prefix.cbz").SetSatisfiedImportance(1).SetSatisfiedByProviderID(low.ID).SetDownloadPriority(true).ExecX(ctx)
	seedMidCycleSourceBacklog(ctx, t, client, "ordinary-a", "fast", 1)
	selected := seedMidCycleSourceBacklog(ctx, t, client, "marked-a", "fast", 1)
	lower := client.SeriesProvider.Create().SetSeries(selected).SetProvider("low-a").SetImportance(1).SaveX(ctx)
	client.Chapter.Update().Where(chapter.SeriesIDEQ(selected.ID)).SetNumber(2000).SetState(chapter.StateUpgradeAvailable).SetFilename("a.cbz").SetSatisfiedImportance(1).SetSatisfiedByProviderID(lower.ID).SetDownloadPriority(true).ExecX(ctx)
	sp := client.SeriesProvider.Query().Where(seriesprovider.SeriesIDEQ(selected.ID), seriesprovider.ProviderEQ("fast")).OnlyX(ctx)
	f := &refillFetcher{started: make(chan string, 100), refs: make(chan fetcher.FetchRef, 100), release: make(chan struct{})}
	storage := t.TempDir()
	hub := sse.NewHub()
	d := download.New(client, f, hub, download.Config{Storage: storage}, settings.Static{Retries: 3, DownloadConc: 1, MaxConcurrentDl: 3}, nil)
	r := job.NewRunner(d, client, hub, storage, settings.Static{Download: 90 * time.Second})
	joinRefillRunner(t, r, cancel, f)
	r.Start(ctx)
	r.Trigger()
	for {
		select {
		case ref := <-f.refs:
			if ref.Provider == "fast" {
				if ref.SeriesProviderID != sp.ID {
					t.Fatal("ordinary sourceA launched before marked sourceA beyond marked sourceB page")
				}
				return
			}
		case <-time.After(3 * time.Second):
			t.Fatal("priority discovery did not reach sourceA")
		}
	}
}

func TestRunner_SourcePriorityFairnessSpansDownloadsAndUpgrades(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := testdb.New(t)
	ordinary := seedMidCycleSourceBacklog(ctx, t, client, "marked-downloads", "slow", 4)
	selected := seedMidCycleSourceBacklog(ctx, t, client, "marked-upgrade-turn", "slow", 1)
	low := client.SeriesProvider.Create().SetSeries(selected).SetProvider("low").SetImportance(1).SaveX(ctx)
	client.Chapter.Update().Where(chapter.SeriesIDEQ(ordinary.ID)).SetDownloadPriority(true).ExecX(ctx)
	client.Chapter.Update().Where(chapter.SeriesIDEQ(selected.ID)).SetNumber(100).SetState(chapter.StateUpgradeAvailable).SetFilename("existing.cbz").SetSatisfiedImportance(1).SetSatisfiedByProviderID(low.ID).SetDownloadPriority(true).ExecX(ctx)
	target := client.SeriesProvider.Query().Where(seriesprovider.SeriesIDEQ(selected.ID), seriesprovider.ProviderEQ("slow")).OnlyX(ctx)
	f := &refillFetcher{started: make(chan string, 100), refs: make(chan fetcher.FetchRef, 100), release: make(chan struct{})}
	storage := t.TempDir()
	hub := sse.NewHub()
	d := download.New(client, f, hub, download.Config{Storage: storage}, settings.Static{Retries: 3, DownloadConc: 1, MaxConcurrentDl: 3}, nil)
	r := job.NewRunner(d, client, hub, storage, settings.Static{Download: 90 * time.Second})
	joinRefillRunner(t, r, cancel, f)
	r.Start(ctx)
	r.Trigger()
	awaitRefillSource(t, f, "slow")
	<-f.refs
	f.open()
	awaitRefillSource(t, f, "slow")
	if next := <-f.refs; next.SeriesProviderID != target.ID {
		t.Fatal("equal-priority upgrade series lost its turn to another marked download")
	}
}

// Lower chapter numbers must win within one series even when the work kinds
// differ. This exercises the actual source scheduler refill path (one source,
// concurrency one), rather than only testing a pure ordering helper.
func TestRunner_SourceChapterOrderMixesWantedAndUpgrade(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := testdb.New(t)
	series := seedMidCycleSourceBacklog(ctx, t, client, "mixed-chapter-order", "fast", 2)
	chapters := client.Chapter.Query().Where(chapter.SeriesIDEQ(series.ID)).Order(chapter.ByNumber()).AllX(ctx)
	// Make chapter 1 an upgrade and chapter 2 a fresh wanted chapter.
	client.Chapter.UpdateOne(chapters[0]).SetNumber(1).SetState(chapter.StateUpgradeAvailable).
		SetFilename("existing.cbz").SetSatisfiedImportance(1).SetDownloadPriority(true).SaveX(ctx)
	client.Chapter.UpdateOne(chapters[1]).SetNumber(99).SetDownloadPriority(true).SaveX(ctx)
	low := client.SeriesProvider.Create().SetSeries(series).SetProvider("low").SetImportance(1).SaveX(ctx)
	client.Chapter.UpdateOne(chapters[0]).SetSatisfiedByProviderID(low.ID).SaveX(ctx)
	fast := client.SeriesProvider.Query().Where(seriesprovider.SeriesIDEQ(series.ID), seriesprovider.ProviderEQ("fast")).OnlyX(ctx)

	f := &refillFetcher{holdProvider: "fast", started: make(chan string, 10), refs: make(chan fetcher.FetchRef, 10), release: make(chan struct{})}
	storage := t.TempDir()
	hub := sse.NewHub()
	d := download.New(client, f, hub, download.Config{Storage: storage}, settings.Static{Retries: 3, DownloadConc: 1, MaxConcurrentDl: 2}, nil)
	r := job.NewRunner(d, client, hub, storage, settings.Static{Download: time.Hour})
	joinRefillRunner(t, r, cancel, f)
	r.Start(ctx)
	r.Trigger()
	awaitRefillSource(t, f, "fast")
	first := <-f.refs
	wantUpgrade := client.ProviderChapter.Query().Where(providerchapter.SeriesProviderIDEQ(fast.ID), providerchapter.ChapterKeyEQ(chapters[0].ChapterKey)).OnlyX(ctx).ID
	if first.ProviderChapterID != wantUpgrade {
		t.Fatalf("higher-number wanted chapter launched before lower-number upgrade")
	}
	f.open()
}

func TestRunner_SourceChapterOrderMixesOrdinaryWantedAndUpgrade(t *testing.T) {
	runOrdinaryMixedChapterOrder(t, false)
}

func TestRunner_SourceChapterOrderMixesOrdinaryUpgradeAndWanted(t *testing.T) {
	runOrdinaryMixedChapterOrder(t, true)
}

func runOrdinaryMixedChapterOrder(t *testing.T, upgradeLower bool) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := testdb.New(t)
	series := seedMidCycleSourceBacklog(ctx, t, client, "ordinary-mixed-chapter-order", "fast", 2)
	chapters := client.Chapter.Query().Where(chapter.SeriesIDEQ(series.ID)).Order(chapter.ByNumber()).AllX(ctx)
	upgradeIndex, wantedIndex := 1, 0
	if upgradeLower {
		upgradeIndex, wantedIndex = 0, 1
	}
	client.Chapter.UpdateOne(chapters[upgradeIndex]).SetNumber(float64(1 + 98*upgradeIndex)).SetState(chapter.StateUpgradeAvailable).
		SetFilename("existing.cbz").SetSatisfiedImportance(1).SaveX(ctx)
	client.Chapter.UpdateOne(chapters[wantedIndex]).SetNumber(float64(99 - 98*upgradeIndex)).SaveX(ctx)
	low := client.SeriesProvider.Create().SetSeries(series).SetProvider("low").SetImportance(1).SaveX(ctx)
	client.Chapter.UpdateOne(chapters[upgradeIndex]).SetSatisfiedByProviderID(low.ID).SaveX(ctx)
	fast := client.SeriesProvider.Query().Where(seriesprovider.SeriesIDEQ(series.ID), seriesprovider.ProviderEQ("fast")).OnlyX(ctx)
	f := &refillFetcher{holdProvider: "fast", started: make(chan string, 10), refs: make(chan fetcher.FetchRef, 10), release: make(chan struct{})}
	d := download.New(client, f, sse.NewHub(), download.Config{Storage: t.TempDir()}, settings.Static{Retries: 3, DownloadConc: 1, MaxConcurrentDl: 2}, nil)
	r := job.NewRunner(d, client, sse.NewHub(), t.TempDir(), settings.Static{Download: time.Hour})
	joinRefillRunner(t, r, cancel, f)
	r.Start(ctx)
	r.Trigger()
	awaitRefillSource(t, f, "fast")
	want := client.ProviderChapter.Query().Where(providerchapter.SeriesProviderIDEQ(fast.ID), providerchapter.ChapterKeyEQ(chapters[0].ChapterKey)).OnlyX(ctx).ID
	if got := (<-f.refs).ProviderChapterID; got != want {
		t.Fatalf("lower-number %s did not launch first", map[bool]string{true: "upgrade", false: "wanted"}[upgradeLower])
	}
	f.open()
}

func TestRunner_SourceChapterOrderAcrossMixedStatePageBoundary(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := testdb.New(t)
	series := seedMidCycleSourceBacklog(ctx, t, client, "mixed-page-boundary", "fast", 1001)
	chapters := client.Chapter.Query().Where(chapter.SeriesIDEQ(series.ID)).Order(chapter.ByNumber()).AllX(ctx)
	low := client.SeriesProvider.Create().SetSeries(series).SetProvider("low").SetImportance(1).SaveX(ctx)
	client.Chapter.Update().Where(chapter.SeriesIDEQ(series.ID), chapter.NumberLT(1001)).
		SetState(chapter.StateUpgradeAvailable).SetFilename("existing.cbz").SetSatisfiedByProviderID(low.ID).
		SetSatisfiedImportance(1).SetDownloadPriority(true).ExecX(ctx)
	client.Chapter.UpdateOne(chapters[1000]).SetNumber(5000).SetDownloadPriority(true).SaveX(ctx)
	fast := client.SeriesProvider.Query().Where(seriesprovider.SeriesIDEQ(series.ID), seriesprovider.ProviderEQ("fast")).OnlyX(ctx)
	f := &refillFetcher{holdProvider: "fast", started: make(chan string, 10), refs: make(chan fetcher.FetchRef, 10), release: make(chan struct{})}
	d := download.New(client, f, sse.NewHub(), download.Config{Storage: t.TempDir()}, settings.Static{Retries: 3, DownloadConc: 1, MaxConcurrentDl: 2}, nil)
	r := job.NewRunner(d, client, sse.NewHub(), t.TempDir(), settings.Static{Download: time.Hour})
	joinRefillRunner(t, r, cancel, f)
	r.Start(ctx)
	r.Trigger()
	awaitRefillSource(t, f, "fast")
	want := client.ProviderChapter.Query().Where(providerchapter.SeriesProviderIDEQ(fast.ID), providerchapter.ChapterKeyEQ(chapters[0].ChapterKey)).OnlyX(ctx).ID
	if got := (<-f.refs).ProviderChapterID; got != want {
		t.Fatalf("page-local state ordering launched later wanted chapter before lower upgrade")
	}
	f.open()
}

func TestRunner_SourceChapterOrderSkipsIneligiblePrefixAcrossPage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := testdb.New(t)
	series := seedMidCycleSourceBacklog(ctx, t, client, "mixed-page-ineligible", "fast", 1001)
	chapters := client.Chapter.Query().Where(chapter.SeriesIDEQ(series.ID)).Order(chapter.ByNumber()).AllX(ctx)
	low := client.SeriesProvider.Create().SetSeries(series).SetProvider("low").SetImportance(1).SaveX(ctx)
	client.Chapter.Update().Where(chapter.SeriesIDEQ(series.ID), chapter.NumberLT(1001)).
		SetState(chapter.StateUpgradeAvailable).SetFilename("existing.cbz").SetSatisfiedByProviderID(low.ID).
		SetSatisfiedImportance(1).SetDownloadPriority(true).ExecX(ctx)
	future := time.Now().Add(time.Hour)
	fast := client.SeriesProvider.Query().Where(seriesprovider.SeriesIDEQ(series.ID), seriesprovider.ProviderEQ("fast")).OnlyX(ctx)
	client.ProviderChapter.Update().Where(providerchapter.SeriesProviderIDEQ(fast.ID), providerchapter.ProviderIndexLT(1000)).
		SetNextAttemptAt(future).ExecX(ctx)
	client.Chapter.UpdateOne(chapters[1000]).SetNumber(5000).SetDownloadPriority(true).SaveX(ctx)

	f := &refillFetcher{holdProvider: "fast", started: make(chan string, 10), refs: make(chan fetcher.FetchRef, 10), release: make(chan struct{})}
	d := download.New(client, f, sse.NewHub(), download.Config{Storage: t.TempDir()}, settings.Static{Retries: 3, DownloadConc: 1, MaxConcurrentDl: 2}, nil)
	r := job.NewRunner(d, client, sse.NewHub(), t.TempDir(), settings.Static{Download: time.Hour})
	joinRefillRunner(t, r, cancel, f)
	r.Start(ctx)
	r.Trigger()
	awaitRefillSource(t, f, "fast")
	want := client.ProviderChapter.Query().Where(providerchapter.SeriesProviderIDEQ(fast.ID), providerchapter.ChapterKeyEQ(chapters[1000].ChapterKey)).OnlyX(ctx).ID
	if got := (<-f.refs).ProviderChapterID; got != want {
		t.Fatalf("ineligible lower-number prefix blocked later eligible wanted chapter")
	}
	f.open()
}

func TestRunner_SourcePriorityFairnessBeyondMarkedPage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, db := testdb.NewWithSQL(t)
	drv := &prioritySweepDriver{Driver: entsql.OpenDB(dialect.Postgres, db), ordinary: make(chan struct{})}
	client := ent.NewClient(ent.Driver(drv))
	a := seedMidCycleSourceBacklog(ctx, t, client, "priority-prefix-a", "slow", 1001)
	b := seedMidCycleSourceBacklog(ctx, t, client, "priority-later-b", "slow", 1)
	client.Chapter.Update().Where(chapter.SeriesIDEQ(a.ID)).SetDownloadPriority(true).ExecX(ctx)
	client.Chapter.Update().Where(chapter.SeriesIDEQ(b.ID)).SetDownloadPriority(true).SetNumber(2000).ExecX(ctx)
	target := client.SeriesProvider.Query().Where(seriesprovider.SeriesIDEQ(b.ID)).OnlyX(ctx)
	f := &refillFetcher{started: make(chan string, 100), refs: make(chan fetcher.FetchRef, 100), release: make(chan struct{})}
	storage := t.TempDir()
	hub := sse.NewHub()
	d := download.New(client, f, hub, download.Config{Storage: storage}, settings.Static{Retries: 3, DownloadConc: 1, MaxConcurrentDl: 3}, nil)
	r := job.NewRunner(d, client, hub, storage, settings.Static{Download: 90 * time.Second})
	joinRefillRunner(t, r, cancel, f)
	r.Start(ctx)
	r.Trigger()
	awaitRefillSource(t, f, "slow")
	<-f.refs
	select {
	case <-drv.ordinary:
	case <-time.After(3 * time.Second):
		t.Fatal("marked sweep never completed")
	}
	f.open()
	awaitRefillSource(t, f, "slow")
	if next := <-f.refs; next.SeriesProviderID != target.ID {
		t.Fatal("marked later-page series lost its next fair source turn")
	}
}

type prioritySweepDriver struct {
	dialect.Driver
	ordinary chan struct{}
	once     sync.Once
}

func (d *prioritySweepDriver) Query(ctx context.Context, query string, args, v any) error {
	err := d.Driver.Query(ctx, query, args, v)
	if strings.Contains(query, `AND NOT "chapters"."download_priority"`) {
		d.once.Do(func() { close(d.ordinary) })
	}
	return err
}
