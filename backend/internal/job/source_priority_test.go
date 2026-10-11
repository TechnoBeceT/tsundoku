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
