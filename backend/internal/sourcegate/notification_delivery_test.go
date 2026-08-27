package sourcegate_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/sourceevents"
	"github.com/technobecet/tsundoku/internal/sourcegate"
)

type deliveryRecorder struct {
	mu        sync.Mutex
	failures  int
	failByID  map[int]int
	attempts  map[int]int
	delivered chan int
}

func newDeliveryRecorder(failures int) *deliveryRecorder {
	return &deliveryRecorder{
		failures:  failures,
		failByID:  make(map[int]int),
		attempts:  make(map[int]int),
		delivered: make(chan int, 16),
	}
}

func (r *deliveryRecorder) LogBreakerTransition(_ context.Context, notificationID int, _ sourceevents.Event) error {
	r.mu.Lock()
	r.attempts[notificationID]++
	if r.failByID[notificationID] > 0 {
		r.failByID[notificationID]--
		r.mu.Unlock()
		return errors.New("forced audit consumer failure")
	}
	if r.failures > 0 {
		r.failures--
		r.mu.Unlock()
		return errors.New("forced audit consumer failure")
	}
	r.mu.Unlock()
	select {
	case r.delivered <- notificationID:
	default:
	}
	return nil
}

func (r *deliveryRecorder) failNotification(notificationID, attempts int) {
	r.mu.Lock()
	r.failByID[notificationID] = attempts
	r.mu.Unlock()
}

func (r *deliveryRecorder) attemptCount(notificationID int) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.attempts[notificationID]
}

func TestNotificationDelivery_RecorderFailureRemainsPendingForRetry(t *testing.T) {
	client := testdb.New(t)
	recorder := newDeliveryRecorder(1)
	var hookCalls atomic.Int64
	service := sourcegate.NewService(client, oneFailureThreshold()).
		WithEventRecorder(recorder).
		WithTransitionHook(func(context.Context, sourcegate.BreakerTransition) error {
			hookCalls.Add(1)
			return nil
		})
	ctx := context.Background()

	service.RecordFailure(ctx, "Comix", errors.New("captcha challenge"), time.Now())
	row := client.SourceBreakerNotification.Query().OnlyX(ctx)
	if row.PublishedAt != nil || row.EventPublishedAt != nil || row.HookPublishedAt != nil {
		t.Fatalf("failed audit delivery was receipted: published=%v event=%v hook=%v", row.PublishedAt, row.EventPublishedAt, row.HookPublishedAt)
	}
	if hookCalls.Load() != 0 {
		t.Fatalf("hook calls = %d, want 0 while the ordered audit predecessor is pending", hookCalls.Load())
	}

	client.SourceBreakerNotification.UpdateOneID(row.ID).ClearNextAttemptAt().ExecX(ctx)
	service.PublishPending(ctx)
	row = client.SourceBreakerNotification.GetX(ctx, row.ID)
	if row.PublishedAt == nil || row.EventPublishedAt == nil || row.HookPublishedAt == nil {
		t.Fatalf("successful retry receipts = published:%v event:%v hook:%v, want all set", row.PublishedAt, row.EventPublishedAt, row.HookPublishedAt)
	}
	if recorder.attemptCount(row.ID) != 2 || hookCalls.Load() != 1 {
		t.Fatalf("attempts = audit:%d hook:%d, want 2/1", recorder.attemptCount(row.ID), hookCalls.Load())
	}
}

func TestNotificationDelivery_HookFailureKeepsOnlyHookPending(t *testing.T) {
	client := testdb.New(t)
	recorder := newDeliveryRecorder(0)
	var hookCalls atomic.Int64
	service := sourcegate.NewService(client, oneFailureThreshold()).
		WithEventRecorder(recorder).
		WithTransitionHook(func(context.Context, sourcegate.BreakerTransition) error {
			if hookCalls.Add(1) == 1 {
				return errors.New("forced summary snapshot failure")
			}
			return nil
		})
	ctx := context.Background()

	service.RecordFailure(ctx, "Comix", errors.New("captcha challenge"), time.Now())
	row := client.SourceBreakerNotification.Query().OnlyX(ctx)
	if row.EventPublishedAt == nil || row.HookPublishedAt != nil || row.PublishedAt != nil {
		t.Fatalf("partial receipts = event:%v hook:%v published:%v, want event only", row.EventPublishedAt, row.HookPublishedAt, row.PublishedAt)
	}

	client.SourceBreakerNotification.UpdateOneID(row.ID).ClearNextAttemptAt().ExecX(ctx)
	service.PublishPending(ctx)
	row = client.SourceBreakerNotification.GetX(ctx, row.ID)
	if row.PublishedAt == nil || row.HookPublishedAt == nil {
		t.Fatalf("hook retry remained pending: published=%v hook=%v", row.PublishedAt, row.HookPublishedAt)
	}
	if recorder.attemptCount(row.ID) != 1 || hookCalls.Load() != 2 {
		t.Fatalf("attempts = audit:%d hook:%d, want independent 1/2", recorder.attemptCount(row.ID), hookCalls.Load())
	}
}

