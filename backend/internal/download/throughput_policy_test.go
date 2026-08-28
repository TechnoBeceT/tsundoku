package download

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/sourcethroughput"
)

type stubThroughputPolicies struct {
	overrides map[int64]sourcethroughput.Override
	err       error
	calls     int
}

func (s *stubThroughputPolicies) Snapshot(context.Context) (map[int64]sourcethroughput.Override, error) {
	s.calls++
	return s.overrides, s.err
}

func TestSourceConcurrencyPolicyForUsesOverrideAndGlobalDefault(t *testing.T) {
	one := 1
	policy := sourceConcurrencyPolicy{
		global:    4,
		overrides: map[int64]int{101: one},
	}

	if got := policy.For(101); got != 1 {
		t.Errorf("For(overridden source) = %d, want 1", got)
	}
	if got := policy.For(202); got != 4 {
		t.Errorf("For(inherited source) = %d, want global 4", got)
	}
	if got := policy.For(0); got != 4 {
		t.Errorf("For(disk source) = %d, want global 4", got)
	}
}

func TestBeginCycleSnapshotsOnceAndFailsClosed(t *testing.T) {
	wantErr := errors.New("policy store unavailable")
	store := &stubThroughputPolicies{err: wantErr}
	d := &Dispatcher{retry: fixedRetrySettings{concurrency: 3}, throughput: store}

	_, err := d.BeginCycle(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("BeginCycle error = %v, want wrapped %v", err, wantErr)
	}
	if store.calls != 1 {
		t.Errorf("Snapshot calls = %d, want 1", store.calls)
	}
}

func TestRunOnceAtSnapshotFailurePrecedesChapterSelection(t *testing.T) {
	wantErr := errors.New("policy store unavailable")
	d := &Dispatcher{
		retry:      fixedRetrySettings{concurrency: 3},
		throughput: &stubThroughputPolicies{err: wantErr},
	}

	_, err := d.RunOnceAt(context.Background(), time.Now(), map[string]int{}, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("RunOnceAt error = %v, want policy error %v before nil client is read", err, wantErr)
	}
}

func TestUpgradeAllSnapshotFailurePrecedesChapterSelection(t *testing.T) {
	wantErr := errors.New("policy store unavailable")
	d := &Dispatcher{
		retry:      fixedRetrySettings{concurrency: 3},
		throughput: &stubThroughputPolicies{err: wantErr},
	}

	_, err := d.UpgradeAll(context.Background(), nil, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("UpgradeAll error = %v, want policy error %v before nil client is read", err, wantErr)
	}
}

func TestUpgradeAllEmptySelectionStillReturnsSnapshotFailure(t *testing.T) {
	wantErr := errors.New("policy store unavailable")
	d := &Dispatcher{
		client:     testdb.New(t),
		retry:      fixedRetrySettings{concurrency: 3},
		throughput: &stubThroughputPolicies{err: wantErr},
	}

	_, err := d.UpgradeAll(context.Background(), nil, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("UpgradeAll error = %v, want policy error %v despite empty selection", err, wantErr)
	}
}

func TestBeginCycleKeepsCurrentSnapshotAndAppliesChangeNextCycle(t *testing.T) {
	one, two := 1, 2
	store := &stubThroughputPolicies{overrides: map[int64]sourcethroughput.Override{101: {DownloadConcurrency: &one}}}
	d := &Dispatcher{retry: fixedRetrySettings{concurrency: 4}, throughput: store}

	cycleOne, err := d.BeginCycle(context.Background())
	if err != nil {
		t.Fatalf("BeginCycle one: %v", err)
	}
	store.overrides = map[int64]sourcethroughput.Override{101: {DownloadConcurrency: &two}}
	if got, _ := d.concurrencyPolicy(cycleOne); got.For(101) != 1 {
		t.Fatalf("existing cycle observed changed override: got %d, want 1", got.For(101))
	}

	cycleTwo, err := d.BeginCycle(context.Background())
	if err != nil {
		t.Fatalf("BeginCycle two: %v", err)
	}
	if got, _ := d.concurrencyPolicy(cycleTwo); got.For(101) != 2 {
		t.Fatalf("next cycle override = %d, want 2", got.For(101))
	}
	if store.calls != 2 {
		t.Fatalf("Snapshot calls = %d, want once per cycle", store.calls)
	}
}

func TestRunPerSourceQueuesUsesEachSourcesCycleLimit(t *testing.T) {
	groups := map[string][]int{
		"limited":   {1, 2, 3},
		"inherited": {1, 2, 3},
	}
	limits := map[string]int{"limited": 1, "inherited": 3}
	release := make(chan struct{})
	var mu sync.Mutex
	inFlight := map[string]int{}
	maxima := map[string]int{}
	started := make(chan struct{}, 6)
	done := make(chan error, 1)

	go func() {
		done <- runPerSourceQueues(context.Background(), groups, func(source string) int { return limits[source] }, func(_ context.Context, source string, _ int) error {
			mu.Lock()
			inFlight[source]++
			maxima[source] = max(maxima[source], inFlight[source])
			mu.Unlock()
			started <- struct{}{}
			<-release
			mu.Lock()
			inFlight[source]--
			mu.Unlock()
			return nil
		})
	}()

	for range 4 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for source queue admission")
		}
	}
	mu.Lock()
	if maxima["limited"] != 1 || maxima["inherited"] != 3 {
		t.Errorf("per-source maxima = %v, want limited=1 inherited=3", maxima)
	}
	mu.Unlock()
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("runPerSourceQueues: %v", err)
	}
}

type fixedRetrySettings struct{ concurrency int }

func (s fixedRetrySettings) MaxRetries(context.Context) int                    { return 3 }
func (s fixedRetrySettings) RetryBackoff(context.Context) time.Duration        { return 0 }
func (s fixedRetrySettings) LockedRetryInterval(context.Context) time.Duration { return 0 }
func (s fixedRetrySettings) DownloadConcurrency(context.Context) int           { return s.concurrency }
func (s fixedRetrySettings) MaxConcurrentDownloads(context.Context) int        { return 10 }
func (s fixedRetrySettings) SuppressSplitParts(context.Context) bool           { return false }
