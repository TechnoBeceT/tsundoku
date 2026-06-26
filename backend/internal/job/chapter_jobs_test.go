// Package job_test contains integration tests for the chapter jobs runner.
// Tests require Docker (via testcontainers) for an ephemeral PostgreSQL instance.
package job_test

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/download"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/fetcher/fake"
	"github.com/technobecet/tsundoku/internal/job"
	"github.com/technobecet/tsundoku/internal/refresh"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sse"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// TestRunner_DownloadCycle_DrainWanted verifies that RunDownloadCycle with the
// fake fetcher drains a wanted Chapter to state=downloaded with a real CBZ on
// disk and emits cycle-level SSE events.
func TestRunner_DownloadCycle_DrainWanted(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()
	hub := sse.NewHub()

	s := client.Series.Create().SetTitle("Job Series").SetSlug("job-series").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("mangadex").SetImportance(10).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).
		SetChapterKey("ch-job-1").
		SetURL("https://mangadex.org/ch-job-1").
		SetProviderIndex(0).
		SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("ch-job-1").SaveX(ctx)

	// Subscribe before the cycle to capture cycle-level events.
	events, unsub := hub.Subscribe()
	defer unsub()

	d := download.New(client, fake.New(), hub, download.Config{
		PerProviderConcurrency: 2,

		Storage: storage,
	}, settings.Static{Retries: 3, Backoff: time.Hour})
	r := job.NewRunner(d, client, hub, storage, settings.Static{})

	if err := r.RunDownloadCycle(ctx); err != nil {
		t.Fatalf("RunDownloadCycle: %v", err)
	}

	got := client.Chapter.GetX(ctx, ch.ID)
	if got.State != entchapter.StateDownloaded {
		t.Errorf("state: want downloaded, got %s", got.State)
	}
	if got.Filename == "" {
		t.Error("filename must be set after download cycle")
	}

	// Verify cycle-level SSE events were emitted.
	cycleStart, cycleDone := collectCycleEvents(events, 2*time.Second)
	if !cycleStart {
		t.Error("expected a cycle.start SSE event, got none")
	}
	if !cycleDone {
		t.Error("expected a cycle.done SSE event, got none")
	}
}

// collectCycleEvents drains events until both cycle.start and cycle.done have
// been observed or the timeout expires. Returns (sawStart, sawDone).
func collectCycleEvents(events <-chan sse.Event, timeout time.Duration) (sawStart, sawDone bool) {
	timer := time.After(timeout)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return
			}
			if ev.Type == "cycle.start" {
				sawStart = true
			}
			if ev.Type == "cycle.done" {
				sawDone = true
			}
			if sawStart && sawDone {
				return
			}
		case <-timer:
			return
		}
	}
}

