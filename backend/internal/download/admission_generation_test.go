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

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/sse"
)

type staggeredSelectionContextKey struct{}

type staggeredSelectionBarrier struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func installStaggeredSelectionBarrier(client *ent.Client) *staggeredSelectionBarrier {
	b := &staggeredSelectionBarrier{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	var chapterQueries atomic.Int32
	client.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
			if _, ok := query.(*ent.ChapterQuery); ok && ctx.Value(staggeredSelectionContextKey{}) != nil {
				if chapterQueries.Add(1) == 3 {
					close(b.entered)
					<-b.release
				}
			}
			return next.Query(ctx, query)
		})
	}))
	return b
}

func (b *staggeredSelectionBarrier) unblock() {
	b.once.Do(func() { close(b.release) })
}

type staggeredFetcher struct {
	calls atomic.Int32
	pages fetcher.ChapterPages
	err   error
}

func (f *staggeredFetcher) Fetch(context.Context, fetcher.FetchRef) (fetcher.ChapterPages, error) {
	f.calls.Add(1)
	return f.pages, f.err
}

type staggeredFixture struct {
	chapter *ent.Chapter
	pc      *ent.ProviderChapter
	key     string
}

func seedStaggeredFixture(t *testing.T, client *ent.Client, slug string, state entchapter.State) staggeredFixture {
	t.Helper()
	ctx := context.Background()
	s := client.Series.Create().SetTitle(slug).SetSlug(slug).SaveX(ctx)
	sp := client.SeriesProvider.Create().
		SetSeries(s).
		SetProvider("91").
		SetProviderName("generation-source").
		SetImportance(1).
		SaveX(ctx)
	pc := client.ProviderChapter.Create().
		SetSeriesProvider(sp).
		SetChapterKey("1").
		SetURL("https://example.test/1").
		SetProviderIndex(0).
		SaveX(ctx)
	ch := client.Chapter.Create().
		SetSeries(s).
		SetChapterKey("1").
		SetState(state).
		SaveX(ctx)
	return staggeredFixture{chapter: ch, pc: pc, key: "generation-source"}
}

func staggeredPolicy() settings.Static {
	return settings.Static{
		Retries:              3,
		Backoff:              time.Hour,
		DownloadConc:         1,
		SourcesFailureThresh: 1,
		SourcesCooldownIv:    time.Hour,
	}
}

func TestRunOnceAt_StaleSelectionCannotClaimNewFailureGeneration(t *testing.T) {
	for _, initial := range []entchapter.State{entchapter.StateWanted, entchapter.StateFailed} {
		t.Run(string(initial), func(t *testing.T) {
			testStaleSelectionCannotClaimNewFailureGeneration(t, initial)
		})
	}
}

func testStaleSelectionCannotClaimNewFailureGeneration(t *testing.T, initial entchapter.State) {
	t.Helper()
	ctx := context.Background()
	client := testdb.New(t)
	fixture := seedStaggeredFixture(t, client, "stale-generation-"+string(initial), initial)
	barrier := installStaggeredSelectionBarrier(client)
	t.Cleanup(barrier.unblock)
	policy := staggeredPolicy()
	gate := sourcegate.NewService(client, policy)
	f := &staggeredFetcher{err: errors.New("connection reset by peer")}
	d := New(client, f, sse.NewHub(), Config{Storage: t.TempDir()}, policy, gate)
	now := time.Now()

	staleDone := make(chan dispatchCall, 1)
	staleCtx := context.WithValue(ctx, staggeredSelectionContextKey{}, true)
	go func() {
		count, err := d.RunOnceAt(staleCtx, now, map[string]int{}, nil)
		staleDone <- dispatchCall{count: count, err: err}
	}()
	<-barrier.entered

	winnerCount, winnerErr := d.RunOnceAt(ctx, now, map[string]int{}, nil)
	assertCycleCall(t, dispatchCall{count: winnerCount, err: winnerErr}, 1)
	beforeRelease, beforeBreaker := assertWinnerBlockedSource(t, client, fixture, gate, f, now)

	barrier.unblock()
	assertCycleCall(t, <-staleDone, 0)
	assertStaleFailureGenerationUnchanged(t, client, fixture, f, beforeRelease, beforeBreaker)
}

