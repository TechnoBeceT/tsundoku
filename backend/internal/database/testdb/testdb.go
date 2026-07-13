// Package testdb provides an ephemeral PostgreSQL database for integration tests.
// It has no production-binary footprint — the only import path is under internal/database/testdb,
// which is never referenced from production code (QCAT-019).
//
// # Why one container per test BINARY, not one per test
//
// Every test used to spin up its OWN postgres:17-alpine container. Measured on the
// dev machine, that cost ~6.0s of container start + ~0.4s of terminate per test,
// against a ~29ms migration+seed and a test body typically well under 100ms — i.e.
// ~94% of the wall-clock of an integration test was the container, and package
// runtime scaled with test COUNT rather than with the work the tests did. Two
// packages (internal/series, internal/handler/series, ~90 tests each) grew past
// Go's default 600s test timeout purely from that per-test container tax.
//
// The container is therefore now a lazily-started, package-level singleton shared
// by every test in the SAME test binary (Go runs one binary per package, so the
// sharing radius is exactly one package — packages still cannot see each other's
// data, and `go test -p N` still bounds the number of live containers by N).
//
// # Isolation mechanism: one DATABASE per New() call
//
// Sharing a container must not share STATE. Each New/NewWithSQL call creates its
// own freshly-migrated, freshly-seeded PostgreSQL DATABASE on the shared server and
// hands back a client bound to it; t.Cleanup drops it. Tests therefore remain fully
// independent and order-insensitive, and two New(t) calls inside the SAME test are
// still isolated from each other (pinned by TestNewIsIsolated).
//
// A per-test database was chosen over truncate-between-tests because truncation is
// order-sensitive by construction (it must know every table, and it leaks sequence
// state and any DDL a test performed — e.g. the category/import-entry legacy-column
// tests, which do raw DDL). Re-running the migration per database costs only ~29ms,
// so the stronger isolation is essentially free.
//
// The shared container is reaped by the testcontainers Ryuk sidecar when the test
// process exits, so no TestMain is required in any consumer package.
package testdb

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/jackc/pgx/v5/stdlib" // pgx driver for database/sql
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/library"
)

// adminDatabase is the bootstrap database the container is created with. It is
// never used by a test directly — it only serves as the connection target for the
// CREATE DATABASE / DROP DATABASE statements that provision the per-test databases.
const adminDatabase = "tsundoku_admin"

// shared holds the process-wide (i.e. per-test-binary) postgres server.
type shared struct {
	admin   *sql.DB // maintenance connection to adminDatabase
	connStr func(dbName string) string
}

//nolint:gochecknoglobals // package-level singleton is the whole point: one container per test binary.
var (
	sharedOnce sync.Once
	sharedSrv  *shared
	sharedErr  error

	// dbSeq makes each per-test database name unique within the binary.
	dbSeq atomic.Uint64

	// provisionMu serializes CREATE/DROP DATABASE. Postgres takes a lock on the
	// template database for the duration of a CREATE DATABASE, so concurrent
	// creates (tests calling t.Parallel()) can otherwise fail with
	// "source database is being accessed by other users".
	provisionMu sync.Mutex
)

// New spins up (or reuses) the test binary's ephemeral postgres:17-alpine container,
// creates a fresh database on it, runs Ent auto-migration and the production seed
// sequence, and returns a ready-to-use *ent.Client. The client and its database are
// cleaned up automatically via t.Cleanup when the test finishes.
//
// It must only be called from test binaries.
func New(t *testing.T) *ent.Client {
	t.Helper()
	client, _ := NewWithSQL(t)
	return client
}

