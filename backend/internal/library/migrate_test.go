package library_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/library"
)

// TestDropLegacyImportEntryColumns_UpgradedDB reproduces the CRITICAL
// upgraded-DB bug: a pre-existing unused ImportEntry stub left orphaned
// NOT-NULL created_at/updated_at/error columns with no default. Ent's
// additive auto-migration never drops them, so on an upgraded DB the first
// insert through the new schema (which never sets those columns) violates
// the NOT NULL constraint. This test simulates that upgraded shape, proves
// the insert fails beforehand, then proves DropLegacyImportEntryColumns
// fixes it.
func TestDropLegacyImportEntryColumns_UpgradedDB(t *testing.T) {
	client, db := testdb.NewWithSQL(t)
	ctx := context.Background()

	addOrphanedLegacyColumns(t, ctx, db)

	if _, err := client.ImportEntry.Create().SetPath("/x-before").SetTitle("t").Save(ctx); err == nil {
		t.Fatal("expected insert to fail with orphaned NOT-NULL columns present, got nil error")
	}

	if err := library.DropLegacyImportEntryColumns(ctx, db); err != nil {
		t.Fatalf("DropLegacyImportEntryColumns: %v", err)
	}

	if _, err := client.ImportEntry.Create().SetPath("/x-after").SetTitle("t").Save(ctx); err != nil {
		t.Fatalf("expected insert to succeed after dropping legacy columns, got: %v", err)
	}

	// Idempotent: a second call on a DB that never had the columns is a no-op.
	if err := library.DropLegacyImportEntryColumns(ctx, db); err != nil {
		t.Fatalf("DropLegacyImportEntryColumns (second call): %v", err)
	}
}

// addOrphanedLegacyColumns reproduces the pre-existing ImportEntry stub shape
// on the fresh import_entries table: NOT-NULL created_at/updated_at/error
// columns with no default, exactly as an upgraded production DB would carry
// them (Ent's additive auto-migration never drops superseded columns).
func addOrphanedLegacyColumns(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	_, err := db.ExecContext(ctx, `ALTER TABLE import_entries
		ADD COLUMN created_at timestamptz NOT NULL DEFAULT now(),
		ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now(),
		ADD COLUMN error text NOT NULL DEFAULT ''`)
	if err != nil {
		t.Fatalf("addOrphanedLegacyColumns: add columns: %v", err)
	}
	_, err = db.ExecContext(ctx, `ALTER TABLE import_entries
		ALTER COLUMN created_at DROP DEFAULT,
		ALTER COLUMN updated_at DROP DEFAULT`)
	if err != nil {
		t.Fatalf("addOrphanedLegacyColumns: drop defaults: %v", err)
	}
}
