package push

import (
	"context"
	"fmt"

	"github.com/technobecet/tsundoku/internal/ent"
	entpush "github.com/technobecet/tsundoku/internal/ent/pushsubscription"
)

// Subscription is one device's Web Push registration as the API exchanges it —
// the endpoint plus the two browser-supplied encryption keys. It is the shape
// the handler binds and the sender fans out to.
type Subscription struct {
	// Endpoint is the push-service URL (the natural identity of a subscription).
	Endpoint string
	// P256dh + Auth are the browser's public encryption keys (base64url).
	P256dh string
	Auth   string
}

// Service is the subscription CRUD store over the PushSubscription table.
// Construct with NewService.
type Service struct {
	client *ent.Client
}

// NewService builds a subscription store over the Ent client.
func NewService(client *ent.Client) *Service {
	return &Service{client: client}
}

// Upsert stores a subscription keyed by its endpoint: it creates the row the
// first time and refreshes the encryption keys (and clears failure_count) on a
// re-subscribe from the same device. Idempotent.
func (s *Service) Upsert(ctx context.Context, sub Subscription) error {
	existing, err := s.client.PushSubscription.Query().
		Where(entpush.EndpointEQ(sub.Endpoint)).Only(ctx)
	if ent.IsNotFound(err) {
		if cErr := s.client.PushSubscription.Create().
			SetEndpoint(sub.Endpoint).
			SetP256dh(sub.P256dh).
			SetAuth(sub.Auth).
			Exec(ctx); cErr != nil {
			return fmt.Errorf("push.Upsert: create: %w", cErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("push.Upsert: query: %w", err)
	}
	if uErr := s.client.PushSubscription.UpdateOneID(existing.ID).
		SetP256dh(sub.P256dh).
		SetAuth(sub.Auth).
		SetFailureCount(0).
		ClearLastSuccessAt().
		Exec(ctx); uErr != nil {
		return fmt.Errorf("push.Upsert: update: %w", uErr)
	}
	return nil
}

// Delete removes the subscription for the given endpoint. Deleting an unknown
// endpoint is a no-op (never an error) — the device is already unsubscribed.
func (s *Service) Delete(ctx context.Context, endpoint string) error {
	_, err := s.client.PushSubscription.Delete().
		Where(entpush.EndpointEQ(endpoint)).Exec(ctx)
	if err != nil {
		return fmt.Errorf("push.Delete: %w", err)
	}
	return nil
}

// List returns every stored subscription (the sender's fan-out set).
func (s *Service) List(ctx context.Context) ([]Subscription, error) {
	rows, err := s.client.PushSubscription.Query().All(ctx)
	if err != nil {
		return nil, fmt.Errorf("push.List: %w", err)
	}
	out := make([]Subscription, 0, len(rows))
	for _, r := range rows {
		out = append(out, Subscription{Endpoint: r.Endpoint, P256dh: r.P256dh, Auth: r.Auth})
	}
	return out, nil
}
