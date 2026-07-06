// Package job_test contains integration tests for the chapter jobs runner.
// Tests require Docker (via testcontainers) for an ephemeral PostgreSQL instance.
package job_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/fetcher/fake"
	"github.com/technobecet/tsundoku/internal/job"
	"github.com/technobecet/tsundoku/internal/metrics"
	"github.com/technobecet/tsundoku/internal/refresh"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sse"
	"github.com/technobecet/tsundoku/internal/suwayomi"
	"github.com/technobecet/tsundoku/internal/warmup"
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
		Storage: storage,
	}, settings.Static{Retries: 3, Backoff: time.Hour}, nil)
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
		Storage: storage,
	}, settings.Static{Retries: 3, Backoff: time.Hour}, nil)
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
		Storage: storage,
	}, settings.Static{Retries: 1, Backoff: time.Hour}, nil)
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

// countingIntervals is a goroutine-safe job.Intervals double that records how
// many times DownloadInterval has been read and the last value it returned, and
// lets the test mutate the returned interval mid-run. It proves the Start loop
// re-reads the interval at the top of EACH iteration (the dynamic-timer /
// hot-reload contract) instead of capturing it once at construction.
type countingIntervals struct {
	mu           sync.Mutex
	download     time.Duration
	refresh      time.Duration
	reads        int
	lastReturned time.Duration
}

// DownloadInterval records the read and returns the current (possibly mutated)
// download interval.
func (c *countingIntervals) DownloadInterval(context.Context) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads++
	c.lastReturned = c.download
	return c.download
}

// RefreshInterval returns the fixed refresh interval (unused by these tests but
// required to satisfy job.Intervals).
func (c *countingIntervals) RefreshInterval(context.Context) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.refresh
}

// ExtensionCheckInterval returns a fixed disabled interval (unused by these tests
// but required to satisfy job.Intervals after the interface widening).
func (c *countingIntervals) ExtensionCheckInterval(context.Context) time.Duration {
	return 0
}

// WarmupInterval returns a fixed disabled interval (unused by these tests but
// required to satisfy job.Intervals after the warm-up interface widening).
func (c *countingIntervals) WarmupInterval(context.Context) time.Duration {
	return 0
}

// setDownload mutates the download interval the next read will observe.
func (c *countingIntervals) setDownload(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.download = d
}

// readCount returns how many times DownloadInterval has been read so far.
func (c *countingIntervals) readCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reads
}

// lastDownload returns the value the most recent DownloadInterval read returned.
func (c *countingIntervals) lastDownload() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastReturned
}

