package sourcegate_test

import (
	"context"
	"errors"
	"strings"
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

// notificationFailureDriver rejects only SourceBreakerNotification inserts.
// Breaker-state and every other database mutation still reach PostgreSQL, so
// these tests isolate notification durability from the containment state.
type notificationFailureDriver struct {
	dialect.Driver
	failNotifications atomic.Bool
	failCursors       atomic.Bool
	rejections        atomic.Int64
}

func (d *notificationFailureDriver) Tx(ctx context.Context) (dialect.Tx, error) {
	tx, err := d.Driver.Tx(ctx)
	if err != nil {
		return nil, err
	}
	return &notificationFailureTx{Tx: tx, driver: d}, nil
}

type notificationFailureTx struct {
	dialect.Tx
	driver *notificationFailureDriver
}

func (t *notificationFailureTx) Exec(ctx context.Context, query string, args, v any) error {
	if t.reject(query) {
		return errors.New("forced notification insert failure")
	}
	return t.Tx.Exec(ctx, query, args, v)
}

func (t *notificationFailureTx) Query(ctx context.Context, query string, args, v any) error {
	if t.reject(query) {
		return errors.New("forced notification insert failure")
	}
	return t.Tx.Query(ctx, query, args, v)
}

func (t *notificationFailureTx) reject(query string) bool {
	reject := t.driver.failCursors.Load() && strings.Contains(query, "source_breaker_notification_cursors")
	if !reject {
		reject = t.driver.failNotifications.Load() && strings.Contains(query, "source_breaker_notifications")
	}
	if reject {
		t.driver.rejections.Add(1)
	}
	return reject
}

func newNotificationFailureClient(t *testing.T) (*ent.Client, *notificationFailureDriver) {
	t.Helper()
	_, db := testdb.NewWithSQL(t)
	driver := &notificationFailureDriver{Driver: entsql.OpenDB(dialect.Postgres, db)}
	client := ent.NewClient(ent.Driver(driver))
	t.Cleanup(func() { _ = client.Close() })
	return client, driver
}

func oneFailureThreshold() settings.Static {
	return settings.Static{
		SourcesFailureThresh: 1,
		SourcesCooldownIv:    10 * time.Minute,
	}
}

func TestNotificationEnqueueFailure_DoesNotRollBackBreakerTrip(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*notificationFailureDriver)
	}{
		{name: "notification_insert", configure: func(d *notificationFailureDriver) { d.failNotifications.Store(true) }},
		{name: "cursor_access", configure: func(d *notificationFailureDriver) { d.failCursors.Store(true) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, driver := newNotificationFailureClient(t)
			tt.configure(driver)
			service := sourcegate.NewService(client, oneFailureThreshold()).
				WithEventRecorder(sourceevents.NewService(client))
			ctx := context.Background()
			now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

			service.RecordFailure(ctx, "Comix", errors.New("captcha challenge"), now)
			requireNotificationFailureInjected(t, driver)

			row, err := client.SourceCircuitState.Query().
				Where(entsourcecircuitstate.SourceKeyEQ("Comix")).
				Only(ctx)
			if err != nil {
				t.Fatalf("load breaker after notification failure: %v", err)
			}
			if row.ConsecutiveFailures != 1 || row.CooldownUntil == nil || !row.CooldownUntil.Equal(now.Add(10*time.Minute)) {
				t.Fatalf("breaker state = failures:%d cooldown:%v, want one failure and exact cooldown", row.ConsecutiveFailures, row.CooldownUntil)
			}
			if service.IsAvailable(ctx, "Comix", now) {
				t.Fatal("challenged source remained available after notification storage failed")
			}
		})
	}
}

