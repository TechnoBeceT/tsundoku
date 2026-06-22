// Package testdb provides an ephemeral PostgreSQL container for integration tests.
// It has no production-binary footprint — the only import path is under internal/database/testdb,
// which is never referenced from production code (QCAT-019).
package testdb

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/jackc/pgx/v5/stdlib" // pgx driver for database/sql
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/technobecet/tsundoku/internal/ent"
)

// New spins up an ephemeral postgres:17-alpine container, runs Ent auto-migration,
// and returns a ready-to-use *ent.Client. The container and client are terminated
// automatically via t.Cleanup when the test finishes.
//
// It must only be called from test binaries.
func New(t *testing.T) *ent.Client {
	t.Helper()

	ctx := context.Background()

	ctr, err := postgres.Run(ctx,
		"postgres:17-alpine",
		postgres.BasicWaitStrategies(),
		postgres.WithDatabase("tsundoku_test"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("postgres"),
	)
	if err != nil {
		t.Fatalf("testdb: start postgres container: %v", err)
	}

	connStr, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("testdb: get connection string: %v", err)
	}

	db, err := sql.Open("pgx", connStr)
	if err != nil {
		t.Fatalf("testdb: open database: %v", err)
	}

	drv := entsql.OpenDB(dialect.Postgres, db)
	client := ent.NewClient(ent.Driver(drv))

	if err := client.Schema.Create(ctx); err != nil {
		t.Fatalf("testdb: run ent migration: %v", err)
	}

	t.Cleanup(func() {
		_ = client.Close()
		_ = db.Close()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := ctr.Terminate(shutdownCtx); err != nil {
			t.Logf("testdb: terminate container: %v", err)
		}
	})

	return client
}