func TestNotificationDelivery_HookPanicRemainsPending(t *testing.T) {
	client := testdb.New(t)
	service := sourcegate.NewService(client, oneFailureThreshold()).
		WithTransitionHook(func(context.Context, sourcegate.BreakerTransition) error {
			panic("forced hook panic")
		})
	ctx := context.Background()

	service.RecordFailure(ctx, "Comix", errors.New("captcha challenge"), time.Now())
	row := client.SourceBreakerNotification.Query().OnlyX(ctx)
	if row.HookPublishedAt != nil || row.PublishedAt != nil {
		t.Fatalf("panicking hook was receipted: hook=%v published=%v", row.HookPublishedAt, row.PublishedAt)
	}

	service.WithTransitionHook(func(context.Context, sourcegate.BreakerTransition) error { return nil })
	client.SourceBreakerNotification.UpdateOneID(row.ID).ClearNextAttemptAt().ExecX(ctx)
	service.PublishPending(ctx)
	row = client.SourceBreakerNotification.GetX(ctx, row.ID)
	if row.HookPublishedAt == nil || row.PublishedAt == nil {
		t.Fatalf("recovered hook was not receipted: hook=%v published=%v", row.HookPublishedAt, row.PublishedAt)
	}
}

type ambiguousAuditRecorder struct {
	service *sourceevents.Service
	calls   atomic.Int64
}

func (r *ambiguousAuditRecorder) LogBreakerTransition(ctx context.Context, notificationID int, event sourceevents.Event) error {
	if err := r.service.LogBreakerTransition(ctx, notificationID, event); err != nil {
		return err
	}
	if r.calls.Add(1) == 1 {
		return errors.New("forced receipt loss after audit commit")
	}
	return nil
}

func TestNotificationDelivery_AuditRetryIsIdempotentAfterAmbiguousSuccess(t *testing.T) {
	client := testdb.New(t)
	recorder := &ambiguousAuditRecorder{service: sourceevents.NewService(client)}
	service := sourcegate.NewService(client, oneFailureThreshold()).WithEventRecorder(recorder)
	ctx := context.Background()

	service.RecordFailure(ctx, "Comix", errors.New("captcha challenge"), time.Now())
	row := client.SourceBreakerNotification.Query().OnlyX(ctx)
	if got := client.SourceEvent.Query().CountX(ctx); got != 1 {
		t.Fatalf("audit rows after ambiguous success = %d, want 1", got)
	}
	if row.EventPublishedAt != nil || row.PublishedAt != nil {
		t.Fatalf("ambiguous audit success was receipted: event=%v published=%v", row.EventPublishedAt, row.PublishedAt)
	}

	client.SourceBreakerNotification.UpdateOneID(row.ID).ClearNextAttemptAt().ExecX(ctx)
	service.PublishPending(ctx)
	if got := client.SourceEvent.Query().CountX(ctx); got != 1 {
		t.Fatalf("audit rows after retry = %d, want exactly one idempotent row", got)
	}
	row = client.SourceBreakerNotification.GetX(ctx, row.ID)
	if row.EventPublishedAt == nil || row.PublishedAt == nil {
		t.Fatalf("audit retry lacks receipt: event=%v published=%v", row.EventPublishedAt, row.PublishedAt)
	}
}

