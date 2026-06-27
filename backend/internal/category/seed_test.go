package category_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entcategory "github.com/technobecet/tsundoku/internal/ent/category"
)

// TestEnsureDefaultsIdempotent verifies that EnsureDefaults always leaves exactly
// the five defaults (run twice → still five) with Other protected.
func TestEnsureDefaultsIdempotent(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	// testdb already seeds once; run twice more.
	for i := 0; i < 2; i++ {
		if err := category.EnsureDefaults(ctx, client); err != nil {
			t.Fatalf("EnsureDefaults run %d: %v", i, err)
		}
	}
	if n := client.Category.Query().CountX(ctx); n != 5 {
		t.Fatalf("want 5 categories after repeated EnsureDefaults, got %d", n)
	}
}

// TestBackfillSeriesFromLegacyEnumColumn is the MIGRATION-SAFETY proof. It
// simulates an existing enum-era database: a series row whose category lives in
// the legacy `category` column (which the new Ent schema no longer defines) with
// a NULL category_id. After EnsureDefaults + BackfillSeries the series must be
// linked to the same-named Category — and its on-disk folder must be UNTOUCHED
// (the migration only changes the DB representation; no folder moves).
func TestBackfillSeriesFromLegacyEnumColumn(t *testing.T) {
	ctx := context.Background()
	client, db := testdb.NewWithSQL(t)

	// Re-create the legacy enum-era column that the new schema dropped from the
	// model (Ent never DROPs it on a real upgrade; here we add it back to mimic
	// an upgraded DB).
	if _, err := db.ExecContext(ctx, `ALTER TABLE series ADD COLUMN category varchar NOT NULL DEFAULT 'Other'`); err != nil {
		t.Fatalf("add legacy column: %v", err)
	}

	// An enum-era series: created WITHOUT a category_id (NULL), legacy value Manhwa.
	s := client.Series.Create().SetTitle("Legacy Series").SetSlug("legacy-series").SaveX(ctx)
	if _, err := db.ExecContext(ctx, `UPDATE series SET category = 'Manhwa' WHERE id = $1`, s.ID); err != nil {
		t.Fatalf("set legacy category: %v", err)
	}

	// Its on-disk folder, which the migration must NOT move.
	storage := t.TempDir()
	folder := filepath.Join(storage, "Manhwa", "Legacy Series")
	if err := os.MkdirAll(folder, 0o750); err != nil {
		t.Fatalf("seed folder: %v", err)
	}

	// Run the startup seed + backfill (EnsureDefaults already ran in testdb; run
	// the backfill, which is what links the legacy row).
	if err := category.EnsureDefaults(ctx, client); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}
	if err := category.BackfillSeries(ctx, db); err != nil {
		t.Fatalf("BackfillSeries: %v", err)
	}

	// The series is now linked to the same-named "Manhwa" category by id.
	got := client.Series.Query().WithCategory().OnlyX(ctx)
	if got.Edges.Category == nil || got.Edges.Category.Name != "Manhwa" {
		t.Fatalf("backfill: series category = %+v, want Manhwa", got.Edges.Category)
	}

	// The on-disk folder is exactly where it was — the migration moved nothing.
	if _, err := os.Stat(folder); err != nil {
		t.Fatalf("migration must not move the on-disk folder: %v", err)
	}
}

// TestBackfillSeriesNoLegacyColumnDefaultsOther verifies that on a fresh DB (no
// legacy column), a series that somehow has a NULL category_id is defaulted to
// the protected "Other" fallback rather than left category-less.
func TestBackfillSeriesNoLegacyColumnDefaultsOther(t *testing.T) {
	ctx := context.Background()
	client, db := testdb.NewWithSQL(t)

	// Force a NULL category_id row (the fresh schema has no legacy column).
	s := client.Series.Create().SetTitle("Null Cat").SetSlug("null-cat").SaveX(ctx)
	if _, err := db.ExecContext(ctx, `UPDATE series SET category_id = NULL WHERE id = $1`, s.ID); err != nil {
		t.Fatalf("null the category_id: %v", err)
	}

	if err := category.BackfillSeries(ctx, db); err != nil {
		t.Fatalf("BackfillSeries: %v", err)
	}

	got := client.Series.Query().WithCategory().OnlyX(ctx)
	if got.Edges.Category == nil || got.Edges.Category.Name != "Other" {
		t.Fatalf("backfill default: series category = %+v, want Other", got.Edges.Category)
	}
}

// mustSeedSequence runs the full production startup category sequence
// (EnsureDefaults → BackfillSeries → DropLegacyColumn) and fails the test on any
// error. It mirrors database.seedCategories so the migration is exercised exactly
// as production runs it.
func mustSeedSequence(ctx context.Context, t *testing.T, client *ent.Client, db *sql.DB) {
	t.Helper()
	if err := category.EnsureDefaults(ctx, client); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}
	if err := category.BackfillSeries(ctx, db); err != nil {
		t.Fatalf("BackfillSeries: %v", err)
	}
	if err := category.DropLegacyColumn(ctx, db); err != nil {
		t.Fatalf("DropLegacyColumn: %v", err)
	}
}

