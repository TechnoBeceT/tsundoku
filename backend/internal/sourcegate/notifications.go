package sourcegate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"entgo.io/ent/dialect/sql"

	"github.com/technobecet/tsundoku/internal/ent"
	entsourcebreakernotification "github.com/technobecet/tsundoku/internal/ent/sourcebreakernotification"
	entsourcebreakernotificationcursor "github.com/technobecet/tsundoku/internal/ent/sourcebreakernotificationcursor"
	"github.com/technobecet/tsundoku/internal/pkg/errorclass"
	"github.com/technobecet/tsundoku/internal/sourceevents"
)

const (
	// notificationScanInterval is the idle cadence of the lifetime dispatcher.
	// Transition callers still make an immediate publication attempt; this scan
	// closes the no-new-transition/no-restart retry gap.
	notificationScanInterval = time.Second
	// Consumer failures back off per ordered stream head. The dispatcher keeps
	// scanning other sources, so one poison event cannot stop independent streams.
	notificationRetryBase = 250 * time.Millisecond
	notificationRetryMax  = 30 * time.Second
)

// notificationEnqueueError identifies the one optional-storage failure for
// which breaker mutations have a state-only fallback. Other transaction errors
// retain their existing handling because retrying an ambiguous commit could
// apply a failure increment twice.
type notificationEnqueueError struct {
	cause error
}

func newNotificationEnqueueError(cause error) error {
	return &notificationEnqueueError{cause: cause}
}

func (e *notificationEnqueueError) Error() string {
	return "enqueue transition notification: " + e.cause.Error()
}

func (e *notificationEnqueueError) Unwrap() error { return e.cause }

func isNotificationEnqueueError(err error) bool {
	var target *notificationEnqueueError
	return errors.As(err, &target)
}

// enqueueTransition persists one notification in the same transaction as its
// breaker mutation. The cursor insert is conflict-safe and does not update an
// existing row, so a later transition may commit while an earlier publisher
// holds that cursor's write lock. queued is false when neither optional
// notification consumer is attached.
func (s *Service) enqueueTransition(
	ctx context.Context,
	tx *ent.Tx,
	transition BreakerTransition,
	status sourceevents.Status,
	cause error,
) (queued bool, err error) {
	eventRequested := s.events != nil
	hookRequested := s.onTransition != nil
	if !eventRequested && !hookRequested {
		return false, nil
	}
	cursorExists, err := tx.SourceBreakerNotificationCursor.Query().
		Where(entsourcebreakernotificationcursor.SourceKeyEQ(transition.SourceKey)).
		Exist(ctx)
	if err != nil {
		return false, err
	}
	if !cursorExists {
		if err := tx.SourceBreakerNotificationCursor.Create().
			SetSourceKey(transition.SourceKey).
			OnConflictColumns(entsourcebreakernotificationcursor.FieldSourceKey).
			DoNothing().
			Exec(ctx); err != nil {
			return false, err
		}
	}

	create := tx.SourceBreakerNotification.Create().
		SetSourceKey(transition.SourceKey).
		SetEventType(string(transition.EventType)).
		SetStatus(string(status)).
		SetEventRequested(eventRequested).
		SetHookRequested(hookRequested)
	if cause != nil {
		create.
			SetErrorMessage(truncateError(cause)).
			SetErrorCategory(errorclass.Classify(cause))
	}
	if transition.State != nil {
		create.
			SetConsecutiveFailures(transition.State.ConsecutiveFailures).
			SetNillableCooldownUntil(transition.State.CooldownUntil).
			SetNillableFailingSince(transition.State.FailingSince).
			SetLastError(transition.State.LastError)
	}
	if err := create.Exec(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// PublishPending publishes every due committed transition. Each source is
// drained independently under its database cursor, so replay cannot race a live
// publisher or reverse that source's order. A failed stream head remains pending
// with retry backoff and blocks later rows for only that source.
func (s *Service) PublishPending(ctx context.Context) {
	var keys []struct {
		SourceKey string `json:"source_key"`
	}
	err := s.client.SourceBreakerNotification.Query().
		Where(entsourcebreakernotification.PublishedAtIsNil()).
		GroupBy(entsourcebreakernotification.FieldSourceKey).
		Scan(ctx, &keys)
	if err != nil {
		slog.WarnContext(ctx, "sourcegate: pending transition query failed (best-effort, retaining)", "err", err)
		return
	}
	for _, key := range keys {
		s.publishSourceTransitions(ctx, key.SourceKey)
	}
}

// StartPublisher performs the startup replay synchronously, then scans for new
// or retryable notifications for the lifetime of ctx. The returned channel
// closes after cancellation stops the goroutine, allowing shutdown tests and
// callers that need a join point to observe termination.
func (s *Service) StartPublisher(ctx context.Context) <-chan struct{} {
	s.PublishPending(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTimer(notificationScanInterval)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				slog.InfoContext(ctx, "sourcegate: notification publisher stopped (context cancelled)")
				return
			case <-timer.C:
				s.PublishPending(ctx)
				timer.Reset(notificationScanInterval)
			}
		}
	}()
	return done
}