// TestRunner_Start_ReReadsIntervalPerIteration proves the Start loop reads the
// download interval AT THE TOP OF EACH ITERATION (hot reload), not once at
// construction. It (1) waits until the double has been read several times across
// successive cycles, then (2) mutates the returned interval mid-run and asserts
// the loop observes the NEW value on its next pass. If someone refactors Start to
// capture the interval once (e.g. a single time.NewTicker), the read count would
// stay at 1 and step (1) fails — the test is the teeth behind the dynamic timer.
func TestRunner_Start_ReReadsIntervalPerIteration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := testdb.New(t)
	storage := t.TempDir()
	hub := sse.NewHub()

	// Short interval so cycles fire quickly; no chapters exist, so each cycle is a
	// fast no-op and the loop spins back to re-read the interval.
	intervals := &countingIntervals{download: 10 * time.Millisecond, refresh: time.Hour}

	d := download.New(client, fake.New(), hub, download.Config{Storage: storage}, settings.Static{Retries: 1, Backoff: time.Hour}, nil)
	r := job.NewRunner(d, client, hub, storage, intervals)

	r.Start(ctx)

	// (1) The loop must re-read the interval multiple times — proof it reads per
	// iteration rather than capturing once.
	deadline := time.Now().Add(5 * time.Second)
	for intervals.readCount() < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("Start re-read the download interval only %d time(s) in 5s — expected per-iteration reads (>=3); did it capture the interval once?", intervals.readCount())
		}
		time.Sleep(5 * time.Millisecond)
	}

	// (2) Mutate the interval; the loop must pick up the NEW value on its next pass.
	newVal := 25 * time.Millisecond
	intervals.setDownload(newVal)

	deadline = time.Now().Add(5 * time.Second)
	for intervals.lastDownload() != newVal {
		if time.Now().After(deadline) {
			t.Fatalf("Start did not re-read the mutated interval within 5s (last returned %v, want %v) — the loop captured the old value", intervals.lastDownload(), newVal)
		}
		time.Sleep(5 * time.Millisecond)
	}
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

	d := download.New(client, fake.New(), hub, download.Config{Storage: storage}, settings.Static{Retries: 3, Backoff: time.Hour}, nil)
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
		download.New(client, fake.New(), sse.NewHub(), download.Config{Storage: t.TempDir()}, settings.Static{Retries: 1, Backoff: time.Hour}, nil),
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
func (fakeSuwayomi) FetchMangaDetails(context.Context, int) (suwayomi.Manga, error) {
	return suwayomi.Manga{}, nil
}
func (fakeSuwayomi) ChapterPages(context.Context, int) ([]string, error)       { return nil, nil }
func (fakeSuwayomi) PageBytes(context.Context, string) ([]byte, string, error) { return nil, "", nil }
func (fakeSuwayomi) ServerSettings(context.Context) (suwayomi.SuwayomiSettings, error) {
	return suwayomi.SuwayomiSettings{}, nil
}
func (fakeSuwayomi) SetServerSettings(context.Context, suwayomi.SuwayomiSettingsPatch) error {
	return nil
}
func (fakeSuwayomi) Extensions(context.Context) ([]suwayomi.Extension, error) { return nil, nil }
func (fakeSuwayomi) SetExtensionState(context.Context, string, suwayomi.ExtensionAction) error {
	return nil
}
func (fakeSuwayomi) FetchExtensions(context.Context) ([]suwayomi.Extension, error) { return nil, nil }
func (fakeSuwayomi) ExtensionRepos(context.Context) ([]string, error)              { return nil, nil }
func (fakeSuwayomi) SetExtensionRepos(context.Context, []string) error             { return nil }
func (fakeSuwayomi) SourcePreferences(context.Context, string) ([]suwayomi.SourcePreference, error) {
	return nil, nil
}
func (fakeSuwayomi) SetSourcePreference(context.Context, string, int, suwayomi.PreferenceValue) ([]suwayomi.SourcePreference, error) {
	return nil, nil
}
func (fakeSuwayomi) ExtensionSources(context.Context, string) ([]suwayomi.Source, error) {
	return nil, nil
}
func (fakeSuwayomi) SetSourceEnabled(context.Context, string, bool) error { return nil }

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
	refreshSvc := refresh.NewService(client, suwayomi.NewIngest(fc, client), hub, settings.Static{Concurrency: 2}, nil)

	d := download.New(client, fake.New(), hub, download.Config{Storage: storage}, settings.Static{Retries: 3, Backoff: time.Hour}, nil)
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

// extCheckFake is a suwayomi.Client double used exclusively by
// TestRunner_StartExtensionCheck_FetchesAndBroadcasts. It embeds the Client
// interface (nil — any method other than FetchExtensions would panic, but
// StartExtensionCheck only calls FetchExtensions) and overrides FetchExtensions
// to return a deterministic extension list and count its calls.
type extCheckFake struct {
	suwayomi.Client
	mu    sync.Mutex
	calls int
}

func (f *extCheckFake) FetchExtensions(_ context.Context) ([]suwayomi.Extension, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return []suwayomi.Extension{
		{HasUpdate: true},
		{HasUpdate: true},
		{HasUpdate: false},
	}, nil
}