func assertWinnerBlockedSource(t *testing.T, client *ent.Client, fixture staggeredFixture, gate *sourcegate.Service, f *staggeredFetcher, now time.Time) (*ent.ProviderChapter, *ent.SourceCircuitState) {
	t.Helper()
	if got := f.calls.Load(); got != 1 {
		t.Fatalf("winner engine calls = %d, want 1", got)
	}
	pc := client.ProviderChapter.GetX(context.Background(), fixture.pc.ID)
	if pc.NextAttemptAt == nil || !pc.NextAttemptAt.After(now) {
		t.Fatalf("winner next_attempt_at = %v, want a live cooldown after %v", pc.NextAttemptAt, now)
	}
	if gate.IsAvailable(context.Background(), fixture.key, now) {
		t.Fatal("winner did not trip the source breaker before the stale selection resumed")
	}
	return pc, client.SourceCircuitState.Query().OnlyX(context.Background())
}

func assertStaleFailureGenerationUnchanged(t *testing.T, client *ent.Client, fixture staggeredFixture, f *staggeredFetcher, beforePC *ent.ProviderChapter, beforeBreaker *ent.SourceCircuitState) {
	t.Helper()
	ctx := context.Background()
	if got := f.calls.Load(); got != 1 {
		t.Fatalf("engine calls = %d, want only the winning generation's call", got)
	}
	if got := client.Chapter.GetX(ctx, fixture.chapter.ID).State; got != entchapter.StateFailed {
		t.Fatalf("chapter state = %s, want failed", got)
	}
	after := client.ProviderChapter.GetX(ctx, fixture.pc.ID)
	if after.Attempts != beforePC.Attempts || after.LastError != beforePC.LastError ||
		!sameOptionalTime(after.NextAttemptAt, beforePC.NextAttemptAt) {
		t.Fatalf("stale selection mutated retry state: before=%+v after=%+v", beforePC, after)
	}
	afterBreaker := client.SourceCircuitState.Query().OnlyX(ctx)
	if afterBreaker.ConsecutiveFailures != beforeBreaker.ConsecutiveFailures ||
		afterBreaker.LastError != beforeBreaker.LastError ||
		!sameOptionalTime(afterBreaker.CooldownUntil, beforeBreaker.CooldownUntil) {
		t.Fatalf("stale selection mutated breaker state: before=%+v after=%+v", beforeBreaker, afterBreaker)
	}
}

func TestRunOnceAt_StaleFailedSelectionCannotClaimLocalFailureGeneration(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	fixture := seedStaggeredFixture(t, client, "stale-local-failure-generation", entchapter.StateFailed)
	barrier := installStaggeredSelectionBarrier(client)
	t.Cleanup(barrier.unblock)
	policy := staggeredPolicy()
	f := &staggeredFetcher{pages: fetcher.ChapterPages{
		Pages:     []fetcher.PageImage{{Data: []byte{0xAB}, Ext: "jpg"}},
		PageCount: 1,
	}}
	blockedStorage := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedStorage, []byte("file"), 0o600); err != nil {
		t.Fatalf("create blocked storage fixture: %v", err)
	}
	d := New(client, f, sse.NewHub(), Config{Storage: blockedStorage}, policy, nil)
	now := time.Now()

	staleDone := make(chan dispatchCall, 1)
	staleCtx := context.WithValue(ctx, staggeredSelectionContextKey{}, true)
	go func() {
		count, err := d.RunOnceAt(staleCtx, now, map[string]int{}, nil)
		staleDone <- dispatchCall{count: count, err: err}
	}()
	<-barrier.entered

	winnerCount, winnerErr := d.RunOnceAt(ctx, now, map[string]int{}, nil)
	assertCycleCall(t, dispatchCall{count: winnerCount, err: winnerErr}, 1)
	beforeRelease := client.ProviderChapter.GetX(ctx, fixture.pc.ID)
	if beforeRelease.Attempts != 0 || beforeRelease.NextAttemptAt != nil || beforeRelease.LastError != "" {
		t.Fatalf("local render failure unexpectedly changed candidate retry state: %+v", beforeRelease)
	}

	barrier.unblock()
	assertCycleCall(t, <-staleDone, 0)
	if got := f.calls.Load(); got != 1 {
		t.Fatalf("engine calls = %d, want only the winning failed generation's call", got)
	}
	if got := client.Chapter.GetX(ctx, fixture.chapter.ID).State; got != entchapter.StateFailed {
		t.Fatalf("chapter state = %s, want failed", got)
	}
}

