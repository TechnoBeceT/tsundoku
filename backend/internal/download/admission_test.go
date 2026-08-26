package download

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sse"
)

// admissionFetcher is the narrow engine double used to prove the dispatcher's
// own admission boundary. Its calls are real Dispatcher fetch calls; only the
// external engine is replaced.
type admissionFetcher struct {
	calls atomic.Int32
	err   error
	panic bool
}

type blockingAdmissionFetcher struct {
	started chan string
	release <-chan struct{}
	cur     atomic.Int32
	peak    atomic.Int32
}

type blockingWaiter struct {
	entered chan struct{}
	release <-chan struct{}
}

func (w blockingWaiter) Wait(ctx context.Context, sourceKey string) {
	if sourceKey != "delayed" {
		return
	}
	close(w.entered)
	select {
	case <-w.release:
	case <-ctx.Done():
	}
}

func (f *blockingAdmissionFetcher) Fetch(_ context.Context, ref fetcher.FetchRef) (fetcher.ChapterPages, error) {
	cur := f.cur.Add(1)
	for {
		peak := f.peak.Load()
		if cur <= peak || f.peak.CompareAndSwap(peak, cur) {
			break
		}
	}
	f.started <- ref.Provider
	<-f.release
	f.cur.Add(-1)
	return fetcher.ChapterPages{}, nil
}

func (f *admissionFetcher) Fetch(context.Context, fetcher.FetchRef) (fetcher.ChapterPages, error) {
	f.calls.Add(1)
	if f.panic {
		panic("engine panic")
	}
	return fetcher.ChapterPages{}, f.err
}

// TestFetchWithAdmission_PolitenessWaitLeavesGlobalCapacityForHealthySource
// is the Task 2 regression proof. The delayed source has already reserved its
// next slot, so it is sleeping in sourcegate.Wait; the healthy source must still
// reach its engine call with the only global permit available.
func TestFetchWithAdmission_PolitenessWaitLeavesGlobalCapacityForHealthySource(t *testing.T) {
	ctx := context.Background()
	global := semaphore.NewWeighted(1)
	releaseWait := make(chan struct{})
	delayedDone := make(chan error, 1)
	d := &Dispatcher{f: &admissionFetcher{}, waiter: blockingWaiter{entered: make(chan struct{}), release: releaseWait}}
	go func() {
		delayedDone <- admissionCall(d, ctx, "delayed", global)
	}()
	<-d.waiter.(blockingWaiter).entered
	healthyDone := make(chan error, 1)
	go func() {
		healthyDone <- admissionCall(d, ctx, "healthy", global)
	}()
	select {
	case err := <-healthyDone:
		if err != nil {
			t.Fatalf("healthy admission: %v", err)
		}
	case <-time.After(admissionTestTimeout):
		t.Fatal("healthy source could not begin while delayed source waited for politeness")
	}

	close(releaseWait)
	if err := <-delayedDone; err != nil {
		t.Fatalf("delayed admission: %v", err)
	}
}

func TestFetchWithAdmission_CancellationDuringPolitenessWaitDoesNotLeakPermit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	global := semaphore.NewWeighted(1)
	d := &Dispatcher{f: &admissionFetcher{}, waiter: blockingWaiter{entered: make(chan struct{}), release: make(chan struct{})}}

	done := make(chan error, 1)
	go func() {
		done <- admissionCall(d, ctx, "delayed", global)
	}()
	<-d.waiter.(blockingWaiter).entered
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("politeness wait error = %v, want context canceled", err)
		}
	case <-time.After(admissionTestTimeout):
		t.Fatal("politeness wait did not stop after cancellation")
	}
	if !global.TryAcquire(1) {
		t.Fatal("global admission leaked during cancelled politeness wait")
	}
	global.Release(1)
	if got := d.f.(*admissionFetcher).calls.Load(); got != 0 {
		t.Fatalf("engine calls = %d, want 0 after cancelled politeness wait", got)
	}
}

func TestFetchWithAdmission_GlobalCapBoundsActualEngineCalls(t *testing.T) {
	global := semaphore.NewWeighted(1)
	release := make(chan struct{})
	f := &blockingAdmissionFetcher{started: make(chan string, 2), release: release}
	d := &Dispatcher{f: f}
	done := make(chan error, 2)
	for _, source := range []string{"first", "second"} {
		go func(source string) {
			done <- admissionCall(d, context.Background(), source, global)
		}(source)
	}

	<-f.started
	select {
	case source := <-f.started:
		t.Fatalf("second engine call %q began while the sole global permit was held", source)
	case <-time.After(50 * time.Millisecond):
		// One held call is enough to show a second cannot enter the engine.
	}
	close(release)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatalf("admission: %v", err)
		}
	}
	if got := f.peak.Load(); got != 1 {
		t.Fatalf("actual engine peak = %d, want global cap 1", got)
	}
}

func admissionCall(d *Dispatcher, ctx context.Context, sourceKey string, global *semaphore.Weighted) error {
	_, err := d.fetchWithAdmission(ctx, sourceKey, fetcher.FetchRef{}, newProviderLimiter(1), global, nil)
	return err
}