func (f *extCheckFake) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// TestRunner_StartExtensionCheck_FetchesAndBroadcasts verifies that
// StartExtensionCheck calls FetchExtensions and broadcasts an extensions.checked
// SSE event with updatesAvailable equal to the count of extensions whose
// HasUpdate field is true.
func TestRunner_StartExtensionCheck_FetchesAndBroadcasts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := testdb.New(t)
	storage := t.TempDir()
	hub := sse.NewHub()
	events, unsub := hub.Subscribe()
	defer unsub()

	swFake := &extCheckFake{}

	d := download.New(client, fake.New(), hub, download.Config{Storage: storage}, settings.Static{Retries: 1, Backoff: time.Hour}, nil)
	// Short ExtCheck so the job fires quickly in the test.
	r := job.NewRunner(d, client, hub, storage, settings.Static{ExtCheck: 20 * time.Millisecond})

	r.StartExtensionCheck(ctx, swFake)

	// Wait for the extensions.checked SSE event and verify its payload.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-events:
			if ev.Type != "extensions.checked" {
				continue
			}
			raw, _ := ev.Data.(json.RawMessage)
			var p struct {
				UpdatesAvailable int `json:"updatesAvailable"`
			}
			if err := json.Unmarshal([]byte(raw), &p); err != nil {
				t.Fatalf("unmarshal extensions.checked: %v", err)
			}
			if p.UpdatesAvailable != 2 {
				t.Fatalf("updatesAvailable = %d, want 2", p.UpdatesAvailable)
			}
			if swFake.callCount() == 0 {
				t.Error("FetchExtensions was never called")
			}
			return
		case <-deadline:
			t.Fatalf("timed out waiting for extensions.checked; FetchExtensions called %d time(s)", swFake.callCount())
		}
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
	refreshSvc := refresh.NewService(client, suwayomi.NewIngest(fc, client), hub, settings.Static{Concurrency: 2}, nil)

	d := download.New(client, fake.New(), hub, download.Config{Storage: storage}, settings.Static{Retries: 3, Backoff: time.Hour}, nil)
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

// warmupFake is a suwayomi.Client double for the warm-up seed-at-boot test. It
// returns one enabled online source and counts + signals every Browse call (the
// actual warm), so the test can prove the seed pass fires PROMPTLY after
// StartWarmup rather than only after the first interval elapses. It embeds the
// Client interface so any unrelated method is a nil-panic (StartWarmup's warm
// path only calls Sources + Browse).
type warmupFake struct {
	suwayomi.Client
	mu      sync.Mutex
	browses int
	fired   chan struct{}
}

func (f *warmupFake) Sources(context.Context) ([]suwayomi.Source, error) {
	return []suwayomi.Source{{ID: "warm-1", Name: "Warm One", Lang: "en"}}, nil
}

func (f *warmupFake) Browse(context.Context, string, suwayomi.BrowseType, int) (suwayomi.BrowseResult, error) {
	f.mu.Lock()
	first := f.browses == 0
	f.browses++
	f.mu.Unlock()
	if first {
		close(f.fired) // signal the first (seed) warm exactly once
	}
	return suwayomi.BrowseResult{}, nil
}

// TestRunner_StartWarmup_SeedsAtBoot proves StartWarmup runs the seed (WarmAll)
// pass at the TOP of its loop — promptly at boot — instead of waiting a full
// interval first. The warm-up interval is set to ONE HOUR, so the only way a
// Browse (the warm call) can fire within the 2s window is the boot seed. If
// StartWarmup ever regresses to wait-then-pass, no Browse fires and the test
// times out. The generous 2s bound (vs. the hour interval) keeps it non-flaky —
// it asserts "fired promptly", not an exact timing.
func TestRunner_StartWarmup_SeedsAtBoot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := testdb.New(t)
	storage := t.TempDir()
	hub := sse.NewHub()

	fc := &warmupFake{fired: make(chan struct{})}
	warmupSvc := warmup.NewService(fc, metrics.NewService(client), settings.Static{WarmupSlow: 5000}, nil)

	d := download.New(client, fake.New(), hub, download.Config{Storage: storage}, settings.Static{Retries: 1, Backoff: time.Hour}, nil)
	// A one-hour interval: only the boot seed (top-of-loop pass) can warm within
	// the test window — an interval-first loop would leave Browse un-called.
	r := job.NewRunner(d, client, hub, storage, settings.Static{WarmupIv: time.Hour})

	r.StartWarmup(ctx, warmupSvc)

	select {
	case <-fc.fired:
		// Seed warm fired promptly — boot seed, not interval-delayed.
	case <-time.After(2 * time.Second):
		t.Fatal("warm-up seed did not fire within 2s of StartWarmup — the boot seed is delayed by the interval")
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
		Storage: storage,
	}, settings.Static{Retries: 1, Backoff: time.Hour}, nil)
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