func TestRunOnceAt_StaleSelectionYieldsAfterCandidateCooldownChanges(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	fixture := seedStaggeredFixture(t, client, "stale-candidate-change", entchapter.StateWanted)
	barrier := installStaggeredSelectionBarrier(client)
	t.Cleanup(barrier.unblock)
	policy := staggeredPolicy()
	f := &staggeredFetcher{err: errors.New("connection reset by peer")}
	d := New(client, f, sse.NewHub(), Config{Storage: t.TempDir()}, policy, nil)
	now := time.Now()

	staleDone := make(chan dispatchCall, 1)
	staleCtx := context.WithValue(ctx, staggeredSelectionContextKey{}, true)
	go func() {
		count, err := d.RunOnceAt(staleCtx, now, map[string]int{}, nil)
		staleDone <- dispatchCall{count: count, err: err}
	}()
	<-barrier.entered

	future := now.Add(2 * time.Hour)
	client.ProviderChapter.UpdateOneID(fixture.pc.ID).
		SetLastError("new owner cooldown").
		SetNextAttemptAt(future).
		ExecX(ctx)
	expectedPC := client.ProviderChapter.GetX(ctx, fixture.pc.ID)
	barrier.unblock()
	assertCycleCall(t, <-staleDone, 0)
	if got := f.calls.Load(); got != 0 {
		t.Fatalf("engine calls = %d, want 0 after candidate cooldown changed", got)
	}
	if got := client.Chapter.GetX(ctx, fixture.chapter.ID).State; got != entchapter.StateWanted {
		t.Fatalf("chapter state = %s, want wanted", got)
	}
	gotPC := client.ProviderChapter.GetX(ctx, fixture.pc.ID)
	if gotPC.LastError != "new owner cooldown" || !sameOptionalTime(gotPC.NextAttemptAt, expectedPC.NextAttemptAt) {
		t.Fatalf("stale selection overwrote candidate cooldown: %+v", gotPC)
	}
}

func TestRunOnceAt_StaleSelectionYieldsAfterCandidateSnapshotChanges(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	fixture := seedStaggeredFixture(t, client, "stale-candidate-snapshot", entchapter.StateWanted)
	barrier := installStaggeredSelectionBarrier(client)
	t.Cleanup(barrier.unblock)
	policy := staggeredPolicy()
	f := &staggeredFetcher{err: errors.New("connection reset by peer")}
	d := New(client, f, sse.NewHub(), Config{Storage: t.TempDir()}, policy, nil)
	now := time.Now()

	staleDone := make(chan dispatchCall, 1)
	staleCtx := context.WithValue(ctx, staggeredSelectionContextKey{}, true)
	go func() {
		count, err := d.RunOnceAt(staleCtx, now, map[string]int{}, nil)
		staleDone <- dispatchCall{count: count, err: err}
	}()
	<-barrier.entered

	const replacementURL = "https://example.test/replaced"
	client.ProviderChapter.UpdateOneID(fixture.pc.ID).
		SetURL(replacementURL).
		ExecX(ctx)
	barrier.unblock()
	assertCycleCall(t, <-staleDone, 0)
	if got := f.calls.Load(); got != 0 {
		t.Fatalf("engine calls = %d, want 0 after candidate snapshot changed", got)
	}
	if got := client.Chapter.GetX(ctx, fixture.chapter.ID).State; got != entchapter.StateWanted {
		t.Fatalf("chapter state = %s, want wanted", got)
	}
	if got := client.ProviderChapter.GetX(ctx, fixture.pc.ID).URL; got != replacementURL {
		t.Fatalf("candidate URL = %q, want replacement %q", got, replacementURL)
	}
}

