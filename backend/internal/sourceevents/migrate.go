package sourceevents

import (
	"context"
	"database/sql"
	"fmt"
)

// DropLegacyColumns removes the two columns left behind by the original, never-
// written SourceEvent stub — `source` and `payload` — which the redefined,
// typed schema replaced. Ent's additive auto-migration (Schema.Create) never
// drops a superseded column, so on an upgraded DB they linger: `source` is a
// NOT-NULL string with no default and would break every new SourceEvent insert.
//
// The stub was referenced nowhere outside generated ent, so its table is EMPTY
// in every deployment — dropping the columns loses NO data (confirmed: the old
// entity had no writer). Idempotent (IF EXISTS) → a no-op on a fresh DB where
// the columns never existed. Mirrors category.DropLegacyColumn /
// library.DropLegacyImportEntryColumns; wired into runPostMigrationCleanup +
// testdb parity.
func DropLegacyColumns(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx,
		`ALTER TABLE source_events DROP COLUMN IF EXISTS source, DROP COLUMN IF EXISTS payload`)
	if err != nil {
		return fmt.Errorf("sourceevents.DropLegacyColumns: %w", err)
	}
	return nil
}