// TestRunner_DownloadCycle_UpgradePass verifies that RunDownloadCycle runs
// DetectUpgrades + Upgrade for newly-flagged chapters: after the initial
// download and adding a higher-importance provider, a second cycle should
// detect the upgrade_available and upgrade the chapter.
func TestRunner_DownloadCycle_UpgradePass(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()
	hub := sse.NewHub()

	s := client.Series.Create().SetTitle("Upg Cycle Series").SetSlug("upg-cycle-series").SaveX(ctx)
	spLow := client.SeriesProvider.Create().SetSeries(s).SetProvider("prov-low").SetImportance(2).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(spLow.ID).
		SetChapterKey("ch-upg-cycle").
		SetURL("https://low.example.com/ch-upg-cycle").
		SetProviderIndex(0).
		SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("ch-upg-cycle").SaveX(ctx)

	d := download.New(client, fake.New(), hub, download.Config{
		PerProviderConcurrency: 2,

		Storage: storage,
	}, settings.Static{Retries: 3, Backoff: time.Hour})
	r := job.NewRunner(d, client, hub, storage, settings.Static{})

	// First cycle: download the chapter.
	if err := r.RunDownloadCycle(ctx); err != nil {
		t.Fatalf("first RunDownloadCycle: %v", err)
	}
	if client.Chapter.GetX(ctx, ch.ID).State != entchapter.StateDownloaded {
		t.Fatalf("chapter should be downloaded after first cycle")
	}
	initialImportance := *client.Chapter.GetX(ctx, ch.ID).SatisfiedImportance

	// Add a strictly higher-importance provider for the same key.
	spHigh := client.SeriesProvider.Create().SetSeries(s).SetProvider("prov-high").SetImportance(10).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(spHigh.ID).
		SetChapterKey("ch-upg-cycle").
		SetURL("https://high.example.com/ch-upg-cycle").
		SetProviderIndex(0).
		SaveX(ctx)

	// Second cycle: should detect the upgrade and apply it.
	if err := r.RunDownloadCycle(ctx); err != nil {
		t.Fatalf("second RunDownloadCycle: %v", err)
	}

	final := client.Chapter.GetX(ctx, ch.ID)
	if final.State != entchapter.StateDownloaded {
		t.Errorf("state after upgrade cycle: want downloaded, got %s", final.State)
	}
	if final.SatisfiedImportance == nil {
		t.Fatal("satisfied_importance must be set after upgrade cycle")
	}
	if *final.SatisfiedImportance <= initialImportance {
		t.Errorf("satisfied_importance after upgrade: want > %d, got %d",
			initialImportance, *final.SatisfiedImportance)
	}
}

// TestRunner_Start_TicksAndStopsCleanly verifies that Start ticks at least once
// and that its goroutine actually exits when the context is cancelled (no leak).
//
// Goroutine-stop is verified by comparing runtime.NumGoroutine() before Start
// with a polled count after cancel: if the ticker goroutine does not exit, the
// count stays elevated and the test fails.
func TestRunner_Start_TicksAndStopsCleanly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := testdb.New(t)
	storage := t.TempDir()
	hub := sse.NewHub()

	d := download.New(client, fake.New(), hub, download.Config{
		PerProviderConcurrency: 1,

		Storage: storage,
	}, settings.Static{Retries: 1, Backoff: time.Hour})
	r := job.NewRunner(d, client, hub, storage, settings.Static{Download: 20 * time.Millisecond})

	// Subscribe to observe at least one cycle.start before cancelling.
	events, unsub := hub.Subscribe()
	defer unsub()

	// Baseline goroutine count taken immediately before Start so that any
	// goroutines spawned by test setup are already counted.
	base := runtime.NumGoroutine()

	// Short interval so the ticker fires quickly in tests.
	r.Start(ctx)

	// Wait for at least one tick (cycle.start event) within a reasonable deadline.
	tickSeen := false
	timeout := time.After(2 * time.Second)
loop:
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				break loop
			}
			if ev.Type == "cycle.start" {
				tickSeen = true
				break loop
			}
		case <-timeout:
			break loop
		}
	}

	if !tickSeen {
		t.Error("expected at least one cycle.start event before cancel")
	}

	// Cancel the context — the ticker goroutine must exit.
	cancel()

	// Poll until runtime.NumGoroutine() drops back to <= base+1 (allow +1 slack
	// for transient runtime goroutines) or until the deadline. Failing to reach
	// baseline within 2s means the ticker goroutine leaked / did not exit.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= base+1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("Start goroutine did not exit within 2s after context cancel: goroutines now=%d, base=%d",
		runtime.NumGoroutine(), base)
}