// slowThenFastFetcher sleeps `delay` before succeeding for every chapter of
// slowProvider, and succeeds INSTANTLY for every other provider. It lets a test
// build a REALISTIC (not permanently blocked) multi-pass drain for one source
// while a freshly-adopted source's fetches complete promptly — the shape needed
// to prove the drain loop lets a mid-cycle adopt run alongside a big, still-
// draining backlog instead of waiting for it to finish first.
type slowThenFastFetcher struct {
	slowProvider string
	delay        time.Duration
}

// Fetch sleeps for slowProvider chapters (honouring ctx cancellation), then
// returns a minimal one-page success for any provider.
func (f *slowThenFastFetcher) Fetch(ctx context.Context, ref fetcher.FetchRef) (fetcher.ChapterPages, error) {
	if ref.Provider == f.slowProvider {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return fetcher.ChapterPages{}, ctx.Err()
		}
	}
	return fetcher.ChapterPages{
		Pages:     []fetcher.PageImage{{Data: []byte{0x02}, Ext: "jpg"}},
		PageCount: 1,
	}, nil
}

// TestRunner_RunDownloadCycle_DrainLoopPicksUpMidCycleAdopt proves the Slice 2
// fix for bug #2 (mid-cycle adopt starvation): while source A's big backlog is
// still mid-drain (spanning multiple bounded RunOnce passes), a brand-new
// source B is inserted directly into the DB — simulating an owner adopting a
// series mid-cycle. B's chapters must reach downloaded WITHIN THIS SAME
// RunDownloadCycle call, and — the "ran in parallel, not after" assertion — at
// the moment B finishes, A must NOT yet be fully drained (proving B joined a
// later pass alongside A's remaining backlog rather than waiting for all of A
// to finish first, which is exactly what the pre-Slice-2 unbounded RunOnce
// would have forced).
func TestRunner_RunDownloadCycle_DrainLoopPicksUpMidCycleAdopt(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()
	hub := sse.NewHub()

	const cap = 2                // batch = 2*cap = 4 per source per pass
	const aTotal = 2 * (2 * cap) // 8 chapters: exactly two bounded passes for A alone
	const aDelay = 300 * time.Millisecond

	sA := seedMidCycleSourceBacklog(ctx, t, client, "series-a-midcycle", "A", aTotal)

	f := &slowThenFastFetcher{slowProvider: "A", delay: aDelay}
	d := download.New(client, f, hub, download.Config{Storage: storage},
		settings.Static{Retries: 3, Backoff: time.Hour, DownloadConc: cap}, nil)
	r := job.NewRunner(d, client, hub, storage, settings.Static{})

	cycleDone := make(chan error, 1)
	go func() { cycleDone <- r.RunDownloadCycle(ctx) }()

	// Give pass 1 a head start (well under aDelay, so pass 1 cannot possibly have
	// finished yet), then simulate the mid-cycle adopt: a brand-new source B with
	// its own small backlog, inserted directly into the DB.
	time.Sleep(75 * time.Millisecond)
	sB := seedMidCycleSourceBacklog(ctx, t, client, "series-b-midcycle", "B", 2)
	bIDs := seriesChapterIDs(ctx, t, client, sB)

	waitAllDownloaded(t, ctx, client, bIDs, 15*time.Second,
		"source B's chapters did not reach downloaded within the deadline — the mid-cycle adopt was not picked up by the drain loop")

	// At the instant B finished, A must still have undownloaded chapters — proof
	// B ran ALONGSIDE A's remaining backlog (a later pass), not after it drained
	// entirely (each of A's two passes takes ~aDelay; B's fetches are instant).
	if aDownloaded := downloadedCount(ctx, client, sA); aDownloaded >= aTotal {
		t.Errorf("source A's entire backlog (%d) already finished before B completed — B did not run in parallel with a still-draining backlog, it ran only after", aTotal)
	}

	if err := <-cycleDone; err != nil {
		t.Fatalf("RunDownloadCycle: %v", err)
	}
	if finalA := downloadedCount(ctx, client, sA); finalA != aTotal {
		t.Errorf("source A: want all %d downloaded once the cycle finishes, got %d", aTotal, finalA)
	}
}