// NewWithSQL is New but also returns the underlying *sql.DB so tests that need
// raw SQL (e.g. the category-migration backfill, which reads the legacy enum
// column that no longer exists in the Ent schema) can run it. The same lifecycle
// guarantees apply — both handles are closed via t.Cleanup.
func NewWithSQL(t *testing.T) (*ent.Client, *sql.DB) {
	t.Helper()

	ctx := context.Background()

	srv, err := sharedServer(ctx)
	if err != nil {
		t.Fatalf("testdb: start shared postgres container: %v", err)
	}

	dbName := fmt.Sprintf("tsundoku_test_%d", dbSeq.Add(1))

	provisionMu.Lock()
	_, err = srv.admin.ExecContext(ctx, "CREATE DATABASE "+quoteIdent(dbName))
	provisionMu.Unlock()
	if err != nil {
		t.Fatalf("testdb: create database %s: %v", dbName, err)
	}

	db, err := sql.Open("pgx", srv.connStr(dbName))
	if err != nil {
		t.Fatalf("testdb: open database: %v", err)
	}

	drv := entsql.OpenDB(dialect.Postgres, db)
	client := ent.NewClient(ent.Driver(drv))

	if err := client.Schema.Create(ctx); err != nil {
		t.Fatalf("testdb: run ent migration: %v", err)
	}

	// Mirror production startup (database.Open seedCategories): seed the default
	// categories so integration tests have the five defaults available and series
	// can be linked to a real Category (the app invariant). BackfillSeries and
	// DropLegacyColumn are both no-ops on the fresh schema (no rows, no legacy
	// `category` column) but are run for parity with the production seed sequence.
	if err := category.EnsureDefaults(ctx, client); err != nil {
		t.Fatalf("testdb: seed default categories: %v", err)
	}
	if err := category.BackfillSeries(ctx, db); err != nil {
		t.Fatalf("testdb: backfill series categories: %v", err)
	}
	if err := category.DropLegacyColumn(ctx, db); err != nil {
		t.Fatalf("testdb: drop legacy category column: %v", err)
	}

	// Mirror production startup (database.Open): drop the orphaned columns
	// left behind by the original unused ImportEntry stub. This is a no-op on
	// the fresh testdb table (the columns never existed here) but is run for
	// parity with the production sequence and to exercise the call path.
	if err := library.DropLegacyImportEntryColumns(ctx, db); err != nil {
		t.Fatalf("testdb: drop legacy import_entries columns: %v", err)
	}

	t.Cleanup(func() {
		_ = client.Close()
		_ = db.Close()

		// FORCE terminates any connection the test leaked (a lingering pool
		// conn would otherwise make the DROP fail and leak the database for the
		// rest of the binary's run). Requires PG13+; the image is postgres:17.
		dropCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		provisionMu.Lock()
		_, err := srv.admin.ExecContext(dropCtx,
			"DROP DATABASE IF EXISTS "+quoteIdent(dbName)+" WITH (FORCE)")
		provisionMu.Unlock()
		if err != nil {
			t.Logf("testdb: drop database %s: %v", dbName, err)
		}
	})

	return client, db
}

// startAttempts bounds the container-start retries. See runContainer.
const startAttempts = 5

// runContainer starts the postgres container, retrying a transient failure.
//
// Rootless Docker (RootlessKit) intermittently fails to publish a container port
// with "PortManager.AddPort(): listen tcp4 0.0.0.0:<p>: bind: address already in
// use": the daemon's allocator hands out a port from the ephemeral range that is
// momentarily still held (typically by the *previous* package's just-reaped
// container, since `go test -p 1` starts binaries back-to-back). It is pure
// infrastructure — the tell is the DURATION, a sub-second failure that no
// container-backed test could possibly reach — and it is fatal to a whole package
// now that the container is a per-binary singleton.
//
// A retry is the correct remedy because the failure is port-SPECIFIC: a fresh
// attempt draws a fresh port. Only the transient bind failure is retried; any
// other error (bad image, no daemon) returns immediately rather than burning the
// backoff on a fault that cannot heal.
func runContainer(ctx context.Context) (*postgres.PostgresContainer, error) {
	var lastErr error

	for attempt := 1; attempt <= startAttempts; attempt++ {
		ctr, err := postgres.Run(ctx,
			"postgres:17-alpine",
			postgres.BasicWaitStrategies(),
			postgres.WithDatabase(adminDatabase),
			postgres.WithUsername("postgres"),
			postgres.WithPassword("postgres"),
		)
		if err == nil {
			return ctr, nil
		}
		if !isPortBindFailure(err) {
			return nil, err
		}

		lastErr = err
		time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
	}

	return nil, fmt.Errorf("after %d attempts: %w", startAttempts, lastErr)
}

// isPortBindFailure reports whether err is the transient rootless port-publish race.
func isPortBindFailure(err error) bool {
	return strings.Contains(err.Error(), "address already in use")
}

// sharedServer lazily starts the one container this test binary uses. The container
// itself is terminated by the testcontainers Ryuk reaper when the process exits —
// Go offers no package-level teardown hook without a TestMain in every consumer
// package, and Ryuk is the mechanism testcontainers provides for exactly this.
func sharedServer(ctx context.Context) (*shared, error) {
	sharedOnce.Do(func() {
		ctr, err := runContainer(ctx)
		if err != nil {
			sharedErr = fmt.Errorf("run container: %w", err)
			return
		}

		adminConnStr, err := ctr.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			sharedErr = fmt.Errorf("connection string: %w", err)
			return
		}

		admin, err := sql.Open("pgx", adminConnStr)
		if err != nil {
			sharedErr = fmt.Errorf("open admin database: %w", err)
			return
		}

		sharedSrv = &shared{
			admin: admin,
			connStr: func(dbName string) string {
				// The module hands back .../<adminDatabase>?sslmode=disable — swap
				// only the database path segment, preserving host/port/credentials.
				return strings.Replace(adminConnStr, "/"+adminDatabase+"?", "/"+dbName+"?", 1)
			},
		}
	})

	return sharedSrv, sharedErr
}

// quoteIdent double-quotes a PostgreSQL identifier. Database names here are
// generated (tsundoku_test_<n>), never caller-supplied, so this is belt-and-braces
// against the CREATE/DROP DATABASE statements — which cannot take a bind parameter.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
