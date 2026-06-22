// Package database_test exercises the production Open path against a real
// ephemeral PostgreSQL container (QCAT-019).
package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/technobecet/tsundoku/internal/config"
	"github.com/technobecet/tsundoku/internal/database"
)

// containerDSN spins an ephemeral postgres:17-alpine container and returns
// a DatabaseConfig pointing at it. The container is terminated via t.Cleanup.
func containerDSN(t *testing.T) config.DatabaseConfig {
	t.Helper()

	ctx := context.Background()
	ctr, err := postgres.Run(ctx,
		"postgres:17-alpine",
		postgres.BasicWaitStrategies(),
		postgres.WithDatabase("tsundoku_test"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("testpassword"),
	)
	if err != nil {
		t.Fatalf("containerDSN: start postgres: %v", err)
	}

	connStr, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = ctr.Terminate(ctx) //nolint:errcheck
		t.Fatalf("containerDSN: connection string: %v", err)
	}

	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if terr := ctr.Terminate(shutdownCtx); terr != nil {
			t.Logf("containerDSN: terminate container: %v", terr)
		}
	})

	// Parse the DSN returned by testcontainers into a DatabaseConfig so that
	// database.Open receives the typed struct (not a raw URL).
	_ = connStr // the mapped port is embedded; extract host/port below

	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatalf("containerDSN: host: %v", err)
	}
	mappedPort, err := ctr.MappedPort(ctx, "5432")
	if err != nil {
		t.Fatalf("containerDSN: mapped port: %v", err)
	}

	return config.DatabaseConfig{
		Host:     host,
		Port:     mappedPort.Port(),
		User:     "postgres",
		Password: "testpassword",
		Name:     "tsundoku_test",
		SSLMode:  "disable",
	}
}

// TestOpenMigratesAndConnects calls database.Open against an ephemeral
// PostgreSQL 17 container, confirms it returns a usable *ent.Client, and
// proves that Ent auto-migration ran by querying the Owner table (0 rows, no
// error — a non-existent table would return an error).
func TestOpenMigratesAndConnects(t *testing.T) {
	cfg := containerDSN(t)
	ctx := context.Background()

	client, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Open: unexpected error: %v", err)
	}
	t.Cleanup(func() {
		if cerr := client.Close(); cerr != nil {
			t.Logf("client.Close: %v", cerr)
		}
	})

	// A query against the Owner table proves migration ran (the table exists).
	count, err := client.Owner.Query().Count(ctx)
	if err != nil {
		t.Fatalf("Owner.Query.Count: %v (migration may not have run)", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 owners after fresh migration, got %d", count)
	}
}

// TestOpenRetriesThenFails points Open at a TCP address that is guaranteed
// to refuse connections and asserts that:
//  1. Open returns a non-nil error (the dead host is never reachable).
//  2. The call takes at least as long as the total backoff sum, proving the
//     retry loop actually fired and slept between attempts rather than failing
//     immediately on the first try.
//
// Non-vacuity: if the retry loop were removed (one attempt only), the elapsed
// time would be well below minElapsed and the second assertion would fail.
func TestOpenRetriesThenFails(t *testing.T) {
	// TEST_LOCALHOST_REFUSE_PORT: port 1 is reserved/closed on all modern
	// Linux systems and will not accept connections.
	cfg := config.DatabaseConfig{
		Host:     "127.0.0.1",
		Port:     "1",
		User:     "postgres",
		Password: "doesnotmatter",
		Name:     "tsundoku",
		SSLMode:  "disable",
	}

	ctx := context.Background()
	start := time.Now()

	_, err := database.Open(ctx, cfg)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Open: expected an error for a dead host, got nil")
	}

	// database.Open retries with backoff: 3 attempts, delays 100ms + 200ms
	// before the 2nd and 3rd tries → total sleep ≥ 300ms.
	// If the retry loop were absent, elapsed would be near-zero.
	const minElapsed = 250 * time.Millisecond
	if elapsed < minElapsed {
		t.Fatalf("Open returned in %v — expected ≥ %v, which proves the retry loop never slept (retry removed?)", elapsed, minElapsed)
	}
}
