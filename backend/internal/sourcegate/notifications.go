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

// PublishPending publishes every committed transition left by an interrupted
// process. Each source is drained independently under its database cursor, so
// startup replay cannot race a live publisher or reverse that source's order.
// Failures are best-effort: the pending row remains durable for a later replay.
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
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	locked, err := tx.SourceBreakerNotificationCursor.Update().
		Where(entsourcebreakernotificationcursor.SourceKeyEQ(sourceKey)).
		SetUpdatedAt(time.Now().UTC()).
		Save(ctx)
	if err != nil {
		return err
	}
	if locked != 1 {
		return fmt.Errorf("notification cursor %q updated %d rows", sourceKey, locked)
	}
	rows, err := tx.SourceBreakerNotification.Query().
		Where(
			entsourcebreakernotification.SourceKeyEQ(sourceKey),
			entsourcebreakernotification.PublishedAtIsNil(),
		).
		Order(entsourcebreakernotification.ByID(sql.OrderAsc())).
		All(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := s.publishTransition(ctx, row); err != nil {
			return err
		}
		if err := tx.SourceBreakerNotification.UpdateOneID(row.ID).
			SetPublishedAt(time.Now().UTC()).
			Exec(ctx); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *Service) publishTransition(ctx context.Context, row *ent.SourceBreakerNotification) error {
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
	if row.EventRequested {
		if s.events == nil {
			return errors.New("event recorder requested but not attached")
		}
		var cause error
		if row.ErrorMessage != nil {
			cause = errors.New(*row.ErrorMessage)
		}
		s.logBreakerEvent(
			ctx,
			row.SourceKey,
			transition.EventType,
			sourceevents.Status(row.Status),
			cause,
			row.ErrorCategory,
		)
	}
	if row.HookRequested {
		if s.onTransition == nil {
			return errors.New("transition hook requested but not attached")
		}
		s.fireTransition(transition)
	}
	return nil
}