// seedMidCycleSourceBacklog creates a series with n wanted chapters (numbered
// 1..n) from a single named provider and returns the series. Shared by
// TestRunner_RunDownloadCycle_DrainLoopPicksUpMidCycleAdopt for both the initial
// big backlog (source A) and the simulated mid-cycle adopt (source B).
func seedMidCycleSourceBacklog(ctx context.Context, t *testing.T, client *ent.Client, slug, provider string, n int) *ent.Series {
	t.Helper()
	s := client.Series.Create().SetTitle(slug).SetSlug(slug).SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider(provider).SetImportance(10).SaveX(ctx)
	for i := 0; i < n; i++ {
		num := float64(i + 1)
		key := fmt.Sprintf("%s-%d", provider, i+1)
		client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey(key).
			SetNillableNumber(&num).SetURL("https://" + provider + "/" + key).SetProviderIndex(i).SaveX(ctx)
		client.Chapter.Create().SetSeries(s).SetChapterKey(key).SetNillableNumber(&num).SaveX(ctx)
	}
	return s
}

// seriesChapterIDs returns the ids of every Chapter belonging to s.
func seriesChapterIDs(ctx context.Context, t *testing.T, client *ent.Client, s *ent.Series) []uuid.UUID {
	t.Helper()
	chs := client.Chapter.Query().Where(entchapter.SeriesIDEQ(s.ID)).AllX(ctx)
	ids := make([]uuid.UUID, len(chs))
	for i, ch := range chs {
		ids[i] = ch.ID
	}
	return ids
}

// downloadedCount returns how many of series s's chapters are state=downloaded.
func downloadedCount(ctx context.Context, client *ent.Client, s *ent.Series) int {
	return client.Chapter.Query().
		Where(entchapter.SeriesIDEQ(s.ID), entchapter.StateEQ(entchapter.StateDownloaded)).
		CountX(ctx)
}

