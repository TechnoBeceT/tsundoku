package category_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/database/testdb"
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