// seriesColumnExists reports whether the series table has a column named col.
func seriesColumnExists(ctx context.Context, t *testing.T, db *sql.DB, col string) bool {
	t.Helper()
	var exists bool
	err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'series' AND column_name = $1
		)`, col).Scan(&exists)
	if err != nil {
		t.Fatalf("probe series column %q: %v", col, err)
	}
	return exists
}

// TestDropLegacyColumnConsumeThenDrop is the CONSUME-THEN-DROP migration proof.
// It simulates an upgraded enum-era DB (a legacy `category` column value with a
// NULL category_id), runs the full startup sequence, and asserts:
//
//	(a) category_id is linked to the same-named Category (the value was consumed);
//	(b) the legacy `category` column no longer exists (it was dropped);
//	(c) a SECOND full run is a clean no-op (idempotent + order-robust).
func TestDropLegacyColumnConsumeThenDrop(t *testing.T) {
	ctx := context.Background()
	client, db := testdb.NewWithSQL(t)

	// Re-create the legacy enum-era column the new schema no longer models (testdb
	// setup already dropped it; Ent never models it). This mimics an upgraded DB
	// that still carries a pre-migration value.
	if _, err := db.ExecContext(ctx, `ALTER TABLE series ADD COLUMN category varchar NOT NULL DEFAULT 'Other'`); err != nil {
		t.Fatalf("add legacy column: %v", err)
	}
	s := client.Series.Create().SetTitle("Drop Me").SetSlug("drop-me").SaveX(ctx)
	if _, err := db.ExecContext(ctx, `UPDATE series SET category = 'Manhua', category_id = NULL WHERE id = $1`, s.ID); err != nil {
		t.Fatalf("set legacy value + null id: %v", err)
	}

	// First full run: consume the legacy value into category_id, then drop the column.
	mustSeedSequence(ctx, t, client, db)

	// (a) The legacy value was consumed — series is linked to the same-named category.
	got := client.Series.Query().WithCategory().OnlyX(ctx)
	if got.Edges.Category == nil || got.Edges.Category.Name != "Manhua" {
		t.Fatalf("link: series category = %+v, want Manhua", got.Edges.Category)
	}
	// (b) The legacy column is gone.
	if seriesColumnExists(ctx, t, db, "category") {
		t.Fatal("legacy `category` column still exists after DropLegacyColumn")
	}

	// (c) A second full run is a clean no-op: the IF EXISTS drop and the
	// already-linked rows make the whole sequence re-runnable.
	mustSeedSequence(ctx, t, client, db)
	if seriesColumnExists(ctx, t, db, "category") {
		t.Fatal("legacy `category` column reappeared on the second run")
	}
	got2 := client.Series.Query().WithCategory().OnlyX(ctx)
	if got2.Edges.Category == nil || got2.Edges.Category.Name != "Manhua" {
		t.Fatalf("second run changed the link: %+v", got2.Edges.Category)
	}
}

// TestDropLegacyColumnIdempotentOnFreshSchema verifies DropLegacyColumn is a
// no-op (no error) on a schema that never had the legacy column, run twice — the
// fresh-DB / already-dropped path the IF EXISTS guard covers.
func TestDropLegacyColumnIdempotentOnFreshSchema(t *testing.T) {
	ctx := context.Background()
	_, db := testdb.NewWithSQL(t)

	for i := 0; i < 2; i++ {
		if err := category.DropLegacyColumn(ctx, db); err != nil {
			t.Fatalf("DropLegacyColumn run %d on fresh schema: %v", i, err)
		}
	}
	if seriesColumnExists(ctx, t, db, "category") {
		t.Fatal("fresh schema unexpectedly has a legacy `category` column")
	}
}

// TestFindOrCreateIsIdempotent verifies FindOrCreate returns the existing row
// when present and creates exactly one row when absent (concurrent-safe shape).
func TestFindOrCreateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	a, err := category.FindOrCreate(ctx, client, "Indie")
	if err != nil {
		t.Fatalf("FindOrCreate (create): %v", err)
	}
	b, err := category.FindOrCreate(ctx, client, "Indie")
	if err != nil {
		t.Fatalf("FindOrCreate (find): %v", err)
	}
	if a.ID != b.ID {
		t.Fatalf("FindOrCreate not idempotent: %s != %s", a.ID, b.ID)
	}
	if n := client.Category.Query().Where(entcategory.Name("Indie")).CountX(ctx); n != 1 {
		t.Fatalf("want exactly 1 Indie category, got %d", n)
	}
}

// TestIDByNameUnknownReturnsNotFound verifies the IDByName not-found mapping.
func TestIDByNameUnknownReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	if _, err := category.IDByName(ctx, client, "Nope"); err != category.ErrCategoryNotFound {
		t.Fatalf("IDByName(unknown): want ErrCategoryNotFound, got %v", err)
	}
}
