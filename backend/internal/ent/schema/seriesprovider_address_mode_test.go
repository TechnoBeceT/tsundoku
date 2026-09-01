package schema_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
)

// TestSeriesProviderAddressModeDefaultsUnknown catches a missing migration,
// nullable column, or wrong database default. The provider is created without
// mentioning address mode, matching every legacy create path and every
// pre-existing row Ent upgrades additively.
func TestSeriesProviderAddressModeDefaultsUnknown(t *testing.T) {
	ctx := context.Background()
	client, sqlDB := testdb.NewWithSQL(t)
	series := client.Series.Create().SetTitle("Legacy Address").SetSlug("legacy-address").SaveX(ctx)
	provider := client.SeriesProvider.Create().SetSeries(series).SetProvider("42").SaveX(ctx)

	var got string
	if err := sqlDB.QueryRowContext(ctx, `SELECT address_mode FROM series_providers WHERE id = $1`, provider.ID).Scan(&got); err != nil {
		t.Fatalf("read migrated address_mode: %v", err)
	}
	if got != "unknown" {
		t.Fatalf("address_mode default = %q, want unknown", got)
	}
}

func TestSeriesProviderAddressModeMigrationBackfillsUnknown(t *testing.T) {
	ctx := context.Background()
	client, sqlDB := testdb.NewWithSQL(t)
	series := client.Series.Create().SetTitle("Pre-Migration Address").SetSlug("pre-migration-address").SaveX(ctx)
	provider := client.SeriesProvider.Create().SetSeries(series).SetProvider("43").SaveX(ctx)

	if _, err := sqlDB.ExecContext(ctx, `ALTER TABLE series_providers DROP COLUMN address_mode`); err != nil {
		t.Fatalf("remove address_mode to model the legacy schema: %v", err)
	}
	if err := client.Schema.Create(ctx); err != nil {
		t.Fatalf("run additive migration: %v", err)
	}

	var got string
	if err := sqlDB.QueryRowContext(ctx, `SELECT address_mode FROM series_providers WHERE id = $1`, provider.ID).Scan(&got); err != nil {
		t.Fatalf("read migrated address_mode: %v", err)
	}
	if got != "unknown" {
		t.Fatalf("migrated address_mode = %q, want unknown", got)
	}
}
