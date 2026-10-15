package job_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/job"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourcethroughput"
	"github.com/technobecet/tsundoku/internal/sse"
)

type refillFetcher struct {
	holdProvider string
	started      chan string
	refs         chan fetcher.FetchRef
	release      chan struct{}
	once         sync.Once
}

type refillSettings struct {
	settings.Static
	global atomic.Int64
}

func (s *refillSettings) MaxConcurrentDownloads(context.Context) int { return int(s.global.Load()) }

func TestRunner_SourceRefillTriggerCannotRenewBeforeSourceInterval(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := testdb.New(t)
	seedMidCycleSourceBacklog(ctx, t, client, "trigger-budget", "fast", 4)
	f := &refillFetcher{started: make(chan string, 100), release: make(chan struct{})}
	storage := t.TempDir()
	hub := sse.NewHub()
	d := download.New(client, f, hub, download.Config{Storage: storage}, settings.Static{Retries: 3, DownloadConc: 1, MaxConcurrentDl: 3}, nil)
	r := job.NewRunner(d, client, hub, storage, settings.Static{Download: time.Second})
	joinRefillRunner(t, r, cancel, f)
	r.Start(ctx)
	r.Trigger()
	awaitRefillSource(t, f, "fast")
	awaitRefillSource(t, f, "fast")
	for i := 0; i < 5; i++ {
		r.Trigger()
		time.Sleep(20 * time.Millisecond)
	}
	select {
	case source := <-f.started:
		t.Fatalf("trigger renewed consumed allowance before interval: %s", source)
	case <-time.After(150 * time.Millisecond):
	}
	awaitRefillSource(t, f, "fast")
}

func TestRunner_SourceRefillAppliesGlobalCapacityChangeWhileSlowSourceActive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := testdb.New(t)
	seedMidCycleSourceBacklog(ctx, t, client, "global-change-slow", "slow", 1)
	f := &refillFetcher{started: make(chan string, 100), release: make(chan struct{})}
	storage := t.TempDir()
	hub := sse.NewHub()
	policy := &refillSettings{Static: settings.Static{Retries: 3, DownloadConc: 1}}
	policy.global.Store(1)
	d := download.New(client, f, hub, download.Config{Storage: storage}, policy, nil)
	r := job.NewRunner(d, client, hub, storage, settings.Static{Download: time.Hour})
	joinRefillRunner(t, r, cancel, f)
	r.Start(ctx)
	r.Trigger()
	awaitRefillSource(t, f, "slow")
	seedMidCycleSourceBacklog(ctx, t, client, "global-change-fast", "fast", 1)
	r.Trigger()
	select {
	case got := <-f.started:
		t.Fatalf("global cap 1 allowed another fetch while slow held: %s", got)
	case <-time.After(100 * time.Millisecond):
	}
	policy.global.Store(2)
	r.Trigger()
	awaitRefillSource(t, f, "fast")
}

type refillOverride struct{ concurrency atomic.Int64 }

func (s *refillOverride) Snapshot(context.Context) (map[int64]sourcethroughput.Override, error) {
	n := int(s.concurrency.Load())
	return map[int64]sourcethroughput.Override{101: {DownloadConcurrency: &n}}, nil
}

func TestRunner_SourceRefillAppliesSourceOverrideToNextWindow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := testdb.New(t)
	seedMidCycleSourceBacklog(ctx, t, client, "override-slow", "slow", 1)
	seedMidCycleSourceBacklog(ctx, t, client, "override-fast", "101", 5)
	f := &refillFetcher{started: make(chan string, 100), release: make(chan struct{})}
	storage := t.TempDir()
	hub := sse.NewHub()
	store := &refillOverride{}
	store.concurrency.Store(1)
	d := download.New(client, f, hub, download.Config{Storage: storage}, settings.Static{Retries: 3, DownloadConc: 1, MaxConcurrentDl: 4}, nil).WithSourceThroughputPolicies(store)
	r := job.NewRunner(d, client, hub, storage, settings.Static{Download: 500 * time.Millisecond})
	joinRefillRunner(t, r, cancel, f)
	r.Start(ctx)
	r.Trigger()
	awaitRefillSource(t, f, "101")
	awaitRefillSource(t, f, "101")
	store.concurrency.Store(2)
	awaitRefillSource(t, f, "101")
	deadline := time.After(250 * time.Millisecond)
	for remaining := 2; remaining > 0; {
		select {
		case source := <-f.started:
			if source == "101" {
				remaining--
			}
		case <-deadline:
			t.Fatal("new source override did not enlarge next window's allowance")
		}
	}
}

