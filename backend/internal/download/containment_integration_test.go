package download

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entsourcecircuitstate "github.com/technobecet/tsundoku/internal/ent/sourcecircuitstate"
	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/sse"
)

const (
	containmentChallengedProvider = "101"
	containmentHealthyProvider    = "202"
	containmentFallbackProvider   = "303"
	containmentChallengedKey      = "challenged"
)

type containmentWaiter struct {
	entered chan struct{}
	release <-chan struct{}
}

func (w *containmentWaiter) Wait(ctx context.Context, sourceKey string) {
	if sourceKey != containmentChallengedKey {
		return
	}
	w.entered <- struct{}{}
	select {
	case <-w.release:
	case <-ctx.Done():
	}
}

type containmentFetcher struct {
	healthyStarted    chan struct{}
	healthyRelease    <-chan struct{}
	challengedStarted chan struct{}
	challengedRelease <-chan struct{}
	healthyOnce       sync.Once
	current           atomic.Int32
	peak              atomic.Int32
	fallbackCalls     atomic.Int32
}

func (f *containmentFetcher) Fetch(ctx context.Context, ref fetcher.FetchRef) (fetcher.ChapterPages, error) {
	leave := f.enter()
	defer leave()

	switch ref.Provider {
	case containmentHealthyProvider:
		return f.fetchHealthy(ctx)
	case containmentChallengedProvider:
		return f.fetchChallenged(ctx)
	default:
		f.fallbackCalls.Add(1)
		return fetcher.ChapterPages{}, errors.New("unexpected fallback fetch")
	}
}

func (f *containmentFetcher) enter() func() {
	current := f.current.Add(1)
	for {
		peak := f.peak.Load()
		if current <= peak || f.peak.CompareAndSwap(peak, current) {
			break
		}
	}
	return func() { f.current.Add(-1) }
}

func (f *containmentFetcher) fetchHealthy(ctx context.Context) (fetcher.ChapterPages, error) {
	f.healthyOnce.Do(func() { close(f.healthyStarted) })
	select {
	case <-f.healthyRelease:
		return fetcher.ChapterPages{
			Pages:     []fetcher.PageImage{{Data: []byte{0xab}, Ext: "jpg"}},
			PageCount: 1,
		}, nil
	case <-ctx.Done():
		return fetcher.ChapterPages{}, ctx.Err()
	}
}

func (f *containmentFetcher) fetchChallenged(ctx context.Context) (fetcher.ChapterPages, error) {
	f.challengedStarted <- struct{}{}
	select {
	case <-f.challengedRelease:
		return fetcher.ChapterPages{}, errors.New("cloudflare challenge detected")
	case <-ctx.Done():
		return fetcher.ChapterPages{}, ctx.Err()
	}
}

type containmentFixture struct {
	challengedChapters []*ent.Chapter
	challengedPCs      []*ent.ProviderChapter
	healthyChapter     *ent.Chapter
	healthyProvider    *ent.SeriesProvider
	healthyPC          *ent.ProviderChapter
	fallbackPC         *ent.ProviderChapter
}

// TestRunOnceAt_ChallengedSourceCannotConsumeHealthyProgress composes the real
// scheduler, ranking, admission, retry/cache accounting, and persisted breaker.
// Three challenged calls occupy only their source wait; the healthy winner must
// finish before those waits are released, and the later concurrent failures must
// retain the exact source-wide accounting contract.
func TestRunOnceAt_ChallengedSourceCannotConsumeHealthyProgress(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	client := testdb.New(t)
	fixture := seedContainmentFixture(t, ctx, client)

	waitRelease := make(chan struct{})
	healthyRelease := make(chan struct{})
	challengedRelease := make(chan struct{})
	var waitReleaseOnce, healthyReleaseOnce, challengedReleaseOnce sync.Once
	closeWait := func() { waitReleaseOnce.Do(func() { close(waitRelease) }) }
	closeHealthy := func() { healthyReleaseOnce.Do(func() { close(healthyRelease) }) }
	closeChallenged := func() { challengedReleaseOnce.Do(func() { close(challengedRelease) }) }
	releaseAll := func() {
		closeWait()
		closeHealthy()
		closeChallenged()
	}
	t.Cleanup(releaseAll)

	waiter := &containmentWaiter{entered: make(chan struct{}, 3), release: waitRelease}
	f := &containmentFetcher{
		healthyStarted:    make(chan struct{}),
		healthyRelease:    healthyRelease,
		challengedStarted: make(chan struct{}, 3),
		challengedRelease: challengedRelease,
	}
	policy := settings.Static{
		Retries:              3,
		Backoff:              30 * time.Minute,
		DownloadConc:         3,
		SourcesFailureThresh: 3,
		SourcesCooldownIv:    17 * time.Minute,
	}
	gate := sourcegate.NewService(client, policy)
	storage := t.TempDir()
	sentinelPath := filepath.Join(storage, "existing-library-file.cbz")
	if err := os.WriteFile(sentinelPath, []byte("keep"), 0o600); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	d := New(client, f, sse.NewHub(), Config{Storage: storage}, policy, gate)
	d.waiter = waiter

	healthyDownloaded := observeContainmentDownload(client)
	anchoredNow := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	done := make(chan dispatchCall, 1)
	go func() {
		count, err := d.RunOnceAt(ctx, anchoredNow, map[string]int{}, semaphore.NewWeighted(2))
		done <- dispatchCall{count: count, err: err}
	}()

	assertHealthyProgressDuringContainmentWait(t, ctx, client, fixture, waiter, f, closeHealthy, healthyDownloaded)

	closeWait()
	for range 2 {
		awaitContainmentSignal(t, f.challengedStarted, "challenged engine admission")
	}
	select {
	case <-f.challengedStarted:
		t.Fatal("third challenged engine call exceeded the global limit of two")
	default:
	}
	closeChallenged()

	assertContainmentDispatchResult(t, awaitContainmentDispatch(t, done), f)

	assertContainmentHealthyOutcome(t, ctx, client, fixture)
	assertContainmentChallengeOutcome(t, ctx, client, fixture, anchoredNow, policy.SourcesCooldownIv)
	assertContainmentSentinel(t, sentinelPath)
}

