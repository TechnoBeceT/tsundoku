package sourceevents_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/sourceevents"
)

// TestDropLegacyColumns_UpgradedDB reproduces the upgraded-DB shape: the original
// dead SourceEvent stub left a NOT-NULL `source` column with no default. Ent's
// additive auto-migration never drops it, so on an upgraded DB the first insert
// through the redefined schema (which never sets `source`) violates NOT NULL.
// This proves the insert fails beforehand, then that DropLegacyColumns fixes it,
// and that a second call is an idempotent no-op.
func TestDropLegacyColumns_UpgradedDB(t *testing.T) {
	client, db := testdb.NewWithSQL(t)
	ctx := context.Background()

	// testdb already ran DropLegacyColumns in its seed sequence, so re-add the
	// legacy columns to simulate the pre-drop upgraded shape.
	addLegacySourceEventColumns(t, ctx, db)

	svc := sourceevents.NewService(client)
	if err := client.SourceEvent.Create().
		SetSourceKey("k").
		SetEventType("search").
		SetStatus("success").
		Exec(ctx); err == nil {
		t.Fatal("expected insert to fail with orphaned NOT-NULL `source` column present, got nil error")
	}

	if err := sourceevents.DropLegacyColumns(ctx, db); err != nil {
		t.Fatalf("DropLegacyColumns: %v", err)
	}

	// The best-effort writer now succeeds (LogBatch swallows errors, so assert via
	// a row count that a real insert landed).
	svc.Log(ctx, sourceevents.Event{SourceKey: "k", Type: sourceevents.EventSearch, Status: sourceevents.StatusSuccess})
	n, err := client.SourceEvent.Query().Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 event after dropping legacy columns, got %d", n)
	}

	// Idempotent: a second call on a DB that no longer has the columns is a no-op.
	if err := sourceevents.DropLegacyColumns(ctx, db); err != nil {
		t.Fatalf("DropLegacyColumns (second call): %v", err)
	}
}

// addLegacySourceEventColumns reproduces the dead stub's `source` + `payload`
// columns on the redefined source_events table: a NOT-NULL `source` with no
// default, exactly as an upgraded production DB would carry them.
func addLegacySourceEventColumns(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `ALTER TABLE source_events
		ADD COLUMN source text NOT NULL DEFAULT '',
		ADD COLUMN payload text NOT NULL DEFAULT ''`); err != nil {
		t.Fatalf("add legacy columns: %v", err)
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE source_events
		ALTER COLUMN source DROP DEFAULT`); err != nil {
		t.Fatalf("drop source default: %v", err)
	}
}