type pausedRefillSource struct{}

func (pausedRefillSource) Disabled(context.Context) (map[int64]bool, error) {
	return map[int64]bool{599: true}, nil
}

func TestRunner_SourceRefillReachesHealthySourceBeyondPausedPrefix(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := testdb.New(t)
	paused := seedMidCycleSourceBacklog(ctx, t, client, "paused-prefix", "599", 1000)
	fast := seedMidCycleSourceBacklog(ctx, t, client, "after-prefix", "fast", 1)
	client.Chapter.Update().Where(entchapter.SeriesIDEQ(fast.ID)).SetNumber(1001).SaveX(ctx)
	f := &refillFetcher{started: make(chan string, 100), release: make(chan struct{})}
	storage := t.TempDir()
	hub := sse.NewHub()
	d := download.New(client, f, hub, download.Config{Storage: storage}, settings.Static{Retries: 3, DownloadConc: 1, MaxConcurrentDl: 3}, nil).WithDisabledSources(pausedRefillSource{})
	r := job.NewRunner(d, client, hub, storage, settings.Static{Download: time.Hour})
	joinRefillRunner(t, r, cancel, f)
	r.Start(ctx)
	r.Trigger()
	awaitRefillSource(t, f, "fast")
	if got := downloadedCount(ctx, client, paused); got != 0 {
		t.Fatalf("paused source downloaded %d chapters", got)
	}
}

func TestRunner_SourceRefillDownloadsAndUpgradesShareOneAllowance(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	s := seedMidCycleSourceBacklog(ctx, t, client, "shared-window", "fast", 3)
	low := client.SeriesProvider.Create().SetSeries(s).SetProvider("low").SetImportance(1).SaveX(ctx)
	client.Chapter.Update().Where(entchapter.SeriesIDEQ(s.ID), entchapter.NumberGT(1)).
		SetState(entchapter.StateUpgradeAvailable).SetFilename("existing.cbz").SetSatisfiedByProviderID(low.ID).SetSatisfiedImportance(1).SaveX(ctx)
	f := &refillFetcher{started: make(chan string, 100), release: make(chan struct{})}
	storage := t.TempDir()
	hub := sse.NewHub()
	d := download.New(client, f, hub, download.Config{Storage: storage}, settings.Static{Retries: 3, DownloadConc: 1, MaxConcurrentDl: 3}, nil)
	r := job.NewRunner(d, client, hub, storage, settings.Static{})
	if err := r.RunDownloadCycle(ctx); err != nil {
		t.Fatal(err)
	}
	if got := len(f.started); got != 2 {
		t.Fatalf("one source allowance admitted %d fetches, want 1 download + 1 upgrade", got)
	}
	if got := client.Chapter.Query().Where(entchapter.SeriesIDEQ(s.ID), entchapter.StateEQ(entchapter.StateUpgradeAvailable)).CountX(ctx); got != 1 {
		t.Fatalf("remaining upgrades = %d, want one deferred by shared allowance", got)
	}
}