func assertHealthyProgressDuringContainmentWait(
	t *testing.T,
	ctx context.Context,
	client *ent.Client,
	fixture containmentFixture,
	waiter *containmentWaiter,
	f *containmentFetcher,
	closeHealthy func(),
	healthyDownloaded <-chan struct{},
) {
	t.Helper()
	for range 3 {
		awaitContainmentSignal(t, waiter.entered, "challenged politeness wait")
	}
	awaitContainmentSignal(t, f.healthyStarted, "healthy engine admission")
	closeHealthy()
	awaitContainmentSignal(t, healthyDownloaded, "healthy persisted download")
	if got := client.Chapter.GetX(ctx, fixture.healthyChapter.ID).State; got != entchapter.StateDownloaded {
		t.Fatalf("healthy chapter state = %s, want downloaded while challenged source still waits", got)
	}
	select {
	case <-f.challengedStarted:
		t.Fatal("challenged engine call started before its politeness wait was released")
	default:
	}
}

func assertContainmentDispatchResult(t *testing.T, result dispatchCall, f *containmentFetcher) {
	t.Helper()
	if result.err != nil || result.count != 4 {
		t.Fatalf("RunOnceAt = count %d, err %v; want count 4, nil", result.count, result.err)
	}
	if got := f.peak.Load(); got != 2 {
		t.Fatalf("peak engine occupancy = %d, want exact global limit 2", got)
	}
	if got := f.fallbackCalls.Load(); got != 0 {
		t.Fatalf("lower-ranked fallback calls = %d, want 0", got)
	}
}

func assertContainmentSentinel(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("pre-existing library file stat: %v", err)
	}
	if info.Size() != 4 {
		t.Fatalf("pre-existing library file size = %d, want preserved size 4", info.Size())
	}
}

func seedContainmentFixture(t *testing.T, ctx context.Context, client *ent.Client) containmentFixture {
	t.Helper()
	challengedSeries := client.Series.Create().SetTitle("Challenged Queue").SetSlug("challenged-queue").SaveX(ctx)
	challengedProvider := client.SeriesProvider.Create().
		SetSeries(challengedSeries).
		SetProvider(containmentChallengedProvider).
		SetProviderName(containmentChallengedKey).
		SetImportance(20).
		SaveX(ctx)
	fixture := containmentFixture{}
	for index, key := range []string{"1", "2", "3"} {
		pc := client.ProviderChapter.Create().
			SetSeriesProvider(challengedProvider).
			SetChapterKey(key).
			SetNumber(float64(index + 1)).
			SetURL("https://challenged.example/" + key).
			SetProviderIndex(index).
			SetPageLinks([]fetcher.PageLink{{URL: "https://cached.example/" + key}}).
			SaveX(ctx)
		ch := client.Chapter.Create().
			SetSeries(challengedSeries).
			SetChapterKey(key).
			SetNumber(float64(index + 1)).
			SaveX(ctx)
		fixture.challengedPCs = append(fixture.challengedPCs, pc)
		fixture.challengedChapters = append(fixture.challengedChapters, ch)
	}

	healthySeries := client.Series.Create().SetTitle("Healthy Queue").SetSlug("healthy-queue").SaveX(ctx)
	fixture.healthyProvider = client.SeriesProvider.Create().
		SetSeries(healthySeries).
		SetProvider(containmentHealthyProvider).
		SetProviderName("healthy").
		SetImportance(20).
		SaveX(ctx)
	fallbackProvider := client.SeriesProvider.Create().
		SetSeries(healthySeries).
		SetProvider(containmentFallbackProvider).
		SetProviderName("fallback").
		SetImportance(5).
		SaveX(ctx)
	fixture.healthyPC = client.ProviderChapter.Create().
		SetSeriesProvider(fixture.healthyProvider).
		SetChapterKey("1").
		SetNumber(1).
		SetURL("https://healthy.example/1").
		SetProviderIndex(0).
		SaveX(ctx)
	fixture.fallbackPC = client.ProviderChapter.Create().
		SetSeriesProvider(fallbackProvider).
		SetChapterKey("1").
		SetNumber(1).
		SetURL("https://fallback.example/1").
		SetProviderIndex(0).
		SetPageLinks([]fetcher.PageLink{{URL: "https://cached.example/fallback"}}).
		SaveX(ctx)
	fixture.healthyChapter = client.Chapter.Create().
		SetSeries(healthySeries).
		SetChapterKey("1").
		SetNumber(1).
		SaveX(ctx)
	return fixture
}