func TestNotificationEnqueueFailure_DoesNotRollBackNaturalRecovery(t *testing.T) {
	client, driver := newNotificationFailureClient(t)
	ctx := context.Background()
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	seed := sourcegate.NewService(client, oneFailureThreshold())
	seed.RecordFailure(ctx, "Comix", errors.New("captcha challenge"), now)
	driver.failNotifications.Store(true)
	service := sourcegate.NewService(client, oneFailureThreshold()).
		WithEventRecorder(sourceevents.NewService(client))

	service.RecordSuccess(ctx, "Comix")
	requireNotificationFailureInjected(t, driver)

	row, err := client.SourceCircuitState.Query().
		Where(entsourcecircuitstate.SourceKeyEQ("Comix")).
		Only(ctx)
	if err != nil {
		t.Fatalf("load breaker after recovery notification failure: %v", err)
	}
	if row.ConsecutiveFailures != 0 || row.CooldownUntil != nil || row.FailingSince != nil || row.LastError != "" {
		t.Fatalf("recovery state = failures:%d cooldown:%v failing_since:%v last_error:%q, want fully reset", row.ConsecutiveFailures, row.CooldownUntil, row.FailingSince, row.LastError)
	}
	if !row.NotificationGap {
		t.Fatal("notification_gap = false after the recovery notification could not be stored")
	}
}

func TestNotificationEnqueueFailure_DoesNotRollBackOwnerReset(t *testing.T) {
	client, driver := newNotificationFailureClient(t)
	ctx := context.Background()
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	seed := sourcegate.NewService(client, oneFailureThreshold())
	seed.RecordFailure(ctx, "Comix", errors.New("captcha challenge"), now)
	driver.failNotifications.Store(true)
	service := sourcegate.NewService(client, oneFailureThreshold()).
		WithEventRecorder(sourceevents.NewService(client))

	if err := service.Reset(ctx, "Comix"); err != nil {
		t.Fatalf("Reset returned notification storage error: %v", err)
	}
	requireNotificationFailureInjected(t, driver)
	if !service.IsAvailable(ctx, "Comix", now) {
		t.Fatal("source remained unavailable after owner reset notification storage failed")
	}
	row, err := client.SourceCircuitState.Query().
		Where(entsourcecircuitstate.SourceKeyEQ("Comix")).
		Only(ctx)
	if err != nil {
		t.Fatalf("load breaker tombstone after owner reset: %v", err)
	}
	if row.ConsecutiveFailures != 0 || row.CooldownUntil != nil || row.FailingSince != nil || row.LastError != "" {
		t.Fatalf("owner-reset fallback retained breaker state: %+v", row)
	}
	if !row.NotificationGap {
		t.Fatal("notification_gap = false after the owner-reset notification could not be stored")
	}
}

func TestNotificationEnqueueFailure_BlocksLaterTransitionsFromOvertakingGap(t *testing.T) {
	client, driver := newNotificationFailureClient(t)
	ctx := context.Background()
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	recorder := sourceevents.NewService(client)
	service := sourcegate.NewService(client, oneFailureThreshold()).WithEventRecorder(recorder)
	driver.failNotifications.Store(true)

	service.RecordFailure(ctx, "Comix", errors.New("captcha challenge"), now)
	requireNotificationFailureInjected(t, driver)
	driver.failNotifications.Store(false)
	service.RecordSuccess(ctx, "Comix")
	service.RecordFailure(ctx, "Comix", errors.New("captcha challenge again"), now.Add(time.Minute))

	row, err := client.SourceCircuitState.Query().
		Where(entsourcecircuitstate.SourceKeyEQ("Comix")).
		Only(ctx)
	if err != nil {
		t.Fatalf("load breaker with notification gap: %v", err)
	}
	if !row.NotificationGap {
		t.Fatal("notification_gap = false after a missing transition")
	}
	if got := client.SourceBreakerNotification.Query().CountX(ctx); got != 0 {
		t.Fatalf("stored notifications = %d, want 0 so later transitions cannot overtake the missing trip", got)
	}
	if got := client.SourceEvent.Query().CountX(ctx); got != 0 {
		t.Fatalf("published breaker events = %d, want 0 after the stream gap", got)
	}
}

func requireNotificationFailureInjected(t *testing.T, driver *notificationFailureDriver) {
	t.Helper()
	if driver.rejections.Load() == 0 {
		t.Fatal("notification failure seam did not reject an outbox operation")
	}
}