func TestRunner_SourceRefillRecoversFromWindowAdmissionError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := testdb.New(t)
	seedRefillUpgrade(t, ctx, client)
	failed := make(chan struct{})
	var once sync.Once
	client.Chapter.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			m := mutation.(*ent.ChapterMutation)
			state, _ := m.State()
			if state == entchapter.StateUpgrading {
				reject := false
				once.Do(func() { reject = true; close(failed) })
				if reject {
					return nil, errors.New("temporary admission storage error")
				}
			}
			return next.Mutate(ctx, mutation)
		})
	})
	f := &refillFetcher{started: make(chan string, 100), release: make(chan struct{})}
	storage := t.TempDir()
	hub := sse.NewHub()
	d := download.New(client, f, hub, download.Config{Storage: storage}, settings.Static{Retries: 3, DownloadConc: 1, MaxConcurrentDl: 3}, nil)
	r := job.NewRunner(d, client, hub, storage, settings.Static{Download: 100 * time.Millisecond})
	joinRefillRunner(t, r, cancel, f)
	r.Start(ctx)
	r.Trigger()
	select {
	case <-failed:
	case <-time.After(3 * time.Second):
		t.Fatal("window admission failure did not execute")
	}
	seedMidCycleSourceBacklog(ctx, t, client, "after-hard-error", "fast", 1)
	awaitRefillSource(t, f, "fast")
}

type stubbornRefillFetcher struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (f *stubbornRefillFetcher) Fetch(context.Context, fetcher.FetchRef) (fetcher.ChapterPages, error) {
	f.once.Do(func() { close(f.started) })
	<-f.release
	return fetcher.ChapterPages{Pages: []fetcher.PageImage{{Data: []byte{1}, Ext: "jpg"}}, PageCount: 1}, nil
}

func TestRunner_ShutdownDownloadsWaitsForOwnedFetchAndRejectsLaterAdmission(t *testing.T) {
	client := testdb.New(t)
	storage := t.TempDir()
	hub := sse.NewHub()
	f := &stubbornRefillFetcher{started: make(chan struct{}), release: make(chan struct{})}
	seedMidCycleSourceBacklog(context.Background(), t, client, "shutdown-slow", "slow", 2)
	d := download.New(client, f, hub, download.Config{Storage: storage}, settings.Static{Retries: 3, DownloadConc: 1, MaxConcurrentDl: 3}, nil)
	r := job.NewRunner(d, client, hub, storage, settings.Static{Download: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)
	r.Trigger()
	select {
	case <-f.started:
	case <-time.After(3 * time.Second):
		t.Fatal("fetch did not start")
	}
	shut, stop := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer stop()
	if err := r.ShutdownDownloads(shut); err != context.DeadlineExceeded {
		t.Fatalf("shutdown while fetch held = %v", err)
	}
	close(f.release)
	joined, stopJoin := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopJoin()
	if err := r.ShutdownDownloads(joined); err != nil {
		t.Fatalf("join fetch tail: %v", err)
	}
	if err := r.RunDownloadCycle(context.Background()); err != context.Canceled {
		t.Fatalf("post-shutdown admission = %v", err)
	}
}

func (f *refillFetcher) Fetch(ctx context.Context, ref fetcher.FetchRef) (fetcher.ChapterPages, error) {
	f.started <- ref.Provider
	if f.refs != nil {
		f.refs <- ref
	}
	if ref.Provider == "slow" || (f.holdProvider != "" && ref.Provider == f.holdProvider) {
		select {
		case <-f.release:
		case <-ctx.Done():
			return fetcher.ChapterPages{}, ctx.Err()
		}
	}
	return fetcher.ChapterPages{Pages: []fetcher.PageImage{{Data: []byte{1}, Ext: "jpg"}}, PageCount: 1}, nil
}

func TestRunner_SourceRefillRotatesSeriesAcrossWindows(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := testdb.New(t)
	for _, slug := range []string{"rotation-a", "rotation-b", "rotation-c"} {
		seedMidCycleSourceBacklog(ctx, t, client, slug, "fast", 5)
	}
	f := &refillFetcher{started: make(chan string, 100), refs: make(chan fetcher.FetchRef, 100), release: make(chan struct{})}
	storage := t.TempDir()
	hub := sse.NewHub()
	d := download.New(client, f, hub, download.Config{Storage: storage}, settings.Static{Retries: 3, DownloadConc: 1, MaxConcurrentDl: 3}, nil)
	r := job.NewRunner(d, client, hub, storage, settings.Static{Download: 150 * time.Millisecond})
	joinRefillRunner(t, r, cancel, f)
	r.Start(ctx)
	r.Trigger()
	for range 4 {
		awaitRefillSource(t, f, "fast")
	}
	series := make(map[string]bool)
	chapters := make(map[string]bool)
	for range 4 {
		ref := <-f.refs
		series[ref.SeriesProviderID.String()] = true
		if chapters[ref.ProviderChapterID.String()] {
			t.Fatal("source windows fetched the same chapter twice")
		}
		chapters[ref.ProviderChapterID.String()] = true
	}
	if len(series) != 3 {
		t.Fatalf("first two 2-item windows served %d series, want all 3", len(series))
	}
}

func TestRunner_SourceRefillMultipleStartsKeepOneSourceWindow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := testdb.New(t)
	seedMidCycleSourceBacklog(ctx, t, client, "multiple-starts", "slow", 4)
	f := &refillFetcher{started: make(chan string, 100), release: make(chan struct{})}
	storage := t.TempDir()
	hub := sse.NewHub()
	d := download.New(client, f, hub, download.Config{Storage: storage}, settings.Static{Retries: 3, DownloadConc: 1, MaxConcurrentDl: 3}, nil)
	r := job.NewRunner(d, client, hub, storage, settings.Static{Download: time.Hour})
	joinRefillRunner(t, r, cancel, f)
	r.Start(ctx)
	r.Trigger()
	awaitRefillSource(t, f, "slow")
	r.Start(ctx)
	r.Trigger()
	select {
	case <-f.started:
		t.Fatal("second Start overlapped the active source window")
	case <-time.After(100 * time.Millisecond):
	}
}

