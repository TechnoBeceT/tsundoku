package sourcegate_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entsourcebreakernotification "github.com/technobecet/tsundoku/internal/ent/sourcebreakernotification"
	"github.com/technobecet/tsundoku/internal/sourceevents"
	"github.com/technobecet/tsundoku/internal/sourcegate"
)

type publicationBarrierRecorder struct {
	tripEntered chan struct{}
	releaseTrip <-chan struct{}
	tripOnce    sync.Once
	mu          sync.Mutex
	events      []sourceevents.EventType
}

func (r *publicationBarrierRecorder) Log(_ context.Context, event sourceevents.Event) {
	if event.Type == sourceevents.EventBreakerTrip {
		r.tripOnce.Do(func() { close(r.tripEntered) })
		<-r.releaseTrip
	}
	r.mu.Lock()
	r.events = append(r.events, event.Type)
	r.mu.Unlock()
}

func (r *publicationBarrierRecorder) LogBatch(ctx context.Context, events []sourceevents.Event) {
	for _, event := range events {
		r.Log(ctx, event)
	}
}

func (r *publicationBarrierRecorder) eventTypes() []sourceevents.EventType {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]sourceevents.EventType(nil), r.events...)
}

type hookSummary struct {
	erroring    int
	coolingDown int
}

type publicationHookCapture struct {
	mu        sync.Mutex
	summaries []hookSummary
}

func (c *publicationHookCapture) record(transition sourcegate.BreakerTransition) {
	snapshot := make(map[string]sourcegate.BreakerState)
	if transition.State != nil {
		snapshot[transition.SourceKey] = *transition.State
	}
	erroring, coolingDown := sourcegate.SummaryCounts(snapshot, time.Now())
	c.mu.Lock()
	c.summaries = append(c.summaries, hookSummary{erroring: erroring, coolingDown: coolingDown})
	c.mu.Unlock()
}

func (c *publicationHookCapture) all() []hookSummary {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]hookSummary(nil), c.summaries...)
}

func TestTransitionPublicationFollowsCommitOrder(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	const key = "Comix"
	releaseTrip := make(chan struct{})
	var releaseTripOnce sync.Once
	release := func() { releaseTripOnce.Do(func() { close(releaseTrip) }) }
	t.Cleanup(release)
	recorder := &publicationBarrierRecorder{
		tripEntered: make(chan struct{}),
		releaseTrip: releaseTrip,
	}
	hooks := &publicationHookCapture{}
	tripService := sourcegate.NewService(client, thresholds()).
		WithEventRecorder(recorder).
		WithTransitionHook(hooks.record)
	resetService := sourcegate.NewService(client, thresholds()).
		WithEventRecorder(recorder).
		WithTransitionHook(hooks.record)

	now := time.Now()
	tripService.RecordFailure(ctx, key, errors.New("first"), now)
	tripService.RecordFailure(ctx, key, errors.New("second"), now)
	tripDone := make(chan struct{})
	go func() {
		tripService.RecordFailure(ctx, key, errors.New("cloudflare challenge"), now)
		close(tripDone)
	}()
	awaitPublicationSignal(t, recorder.tripEntered, "trip publication did not reach the recorder")

	// The recovery commits while trip publication is still held. Releasing the
	// recorder afterward proves a later committed publisher cannot pass the
	// cursor, while each hook retains the state of its own transition.
	resetDone := make(chan struct{})
	go func() {
		resetService.RecordSuccess(ctx, key)
		close(resetDone)
	}()
	awaitCommittedReset(t, client, resetService, key)
	release()
	awaitPublicationSignal(t, tripDone, "trip publication did not finish")
	awaitPublicationSignal(t, resetDone, "reset publication did not finish")

	gotEvents := recorder.eventTypes()
	wantEvents := []sourceevents.EventType{sourceevents.EventBreakerTrip, sourceevents.EventBreakerReset}
	if len(gotEvents) != len(wantEvents) || gotEvents[0] != wantEvents[0] || gotEvents[1] != wantEvents[1] {
		t.Fatalf("audit order = %v, want %v", gotEvents, wantEvents)
	}

	gotHooks := hooks.all()
	wantHooks := []hookSummary{{erroring: 1, coolingDown: 1}, {erroring: 0, coolingDown: 0}}
	if len(gotHooks) != len(wantHooks) || gotHooks[0] != wantHooks[0] || gotHooks[1] != wantHooks[1] {
		t.Fatalf("hook summaries = %+v, want %+v", gotHooks, wantHooks)
	}
	if got := client.SourceBreakerNotification.Query().
		Where(entsourcebreakernotification.PublishedAtNotNil()).
		CountX(ctx); got != 2 {
		t.Fatalf("published transition receipts = %d, want 2", got)
	}
}

func awaitPublicationSignal(t *testing.T, signal <-chan struct{}, failure string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal(failure)
	}
}

func awaitCommittedReset(t *testing.T, client *ent.Client, svc *sourcegate.Service, key string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := svc.Snapshot(context.Background())
		if err != nil {
			t.Fatalf("snapshot while waiting for committed reset: %v", err)
		}
		state, ok := snapshot[key]
		pending := client.SourceBreakerNotification.Query().
			Where(entsourcebreakernotification.PublishedAtIsNil()).
			CountX(context.Background())
		if ok && state.ConsecutiveFailures == 0 && state.CooldownUntil == nil && state.FailingSince == nil && pending == 2 {
			return
		}
	}
	t.Fatal("reset did not commit while trip publication was held")
}
