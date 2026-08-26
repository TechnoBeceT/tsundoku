package sourcegate_test

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
	"github.com/technobecet/tsundoku/internal/ent"
	entsourcecircuitstate "github.com/technobecet/tsundoku/internal/ent/sourcecircuitstate"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceevents"
	"github.com/technobecet/tsundoku/internal/sourcegate"
)

// TestTransitionNotifications_ConcurrentFirstFailures derives its event verdict
// from the persisted transition, not a stale no-row read. The driver barrier
// forces the historical query-then-write implementation to give every caller
// that same no-row snapshot; it then misses the threshold trip deterministically.
func TestTransitionNotifications_ConcurrentFirstFailures(t *testing.T) {
	const failures = 3
	ctx := context.Background()
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	gate, client, barrier, rec, hooked := newTransitionHarness(t, failures)
	barrier.releaseAfter(t, failures)

	runConcurrent(failures, func() {
		gate.RecordFailure(ctx, "First Race", errors.New("captcha"), now)
	})
	barrier.stop()

	assertTransitionCount(t, rec, hooked, sourceevents.EventBreakerTrip, 1)
	assertFailureCount(t, client, "First Race", failures)
}

// TestTransitionNotifications_ConcurrentExistingFailures proves only the
// threshold-crossing write notifies when every historical pre-read sees the
// same existing count immediately below the threshold.
func TestTransitionNotifications_ConcurrentExistingFailures(t *testing.T) {
	const (
		threshold = 3
		workers   = 3
	)
	ctx := context.Background()
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	gate, client, barrier, rec, hooked := newTransitionHarness(t, threshold)
	gate.RecordFailure(ctx, "Existing Race", errors.New("first"), now)
	gate.RecordFailure(ctx, "Existing Race", errors.New("second"), now)
	clearTransitionObservations(rec, hooked)

	barrier.releaseAfter(t, workers)
	runConcurrent(workers, func() {
		gate.RecordFailure(ctx, "Existing Race", errors.New("captcha"), now.Add(time.Minute))
	})
	barrier.stop()

	assertTransitionCount(t, rec, hooked, sourceevents.EventBreakerTrip, 1)
	assertFailureCount(t, client, "Existing Race", (threshold-1)+workers)
}

// TestTransitionNotifications_ConcurrentSuccesses emits exactly one reset when
// concurrent callers recover a tripped source. The old pre-read form lets every
// success see the still-tripped row and duplicates the transition notification.
func TestTransitionNotifications_ConcurrentSuccesses(t *testing.T) {
	const workers = 2
	ctx := context.Background()
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	gate, client, barrier, rec, hooked := newTransitionHarness(t, 1)
	gate.RecordFailure(ctx, "Success Race", errors.New("captcha"), now)
	clearTransitionObservations(rec, hooked)

	barrier.releaseAfter(t, workers)
	runConcurrent(workers, func() {
		gate.RecordSuccess(ctx, "Success Race")
	})
	barrier.stop()

	assertTransitionCount(t, rec, hooked, sourceevents.EventBreakerReset, 1)
	assertFailureCount(t, client, "Success Race", 0)
}

func newTransitionHarness(t *testing.T, threshold int) (*sourcegate.Service, *ent.Client, *rootReadBarrier, *captureRecorder, *atomic.Int64) {
	t.Helper()
	_, sqlDB := testdb.NewWithSQL(t)
	barrier := newRootReadBarrier()
	client := ent.NewClient(ent.Driver(&rootReadBarrierDriver{
		Driver:  entsql.OpenDB(dialect.Postgres, sqlDB),
		barrier: barrier,
	}))
	t.Cleanup(func() { _ = client.Close() })
	recorder := &captureRecorder{}
	hooked := &atomic.Int64{}
	gate := sourcegate.NewService(client, settings.Static{
		SourcesFailureThresh: threshold,
		SourcesCooldownIv:    time.Hour,
	}).WithEventRecorder(recorder).WithTransitionHook(func() { hooked.Add(1) })
	return gate, client, barrier, recorder, hooked
}

func assertTransitionCount(t *testing.T, recorder *captureRecorder, hooked *atomic.Int64, eventType sourceevents.EventType, want int) {
	t.Helper()
	if got := len(recorder.byType(eventType)); got != want {
		t.Fatalf("%s events = %d, want %d", eventType, got, want)
	}
	if got := hooked.Load(); got != int64(want) {
		t.Fatalf("transition hook calls = %d, want %d", got, want)
	}
}

func assertFailureCount(t *testing.T, client *ent.Client, key string, want int) {
	t.Helper()
	row, err := client.SourceCircuitState.Query().Where(entsourcecircuitstate.SourceKeyEQ(key)).Only(context.Background())
	if err != nil {
		t.Fatalf("load breaker row: %v", err)
	}
	if row.ConsecutiveFailures != want {
		t.Fatalf("consecutive_failures = %d, want %d", row.ConsecutiveFailures, want)
	}
}

func clearTransitionObservations(recorder *captureRecorder, hooked *atomic.Int64) {
	recorder.mu.Lock()
	recorder.events = nil
	recorder.mu.Unlock()
	hooked.Store(0)
}

// rootReadBarrierDriver blocks only root-client reads. Ent transactions use the
// underlying driver's transaction directly, so an atomic transaction bypasses
// this seam while the old external pre-read implementation is forced to race.
type rootReadBarrierDriver struct {
	dialect.Driver
	barrier *rootReadBarrier
}

func (d *rootReadBarrierDriver) Query(ctx context.Context, query string, args, value any) error {
	err := d.Driver.Query(ctx, query, args, value)
	if err == nil && strings.Contains(query, `FROM "source_circuit_states"`) {
		d.barrier.wait(ctx)
	}
	return err
}

type rootReadBarrier struct {
	active  atomic.Bool
	ctx     context.Context
	cancel  context.CancelFunc
	arrived chan struct{}
	release chan struct{}
	done    chan struct{}
	once    sync.Once
}

func newRootReadBarrier() *rootReadBarrier {
	ctx, cancel := context.WithCancel(context.Background())
	return &rootReadBarrier{
		ctx:     ctx,
		cancel:  cancel,
		arrived: make(chan struct{}, 8),
		release: make(chan struct{}),
		done:    make(chan struct{}),
	}
}

func (b *rootReadBarrier) releaseAfter(t *testing.T, n int) {
	t.Helper()
	b.active.Store(true)
	go func() {
		defer close(b.done)
		for range n {
			select {
			case <-b.ctx.Done():
				return
			case <-b.arrived:
			}
		}
		close(b.release)
	}()
}

func (b *rootReadBarrier) wait(ctx context.Context) {
	if !b.active.Load() {
		return
	}
	select {
	case <-b.ctx.Done():
		return
	case b.arrived <- struct{}{}:
	}
	select {
	case <-b.ctx.Done():
	case <-ctx.Done():
	case <-b.release:
	}
}

func (b *rootReadBarrier) stop() {
	b.once.Do(func() { b.cancel() })
	<-b.done
}