func (f *refillFetcher) open() { f.once.Do(func() { close(f.release) }) }

func awaitRefillSource(t *testing.T, f *refillFetcher, source string) {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case got := <-f.started:
			if got == source {
				return
			}
		case <-timer.C:
			t.Fatalf("source %s did not start while slow source remained held", source)
		}
	}
}

func joinRefillRunner(t *testing.T, r *job.Runner, cancel context.CancelFunc, f *refillFetcher) {
	t.Helper()
	t.Cleanup(func() {
		cancel()
		f.open()
		ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		if err := r.ShutdownDownloads(ctx); err != nil {
			t.Errorf("join source scheduler: %v", err)
		}
	})
}

func seedRefillUpgrade(t *testing.T, ctx context.Context, client *ent.Client) {
	t.Helper()
	s := seedMidCycleSourceBacklog(ctx, t, client, "slow-upgrade", "slow", 1)
	low := client.SeriesProvider.Create().SetSeries(s).SetProvider("low").SetImportance(1).SaveX(ctx)
	ch := client.Chapter.Query().Where(entchapter.SeriesIDEQ(s.ID)).OnlyX(ctx)
	client.Chapter.UpdateOne(ch).SetState(entchapter.StateUpgradeAvailable).
		SetFilename("existing.cbz").SetSatisfiedByProviderID(low.ID).SetSatisfiedImportance(1).SaveX(ctx)
}

func TestRunner_SourceRefillStartsNewSourceDuringDownloadAndUpgrade(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		name := "download"
		if upgrade {
			name = "upgrade"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client := testdb.New(t)
			if upgrade {
				seedRefillUpgrade(t, ctx, client)
			} else {
				seedMidCycleSourceBacklog(ctx, t, client, "slow-download", "slow", 1)
			}
			f := &refillFetcher{started: make(chan string, 100), release: make(chan struct{})}
			defer f.open()
			storage := t.TempDir()
			hub := sse.NewHub()
			d := download.New(client, f, hub, download.Config{Storage: storage}, settings.Static{Retries: 3, DownloadConc: 1, MaxConcurrentDl: 3}, nil)
			r := job.NewRunner(d, client, hub, storage, settings.Static{Download: time.Hour})
			joinRefillRunner(t, r, cancel, f)
			r.Start(ctx)
			r.Trigger()
			awaitRefillSource(t, f, "slow")
			seedMidCycleSourceBacklog(ctx, t, client, "fast-new", "fast", 1)
			r.Trigger()
			awaitRefillSource(t, f, "fast")
		})
	}
}

