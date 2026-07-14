package push

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/notify"
)

// failurePruneThreshold is the consecutive-failure count at which a subscription
// that keeps returning transient (non-2xx, non-Gone) errors is pruned — a device
// that silently stopped accepting pushes is eventually forgotten.
const failurePruneThreshold = 5

// pushTTLSeconds is the TTL the push service holds an undelivered notification.
// One day: long enough to reach a phone that was briefly offline, short enough
// that a stale "new chapter" ping never arrives a week late.
const pushTTLSeconds = 86400

// Sender fans a rendered notification out to every stored subscription over Web
// Push, VAPID-signed with the server key pair. It satisfies notify.Pusher.
// Construct with NewSender.
type Sender struct {
	client    *ent.Client
	vapidPub  string
	vapidPriv string
	subject   string
}

// NewSender builds a Web Push sender. subject is the VAPID "sub" claim — a
// mailto: or https: URL identifying this server to the push service.
func NewSender(client *ent.Client, vapidPublic, vapidPrivate, subject string) *Sender {
	return &Sender{client: client, vapidPub: vapidPublic, vapidPriv: vapidPrivate, subject: subject}
}

// Push dispatches the payload to every subscription. It is best-effort (returns
// nothing): a marshal/query/network failure is logged and swallowed, and each
// subscription is handled independently so one dead endpoint never blocks the
// rest. Dead endpoints (404/410 Gone) are pruned immediately; endpoints that
// keep failing transiently are pruned once they cross failurePruneThreshold.
func (s *Sender) Push(ctx context.Context, payload notify.NewChapterNotification) {
	body, err := json.Marshal(payload)
	if err != nil {
		slog.WarnContext(ctx, "push: marshal payload failed", "err", err)
		return
	}
	rows, err := s.client.PushSubscription.Query().All(ctx)
	if err != nil {
		slog.WarnContext(ctx, "push: list subscriptions failed", "err", err)
		return
	}
	for _, row := range rows {
		s.sendOne(ctx, row, body)
	}
}

// sendOne delivers one encrypted payload and reconciles the subscription row
// against the push service's response.
func (s *Sender) sendOne(ctx context.Context, row *ent.PushSubscription, body []byte) {
	resp, err := webpush.SendNotificationWithContext(ctx, body, &webpush.Subscription{
		Endpoint: row.Endpoint,
		Keys:     webpush.Keys{P256dh: row.P256dh, Auth: row.Auth},
	}, &webpush.Options{
		Subscriber:      s.subject,
		VAPIDPublicKey:  s.vapidPub,
		VAPIDPrivateKey: s.vapidPriv,
		TTL:             pushTTLSeconds,
	})
	if err != nil {
		slog.WarnContext(ctx, "push: send failed", "endpoint", row.Endpoint, "err", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	s.reconcile(ctx, row, resp.StatusCode)
}

// reconcile updates the subscription row from the push-service status: a 2xx
// stamps last_success_at (and resets the failure counter); a 404/410 Gone prunes
// the row (the browser dropped the subscription); any other non-2xx bumps
// failure_count and prunes once it crosses the threshold.
func (s *Sender) reconcile(ctx context.Context, row *ent.PushSubscription, status int) {
	switch {
	case status >= 200 && status < 300:
		if err := s.client.PushSubscription.UpdateOneID(row.ID).
			SetLastSuccessAt(time.Now()).
			SetFailureCount(0).
			Exec(ctx); err != nil {
			slog.WarnContext(ctx, "push: mark success failed", "endpoint", row.Endpoint, "err", err)
		}
	case status == http.StatusNotFound || status == http.StatusGone:
		s.prune(ctx, row, "gone")
	default:
		if row.FailureCount+1 >= failurePruneThreshold {
			s.prune(ctx, row, "too many failures")
			return
		}
		if err := s.client.PushSubscription.UpdateOneID(row.ID).
			SetFailureCount(row.FailureCount + 1).
			Exec(ctx); err != nil {
			slog.WarnContext(ctx, "push: bump failure_count failed", "endpoint", row.Endpoint, "err", err)
		}
	}
}

// prune deletes a subscription row that will never deliver again.
func (s *Sender) prune(ctx context.Context, row *ent.PushSubscription, reason string) {
	if err := s.client.PushSubscription.DeleteOneID(row.ID).Exec(ctx); err != nil {
		slog.WarnContext(ctx, "push: prune failed", "endpoint", row.Endpoint, "reason", reason, "err", err)
		return
	}
	slog.InfoContext(ctx, "push: pruned dead subscription", "endpoint", row.Endpoint, "reason", reason)
}