func TestNotificationPublisher_RetriesAfterStartupWithoutAnotherTransition(t *testing.T) {
	client := testdb.New(t)
	recorder := newDeliveryRecorder(1)
	service := sourcegate.NewService(client, oneFailureThreshold()).WithEventRecorder(recorder)
	ctx, cancel := context.WithCancel(context.Background())
	done := service.StartPublisher(ctx)
	t.Cleanup(func() {
		cancel()
		awaitPublisherStop(t, done)
	})

	service.RecordFailure(ctx, "Comix", errors.New("captcha challenge"), time.Now())
	row := client.SourceBreakerNotification.Query().OnlyX(ctx)
	awaitDeliveredNotification(t, recorder.delivered, row.ID)
	row = awaitPublishedNotification(t, client, row.ID)
	if row.PublishedAt == nil || recorder.attemptCount(row.ID) != 2 {
		t.Fatalf("live retry = published:%v attempts:%d, want receipt after two attempts", row.PublishedAt, recorder.attemptCount(row.ID))
	}
}

func TestNotificationPublisher_DetectsRowsCommittedAfterStartupScanAndStops(t *testing.T) {
	client := testdb.New(t)
	recorder := newDeliveryRecorder(0)
	service := sourcegate.NewService(client, oneFailureThreshold()).WithEventRecorder(recorder)
	ctx, cancel := context.WithCancel(context.Background())
	done := service.StartPublisher(ctx)

	client.SourceBreakerNotificationCursor.Create().SetSourceKey("Late Source").ExecX(ctx)
	row := client.SourceBreakerNotification.Create().
		SetSourceKey("Late Source").
		SetEventType(string(sourceevents.EventBreakerTrip)).
		SetStatus(string(sourceevents.StatusFailed)).
		SetEventRequested(true).
		SaveX(ctx)
	awaitDeliveredNotification(t, recorder.delivered, row.ID)
	_ = awaitPublishedNotification(t, client, row.ID)

	cancel()
	awaitPublisherStop(t, done)
}

func TestNotificationDelivery_FailedPredecessorBlocksItsStreamButNotOtherSources(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	recorder := newDeliveryRecorder(0)
	service := sourcegate.NewService(client, oneFailureThreshold()).WithEventRecorder(recorder)
	client.SourceBreakerNotificationCursor.Create().SetSourceKey("Blocked").ExecX(ctx)
	client.SourceBreakerNotificationCursor.Create().SetSourceKey("Healthy").ExecX(ctx)
	first := createPendingNotification(t, client, "Blocked", sourceevents.EventBreakerTrip)
	second := createPendingNotification(t, client, "Blocked", sourceevents.EventBreakerReset)
	healthy := createPendingNotification(t, client, "Healthy", sourceevents.EventBreakerTrip)
	recorder.failNotification(first.ID, 1)

	service.PublishPending(ctx)

	if recorder.attemptCount(first.ID) != 1 || recorder.attemptCount(second.ID) != 0 {
		t.Fatalf("blocked stream attempts = first:%d second:%d, want 1/0", recorder.attemptCount(first.ID), recorder.attemptCount(second.ID))
	}
	if recorder.attemptCount(healthy.ID) != 1 {
		t.Fatalf("healthy source attempts = %d, want independent progress", recorder.attemptCount(healthy.ID))
	}
	if client.SourceBreakerNotification.GetX(ctx, healthy.ID).PublishedAt == nil {
		t.Fatal("healthy source notification remained pending behind another source's failure")
	}
}

func createPendingNotification(t *testing.T, client *ent.Client, sourceKey string, eventType sourceevents.EventType) *ent.SourceBreakerNotification {
	t.Helper()
	status := sourceevents.StatusSuccess
	if eventType == sourceevents.EventBreakerTrip {
		status = sourceevents.StatusFailed
	}
	return client.SourceBreakerNotification.Create().
		SetSourceKey(sourceKey).
		SetEventType(string(eventType)).
		SetStatus(string(status)).
		SetEventRequested(true).
		SaveX(context.Background())
}

func awaitDeliveredNotification(t *testing.T, delivered <-chan int, want int) {
	t.Helper()
	select {
	case got := <-delivered:
		if got != want {
			t.Fatalf("delivered notification = %d, want %d", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("notification %d was not delivered within 2s", want)
	}
}

func awaitPublisherStop(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("notification publisher did not stop within 2s of cancellation")
	}
}

func awaitPublishedNotification(t *testing.T, client *ent.Client, id int) *ent.SourceBreakerNotification {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		row := client.SourceBreakerNotification.GetX(context.Background(), id)
		if row.PublishedAt != nil {
			return row
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("notification %d was delivered but not receipted within 2s", id)
	return nil
}