func TestRunner_SourceRefillRenewsFastAllowanceWhileSlowRemainsActive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := testdb.New(t)
	seedMidCycleSourceBacklog(ctx, t, client, "slow-renew", "slow", 1)
	seedMidCycleSourceBacklog(ctx, t, client, "fast-renew", "fast", 5)
	f := &refillFetcher{started: make(chan string, 100), release: make(chan struct{})}
	defer f.open()
	storage := t.TempDir()
	hub := sse.NewHub()
	d := download.New(client, f, hub, download.Config{Storage: storage}, settings.Static{Retries: 3, DownloadConc: 1, MaxConcurrentDl: 3}, nil)
	r := job.NewRunner(d, client, hub, storage, settings.Static{Download: 200 * time.Millisecond})
	joinRefillRunner(t, r, cancel, f)
	r.Start(ctx)
	r.Trigger()
	for i := 0; i < 5; i++ {
		awaitRefillSource(t, f, "fast")
	}
}

// refillFeedDriver observes real PostgreSQL reads at the Runner boundary.
type refillFeedDriver struct {
	dialect.Driver
	reads atomic.Int64
}

func (d *refillFeedDriver) Query(ctx context.Context, query string, args, v any) error {
	if strings.Contains(query, `FROM "provider_chapters"`) {
		d.reads.Add(1)
	}
	return d.Driver.Query(ctx, query, args, v)
}

func TestRunner_SourceRefillIdleCompletionPollsDoNotReloadFeeds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, db := testdb.NewWithSQL(t)
	drv := &refillFeedDriver{Driver: entsql.OpenDB(dialect.Postgres, db)}
	client := ent.NewClient(ent.Driver(drv))
	seedMidCycleSourceBacklog(ctx, t, client, "poll-held", "slow", 4)
	f := &refillFetcher{started: make(chan string, 100), release: make(chan struct{})}
	storage := t.TempDir()
	hub := sse.NewHub()
	d := download.New(client, f, hub, download.Config{Storage: storage}, settings.Static{Retries: 3, DownloadConc: 1, MaxConcurrentDl: 3}, nil)
	r := job.NewRunner(d, client, hub, storage, settings.Static{Download: 90 * time.Second})
	joinRefillRunner(t, r, cancel, f)
	r.Start(ctx)
	r.Trigger()
	awaitRefillSource(t, f, "slow")
	// Admission has finished its reads once Fetch starts. Allow the initial
	// discovery pass to return before measuring multiple active status polls.
	time.Sleep(100 * time.Millisecond)
	before := drv.reads.Load()
	if before == 0 {
		t.Fatal("feed read observation did not capture initial selection")
	}
	time.Sleep(2200 * time.Millisecond)
	if after := drv.reads.Load(); after != before {
		t.Fatalf("idle completion polls reloaded feeds: reads %d -> %d", before, after)
	}
	seedMidCycleSourceBacklog(ctx, t, client, "poll-new-fast", "fast", 2)
	r.Trigger()
	awaitRefillSource(t, f, "fast")
	awaitRefillSource(t, f, "fast")
	if drv.reads.Load() <= before {
		t.Fatal("coalesced trigger did not discover newly ready work")
	}
	// Collecting the completed fast window permits one useful discovery pass;
	// subsequent idle polls must remain cheap while slow is still held.
	time.Sleep(1200 * time.Millisecond)
	afterCompletion := drv.reads.Load()
	time.Sleep(1200 * time.Millisecond)
	if after := drv.reads.Load(); after != afterCompletion {
		t.Fatalf("idle polls after fast completion reloaded feeds: reads %d -> %d", afterCompletion, after)
	}
}

