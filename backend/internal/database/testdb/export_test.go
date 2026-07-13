package testdb

import (
	"context"
	"time"
)

// Test-only seams for the black-box tests in testdb_test.go. The retry classifier,
// the retry loop, the identifier quoter and the DSN rewrite are all unexported
// internals with no reachable path from New/NewWithSQL that could exercise their
// failure branches (they only fire on infrastructure faults), so they are exported
// here — the established pattern in this repo (cf. internal/suwayomi/export_test.go).

// IsPortBindFailure exposes isPortBindFailure.
func IsPortBindFailure(err error) bool { return isPortBindFailure(err) }

// QuoteIdent exposes quoteIdent.
func QuoteIdent(s string) string { return quoteIdent(s) }

// DSNForDatabase exposes dsnForDatabase.
func DSNForDatabase(adminDSN, dbName string) (string, error) { return dsnForDatabase(adminDSN, dbName) }

// AdminDatabase exposes the adminDatabase constant.
const AdminDatabase = adminDatabase

// StartAttempts exposes the startAttempts bound.
const StartAttempts = startAttempts

// FakeContainer stands in for *postgres.PostgresContainer in StartWithRetry tests.
type FakeContainer struct{ Name string }

// StartWithRetry exposes startWithRetry over FakeContainer.
func StartWithRetry(
	ctx context.Context,
	start func(context.Context) (*FakeContainer, error),
	terminate func(context.Context, *FakeContainer),
	backoff func(attempt int) time.Duration,
) (*FakeContainer, error) {
	return startWithRetry(ctx, start, terminate, backoff)
}

// DefaultBackoff exposes defaultBackoff.
func DefaultBackoff(attempt int) time.Duration { return defaultBackoff(attempt) }
