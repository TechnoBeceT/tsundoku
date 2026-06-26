package category

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/technobecet/tsundoku/internal/ent"
	entcategory "github.com/technobecet/tsundoku/internal/ent/category"
)

// DefaultCategoryName is the protected fallback category every series can always
// be filed under. It is the only protected default and the backfill target for
// any legacy row whose category cannot be matched.
const DefaultCategoryName = "Other"

// defaultCategory describes one of the five seeded categories.
type defaultCategory struct {
	name      string
	sortOrder int
	protected bool
}

// defaultCategories is the ordered seed set. It reproduces the former
// Series.category enum exactly (same names, same order) so an existing-enum-era
// library backfills with ZERO data migration — every legacy enum string matches
// a seeded category of the same name. Only "Other" is protected.
var defaultCategories = []defaultCategory{
	{name: "Manga", sortOrder: 0},
	{name: "Manhwa", sortOrder: 1},
	{name: "Manhua", sortOrder: 2},
	{name: "Comic", sortOrder: 3},
	{name: DefaultCategoryName, sortOrder: 4, protected: true},
}

// EnsureDefaults idempotently creates any of the five default categories that
// are missing, with their canonical sort order and the protected flag on
// "Other". Running it twice is a no-op (find-or-create by name). It is invoked
// at startup (after Ent auto-migration) so a fresh DB and an existing DB both
// end with the five defaults present; new owner-created categories are never
// touched.
func EnsureDefaults(ctx context.Context, client *ent.Client) error {
	for _, d := range defaultCategories {
		exists, err := client.Category.Query().Where(entcategory.Name(d.name)).Exist(ctx)
		if err != nil {
			return fmt.Errorf("category.EnsureDefaults: check %q: %w", d.name, err)
		}
		if exists {
			continue
		}
		if err := client.Category.Create().
			SetName(d.name).
			SetSortOrder(d.sortOrder).
			SetProtected(d.protected).
			Exec(ctx); err != nil {
			// A concurrent startup could win the unique-name race between the
			// Exist check and Create; treat an already-created row as success.
			if ent.IsConstraintError(err) {
				continue
			}
			return fmt.Errorf("category.EnsureDefaults: create %q: %w", d.name, err)
		}
	}
	return nil
}

// BackfillSeries links every series that still has a NULL category_id to a
// Category — the one-time migration from the legacy Series.category enum column.
//
// It runs at startup AFTER EnsureDefaults. For each unlinked series it sets
// category_id to the Category whose name matches the legacy enum value (every
// legacy value — Manga…Other — has a same-named seeded category), falling back
// to "Other" when the legacy column is absent (a brand-new DB never had it) or a
// row's value does not match. The work is a single UPDATE; it is idempotent
// (a second run finds no NULL rows) and does ZERO disk I/O — it cannot move a
// folder, so an existing series' on-disk location is provably untouched by the
// migration.
//
// It takes the raw *sql.DB because the legacy `category` column no longer exists
// in the Ent schema and so cannot be read through the typed client.
func BackfillSeries(ctx context.Context, db *sql.DB) error {
	otherID, err := otherCategoryID(ctx, db)
	if err != nil {
		return err
	}

	legacyExists, err := legacyCategoryColumnExists(ctx, db)
	if err != nil {
		return err
	}

	var query string
	if legacyExists {
		// Match each unlinked series to the same-named category; fall back to
		// "Other" for any value that does not match a seeded category.
		query = `UPDATE series
		         SET category_id = COALESCE(
		             (SELECT c.id FROM categories c WHERE c.name = series.category),
		             $1)
		         WHERE category_id IS NULL`
	} else {
		// No legacy column (fresh DB): any unlinked row defaults to "Other".
		query = `UPDATE series SET category_id = $1 WHERE category_id IS NULL`
	}

	if _, err := db.ExecContext(ctx, query, otherID); err != nil {
		return fmt.Errorf("category.BackfillSeries: backfill update: %w", err)
	}
	return nil
}

// otherCategoryID returns the id of the protected "Other" category, the backfill
// fallback. EnsureDefaults must have run first.
func otherCategoryID(ctx context.Context, db *sql.DB) (string, error) {
	var id string
	err := db.QueryRowContext(ctx, `SELECT id FROM categories WHERE name = $1`, DefaultCategoryName).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("category.BackfillSeries: load %q id: %w", DefaultCategoryName, err)
	}
	return id, nil
}

// legacyCategoryColumnExists reports whether the series table still carries the
// pre-migration `category` enum column. Ent's auto-migration never drops it (it
// only adds the new category_id), so on an upgraded DB it is present and the
// backfill reads it; on a fresh DB it never existed.
func legacyCategoryColumnExists(ctx context.Context, db *sql.DB) (bool, error) {
	var exists bool
	err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'series' AND column_name = 'category'
		)`).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("category.BackfillSeries: probe legacy column: %w", err)
	}
	return exists, nil
}