// TestRunner_Trigger_RunsCycle verifies Trigger() causes the running download
// loop to execute a cycle that drains a wanted chapter — without waiting for the
// (long) ticker interval.
func TestRunner_Trigger_RunsCycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := testdb.New(t)
	storage := t.TempDir()
	hub := sse.NewHub()

	s := client.Series.Create().SetTitle("Trig Series").SetSlug("trig-series").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("mangadex").SetImportance(10).SaveX(ctx)
	client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey("ch-1").
		SetURL("https://x/ch-1").SetProviderIndex(0).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("ch-1").SaveX(ctx)

	d := download.New(client, fake.New(), hub, download.Config{PerProviderConcurrency: 2, Storage: storage}, settings.Static{Retries: 3, Backoff: time.Hour})
	r := job.NewRunner(d, client, hub, storage, settings.Static{Download: time.Hour})

	// Long interval so only the trigger can drive the cycle within the test.
	r.Start(ctx)
	r.Trigger()

	// Poll for the chapter to reach downloaded (cycle runs async in the loop).
	deadline := time.Now().Add(10 * time.Second)
	for {
		if client.Chapter.GetX(ctx, ch.ID).State == entchapter.StateDownloaded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("triggered cycle did not drain the wanted chapter within 10s")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestRunner_Trigger_Coalesces verifies Trigger() is non-blocking and never
// panics when called repeatedly with no loop draining the channel (buffer 1).
func TestRunner_Trigger_Coalesces(t *testing.T) {
	client := testdb.New(t)
	r := job.NewRunner(
		download.New(client, fake.New(), sse.NewHub(), download.Config{PerProviderConcurrency: 1, Storage: t.TempDir()}, settings.Static{Retries: 1, Backoff: time.Hour}),
		client, sse.NewHub(), t.TempDir(), settings.Static{},
	)
	// No Start → nothing drains the channel. Many triggers must not block/panic.
	for i := 0; i < 100; i++ {
		r.Trigger()
	}
}

// fakeSuwayomi is a minimal suwayomi.Client returning one chapter for any manga,
// used to prove StartRefresh discovers chapters and then triggers a download.
type fakeSuwayomi struct{}

func (fakeSuwayomi) Sources(context.Context) ([]suwayomi.Source, error) { return nil, nil }
func (fakeSuwayomi) Search(context.Context, string, string) ([]suwayomi.Manga, error) {
	return nil, nil
}
func (fakeSuwayomi) Browse(context.Context, string, suwayomi.BrowseType, int) (suwayomi.BrowseResult, error) {
	return suwayomi.BrowseResult{}, nil
}
func (fakeSuwayomi) FetchChapters(context.Context, int) ([]suwayomi.Chapter, error) {
	n := 1.0
	return []suwayomi.Chapter{{ID: 1, Index: 0, Number: &n, URL: "u1"}}, nil
}
func (fakeSuwayomi) MangaChapters(context.Context, int) ([]suwayomi.Chapter, error) { return nil, nil }
func (fakeSuwayomi) MangaMeta(context.Context, int) (suwayomi.Manga, error) {
	return suwayomi.Manga{}, nil
}
func (fakeSuwayomi) ChapterPages(context.Context, int) ([]string, error)       { return nil, nil }
func (fakeSuwayomi) PageBytes(context.Context, string) ([]byte, string, error) { return nil, "", nil }

// TestRunner_StartRefresh_DiscoversAndDownloads verifies the refresh ticker
// re-fetches a monitored series (creating a wanted chapter) and then triggers a
// download cycle that drains it — end to end.
func TestRunner_StartRefresh_DiscoversAndDownloads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := testdb.New(t)
	storage := t.TempDir()
	hub := sse.NewHub()

	// Monitored series + provider with a known suwayomi_id, NO chapters yet.
	s := client.Series.Create().SetTitle("Disc Series").SetSlug("disc-series").SetMonitored(true).SaveX(ctx)
	client.SeriesProvider.Create().SetSeries(s).SetProvider("mangadex").SetSuwayomiID(42).SetImportance(10).SaveX(ctx)

	fc := fakeSuwayomi{}
	refreshSvc := refresh.NewService(client, suwayomi.NewIngest(fc, client), hub, settings.Static{Concurrency: 2})

	d := download.New(client, fake.New(), hub, download.Config{PerProviderConcurrency: 2, Storage: storage}, settings.Static{Retries: 3, Backoff: time.Hour})
	r := job.NewRunner(d, client, hub, storage, settings.Static{Download: time.Hour, Refresh: 100 * time.Millisecond})

	r.Start(ctx) // download loop (trigger-driven here)
	// fast refresh tick for the test; healthCount is a no-op stub.
	r.StartRefresh(ctx, refreshSvc,
		func(context.Context) (int, error) { return 0, nil })

	deadline := time.Now().Add(15 * time.Second)
	for {
		downloaded := client.Chapter.Query().Where(entchapter.StateEQ(entchapter.StateDownloaded)).CountX(ctx)
		if downloaded == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("refresh tick did not discover + download the chapter within 15s")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestStartRefresh_BroadcastsHealthSummary verifies that StartRefresh emits a
// health.summary SSE event after each sweep, with the payload produced by the
// supplied healthCount function.
func TestStartRefresh_BroadcastsHealthSummary(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := testdb.New(t)
	storage := t.TempDir()
	hub := sse.NewHub()
	events, unsub := hub.Subscribe()
	defer unsub()

	fc := fakeSuwayomi{}
	refreshSvc := refresh.NewService(client, suwayomi.NewIngest(fc, client), hub, settings.Static{Concurrency: 2})

	d := download.New(client, fake.New(), hub, download.Config{PerProviderConcurrency: 2, Storage: storage}, settings.Static{Retries: 3, Backoff: time.Hour})
	r := job.NewRunner(d, client, hub, storage, settings.Static{Refresh: 50 * time.Millisecond})

	// Stub the unhealthy count so the assertion is deterministic.
	healthCount := func(context.Context) (int, error) { return 3, nil }

	r.StartRefresh(ctx, refreshSvc, healthCount)

	// Drain events until health.summary (skipping refresh.start/done/cycle.*).
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-events:
			if ev.Type == "health.summary" {
				raw, _ := ev.Data.(json.RawMessage)
				var p struct {
					Unhealthy int `json:"unhealthy"`
				}
				if err := json.Unmarshal([]byte(raw), &p); err != nil {
					t.Fatalf("unmarshal health.summary: %v", err)
				}
				if p.Unhealthy != 3 {
					t.Fatalf("health.summary unhealthy = %d, want 3", p.Unhealthy)
				}
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for health.summary")
		}
	}
}

// TestRunner_Reconcile_SmokesWrapper verifies that Reconcile wraps
// disk.Reconcile and returns its result. A temp storage dir with a real
// rendered CBZ is used to exercise the path without hitting the reconcile
// deep logic (Task 7 owns those tests).
func TestRunner_Reconcile_SmokesWrapper(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()
	hub := sse.NewHub()

	// Render a real CBZ so Reconcile has something to scan.
	num := 1.0
	max := 1.0
	_, err := disk.RenderChapter(disk.RenderRequest{
		Storage: storage,
		Meta: disk.RenderMeta{
			Provider:    "mangadex",
			Language:    "en",
			SeriesTitle: "Reconcile Smoke",
			Category:    disk.CategoryManga,
			Number:      &num,
			MaxChapter:  &max,
			ChapterKey:  "1",
			Importance:  1,
		},
		Pages: []fetcher.PageImage{{Data: []byte{0x00}, Ext: "jpg"}},
	})
	if err != nil {
		t.Fatalf("RenderChapter: %v", err)
	}

	d := download.New(client, fake.New(), hub, download.Config{
		PerProviderConcurrency: 1,

		Storage: storage,
	}, settings.Static{Retries: 1, Backoff: time.Hour})
	r := job.NewRunner(d, client, hub, storage, settings.Static{})

	result, err := r.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if result.SeriesUpserted == 0 {
		t.Error("Reconcile: SeriesUpserted = 0, want > 0")
	}
	if result.ChaptersUpserted == 0 {
		t.Error("Reconcile: ChaptersUpserted = 0, want > 0")
	}
}