func TestRunOnceAt_StaleSelectionYieldsAfterBreakerTrips(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	fixture := seedStaggeredFixture(t, client, "stale-breaker-change", entchapter.StateWanted)
	barrier := installStaggeredSelectionBarrier(client)
	t.Cleanup(barrier.unblock)
	policy := staggeredPolicy()
	gate := sourcegate.NewService(client, policy)
	f := &staggeredFetcher{err: errors.New("connection reset by peer")}
	d := New(client, f, sse.NewHub(), Config{Storage: t.TempDir()}, policy, gate)
	now := time.Now()

	staleDone := make(chan dispatchCall, 1)
	staleCtx := context.WithValue(ctx, staggeredSelectionContextKey{}, true)
	go func() {
		count, err := d.RunOnceAt(staleCtx, now, map[string]int{}, nil)
		staleDone <- dispatchCall{count: count, err: err}
	}()
	<-barrier.entered

	client.SourceCircuitState.Create().
		SetSourceKey(fixture.key).
		SetConsecutiveFailures(1).
		SetCooldownUntil(now.Add(time.Hour)).
		SetLastError("new breaker trip").
		SaveX(ctx)
	barrier.unblock()
	assertCycleCall(t, <-staleDone, 0)
	if got := f.calls.Load(); got != 0 {
		t.Fatalf("engine calls = %d, want 0 after breaker trip", got)
	}
	if got := client.Chapter.GetX(ctx, fixture.chapter.ID).State; got != entchapter.StateWanted {
		t.Fatalf("chapter state = %s, want wanted", got)
	}
}

func TestRunOnceAt_StaleSelectionYieldsAfterWinnerSuccess(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	fixture := seedStaggeredFixture(t, client, "stale-winner-success", entchapter.StateWanted)
	barrier := installStaggeredSelectionBarrier(client)
	t.Cleanup(barrier.unblock)
	policy := staggeredPolicy()
	f := &staggeredFetcher{pages: fetcher.ChapterPages{
		Pages:     []fetcher.PageImage{{Data: []byte{0xAB}, Ext: "jpg"}},
		PageCount: 1,
	}}
	d := New(client, f, sse.NewHub(), Config{Storage: t.TempDir()}, policy, nil)
	now := time.Now()

	staleDone := make(chan dispatchCall, 1)
	staleCtx := context.WithValue(ctx, staggeredSelectionContextKey{}, true)
	go func() {
		count, err := d.RunOnceAt(staleCtx, now, map[string]int{}, nil)
		staleDone <- dispatchCall{count: count, err: err}
	}()
	<-barrier.entered

	winnerCount, winnerErr := d.RunOnceAt(ctx, now, map[string]int{}, nil)
	assertCycleCall(t, dispatchCall{count: winnerCount, err: winnerErr}, 1)
	barrier.unblock()
	assertCycleCall(t, <-staleDone, 0)
	if got := f.calls.Load(); got != 1 {
		t.Fatalf("engine calls = %d, want only the winner's call", got)
	}
	if got := client.Chapter.GetX(ctx, fixture.chapter.ID).State; got != entchapter.StateDownloaded {
		t.Fatalf("chapter state = %s, want downloaded", got)
	}
}

func sameOptionalTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}
