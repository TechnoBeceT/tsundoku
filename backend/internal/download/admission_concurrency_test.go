package download

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/sse"
)

var errClaimTestFetch = errors.New("chapter has no pages")

type dispatchCall struct {
	count int
	err   error
}

type claimBlockingFetcher struct {
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newClaimBlockingFetcher() *claimBlockingFetcher {
	return &claimBlockingFetcher{
		started: make(chan struct{}, 2),
		release: make(chan struct{}),
	}
}

func (f *claimBlockingFetcher) Fetch(context.Context, fetcher.FetchRef) (fetcher.ChapterPages, error) {
	f.calls.Add(1)
	f.started <- struct{}{}
	<-f.release
	return fetcher.ChapterPages{}, errClaimTestFetch
}

func (f *claimBlockingFetcher) unblock() {
	f.once.Do(func() { close(f.release) })
}

type stateMutationBarrier struct {
	entered chan struct{}
	release chan struct{}
}

func installStateMutationBarrier(client *ent.Client, target entchapter.State) stateMutationBarrier {
	barrier := stateMutationBarrier{
		entered: make(chan struct{}, 2),
		release: make(chan struct{}),
	}
	client.Chapter.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if mutationSetsChapterState(mutation, target) {
				barrier.entered <- struct{}{}
				<-barrier.release
			}
			return next.Mutate(ctx, mutation)
		})
	})
	return barrier
}

func TestRunOnceAt_ConcurrentCyclesOnlyOneClaimsChapter(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	s := client.Series.Create().SetTitle("Concurrent download claim").SetSlug("concurrent-download-claim").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("41").SetProviderName("source").SetImportance(1).SaveX(ctx)
	pc := client.ProviderChapter.Create().
		SetSeriesProvider(sp).SetChapterKey("1").SetURL("https://example.test/1").SetProviderIndex(0).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("1").SaveX(ctx)
	barrier := installStateMutationBarrier(client, entchapter.StateDownloading)
	f := newClaimBlockingFetcher()
	t.Cleanup(f.unblock)
	d := New(client, f, sse.NewHub(), Config{Storage: t.TempDir()}, admissionPolicy(), nil)

	done := make(chan dispatchCall, 2)
	for range 2 {
		go func() {
			count, err := d.RunOnceAt(ctx, time.Now(), map[string]int{}, nil)
			done <- dispatchCall{count: count, err: err}
		}()
	}
	for range 2 {
		<-barrier.entered
	}
	close(barrier.release)
	<-f.started

	var loser dispatchCall
	duplicate := false
	select {
	case <-f.started:
		duplicate = true
	case loser = <-done:
	case <-time.After(admissionTestTimeout):
		t.Fatal("neither a losing claimant nor a duplicate engine call completed")
	}
	f.unblock()
	if duplicate {
		<-done
		<-done
		t.Fatalf("engine calls = %d, want exactly 1 concurrent download owner", f.calls.Load())
	}
	winner := <-done
	assertCycleCall(t, loser, 0)
	assertCycleCall(t, winner, 1)
	if got := f.calls.Load(); got != 1 {
		t.Fatalf("engine calls = %d, want 1", got)
	}
	if got := client.Chapter.GetX(ctx, ch.ID).State; got != entchapter.StateFailed {
		t.Fatalf("chapter state = %s, want failed after the sole engine attempt", got)
	}
	if got := client.ProviderChapter.GetX(ctx, pc.ID).Attempts; got != 1 {
		t.Fatalf("provider attempts = %d, want 1 from the sole engine owner", got)
	}
}

