package push_test

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/push"
)

// TestEnsureVAPID_GeneratesOnceStable proves the key pair is generated exactly
// once and every subsequent call returns the identical, valid-base64url pair.
func TestEnsureVAPID_GeneratesOnceStable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := testdb.New(t)

	pub1, priv1, err := push.EnsureVAPID(ctx, client)
	if err != nil {
		t.Fatalf("first EnsureVAPID: %v", err)
	}
	if pub1 == "" || priv1 == "" {
		t.Fatalf("empty keys: pub=%q priv=%q", pub1, priv1)
	}
	if _, err := base64.RawURLEncoding.DecodeString(pub1); err != nil {
		t.Fatalf("public key not base64url: %v", err)
	}
	if _, err := base64.RawURLEncoding.DecodeString(priv1); err != nil {
		t.Fatalf("private key not base64url: %v", err)
	}

	pub2, priv2, err := push.EnsureVAPID(ctx, client)
	if err != nil {
		t.Fatalf("second EnsureVAPID: %v", err)
	}
	if pub2 != pub1 || priv2 != priv1 {
		t.Fatalf("keys not stable: (%q,%q) != (%q,%q)", pub2, priv2, pub1, priv1)
	}
}

// TestEnsureVAPID_ReadErrorDoesNotRotate proves a transient READ error at boot
// aborts (returns the error) rather than regenerating — rotating the keypair
// would silently invalidate every existing device subscription.
func TestEnsureVAPID_ReadErrorDoesNotRotate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := testdb.New(t)

	pub1, priv1, err := push.EnsureVAPID(ctx, client)
	if err != nil {
		t.Fatalf("seed EnsureVAPID: %v", err)
	}

	// A cancelled context makes the settings read fail (a real error, not a
	// missing row): EnsureVAPID must abort, not regenerate.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := push.EnsureVAPID(cancelled, client); err == nil {
		t.Fatalf("expected EnsureVAPID to abort on a read error, got nil")
	}

	// The stored keypair is untouched.
	pub2, priv2, err := push.EnsureVAPID(ctx, client)
	if err != nil {
		t.Fatalf("re-read EnsureVAPID: %v", err)
	}
	if pub2 != pub1 || priv2 != priv1 {
		t.Fatalf("keypair rotated after a transient read error: (%q,%q) != (%q,%q)", pub2, priv2, pub1, priv1)
	}
}