func TestRunner_SourceRefillHonorsPersistedSourceConcurrency(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := testdb.New(t)
	series := seedMidCycleSourceBacklog(ctx, t, client, "cap-source", "101", 5)
	client.SeriesProvider.Query().Where(entseriesprovider.SeriesID(series.ID)).OnlyX(ctx).Update().SetProviderName("slow").SaveX(ctx)
	cap := 3
	client.SourceThroughputPolicy.Create().SetSourceID(101).SetDownloadConcurrency(cap).SaveX(ctx)
	f := &refillFetcher{holdProvider: "101", started: make(chan string, 100), release: make(chan struct{})}
	storage := t.TempDir()
	hub := sse.NewHub()
	throughput := sourcethroughput.NewService(client, settings.Static{DownloadConc: 5, MaxConcurrentDl: 5})
	d := download.New(client, f, hub, download.Config{Storage: storage}, settings.Static{Retries: 3, DownloadConc: 1, MaxConcurrentDl: 5}, nil).WithSourceThroughputPolicies(throughput)
	r := job.NewRunner(d, client, hub, storage, settings.Static{Download: time.Hour})
	joinRefillRunner(t, r, cancel, f)
	r.Start(ctx)
	r.Trigger()
	for i := 0; i < cap; i++ {
		awaitRefillSource(t, f, "101")
	}
	select {
	case got := <-f.started:
		t.Fatalf("source exceeded persisted cap: %s", got)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestRunner_SourceRefillAliasPolicyBeforeBoundedDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name      string
		diskCount int
		liveFirst bool
	}{
		{name: "disk first", diskCount: 5},
		{name: "live first", diskCount: 5, liveFirst: true},
		{name: "disk full page before live", diskCount: 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := testdb.New(t)
			disk := seedMidCycleSourceBacklog(ctx, t, client, "alias-disk", "Elf Toon", tc.diskCount)
			live := seedMidCycleSourceBacklog(ctx, t, client, "alias-live", "101", 5)
			client.SeriesProvider.Update().Where(entseriesprovider.SeriesIDEQ(live.ID)).SetProviderName("Elf Toon").ExecX(ctx)
			if !tc.liveFirst {
				client.Chapter.Update().Where(entchapter.SeriesIDEQ(live.ID)).SetNumber(2000).ExecX(ctx)
			} else {
				client.Chapter.Update().Where(entchapter.SeriesIDEQ(disk.ID)).SetNumber(2000).ExecX(ctx)
			}
			// Fresh downloads and upgrades consume the same physical-source window.
			low := client.SeriesProvider.Create().SetSeries(disk).SetProvider("low").SetImportance(1).SaveX(ctx)
			client.Chapter.Update().Where(entchapter.SeriesIDEQ(disk.ID), entchapter.NumberLTE(2)).
				SetState(entchapter.StateUpgradeAvailable).SetFilename("existing.cbz").SetSatisfiedImportance(1).SetSatisfiedByProviderID(low.ID).ExecX(ctx)
			store := &refillOverride{}
			store.concurrency.Store(2)
			f := &refillFetcher{started: make(chan string, 100), release: make(chan struct{})}
			storage := t.TempDir()
			hub := sse.NewHub()
			d := download.New(client, f, hub, download.Config{Storage: storage}, settings.Static{Retries: 3, DownloadConc: 5, MaxConcurrentDl: 10}, nil).WithSourceThroughputPolicies(store)
			r := job.NewRunner(d, client, hub, storage, settings.Static{})
			if err := r.RunDownloadCycle(ctx); err != nil {
				t.Fatal(err)
			}
			if got := len(f.started); got != 4 {
				t.Fatalf("one physical source started %d chapters, want shared override allowance 4", got)
			}
		})
	}
}

type aliasCapFetcher struct {
	started    chan string
	release    chan struct{}
	active     atomic.Int64
	peak       atomic.Int64
	aliasCalls atomic.Int64
	liveCalls  atomic.Int64
	once       sync.Once
}

func (f *aliasCapFetcher) open() { f.once.Do(func() { close(f.release) }) }