func TestUpgradeAll_ConcurrentCyclesOnlyOneClaimsChapter(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	fixture := seedConcurrentUpgrade(t, client, "concurrent-upgrade-claim")
	barrier := installStateMutationBarrier(client, entchapter.StateUpgrading)
	f := newClaimBlockingFetcher()
	t.Cleanup(f.unblock)
	d := New(client, f, sse.NewHub(), Config{Storage: t.TempDir()}, admissionPolicy(), nil)

	done := make(chan upgradeCall, 2)
	for range 2 {
		go func() {
			count, err := d.UpgradeAll(ctx, nil, nil)
			done <- upgradeCall{count: count, err: err}
		}()
	}
	for range 2 {
		<-barrier.entered
	}
	close(barrier.release)
	<-f.started

	var loser upgradeCall
	duplicate := false
	select {
	case <-f.started:
		duplicate = true
	case loser = <-done:
	case <-time.After(admissionTestTimeout):
		t.Fatal("neither a losing upgrade claimant nor a duplicate engine call completed")
	}
	f.unblock()
	if duplicate {
		<-done
		<-done
		t.Fatalf("engine calls = %d, want exactly 1 concurrent upgrade owner", f.calls.Load())
	}
	winner := <-done
	assertUpgradeCycleCall(t, loser, 0)
	assertUpgradeCycleCall(t, winner, 1)
	if got := f.calls.Load(); got != 1 {
		t.Fatalf("engine calls = %d, want 1", got)
	}
	if got := client.Chapter.GetX(ctx, fixture.chapter.ID).State; got != entchapter.StateDownloaded {
		t.Fatalf("chapter state = %s, want downloaded after handled upgrade failure", got)
	}
	if got := client.ProviderChapter.GetX(ctx, fixture.highPC.ID).Attempts; got != 1 {
		t.Fatalf("upgrade target attempts = %d, want 1 from the sole engine owner", got)
	}
}

func TestUpgradeAll_ClaimRequiresFrozenProvenance(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	fixture := seedConcurrentUpgrade(t, client, "frozen-upgrade-claim")
	barrier := installStateMutationBarrier(client, entchapter.StateUpgrading)
	f := &admissionFetcher{err: errClaimTestFetch}
	d := New(client, f, sse.NewHub(), Config{Storage: t.TempDir()}, admissionPolicy(), nil)

	done := make(chan upgradeCall, 1)
	go func() {
		count, err := d.UpgradeAll(ctx, nil, nil)
		done <- upgradeCall{count: count, err: err}
	}()
	<-barrier.entered
	client.Chapter.UpdateOneID(fixture.chapter.ID).SetSatisfiedImportance(7).ExecX(ctx)
	close(barrier.release)
	result := <-done

	assertUpgradeCycleCall(t, result, 0)
	if got := f.calls.Load(); got != 0 {
		t.Fatalf("engine calls = %d, want 0 after frozen provenance changed", got)
	}
	got := client.Chapter.GetX(ctx, fixture.chapter.ID)
	if got.State != entchapter.StateUpgradeAvailable {
		t.Fatalf("chapter state = %s, want upgrade_available after losing the frozen claim", got.State)
	}
	if got.SatisfiedImportance == nil || *got.SatisfiedImportance != 7 {
		t.Fatalf("satisfied_importance = %v, want concurrent value 7 preserved", got.SatisfiedImportance)
	}
	if attempts := client.ProviderChapter.GetX(ctx, fixture.highPC.ID).Attempts; attempts != 0 {
		t.Fatalf("upgrade target attempts = %d, want 0 without engine ownership", attempts)
	}
}

type staleUpgradeResolverKey struct{}

