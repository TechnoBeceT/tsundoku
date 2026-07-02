package library

import (
	"context"
	"database/sql"
	"fmt"
)

// DropLegacyImportEntryColumns removes the columns left behind by the original
// unused ImportEntry stub (created_at/updated_at/error). Ent's additive
// auto-migration never drops them, so on an upgraded DB they linger as
// NOT-NULL columns with no default and would break every ImportEntry insert.
// The table was never used, so dropping the columns loses no data. Idempotent
// (IF EXISTS) → a no-op on a fresh DB where the columns never existed.
func DropLegacyImportEntryColumns(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx,
		`ALTER TABLE import_entries DROP COLUMN IF EXISTS created_at, DROP COLUMN IF EXISTS updated_at, DROP COLUMN IF EXISTS error`)
	if err != nil {
		return fmt.Errorf("library.DropLegacyImportEntryColumns: %w", err)
	}
	return nil
}
