package testdb_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
)

// TestNewReturnsCleanClient verifies that a freshly created testdb client has
// an empty database — Owner.Query().Count must return 0.
func TestNewReturnsCleanClient(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	count, err := client.Owner.Query().Count(ctx)
	if err != nil {
		t.Fatalf("owner count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected empty database, got %d owner(s)", count)
	}
}

// TestNewIsIsolated verifies that two separate New(t) clients are fully isolated:
// a row created in client A must not be visible from client B.
func TestNewIsIsolated(t *testing.T) {
	ctx := context.Background()

	clientA := testdb.New(t)
	clientB := testdb.New(t)

	// Create an Owner in client A.
	clientA.Owner.Create().SetUsername("alice").SetPasswordHash("$2a$10$placeholder").SaveX(ctx)

	// Client B must still see an empty owners table.
	count, err := clientB.Owner.Query().Count(ctx)
	if err != nil {
		t.Fatalf("clientB owner count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected isolated database (count=0 in clientB), got %d", count)
	}
}
