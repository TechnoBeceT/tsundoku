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
//
// # Running the suite: `go test ./... -count=1 -p 1`
//
// Use -p 1 (serial packages). With -p 4 several ephemeral-Postgres containers race
// each other for host ports under RootlessKit and produce SUB-SECOND false failures.
// The duration is the tell: a real assertion in a container-backed test CANNOT fail
// that fast, because the container has not finished starting. On such a failure run
// `docker container prune -f` and re-run that package in isolation BEFORE treating it
// as a real defect. (runContainer retries the transient bind error, but the race is
// mitigated, not eliminated — see its doc comment.)
//
// NO -timeout flag is needed. The old `-timeout 25m` is dead; do not restore it. And
// do NOT "split the slow packages" — that intuitive fix is aimed at the wrong cause.
// internal/series once took ~625s not because the package was too big, but because
// this helper started a FRESH container per TEST: ~6.0s each, ~94% of every test's
// wall-clock, while the test BODIES ran in under 100ms. Splitting a package would only
// ADD another container start and make it worse. If the suite ever slows down again,
// MEASURE where the time actually goes before reshaping any code.
//
// # Test conventions (fleet standard, recorded here as the de-facto testing home)
//
// Tests are co-located `*_test.go` and black-box (`package x_test`) by default.
// Target near-100% coverage of REACHABLE code. A branch that is provably unreachable
// (a compiler-required terminal return, a defensive guard no caller can trigger) must
// be DOCUMENTED as such in a comment — never faked with a bogus test, and never made
// reachable by adding a production injection seam purely to move the coverage number.
package testdb

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
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
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sourceevents"
)

// adminDatabase is the bootstrap database the container is created with. It is
// never used by a test directly — it only serves as the connection target for the
// CREATE DATABASE / DROP DATABASE statements that provision the per-test databases.
const adminDatabase = "tsundoku_admin"

// shared holds the process-wide (i.e. per-test-binary) postgres server.
type shared struct {
	admin    *sql.DB // maintenance connection to adminDatabase
	adminDSN string  // DSN of adminDatabase; the template every per-test DSN is derived from
}

// NEVER set TESTCONTAINERS_RYUK_DISABLED for this package. The container behind the
// singleton below has NO other teardown: Go gives no package-level hook to stop it
// (that would need a TestMain in every consumer package), so the Ryuk reaper is the
// only thing that removes it when the test process exits. Disabling Ryuk leaks a
// postgres container per test binary, permanently, until the host is cleaned by hand.
//
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

	// Resolve the DSN BEFORE provisioning, so an unusable DSN cannot leave an
	// orphan database behind.
	dsn, err := dsnForDatabase(srv.adminDSN, dbName)
	if err != nil {
		t.Fatalf("testdb: derive dsn for %s: %v", dbName, err)
	}

	provisionMu.Lock()
	_, err = srv.admin.ExecContext(ctx, "CREATE DATABASE "+quoteIdent(dbName))
	provisionMu.Unlock()
	if err != nil {
		t.Fatalf("testdb: create database %s: %v", dbName, err)
	}

	// CLEANUP IS REGISTERED AT ACQUISITION TIME, never at the end of the happy path.
	//
	// Every step below (sql.Open, Schema.Create, the seed sequence) is fallible and
	// calls t.Fatalf. If the cleanups were registered only after the last of them, a
	// failing migration would leak this database AND its connection pool for the rest
	// of the test binary's life — and because the shared server's max_connections is
	// 100, a package with more tests than that would bury the REAL error under a
	// cascade of "sorry, too many clients already". t.Cleanup runs LIFO, so
	// registering in acquisition order (drop → pool close → ent close) executes in the
	// required teardown order (ent close → pool close → drop).
	t.Cleanup(func() { dropDatabase(t, srv, dbName) })

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("testdb: open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	drv := entsql.OpenDB(dialect.Postgres, db)
	client := ent.NewClient(ent.Driver(drv))
	t.Cleanup(func() { _ = client.Close() })

	if err := client.Schema.Create(ctx); err != nil {
		t.Fatalf("testdb: run ent migration: %v", err)
	}

	mirrorProductionSeedSequence(t, ctx, client, db)

	return client, db
}

// mirrorProductionSeedSequence runs, in order, every post-auto-migration step
// database.Open's runPostMigrationCleanup runs in production, so an
// integration test's fresh database ends up in the exact same shape a real
// startup would leave it. Every step is a no-op on a brand-new schema (no
// rows, no legacy columns) except category.EnsureDefaults (which seeds the
// five default categories tests rely on) — the rest run purely for parity
// with the production sequence and to exercise the call path.
func mirrorProductionSeedSequence(t *testing.T, ctx context.Context, client *ent.Client, db *sql.DB) {
	t.Helper()

	if err := category.EnsureDefaults(ctx, client); err != nil {
		t.Fatalf("testdb: seed default categories: %v", err)
	}
	if err := category.BackfillSeries(ctx, db); err != nil {
		t.Fatalf("testdb: backfill series categories: %v", err)
	}
	if err := category.DropLegacyColumn(ctx, db); err != nil {
		t.Fatalf("testdb: drop legacy category column: %v", err)
	}
	if err := library.DropLegacyImportEntryColumns(ctx, db); err != nil {
		t.Fatalf("testdb: drop legacy import_entries columns: %v", err)
	}
	if err := sourceevents.DropLegacyColumns(ctx, db); err != nil {
		t.Fatalf("testdb: drop legacy source_events columns: %v", err)
	}
	if _, err := series.BackfillFirstDownloadedAt(ctx, db); err != nil {
		t.Fatalf("testdb: backfill chapters first_downloaded_at: %v", err)
	}
}