// publishSourceTransitions drains one physical source in committed transition
// order. The cursor's PostgreSQL write lock spans recorder and hook invocation;
// another process may commit a later outbox row, but its publisher cannot pass
// this one. Publication receipts are updates, never automatic row deletions.
func (s *Service) publishSourceTransitions(ctx context.Context, sourceKey string) {
	if err := s.publishSourceTransitionsTx(ctx, sourceKey); err != nil {
		slog.WarnContext(ctx, "sourcegate: transition publication failed (best-effort, retaining)",
			"source_key", sourceKey, "err", err)
	}
}

func (s *Service) publishSourceTransitionsTx(ctx context.Context, sourceKey string) error {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	rows, err := lockPendingNotifications(ctx, tx, sourceKey)
	if err != nil {
		return err
	}
	deliveryErr, err := s.publishTransitionRows(ctx, tx, rows, time.Now().UTC())
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return deliveryErr
}

func lockPendingNotifications(
	ctx context.Context,
	tx *ent.Tx,
	sourceKey string,
) ([]*ent.SourceBreakerNotification, error) {
	locked, err := tx.SourceBreakerNotificationCursor.Update().
		Where(entsourcebreakernotificationcursor.SourceKeyEQ(sourceKey)).
		SetUpdatedAt(time.Now().UTC()).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	if locked != 1 {
		return nil, fmt.Errorf("notification cursor %q updated %d rows", sourceKey, locked)
	}
	rows, err := tx.SourceBreakerNotification.Query().
		Where(
			entsourcebreakernotification.SourceKeyEQ(sourceKey),
			entsourcebreakernotification.PublishedAtIsNil(),
		).
		Order(entsourcebreakernotification.ByID(sql.OrderAsc())).
		All(ctx)
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *Service) publishTransitionRows(
	ctx context.Context,
	tx *ent.Tx,
	rows []*ent.SourceBreakerNotification,
	now time.Time,
) (error, error) {
	for _, row := range rows {
		// Never select only "due" rows: a later row can be due while the stream
		// head is backing off. Inspecting in ID order and stopping here is the
		// poison-event rule that prevents overtaking.
		if row.NextAttemptAt != nil && row.NextAttemptAt.After(now) {
			return nil, nil
		}
		if err := s.publishTransition(ctx, tx, row); err != nil {
			var consumerErr *notificationConsumerError
			if !errors.As(err, &consumerErr) {
				return nil, err
			}
			if err := retainFailedNotification(ctx, tx, row, consumerErr, now); err != nil {
				return nil, err
			}
			return fmt.Errorf("notification %d: %w", row.ID, consumerErr), nil
		}
	}
	return nil, nil
}

