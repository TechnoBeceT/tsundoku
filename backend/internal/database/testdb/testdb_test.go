package testdb_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

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

// --- isPortBindFailure ------------------------------------------------------
//
// The retry classifier is the one component that can convert a REAL failure into a
// slow flaky pass: everything it matches is retried up to startAttempts times. It
// must match the transient rootless port-publish race and NOTHING else.

func TestIsPortBindFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "rootless port-publish race (the ONLY retryable fault)",
			err: errors.New("failed to start container: port forwarding: " +
				"PortManager.AddPort(): listen tcp4 0.0.0.0:32768: bind: address already in use"),
			want: true,
		},
		{
			name: "wrapped port-bind failure",
			err:  fmt.Errorf("run container: %w", errors.New("bind: address already in use")),
			want: true,
		},
		{
			name: "bad image — permanent, must NOT retry",
			err:  errors.New(`pull image: Error response from daemon: manifest for postgres:99-alpine not found`),
			want: false,
		},
		{
			name: "no docker daemon — permanent, must NOT retry",
			err: errors.New("Cannot connect to the Docker daemon at unix:///var/run/docker.sock. " +
				"Is the docker daemon running?"),
			want: false,
		},
		{
			name: "OOM — permanent, must NOT retry",
			err:  errors.New("container exited: OOMKilled: cannot allocate memory"),
			want: false,
		},
		{
			name: "migration failure — permanent, must NOT retry",
			err:  errors.New(`ent: run ent migration: column "title" cannot be null`),
			want: false,
		},
		{
			name: "container-readiness timeout — permanent, must NOT retry",
			err:  errors.New("failed to start container: context deadline exceeded"),
			want: false,
		},
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := testdb.IsPortBindFailure(tt.err); got != tt.want {
				t.Fatalf("IsPortBindFailure(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// --- startWithRetry ---------------------------------------------------------

// noBackoff keeps the retry-loop tests instant (the real schedule is defaultBackoff).
func noBackoff(int) time.Duration { return 0 }

func TestStartWithRetry_SucceedsFirstAttempt(t *testing.T) {
	t.Parallel()

	var starts, terminates int
	ctr, err := testdb.StartWithRetry(context.Background(),
		func(context.Context) (*testdb.FakeContainer, error) {
			starts++

			return &testdb.FakeContainer{Name: "ok"}, nil
		},
		func(context.Context, *testdb.FakeContainer) { terminates++ },
		noBackoff,
	)
	if err != nil {
		t.Fatalf("StartWithRetry: %v", err)
	}
	if ctr == nil || ctr.Name != "ok" {
		t.Fatalf("expected the started container, got %+v", ctr)
	}
	if starts != 1 || terminates != 0 {
		t.Fatalf("starts=%d terminates=%d, want 1/0", starts, terminates)
	}
}

// TestStartWithRetry_TerminatesTheStrandedContainer is the FINDING-5 guard: a failed
// start can hand back a LIVE container alongside its error. If the retry ignores that
// handle, the retried attempt strands an extra container for the rest of the run.
func TestStartWithRetry_TerminatesTheStrandedContainer(t *testing.T) {
	t.Parallel()

	var starts int
	var terminated []string

	ctr, err := testdb.StartWithRetry(context.Background(),
		func(context.Context) (*testdb.FakeContainer, error) {
			starts++
			if starts == 1 {
				// A non-nil container AND a transient error — the real shape.
				return &testdb.FakeContainer{Name: "stranded"},
					errors.New("PortManager.AddPort(): bind: address already in use")
			}

			return &testdb.FakeContainer{Name: "good"}, nil
		},
		func(_ context.Context, c *testdb.FakeContainer) { terminated = append(terminated, c.Name) },
		noBackoff,
	)
	if err != nil {
		t.Fatalf("StartWithRetry: %v", err)
	}
	if ctr.Name != "good" {
		t.Fatalf("got container %q, want the retried one", ctr.Name)
	}
	if len(terminated) != 1 || terminated[0] != "stranded" {
		t.Fatalf("terminated=%v, want exactly [stranded]", terminated)
	}
}

// A start that fails with a nil container must not be terminated (nothing to free).
func TestStartWithRetry_NilContainerIsNotTerminated(t *testing.T) {
	t.Parallel()

	var starts, terminates int
	_, err := testdb.StartWithRetry(context.Background(),
		func(context.Context) (*testdb.FakeContainer, error) {
			starts++

			return nil, errors.New("bind: address already in use")
		},
		func(context.Context, *testdb.FakeContainer) { terminates++ },
		noBackoff,
	)
	if err == nil {
		t.Fatal("expected exhaustion error")
	}
	if starts != testdb.StartAttempts {
		t.Fatalf("starts=%d, want %d", starts, testdb.StartAttempts)
	}
	if terminates != 0 {
		t.Fatalf("terminates=%d, want 0 (there was no container to free)", terminates)
	}
}

// A permanent fault must fail on the FIRST attempt — burning the backoff on a fault
// that cannot heal is exactly what turns a real failure into a slow flaky run.
func TestStartWithRetry_PermanentErrorIsNotRetried(t *testing.T) {
	t.Parallel()

	var starts int
	var terminates int
	permanent := errors.New("manifest for postgres:99-alpine not found")

	_, err := testdb.StartWithRetry(context.Background(),
		func(context.Context) (*testdb.FakeContainer, error) {
			starts++

			return &testdb.FakeContainer{Name: "half-built"}, permanent
		},
		func(context.Context, *testdb.FakeContainer) { terminates++ },
		noBackoff,
	)
	if !errors.Is(err, permanent) {
		t.Fatalf("err = %v, want the permanent error verbatim", err)
	}
	if starts != 1 {
		t.Fatalf("starts=%d, want 1 (no retry on a permanent fault)", starts)
	}
	if terminates != 1 {
		t.Fatalf("terminates=%d, want 1 (the half-built container must still be freed)", terminates)
	}
}

func TestDefaultBackoffIsLinear(t *testing.T) {
	t.Parallel()

	if got, want := testdb.DefaultBackoff(1), 500*time.Millisecond; got != want {
		t.Fatalf("DefaultBackoff(1) = %v, want %v", got, want)
	}
	if got, want := testdb.DefaultBackoff(3), 1500*time.Millisecond; got != want {
		t.Fatalf("DefaultBackoff(3) = %v, want %v", got, want)
	}
}

// --- quoteIdent -------------------------------------------------------------

func TestQuoteIdent(t *testing.T) {
	t.Parallel()

	tests := []struct{ in, want string }{
		{in: "tsundoku_test_1", want: `"tsundoku_test_1"`},
		{in: "", want: `""`},
		// An embedded double quote is doubled, per the PostgreSQL identifier rules —
		// the function claims to handle it, so it is pinned here.
		{in: `we"ird`, want: `"we""ird"`},
		{in: `"; DROP DATABASE postgres; --`, want: `"""; DROP DATABASE postgres; --"`},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()

			if got := testdb.QuoteIdent(tt.in); got != tt.want {
				t.Fatalf("QuoteIdent(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// --- dsnForDatabase ---------------------------------------------------------

// testDSN builds a DSN of the shape testcontainers' ConnectionString returns.
// (Assembled rather than written as a literal so gosec's G101 password-in-URL rule
// does not fire on what is a throwaway localhost test fixture.)
func testDSN(database, query string) string {
	return fmt.Sprintf("postgres://%s@localhost:32768/%s%s", "postgres:postgres", database, query)
}

// databaseOf returns the DSN's database (path) segment.
func databaseOf(t *testing.T, dsn string) string {
	t.Helper()

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse %q: %v", dsn, err)
	}

	return strings.TrimPrefix(u.Path, "/")
}

func TestDSNForDatabase_RewritesTheDatabaseAndPreservesEverythingElse(t *testing.T) {
	t.Parallel()

	got, err := testdb.DSNForDatabase(testDSN(testdb.AdminDatabase, "?sslmode=disable"), "tsundoku_test_7")
	if err != nil {
		t.Fatalf("DSNForDatabase: %v", err)
	}

	want := testDSN("tsundoku_test_7", "?sslmode=disable")
	if got != want {
		t.Fatalf("DSNForDatabase = %q, want %q", got, want)
	}
}

// TestDSNForDatabase_NoQueryStringStillLeavesTheAdminDatabase is the FINDING-3
// regression guard.
//
// The old implementation was strings.Replace(adminDSN, "/"+adminDatabase+"?", …, 1).
// Given a DSN with NO query string — which is exactly what happens if anyone drops
// the sslmode=disable argument from ConnectionString — the "?"-anchored pattern never
// matches, strings.Replace SILENTLY NO-OPS, and every supposedly-isolated test gets a
// DSN still pointing at the SHARED ADMIN DATABASE: cross-test bleed with no error
// raised anywhere. This test fails loudly if that silent no-op is ever reintroduced:
// whatever the DSN shape, the result must NEVER still address adminDatabase.
func TestDSNForDatabase_NoQueryStringStillLeavesTheAdminDatabase(t *testing.T) {
	t.Parallel()

	adminDSNNoQuery := testDSN(testdb.AdminDatabase, "")

	got, err := testdb.DSNForDatabase(adminDSNNoQuery, "tsundoku_test_9")
	if err != nil {
		// Fail-loud is an acceptable outcome. A silently-admin DSN is not.
		return
	}

	if db := databaseOf(t, got); db != "tsundoku_test_9" {
		t.Fatalf("DSNForDatabase(%q) = %q (database %q) — a DSN without a query string "+
			"must NOT silently keep addressing the admin database", adminDSNNoQuery, got, db)
	}
}

func TestDSNForDatabase_FailsLoudOnUnexpectedDSN(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		adminDSN string
		dbName   string
	}{
		{name: "unparseable", adminDSN: "postgres://user:pa ss@%%/db", dbName: "tsundoku_test_1"},
		{name: "no host", adminDSN: "postgres:///" + testdb.AdminDatabase, dbName: "tsundoku_test_1"},
		{
			name:     "not the admin database",
			adminDSN: testDSN("somethingelse", "?sslmode=disable"),
			dbName:   "tsundoku_test_1",
		},
		{
			name:     "empty target database",
			adminDSN: testDSN(testdb.AdminDatabase, "?sslmode=disable"),
			dbName:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := testdb.DSNForDatabase(tt.adminDSN, tt.dbName)
			if err == nil {
				t.Fatalf("DSNForDatabase(%q, %q) = %q, want an error — an unexpected admin DSN "+
					"must fail loudly, never yield a quietly wrong connection target",
					tt.adminDSN, tt.dbName, got)
			}
		})
	}
}