func TestUpgradeAll_StaleNoFetchResolverCannotOverwriteActiveOwner(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	fixture := seedConcurrentUpgrade(t, client, "stale-upgrade-resolver")
	client.SourceCircuitState.Create().
		SetSourceKey(fixture.high.ProviderName).
		SetConsecutiveFailures(1).
		SetCooldownUntil(time.Now().Add(time.Hour)).
		SaveX(ctx)

	queryEntered := make(chan struct{})
	queryRelease := make(chan struct{})
	var queryOnce sync.Once
	client.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
			if _, ok := query.(*ent.SourceCircuitStateQuery); ok && ctx.Value(staleUpgradeResolverKey{}) != nil {
				queryOnce.Do(func() {
					close(queryEntered)
					<-queryRelease
				})
			}
			return next.Query(ctx, query)
		})
	}))

	f := newClaimBlockingFetcher()
	t.Cleanup(f.unblock)
	policy := settings.Static{Retries: 3, DownloadConc: 1, SourcesCooldownIv: time.Hour}
	stale := New(client, f, sse.NewHub(), Config{Storage: t.TempDir()}, policy, sourcegate.NewService(client, policy))
	winner := New(client, f, sse.NewHub(), Config{Storage: t.TempDir()}, policy, nil)
	staleDone := make(chan upgradeCall, 1)
	staleCtx := context.WithValue(ctx, staleUpgradeResolverKey{}, true)
	go func() {
		count, err := stale.UpgradeAll(staleCtx, nil, nil)
		staleDone <- upgradeCall{count: count, err: err}
	}()
	<-queryEntered

	winnerDone := make(chan upgradeCall, 1)
	go func() {
		count, err := winner.UpgradeAll(ctx, nil, nil)
		winnerDone <- upgradeCall{count: count, err: err}
	}()
	<-f.started
	if got := client.Chapter.GetX(ctx, fixture.chapter.ID).State; got != entchapter.StateUpgrading {
		t.Fatalf("winner state = %s, want upgrading while its engine call is blocked", got)
	}

	close(queryRelease)
	var staleResult upgradeCall
	select {
	case staleResult = <-staleDone:
	case <-time.After(admissionTestTimeout):
		t.Fatal("stale no-fetch resolver did not yield while the real owner was blocked")
	}
	stateWhileBlocked := client.Chapter.GetX(ctx, fixture.chapter.ID).State
	f.unblock()
	winnerResult := <-winnerDone

	assertUpgradeCycleCall(t, staleResult, 0)
	assertUpgradeCycleCall(t, winnerResult, 1)
	if stateWhileBlocked != entchapter.StateUpgrading {
		t.Fatalf("stale resolver overwrote active owner state with %s, want upgrading", stateWhileBlocked)
	}
	if got := f.calls.Load(); got != 1 {
		t.Fatalf("engine calls = %d, want only the winning cycle's call", got)
	}
}

func assertCycleCall(t *testing.T, result dispatchCall, wantCount int) {
	t.Helper()
	if result.err != nil {
		t.Fatalf("RunOnceAt error = %v", result.err)
	}
	if result.count != wantCount {
		t.Fatalf("RunOnceAt count = %d, want %d", result.count, wantCount)
	}
}

func assertUpgradeCycleCall(t *testing.T, result upgradeCall, wantCount int) {
	t.Helper()
	if result.err != nil {
		t.Fatalf("UpgradeAll error = %v", result.err)
	}
	if result.count != wantCount {
		t.Fatalf("UpgradeAll count = %d, want %d", result.count, wantCount)
	}
}

type concurrentUpgradeFixture struct {
	chapter *ent.Chapter
	high    *ent.SeriesProvider
	highPC  *ent.ProviderChapter
}

func seedConcurrentUpgrade(t *testing.T, client *ent.Client, slug string) concurrentUpgradeFixture {
	t.Helper()
	ctx := context.Background()
	s := client.Series.Create().SetTitle(slug).SetSlug(slug).SaveX(ctx)
	low := client.SeriesProvider.Create().
		SetSeries(s).SetProvider("41").SetProviderName("source-low").SetImportance(1).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProvider(low).SetChapterKey("1").SetURL("https://low.example.test/1").SetProviderIndex(0).SaveX(ctx)
	high := client.SeriesProvider.Create().
		SetSeries(s).SetProvider("42").SetProviderName("source-high").SetImportance(10).SaveX(ctx)
	highPC := client.ProviderChapter.Create().
		SetSeriesProvider(high).SetChapterKey("1").SetURL("https://high.example.test/1").SetProviderIndex(0).SaveX(ctx)
	ch := client.Chapter.Create().
		SetSeries(s).
		SetChapterKey("1").
		SetState(entchapter.StateUpgradeAvailable).
		SetSatisfiedByProviderID(low.ID).
		SetSatisfiedImportance(low.Importance).
		SaveX(ctx)
	return concurrentUpgradeFixture{chapter: ch, high: high, highPC: highPC}
}
