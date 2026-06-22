// Package database_test exercises the production Open path against a real
// ephemeral PostgreSQL container (QCAT-019).
package database_test

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
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

	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if terr := ctr.Terminate(shutdownCtx); terr != nil {
			t.Logf("containerDSN: terminate container: %v", terr)
		}
	})

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

// TestOpenRetriesThenFails asserts that Open exhausts the full retry budget
// before returning an error.  The load-bearing assertion is an attempt counter
// (via the pingDB seam), not wall-clock time, so the test is robust against
// slow CI machines and scheduler jitter.
//
// Non-vacuity: if the retry loop were removed (one attempt only), attempts
// would equal 1, not maxAttempts, and the assertion would fail.
func TestOpenRetriesThenFails(t *testing.T) {
	var attempts int32
	restore := database.SetPingForTest(func(ctx context.Context, db *sql.DB) error {
		atomic.AddInt32(&attempts, 1)
		return errors.New("simulated ping failure")
	})
	defer restore()

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

	// Primary assertion: the retry loop ran the full attempt budget.
	got := int(atomic.LoadInt32(&attempts))
	want := *database.MaxAttempts
	if got != want {
		t.Fatalf("ping called %d times; expected exactly %d (full retry budget)", got, want)
	}

	// Sanity check: total sleep must be at least the sum of the retry delays
	// (100ms + 200ms = 300ms). A loose bound avoids flakiness on slow machines.
	const minElapsed = 250 * time.Millisecond
	if elapsed < minElapsed {
		t.Logf("Open returned in %v — less than expected %v (delays may have been shortened)", elapsed, minElapsed)
	}
}

// TestOpenCancelledDuringBackoff proves that the ctx.Done() branch inside the
// retry-sleep select is reachable and that Open returns promptly when the
// context is cancelled mid-backoff.
//
// Setup: the pingDB seam always returns an error (so a backoff sleep is
// entered after the first attempt), and the context has a 50 ms timeout —
// shorter than the first backoff delay (100 ms).  Open must wake on ctx.Done()
// and return a context error well before the full backoff elapses.
func TestOpenCancelledDuringBackoff(t *testing.T) {
	restore := database.SetPingForTest(func(ctx context.Context, db *sql.DB) error {
		return errors.New("simulated ping failure — force backoff sleep")
	})
	defer restore()

	cfg := config.DatabaseConfig{
		Host:     "127.0.0.1",
		Port:     "1",
		User:     "postgres",
		Password: "doesnotmatter",
		Name:     "tsundoku",
		SSLMode:  "disable",
	}

	// 50 ms timeout < 100 ms first-backoff delay → ctx.Done() fires mid-sleep.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := database.Open(ctx, cfg)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Open: expected an error when context is cancelled, got nil")
	}

	// Must be a context error (DeadlineExceeded or Canceled).
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("expected a context error; got: %v", err)
	}

	// Must have returned promptly — not waited the full 100 ms first-backoff.
	const maxElapsed = 150 * time.Millisecond
	if elapsed >= maxElapsed {
		t.Fatalf("Open took %v — expected < %v, proving it did NOT honour ctx.Done() mid-backoff", elapsed, maxElapsed)
	}
}

// TestOpenSucceedsOnSecondAttempt verifies the "fail once → still opens" path:
// the retry loop must continue past a transient ping error and succeed on the
// next attempt.  A real ephemeral Postgres container is used so that migration
// also runs and the returned client is genuinely usable.
func TestOpenSucceedsOnSecondAttempt(t *testing.T) {
	cfg := containerDSN(t)

	var callCount int32
	restore := database.SetPingForTest(func(ctx context.Context, db *sql.DB) error {
		n := atomic.AddInt32(&callCount, 1)
		if n == 1 {
			return errors.New("simulated transient ping failure on first attempt")
		}
		// Second (and subsequent) calls delegate to the real ping.
		return db.PingContext(ctx)
	})
	defer restore()

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

	// Confirm ping was called exactly twice (first failed, second succeeded).
	got := int(atomic.LoadInt32(&callCount))
	if got != 2 {
		t.Fatalf("pingDB called %d times; expected exactly 2 (fail once, succeed once)", got)
	}

	// Confirm the client is usable: migration ran and Owner table exists.
	count, err := client.Owner.Query().Count(ctx)
	if err != nil {
		t.Fatalf("Owner.Query.Count: %v (migration may not have run)", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 owners after fresh migration, got %d", count)
	}
}
