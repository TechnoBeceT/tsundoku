package download

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disabledsource"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourcethroughput"
	"github.com/technobecet/tsundoku/internal/sse"
)

func TestSourceSchedulerRefreshResolvesAliasesWithoutRenewingSpentAllowance(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	d := New(client, &admissionFetcher{}, sse.NewHub(), Config{}, settings.Static{DownloadConc: 5, MaxConcurrentDl: 10}, nil)
	scheduler, err := d.NewSourceScheduler(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer scheduler.Close()
	window := scheduler.windowFor("Elf Toon", 0, time.Now(), 0)
	for range 3 {
		window.seen[uuid.New()] = true
	}
	window.remaining -= 3
	release, err := scheduler.limiter.acquire(ctx, "Elf Toon", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	permit := scheduler.limiter.sems["Elf Toon"]
	series := client.Series.Create().SetTitle("known alias").SetSlug("known-alias").SaveX(ctx)
	client.SeriesProvider.Create().SetSeries(series).SetProvider("101").SetProviderName("Elf Toon").SaveX(ctx)
	two := 2
	d.WithSourceThroughputPolicies(&stubThroughputPolicies{overrides: map[int64]sourcethroughput.Override{101: {DownloadConcurrency: &two}}})
	refreshSchedulerPolicy(t, scheduler)
	if window.remaining != 1 {
		t.Fatalf("remaining allowance = %d, want 4 minus 3 spent", window.remaining)
	}
	if scheduler.limiter.sems["Elf Toon"] != permit {
		t.Fatal("refresh replaced outstanding permit object")
	}
	if permit.limit != 2 || permit.used != 1 {
		t.Fatalf("held permit limit/used = %d/%d, want 2/1", permit.limit, permit.used)
	}
	four := 4
	d.WithSourceThroughputPolicies(&stubThroughputPolicies{overrides: map[int64]sourcethroughput.Override{101: {DownloadConcurrency: &four}}})
	refreshSchedulerPolicy(t, scheduler)
	if window.remaining != 1 {
		t.Fatalf("policy increase renewed allowance to %d", window.remaining)
	}
}

func refreshSchedulerPolicy(t *testing.T, scheduler *SourceScheduler) {
	t.Helper()
	if err := scheduler.RefreshPolicy(); err != nil {
		t.Fatal(err)
	}
}

//nolint:gocognit // One held admission scenario checks both state contracts and preservation after an owner pause.
func TestSourceSchedulerPauseAfterSelectionPreventsDownloadAndUpgradeAdmission(t *testing.T) {
	for _, state := range []entchapter.State{entchapter.StateWanted, entchapter.StateUpgradeAvailable} {
		t.Run(string(state), func(t *testing.T) {
			ctx := context.Background()
			client := testdb.New(t)
			s := client.Series.Create().SetTitle("pause admission").SetSlug("pause-admission").SaveX(ctx)
			sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("101").SetProviderName("delayed").SetImportance(2).SaveX(ctx)
			client.ProviderChapter.Create().SetSeriesProvider(sp).SetChapterKey("1").SetURL("https://example.test/1").SaveX(ctx)
			ch := client.Chapter.Create().SetSeries(s).SetChapterKey("1").SetState(state).SaveX(ctx)
			if state == entchapter.StateUpgradeAvailable {
				low := client.SeriesProvider.Create().SetSeries(s).SetProvider("low").SetImportance(1).SaveX(ctx)
				ch = client.Chapter.UpdateOne(ch).SetFilename("existing.cbz").SetSatisfiedByProviderID(low.ID).SetSatisfiedImportance(1).SaveX(ctx)
			}
			f := &admissionFetcher{}
			d := New(client, f, sse.NewHub(), Config{Storage: t.TempDir()}, settings.Static{Retries: 3, DownloadConc: 1, MaxConcurrentDl: 2}, nil).WithDisabledSources(disabledsource.NewService(client))
			release := make(chan struct{})
			entered := make(chan struct{})
			d.waiter = blockingWaiter{entered: entered, release: release}
			scheduler, err := d.NewSourceScheduler(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer scheduler.Close()
			if _, err := scheduler.Step(time.Now(), 0); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("selection never reached admission wait")
			}
			client.DisabledSource.Create().SetSourceID(101).SaveX(ctx)
			close(release)
			waitSourceWindowSettled(t, scheduler)
			if got := f.calls.Load(); got != 0 {
				t.Fatalf("owner-paused source reached engine %d times after selection", got)
			}
			assertPausedAdmissionState(t, ch, client.Chapter.GetX(ctx, ch.ID))

		})
	}
}

func waitSourceWindowSettled(t *testing.T, scheduler *SourceScheduler) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		step, err := scheduler.Step(time.Now(), 0)
		if err != nil {
			t.Fatal(err)
		}
		if !step.Running {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("source window did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}

func assertPausedAdmissionState(t *testing.T, before, after *ent.Chapter) {
	t.Helper()
	want := before.State
	// A paused upgrade target is an unresolved stale marker: the existing
	// no-fetch resolver restores downloaded while retaining its working copy.
	if want == entchapter.StateUpgradeAvailable {
		want = entchapter.StateDownloaded
	}
	if after.State != want {
		t.Fatalf("paused admission state = %s, want %s", after.State, want)
	}
	if after.Filename != before.Filename {
		t.Fatalf("paused admission changed readable filename from %s to %s", before.Filename, after.Filename)
	}
}

func TestSourceSchedulerMissingPriorityTurnAtExactPageBoundary(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	series := client.Series.Create().SetTitle("missing priority turn").SetSlug("missing-priority-turn").SaveX(ctx)
	provider := client.SeriesProvider.Create().SetSeries(series).SetProvider("101").SetProviderName("delayed").SetImportance(10).SaveX(ctx)
	seedExactPriorityPage(ctx, client, series, provider)
	f := &admissionFetcher{}
	d := New(client, f, sse.NewHub(), Config{Storage: t.TempDir()}, settings.Static{Retries: 3, DownloadConc: 1, MaxConcurrentDl: 2}, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	d.waiter = blockingWaiter{entered: entered, release: release}
	scheduler, err := d.NewSourceScheduler(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer scheduler.Close()
	// Reproduce a remembered fair turn whose series no longer has eligible work.
	// The remaining source allowance belongs to the still-eligible marked series.
	scheduler.sources[canonicalSourceKey(provider)] = &sourceWindow{started: time.Now(), remaining: 2, seen: make(map[uuid.UUID]bool), lastSeries: series.ID, nextSeries: uuid.New()}
	assertPriorityProgressWithoutSpin(t, scheduler)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("eligible marked work never reached admission")
	}
}

func seedExactPriorityPage(ctx context.Context, client *ent.Client, series *ent.Series, provider *ent.SeriesProvider) {
	chapters := make([]*ent.ChapterCreate, 1000)
	feeds := make([]*ent.ProviderChapterCreate, 1000)
	for i := range chapters {
		key := strconv.Itoa(i + 1)
		chapters[i] = client.Chapter.Create().SetSeries(series).SetChapterKey(key).SetNumber(float64(i + 1)).SetDownloadPriority(true)
		feeds[i] = client.ProviderChapter.Create().SetSeriesProvider(provider).SetChapterKey(key).SetURL("https://example.test/" + key)
	}
	client.Chapter.CreateBulk(chapters...).SaveX(ctx)
	client.ProviderChapter.CreateBulk(feeds...).SaveX(ctx)
}

func assertPriorityProgressWithoutSpin(t *testing.T, scheduler *SourceScheduler) {
	t.Helper()
	started := false
	var step SourceStep
	for range 4 {
		var err error
		step, err = scheduler.Step(time.Now(), 0)
		if err != nil {
			t.Fatal(err)
		}
		started = started || step.Started
	}
	if !started {
		t.Fatal("missing series turn blocked eligible marked work across repeated exact-page sweeps")
	}
	if step.More {
		t.Fatal("held admission kept an unproductive immediate discovery sweep")
	}
}