// waitAllDownloaded polls until every chapter in ids is state=downloaded or
// timeout elapses, failing the test with msg on timeout.
func waitAllDownloaded(t *testing.T, ctx context.Context, client *ent.Client, ids []uuid.UUID, timeout time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if allDownloaded(ctx, client, ids) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(msg)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// allDownloaded reports whether every chapter in ids is currently downloaded.
func allDownloaded(ctx context.Context, client *ent.Client, ids []uuid.UUID) bool {
	for _, id := range ids {
		if client.Chapter.GetX(ctx, id).State != entchapter.StateDownloaded {
			return false
		}
	}
	return true
}

// TestRunner_RunDownloadCycle_NoBusySpin_AllOnCooldown verifies the drain
// loop's termination rule: when every wanted/failed chapter's only source is on
// cooldown (no live candidate), RunOnce dispatches 0 and the drain loop must
// stop after that single pass — RunDownloadCycle returns promptly instead of
// busy-spinning forever on repeated 0-dispatch passes. The cooldown chapter
// must also be left completely untouched (no fetch attempted).
func TestRunner_RunDownloadCycle_NoBusySpin_AllOnCooldown(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()
	hub := sse.NewHub()

	s := client.Series.Create().SetTitle("Cooldown Series").SetSlug("cooldown-series-nospin").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("only").SetImportance(10).SaveX(ctx)
	future := time.Now().Add(time.Hour)
	client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey("c1").
		SetURL("https://only/c1").SetProviderIndex(0).SetAttempts(1).SetNextAttemptAt(future).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("c1").SetState(entchapter.StateFailed).SaveX(ctx)

	// A fetcher that would succeed if ever called — proves no fetch happens for
	// the cooldown-gated chapter.
	d := download.New(client, fake.New(), hub, download.Config{Storage: storage},
		settings.Static{Retries: 3, Backoff: time.Hour}, nil)
	r := job.NewRunner(d, client, hub, storage, settings.Static{})

	done := make(chan error, 1)
	go func() { done <- r.RunDownloadCycle(ctx) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunDownloadCycle: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunDownloadCycle did not return within 3s — the drain loop appears to be busy-spinning on 0-dispatch passes")
	}

	got := client.Chapter.GetX(ctx, ch.ID)
	if got.State != entchapter.StateFailed {
		t.Errorf("cooldown-gated chapter state = %s, want unchanged failed", got.State)
	}
	pc, err := client.ProviderChapter.Query().Only(ctx)
	if err == nil && pc.Attempts != 1 {
		t.Errorf("cooldown-gated source attempts = %d, want unchanged 1 (no fetch attempted)", pc.Attempts)
	}
}

// TestRunner_RunDownloadCycle_NoBusySpin_EmptyWantedSet verifies the drain
// loop's other termination case: with NO wanted/failed chapters at all,
// RunDownloadCycle completes promptly (a single 0-dispatch pass), never
// spinning.
func TestRunner_RunDownloadCycle_NoBusySpin_EmptyWantedSet(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()
	hub := sse.NewHub()

	d := download.New(client, fake.New(), hub, download.Config{Storage: storage},
		settings.Static{Retries: 3, Backoff: time.Hour}, nil)
	r := job.NewRunner(d, client, hub, storage, settings.Static{})

	done := make(chan error, 1)
	go func() { done <- r.RunDownloadCycle(ctx) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunDownloadCycle: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunDownloadCycle did not return within 3s on an empty wanted set — possible busy-spin")
	}
}

// installClaimWriteFailure hooks the Chapter so every wanted/failed→downloading
// claim WRITE fails while all reads still succeed (ent hooks fire only on
// mutations), modelling a writes-fail/reads-succeed DB fault. It targets only the
// state→downloading update, so seeding and any other write are unaffected. Kept
// package-local (job_test) since download_test's copy is in a different package.
func installClaimWriteFailure(client *ent.Client) {
	client.Chapter.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if cm, ok := m.(*ent.ChapterMutation); ok && cm.Op().Is(ent.OpUpdateOne|ent.OpUpdate) {
				if st, exists := cm.State(); exists && st == entchapter.StateDownloading {
					return nil, errors.New("injected write failure: chapter state→downloading")
				}
			}
			return next.Mutate(ctx, m)
		})
	})
}

// TestRunner_RunDownloadCycle_TerminatesWhenClaimWriteFails is the regression
// proof for the drain-loop livelock: under a writes-fail/reads-succeed DB fault,
// the wanted→downloading claim fails every pass, so RunOnce reports 0 forward
// progress and drainDownloads breaks after ONE pass. RunDownloadCycle must RETURN
// promptly (the runner then retries on its next interval — graceful degradation,
// matching pre-Slice-2) instead of hot-spinning RunOnce forever. If the drain loop
// counted mere SELECTION instead of progress, the live-candidate chapter would be
// re-selected every pass and this cycle would never return. The chapter is left
// wanted for the next interval.
func TestRunner_RunDownloadCycle_TerminatesWhenClaimWriteFails(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()
	hub := sse.NewHub()

	s := client.Series.Create().SetTitle("WF Cycle").SetSlug("wf-cycle").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("src").SetImportance(10).SaveX(ctx)
	client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey("c1").
		SetURL("https://src/c1").SetProviderIndex(0).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("c1").SaveX(ctx)

	// Fail the claim write AFTER seeding — reads succeed, only state→downloading fails.
	installClaimWriteFailure(client)

	d := download.New(client, fake.New(), hub, download.Config{Storage: storage},
		settings.Static{Retries: 3, Backoff: time.Hour}, nil)
	r := job.NewRunner(d, client, hub, storage, settings.Static{})

	done := make(chan error, 1)
	go func() { done <- r.RunDownloadCycle(ctx) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunDownloadCycle: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunDownloadCycle did not return within 10s — drainDownloads hot-spun on a claim that never makes forward progress (the write-fail livelock)")
	}

	if got := client.Chapter.GetX(ctx, ch.ID).State; got != entchapter.StateWanted {
		t.Errorf("chapter state = %s, want wanted (claim write failed; left for the next interval)", got)
	}
}