func (f *aliasCapFetcher) Fetch(ctx context.Context, ref fetcher.FetchRef) (fetcher.ChapterPages, error) {
	if ref.Provider == "301" {
		return fetcher.ChapterPages{}, sourceengine.ErrNoPages
	}
	if ref.Provider == "Elf Toon" {
		f.aliasCalls.Add(1)
	} else {
		f.liveCalls.Add(1)
	}
	active := f.active.Add(1)
	defer f.active.Add(-1)
	for old := f.peak.Load(); active > old; old = f.peak.Load() {
		if f.peak.CompareAndSwap(old, active) {
			break
		}
	}
	f.started <- ref.Provider
	select {
	case <-f.release:
	case <-ctx.Done():
		return fetcher.ChapterPages{}, ctx.Err()
	}
	return fetcher.ChapterPages{Pages: []fetcher.PageImage{{Data: []byte{1}, Ext: "jpg"}}, PageCount: 1}, nil
}

func TestRunner_SourceRefillAliasesShareFetchCapAcrossUpgradeAndFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := testdb.New(t)
	fallback := seedMidCycleSourceBacklog(ctx, t, client, "alias-fallback", "301", 4)
	alias := client.SeriesProvider.Create().SetSeries(fallback).SetProvider("Elf Toon").SetImportance(5).SaveX(ctx)
	for _, ch := range client.Chapter.Query().Where(entchapter.SeriesIDEQ(fallback.ID)).AllX(ctx) {
		client.ProviderChapter.Create().SetSeriesProvider(alias).SetChapterKey(ch.ChapterKey).SetURL("https://alias/" + ch.ChapterKey).SaveX(ctx)
	}
	upgrades := seedMidCycleSourceBacklog(ctx, t, client, "alias-upgrades", "101", 4)
	client.SeriesProvider.Update().Where(entseriesprovider.SeriesIDEQ(upgrades.ID)).SetProviderName("Elf Toon").ExecX(ctx)
	low := client.SeriesProvider.Create().SetSeries(upgrades).SetProvider("low").SetImportance(1).SaveX(ctx)
	client.Chapter.Update().Where(entchapter.SeriesIDEQ(upgrades.ID)).SetState(entchapter.StateUpgradeAvailable).
		SetFilename("existing.cbz").SetSatisfiedImportance(1).SetSatisfiedByProviderID(low.ID).ExecX(ctx)
	store := &refillOverride{}
	store.concurrency.Store(2)
	f := &aliasCapFetcher{started: make(chan string, 20), release: make(chan struct{})}
	storage := t.TempDir()
	hub := sse.NewHub()
	d := download.New(client, f, hub, download.Config{Storage: storage}, settings.Static{Retries: 3, DownloadConc: 5, MaxConcurrentDl: 10}, nil).WithSourceThroughputPolicies(store)
	r := job.NewRunner(d, client, hub, storage, settings.Static{})
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() { defer close(finished); done <- r.RunDownloadCycle(ctx) }()
	t.Cleanup(func() { cancel(); f.open(); <-finished })
	for range 2 {
		select {
		case <-f.started:
		case <-time.After(3 * time.Second):
			t.Fatal("shared source did not fill cap 2")
		}
	}
	select {
	case source := <-f.started:
		t.Fatalf("alias %s exceeded cap while two shared calls held", source)
	case <-time.After(200 * time.Millisecond):
	}
	if peak := f.peak.Load(); peak != 2 {
		t.Fatalf("shared source peak = %d, want 2", peak)
	}
	finishAliasCapCycle(t, f, done)
}

func finishAliasCapCycle(t *testing.T, f *aliasCapFetcher, done <-chan error) {
	t.Helper()
	f.open()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("mixed alias cycle did not finish")
	}
	if f.peak.Load() > 2 || f.aliasCalls.Load() == 0 || f.liveCalls.Load() == 0 {
		t.Fatalf("mixed fetch peak/alias/live = %d/%d/%d, want cap 2 with both paths exercised", f.peak.Load(), f.aliasCalls.Load(), f.liveCalls.Load())
	}
}
