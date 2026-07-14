package push_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/push"
)

// TestSubscriptions_UpsertIdempotent proves upsert-by-endpoint: a second Upsert
// for the same endpoint updates in place (one row) rather than duplicating, and
// refreshes the keys.
func TestSubscriptions_UpsertIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := push.NewService(testdb.New(t))

	sub := push.Subscription{Endpoint: "https://push.example/abc", P256dh: "key1", Auth: "auth1"}
	if err := svc.Upsert(ctx, sub); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	sub.P256dh = "key2"
	sub.Auth = "auth2"
	if err := svc.Upsert(ctx, sub); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	list, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 subscription, got %d", len(list))
	}
	if list[0].P256dh != "key2" || list[0].Auth != "auth2" {
		t.Fatalf("keys not refreshed: %+v", list[0])
	}
}

// TestSubscriptions_Delete proves Delete removes the row and that deleting an
// unknown endpoint is a no-op (never an error).
func TestSubscriptions_Delete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := push.NewService(testdb.New(t))

	sub := push.Subscription{Endpoint: "https://push.example/xyz", P256dh: "k", Auth: "a"}
	if err := svc.Upsert(ctx, sub); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := svc.Delete(ctx, sub.Endpoint); err != nil {
		t.Fatalf("delete: %v", err)
	}
	list, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("want 0 subscriptions, got %d", len(list))
	}
	// Deleting an already-absent endpoint is a no-op.
	if err := svc.Delete(ctx, "https://push.example/never"); err != nil {
		t.Fatalf("delete-absent should be no-op, got %v", err)
	}
}
