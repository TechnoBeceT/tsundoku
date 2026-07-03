package category

import (
	"context"
	"fmt"

	"github.com/technobecet/tsundoku/internal/ent"
	entcategory "github.com/technobecet/tsundoku/internal/ent/category"
)

// NormalizeSortOrder renumbers every category to a contiguous, unique sort_order
// (0..N-1) in the current (sort_order, name) order. It is idempotent — an
// already-contiguous set is left unchanged — and DB-only, so it never touches
// disk and can never move a folder.
//
// It repairs the deployed reorder-collision bug (F3): new categories were created
// with sort_order 0 (Ent's default), colliding with the seeded "Manga" (also 0).
// The frontend reorder is a two-PATCH value-SWAP, which is a NO-OP when the two
// rows share a sort_order — so the top slot could never move. Renumbering to
// distinct contiguous values on startup makes every swap see distinct values and
// work. It runs at startup (via EnsureDefaults) and in testdb for parity.
func NormalizeSortOrder(ctx context.Context, client *ent.Client) error {
	rows, err := client.Category.Query().
		Order(entcategory.BySortOrder(), entcategory.ByName()).
		All(ctx)
	if err != nil {
		return fmt.Errorf("category.NormalizeSortOrder: load categories: %w", err)
	}
	for i, c := range rows {
		if c.SortOrder == i {
			continue
		}
		if err := client.Category.UpdateOneID(c.ID).SetSortOrder(i).Exec(ctx); err != nil {
			return fmt.Errorf("category.NormalizeSortOrder: renumber %q: %w", c.Name, err)
		}
	}
	return nil
}

// nextSortOrder returns max(sort_order)+1 across all categories, or 0 when there
// are none — the append-at-the-end position for a newly created category. It is
// what Create uses when the caller omits sortOrder, so a new category never lands
// on the colliding default 0 (the root cause of F3).
func nextSortOrder(ctx context.Context, client *ent.Client) (int, error) {
	var v []struct {
		Max *int `json:"max"`
	}
	err := client.Category.Query().
		Aggregate(ent.Max(entcategory.FieldSortOrder)).
		Scan(ctx, &v)
	if err != nil {
		return 0, fmt.Errorf("category.Create: max sort_order: %w", err)
	}
	if len(v) == 0 || v[0].Max == nil {
		// No categories yet — start at 0.
		return 0, nil
	}
	return *v[0].Max + 1, nil
}