// dropDatabase removes a per-test database from the shared server.
func dropDatabase(t *testing.T, srv *shared, dbName string) {
	t.Helper()

	// FORCE terminates any connection the test leaked (a lingering pool conn would
	// otherwise make the DROP fail and leak the database for the rest of the binary's
	// run). Requires PG13+; the image is postgres:17.
	dropCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	provisionMu.Lock()
	_, err := srv.admin.ExecContext(dropCtx,
		"DROP DATABASE IF EXISTS "+quoteIdent(dbName)+" WITH (FORCE)")
	provisionMu.Unlock()
	if err != nil {
		t.Logf("testdb: drop database %s: %v", dbName, err)
	}
}

// startAttempts bounds the container-start retries. See runContainer.
const startAttempts = 5

// backoffStep is the linear backoff unit between start attempts (attempt N waits
// N*backoffStep) — long enough for the daemon to release the contended port.
const backoffStep = 500 * time.Millisecond

// defaultBackoff is runContainer's backoff schedule, injected so the retry loop
// itself can be exercised without real sleeps.
func defaultBackoff(attempt int) time.Duration {
	return time.Duration(attempt) * backoffStep
}

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
	return startWithRetry(ctx, func(ctx context.Context) (*postgres.PostgresContainer, error) {
		return postgres.Run(ctx,
			"postgres:17-alpine",
			postgres.BasicWaitStrategies(),
			postgres.WithDatabase(adminDatabase),
			postgres.WithUsername("postgres"),
			postgres.WithPassword("postgres"),
		)
	}, func(ctx context.Context, ctr *postgres.PostgresContainer) {
		_ = ctr.Terminate(ctx)
	}, defaultBackoff)
}

// startWithRetry is runContainer's retry loop, factored out of the postgres module
// so it can be exercised directly (the start/terminate/backoff seams are injected).
//
// A FAILED start can still hand back a LIVE container: testcontainers creates the
// container first and only then publishes ports / runs the wait strategy, so the
// bind failure we retry on arrives *alongside* a non-nil handle. Terminating that
// handle before retrying is what stops a retried attempt from stranding a container
// for the rest of the run.
func startWithRetry[T comparable](
	ctx context.Context,
	start func(context.Context) (T, error),
	terminate func(context.Context, T),
	backoff func(attempt int) time.Duration,
) (T, error) {
	var zero T
	var lastErr error

	for attempt := 1; attempt <= startAttempts; attempt++ {
		ctr, err := start(ctx)
		if err == nil {
			return ctr, nil
		}
		if ctr != zero {
			terminate(ctx, ctr)
		}
		if !isPortBindFailure(err) {
			return zero, err
		}

		lastErr = err
		time.Sleep(backoff(attempt))
	}

	return zero, fmt.Errorf("after %d attempts: %w", startAttempts, lastErr)
}

// isPortBindFailure reports whether err is the transient rootless port-publish race
// (see runContainer). It is deliberately NARROW: every error it matches is retried,
// so a classifier that over-matches would turn a REAL, permanent failure (bad image,
// no daemon, OOM, a broken migration) into a slow flaky pass.
func isPortBindFailure(err error) bool {
	return err != nil && strings.Contains(err.Error(), "address already in use")
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

		sharedSrv = &shared{admin: admin, adminDSN: adminConnStr}
	})

	return sharedSrv, sharedErr
}

// dsnForDatabase derives a per-test DSN from the admin DSN by replacing the database
// PATH SEGMENT, preserving scheme/credentials/host/port and every query parameter.
//
// FAIL-LOUD BY CONSTRUCTION. This used to be a strings.Replace of "/<admin>?" — which
// silently NO-OPS if the DSN ever lacks a query string (e.g. someone drops the
// sslmode=disable argument from ConnectionString). The no-op would hand every
// "isolated" test a DSN still pointing at the SHARED ADMIN DATABASE: total isolation
// collapse, cross-test bleed, and not one error raised anywhere. Parsing the URL
// removes the query-string assumption entirely, and an admin DSN that does not look
// the way we expect is a hard error rather than a quietly wrong connection target.
func dsnForDatabase(adminDSN, dbName string) (string, error) {
	u, err := url.Parse(adminDSN)
	if err != nil {
		return "", fmt.Errorf("parse admin dsn: %w", err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("admin dsn %q has no host", adminDSN)
	}
	if got := strings.TrimPrefix(u.Path, "/"); got != adminDatabase {
		return "", fmt.Errorf(
			"admin dsn database is %q, want %q — refusing to derive a per-test dsn from it", got, adminDatabase)
	}
	if dbName == "" {
		return "", fmt.Errorf("empty target database name")
	}

	u.Path = "/" + dbName

	return u.String(), nil
}

// quoteIdent double-quotes a PostgreSQL identifier. Database names here are
// generated (tsundoku_test_<n>), never caller-supplied, so this is belt-and-braces
// against the CREATE/DROP DATABASE statements — which cannot take a bind parameter.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