func retainFailedNotification(
	ctx context.Context,
	tx *ent.Tx,
	row *ent.SourceBreakerNotification,
	cause error,
	now time.Time,
) error {
	attempts := row.PublicationAttempts + 1
	return tx.SourceBreakerNotification.UpdateOneID(row.ID).
		SetPublicationAttempts(attempts).
		SetNextAttemptAt(now.Add(notificationRetryDelay(attempts))).
		SetPublicationError(truncateError(cause)).
		Exec(ctx)
}

func (s *Service) publishTransition(ctx context.Context, tx *ent.Tx, row *ent.SourceBreakerNotification) error {
	transition := transitionFromNotification(row)
	eventDone, err := s.publishAuditEvent(ctx, tx, row, transition.EventType)
	if err != nil {
		return err
	}
	hookDone, err := s.publishTransitionHook(ctx, tx, row, transition)
	if err != nil {
		return err
	}
	if !eventDone || !hookDone {
		return nil
	}
	return tx.SourceBreakerNotification.UpdateOneID(row.ID).
		SetPublishedAt(time.Now().UTC()).
		ClearNextAttemptAt().
		ClearPublicationError().
		Exec(ctx)
}

func transitionFromNotification(row *ent.SourceBreakerNotification) BreakerTransition {
	transition := BreakerTransition{
		SourceKey: row.SourceKey,
		EventType: sourceevents.EventType(row.EventType),
	}
	if transition.EventType == sourceevents.EventBreakerTrip {
		transition.State = &BreakerState{
			SourceKey:           row.SourceKey,
			ConsecutiveFailures: row.ConsecutiveFailures,
			CooldownUntil:       row.CooldownUntil,
			FailingSince:        row.FailingSince,
			LastError:           row.LastError,
			UpdatedAt:           row.CreatedAt,
		}
	}
	return transition
}

func (s *Service) publishAuditEvent(
	ctx context.Context,
	tx *ent.Tx,
	row *ent.SourceBreakerNotification,
	eventType sourceevents.EventType,
) (bool, error) {
	if !row.EventRequested || row.EventPublishedAt != nil {
		return true, nil
	}
	if s.events == nil {
		return false, newNotificationConsumerError(errors.New("event recorder requested but not attached"))
	}
	var cause error
	if row.ErrorMessage != nil {
		cause = errors.New(*row.ErrorMessage)
	}
	err := callBreakerRecorder(func() error {
		return s.logBreakerEvent(
			ctx,
			row.ID,
			row.SourceKey,
			eventType,
			sourceevents.Status(row.Status),
			cause,
			row.ErrorCategory,
		)
	})
	if err != nil {
		return false, newNotificationConsumerError(fmt.Errorf("audit event: %w", err))
	}
	if err := tx.SourceBreakerNotification.UpdateOneID(row.ID).
		SetEventPublishedAt(time.Now().UTC()).
		Exec(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) publishTransitionHook(
	ctx context.Context,
	tx *ent.Tx,
	row *ent.SourceBreakerNotification,
	transition BreakerTransition,
) (bool, error) {
	if !row.HookRequested || row.HookPublishedAt != nil {
		return true, nil
	}
	if s.onTransition == nil {
		return false, newNotificationConsumerError(errors.New("transition hook requested but not attached"))
	}
	if err := s.fireTransition(ctx, transition); err != nil {
		return false, newNotificationConsumerError(fmt.Errorf("transition hook: %w", err))
	}
	if err := tx.SourceBreakerNotification.UpdateOneID(row.ID).
		SetHookPublishedAt(time.Now().UTC()).
		Exec(ctx); err != nil {
		return false, err
	}
	return true, nil
}

type notificationConsumerError struct {
	cause error
}

func newNotificationConsumerError(cause error) error {
	return &notificationConsumerError{cause: cause}
}

func (e *notificationConsumerError) Error() string { return e.cause.Error() }

func (e *notificationConsumerError) Unwrap() error { return e.cause }

func callBreakerRecorder(call func() error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	return call()
}

func notificationRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := notificationRetryBase
	for i := 1; i < attempt && delay < notificationRetryMax; i++ {
		delay *= 2
		if delay > notificationRetryMax {
			return notificationRetryMax
		}
	}
	return delay
}