func TestFetchWithAdmission_CancellationDuringGlobalWaitDoesNotLeakPermit(t *testing.T) {
	global := semaphore.NewWeighted(1)
	if !global.TryAcquire(1) {
		t.Fatal("reserve global admission")
	}

	d := &Dispatcher{f: &admissionFetcher{}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := d.fetchWithAdmission(ctx, "ready", fetcher.FetchRef{}, newProviderLimiter(1), global, nil)
		done <- err
	}()

	// The waiter is now blocked on the occupied semaphore. Cancelling it must
	// return promptly without calling the engine or taking an extra permit.
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("fetchWithAdmission error = %v, want context canceled", err)
	}
	global.Release(1)
	if !global.TryAcquire(1) {
		t.Fatal("global admission leaked after cancellation while waiting")
	}
	global.Release(1)
	if got := d.f.(*admissionFetcher).calls.Load(); got != 0 {
		t.Fatalf("engine calls = %d, want 0 after cancelled global admission", got)
	}
}

func TestFetchWithAdmission_ReleasesGlobalPermitOnEngineError(t *testing.T) {
	global := semaphore.NewWeighted(1)
	want := errors.New("engine unavailable")
	d := &Dispatcher{f: &admissionFetcher{err: want}}

	_, err := d.fetchWithAdmission(context.Background(), "ready", fetcher.FetchRef{}, newProviderLimiter(1), global, nil)
	if !errors.Is(err, want) {
		t.Fatalf("fetchWithAdmission error = %v, want %v", err, want)
	}
	if !global.TryAcquire(1) {
		t.Fatal("global admission leaked after engine error")
	}
	global.Release(1)
}

func TestFetchWithAdmission_ReleasesGlobalPermitWhenEnginePanics(t *testing.T) {
	global := semaphore.NewWeighted(1)
	d := &Dispatcher{f: &admissionFetcher{panic: true}}

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("fetchWithAdmission did not preserve engine panic")
			}
		}()
		_, _ = d.fetchWithAdmission(context.Background(), "ready", fetcher.FetchRef{}, newProviderLimiter(1), global, nil)
	}()
	if !global.TryAcquire(1) {
		t.Fatal("global admission leaked after engine panic")
	}
	global.Release(1)
}

// This keeps timing bounds visible at the test call sites: cancellation and
// admission are asynchronous, but neither test permits a stuck goroutine.
const admissionTestTimeout = time.Second

func TestRunOnceAt_CancelledGlobalAdmissionLeavesChapterQueued(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	s := client.Series.Create().SetTitle("Admission").SetSlug("admission").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("source").SetImportance(1).SaveX(ctx)
	pc := client.ProviderChapter.Create().SetSeriesProvider(sp).SetChapterKey("1").SetURL("https://example.test/1").SetProviderIndex(0).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("1").SaveX(ctx)
	f := &admissionFetcher{}
	d := New(client, f, sse.NewHub(), Config{}, settings.Static{Retries: 3, DownloadConc: 1}, nil)
	global := semaphore.NewWeighted(1)
	if !global.TryAcquire(1) {
		t.Fatal("reserve global admission")
	}
	cctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { _, err := d.RunOnceAt(cctx, time.Now(), map[string]int{}, global); done <- err }()
	select {
	case err := <-done:
		t.Fatalf("RunOnceAt returned before cancellation: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("RunOnceAt: %v", err)
	}
	global.Release(1)
	got := client.Chapter.GetX(ctx, ch.ID)
	if got.State != entchapter.StateWanted {
		t.Fatalf("state = %s, want wanted", got.State)
	}
	if got.LastError != "" || pc.Attempts != 0 || len(pc.PageLinks) != 0 || f.calls.Load() != 0 {
		t.Fatalf("cancelled admission mutated chapter/source state")
	}
}

func TestUpgradeAll_CancelledGlobalAdmissionLeavesChapterAvailable(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	s := client.Series.Create().SetTitle("Upgrade admission").SetSlug("upgrade-admission").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("source").SetImportance(2).SaveX(ctx)
	pc := client.ProviderChapter.Create().SetSeriesProvider(sp).SetChapterKey("1").SetURL("https://example.test/1").SetProviderIndex(0).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("1").SetState(entchapter.StateUpgradeAvailable).SaveX(ctx)
	f := &admissionFetcher{}
	d := New(client, f, sse.NewHub(), Config{}, settings.Static{Retries: 3, DownloadConc: 1}, nil)
	global := semaphore.NewWeighted(1)
	if !global.TryAcquire(1) {
		t.Fatal("reserve global admission")
	}
	cctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { _, err := d.UpgradeAll(cctx, nil, global); done <- err }()
	select {
	case err := <-done:
		t.Fatalf("UpgradeAll returned before cancellation: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("UpgradeAll: %v", err)
	}
	global.Release(1)
	got := client.Chapter.GetX(ctx, ch.ID)
	if got.State != entchapter.StateUpgradeAvailable {
		t.Fatalf("state = %s, want upgrade_available", got.State)
	}
	if got.LastError != "" || pc.Attempts != 0 || len(pc.PageLinks) != 0 || f.calls.Load() != 0 {
		t.Fatalf("cancelled admission mutated upgrade/source state")
	}
}