func observeContainmentDownload(client *ent.Client) <-chan struct{} {
	downloaded := make(chan struct{})
	var once sync.Once
	client.Chapter.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			value, err := next.Mutate(ctx, mutation)
			if err == nil && mutationSetsChapterState(mutation, entchapter.StateDownloaded) {
				once.Do(func() { close(downloaded) })
			}
			return value, err
		})
	})
	return downloaded
}

func assertContainmentHealthyOutcome(t *testing.T, ctx context.Context, client *ent.Client, fixture containmentFixture) {
	t.Helper()
	chapter := client.Chapter.GetX(ctx, fixture.healthyChapter.ID)
	if chapter.State != entchapter.StateDownloaded || chapter.Filename == "" {
		t.Fatalf("healthy outcome = state %s, filename %q; want downloaded file", chapter.State, chapter.Filename)
	}
	if chapter.SatisfiedByProviderID == nil || *chapter.SatisfiedByProviderID != fixture.healthyProvider.ID {
		t.Fatalf("healthy satisfying provider = %v, want higher-ranked %s", chapter.SatisfiedByProviderID, fixture.healthyProvider.ID)
	}
	if chapter.SatisfiedImportance == nil || *chapter.SatisfiedImportance != fixture.healthyProvider.Importance {
		t.Fatalf("healthy satisfied importance = %v, want %d", chapter.SatisfiedImportance, fixture.healthyProvider.Importance)
	}
	fallback := client.ProviderChapter.GetX(ctx, fixture.fallbackPC.ID)
	if fallback.Attempts != 0 || len(fallback.PageLinks) != 1 {
		t.Fatalf("untouched fallback = attempts %d, links %d; want 0, 1", fallback.Attempts, len(fallback.PageLinks))
	}
	if attempts := client.ProviderChapter.GetX(ctx, fixture.healthyPC.ID).Attempts; attempts != 0 {
		t.Fatalf("healthy provider attempts = %d, want 0", attempts)
	}
}

func assertContainmentChallengeOutcome(t *testing.T, ctx context.Context, client *ent.Client, fixture containmentFixture, now time.Time, cooldown time.Duration) {
	t.Helper()
	for index, chapter := range fixture.challengedChapters {
		assertContainmentChallengeChapter(t, ctx, client, index, chapter, fixture.challengedPCs[index])
	}
	assertContainmentBreaker(t, ctx, client, now, cooldown)
}

func assertContainmentChallengeChapter(t *testing.T, ctx context.Context, client *ent.Client, index int, chapter *ent.Chapter, providerChapter *ent.ProviderChapter) {
	t.Helper()
	if got := client.Chapter.GetX(ctx, chapter.ID).State; got != entchapter.StateFailed {
		t.Fatalf("challenged chapter %d state = %s, want failed", index, got)
	}
	pc := client.ProviderChapter.GetX(ctx, providerChapter.ID)
	if pc.Attempts != 0 || pc.NextAttemptAt == nil || len(pc.PageLinks) != 0 {
		t.Fatalf("challenged source %d = attempts %d, next %v, links %d; want 0, set, 0", index, pc.Attempts, pc.NextAttemptAt, len(pc.PageLinks))
	}
}

func assertContainmentBreaker(t *testing.T, ctx context.Context, client *ent.Client, now time.Time, cooldown time.Duration) {
	t.Helper()
	row := client.SourceCircuitState.Query().
		Where(entsourcecircuitstate.SourceKeyEQ(containmentChallengedKey)).
		OnlyX(ctx)
	if row.ConsecutiveFailures != 3 {
		t.Fatalf("breaker failures = %d, want exact concurrent count 3", row.ConsecutiveFailures)
	}
	if row.CooldownUntil == nil || !row.CooldownUntil.Equal(now.Add(cooldown)) {
		t.Fatalf("breaker cooldown = %v, want exact trip time %v", row.CooldownUntil, now.Add(cooldown))
	}
	if row.FailingSince == nil || !row.FailingSince.Equal(now) {
		t.Fatalf("breaker failing_since = %v, want %v", row.FailingSince, now)
	}
}

func awaitContainmentSignal(t *testing.T, signal <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(admissionTestTimeout):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func awaitContainmentDispatch(t *testing.T, done <-chan dispatchCall) dispatchCall {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(2 * admissionTestTimeout):
		t.Fatal("timed out waiting for containment download pass")
		return dispatchCall{}
	}
}
