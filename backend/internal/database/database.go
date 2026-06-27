// Package database provides the production database open path for the Tsundoku
// backend. It builds a pgx connection pool from a [config.DatabaseConfig],
// wraps it as an Ent client, and runs Ent auto-migration before returning.
//
// Open retries the initial connection with exponential backoff so that a
// not-yet-ready PostgreSQL container (e.g. during Docker startup) does not
// crash the process immediately.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/jackc/pgx/v5/stdlib" // pgx driver for database/sql

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/config"
	"github.com/technobecet/tsundoku/internal/ent"
)

// retryPolicy controls how Open retries a failed connection attempt.
// The defaults are short enough to exercise in tests (300 ms total sleep)
// while still being useful for a real startup race with PostgreSQL.
// len(delays) must equal maxAttempts-1 (no sleep after the final attempt).
var retryPolicy = struct {
	maxAttempts int
	delays      []time.Duration
}{
	maxAttempts: 3,
	delays:      []time.Duration{100 * time.Millisecond, 200 * time.Millisecond},
}

// pingDB is the function used by Open to check connectivity after sql.Open.
// It is a package-level variable so that tests can substitute a controlled
// implementation (see export_test.go / SetPingForTest).
var pingDB = func(ctx context.Context, db *sql.DB) error {
	return db.PingContext(ctx)
}

// Open builds a *sql.DB backed by the pgx stdlib driver, wraps it as an Ent
// client, and runs Ent auto-migration (client.Schema.Create).
//
// Retry / backoff: Open attempts to ping PostgreSQL up to maxAttempts times.
// Between consecutive attempts it sleeps for increasing durations (100 ms,
// 200 ms, …). If all attempts fail the last error is returned. Total worst-case
// sleep is the sum of all delay values (300 ms with the defaults).
//
// The caller is responsible for calling Close on the returned client when done.
func Open(ctx context.Context, cfg config.DatabaseConfig) (*ent.Client, error) {
	dsn := cfg.DSN()

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		// UNCOVERABLE: pgx/v5 stdlib registers cleanly; sql.Open only errors for an unregistered driver name.
		return nil, fmt.Errorf("database: sql.Open: %w", err)
	}

	// Retry loop: ping the DB until reachable or attempts exhausted.
	var pingErr error
	for attempt := 0; attempt < retryPolicy.maxAttempts; attempt++ {
		pingErr = pingDB(ctx, db)
		if pingErr == nil {
			break
		}
		// Sleep between attempts (no sleep after the final one).
		if attempt < len(retryPolicy.delays) {
			select {
			case <-time.After(retryPolicy.delays[attempt]):
			case <-ctx.Done():
				// Covered by TestOpenCancelledDuringBackoff: context cancelled
				// while waiting between retry attempts; abort immediately.
				_ = db.Close()
				return nil, fmt.Errorf("database: context cancelled while waiting to retry: %w", ctx.Err())
			}
		}
	}

	if pingErr != nil {
		_ = db.Close()
		return nil, fmt.Errorf("database: connect after %d attempts: %w", retryPolicy.maxAttempts, pingErr)
	}

	drv := entsql.OpenDB(dialect.Postgres, db)
	client := ent.NewClient(ent.Driver(drv))

	if err := client.Schema.Create(ctx); err != nil {
		// UNCOVERABLE in integration tests: reaching this branch requires
		// the DB to accept pings but reject schema DDL — not a realistic
		// scenario with a fresh ephemeral container. Documented, not faked.
		_ = client.Close()
		return nil, fmt.Errorf("database: run migration: %w", err)
	}

	if err := seedCategories(ctx, client, db); err != nil {
		_ = client.Close()
		return nil, err
	}

	return client, nil
}

// seedCategories seeds the five default categories, backfills any legacy
// (enum-era) series onto the matching Category, then drops the now-consumed legacy
// `category` enum column. It runs after migrate so a fresh DB and an upgraded DB
// both end with the defaults present and every series linked. The fixed order —
// EnsureDefaults → BackfillSeries → DropLegacyColumn — is what makes the drop safe:
// the column is only dropped AFTER its values have been migrated into category_id
// in the same startup. No disk I/O happens here — the migration only changes the
// DB representation, never moves a folder.
func seedCategories(ctx context.Context, client *ent.Client, db *sql.DB) error {
	if err := category.EnsureDefaults(ctx, client); err != nil {
		return fmt.Errorf("database: seed default categories: %w", err)
	}
	if err := category.BackfillSeries(ctx, db); err != nil {
		return fmt.Errorf("database: backfill series categories: %w", err)
	}
	if err := category.DropLegacyColumn(ctx, db); err != nil {
		return fmt.Errorf("database: drop legacy category column: %w", err)
	}
	return nil
}
