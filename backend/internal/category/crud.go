package category

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entcategory "github.com/technobecet/tsundoku/internal/ent/category"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
)

// Create adds a new, empty category. name is validated (non-blank, ≤64 chars,
// filesystem-safe) and must be unique. sortOrder is optional — when nil the
// category is APPENDED at the end (max(existing)+1) rather than left on Ent's
// default 0, which would collide with the seeded "Manga" and break the frontend
// reorder swap (F3). No folder is created on disk until a series is first filed
// there. Returns ErrInvalidCategoryName (bad name), ErrCategoryNameTaken
// (duplicate), or the created CategoryDTO (count 0).
func (s *Service) Create(ctx context.Context, name string, sortOrder *int) (CategoryDTO, error) {
	clean, err := ValidateName(name)
	if err != nil {
		return CategoryDTO{}, err
	}

	taken, err := s.nameTaken(ctx, clean, uuid.Nil)
	if err != nil {
		return CategoryDTO{}, err
	}
	if taken {
		return CategoryDTO{}, ErrCategoryNameTaken
	}

	order := sortOrder
	if order == nil {
		next, nErr := nextSortOrder(ctx, s.client)
		if nErr != nil {
			return CategoryDTO{}, nErr
		}
		order = &next
	}

	row, err := s.client.Category.Create().SetName(clean).SetSortOrder(*order).Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			// Lost the unique-name race after the check above.
			return CategoryDTO{}, ErrCategoryNameTaken
		}
		return CategoryDTO{}, fmt.Errorf("category.Create: save %q: %w", clean, err)
	}
	return newCategoryDTO(row, 0), nil
}

// List returns every category ordered by sort_order then name, each with its
// current series count. The counts come from a SINGLE grouped aggregate
// (GROUP BY category_id) over the series table — no N+1, the same no-extra-query
// shape the former Series.Categories() used.
func (s *Service) List(ctx context.Context) ([]CategoryDTO, error) {
	rows, err := s.client.Category.Query().
		Order(entcategory.BySortOrder(), entcategory.ByName()).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("category.List: query categories: %w", err)
	}

	counts, err := s.seriesCounts(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]CategoryDTO, len(rows))
	for i, c := range rows {
		out[i] = newCategoryDTO(c, counts[c.ID])
	}
	return out, nil
}

// seriesCounts returns a category_id → series-count map from one grouped
// aggregate. Series with a NULL category_id (only possible pre-backfill) do not
// contribute to any category's count.
func (s *Service) seriesCounts(ctx context.Context) (map[uuid.UUID]int, error) {
	var rows []struct {
		CategoryID uuid.UUID `json:"category_id"`
		Count      int       `json:"count"`
	}
	err := s.client.Series.Query().
		Where(entseries.CategoryIDNotNil()).
		GroupBy(entseries.FieldCategoryID).
		Aggregate(ent.Count()).
		Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("category.List: aggregate series by category: %w", err)
	}
	counts := make(map[uuid.UUID]int, len(rows))
	for _, r := range rows {
		counts[r.CategoryID] = r.Count
	}
	return counts, nil
}

// Get returns one category by id with its current series count. A missing id
// returns ErrCategoryNotFound. Used by the handlers to return the persisted
// state after a create/rename/reorder (§16 round-trip).
func (s *Service) Get(ctx context.Context, id uuid.UUID) (CategoryDTO, error) {
	row, err := s.byID(ctx, id)
	if err != nil {
		return CategoryDTO{}, err
	}
	count, err := s.client.Series.Query().Where(entseries.CategoryID(id)).Count(ctx)
	if err != nil {
		return CategoryDTO{}, fmt.Errorf("category.Get: count series for %s: %w", id, err)
	}
	return newCategoryDTO(row, count), nil
}

// Reorder updates a category's sort_order (DB-only, no disk work). A missing id
// returns ErrCategoryNotFound.
func (s *Service) Reorder(ctx context.Context, id uuid.UUID, sortOrder int) error {
	if _, err := s.byID(ctx, id); err != nil {
		return err
	}
	if err := s.client.Category.UpdateOneID(id).SetSortOrder(sortOrder).Exec(ctx); err != nil {
		// Defensive path: the row exists (confirmed above); an error here is
		// reachable only on a DB-level failure.
		return fmt.Errorf("category.Reorder: update %s: %w", id, err)
	}
	return nil
}

// Delete removes a category. It is allowed ONLY when no series is filed under it
// (else ErrCategoryNotEmpty) and never for the current default (else
// ErrCategoryIsDefault) — so new / uncategorized series always have a landing
// spot. A demoted "Other" (protected but no longer the default) IS deletable. It
// is DB-only: it deletes no series, no CBZ, and leaves any on-disk folder
// untouched (an empty category folder, if present, is left as is). A missing id
// returns ErrCategoryNotFound.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	row, err := s.byID(ctx, id)
	if err != nil {
		return err
	}
	if row.IsDefault {
		return ErrCategoryIsDefault
	}

	count, err := s.client.Series.Query().Where(entseries.CategoryID(id)).Count(ctx)
	if err != nil {
		return fmt.Errorf("category.Delete: count series for %s: %w", id, err)
	}
	if count > 0 {
		return ErrCategoryNotEmpty
	}

	if err := s.client.Category.DeleteOneID(id).Exec(ctx); err != nil {
		// Defensive path: the row exists (confirmed above) and is empty; an error
		// here is reachable only on a DB-level failure.
		return fmt.Errorf("category.Delete: delete %s: %w", id, err)
	}
	return nil
}

// nameTaken reports whether another category already uses name. excludeID (when
// not uuid.Nil) is skipped so a rename to a category's own current name is not a
// false collision.
func (s *Service) nameTaken(ctx context.Context, name string, excludeID uuid.UUID) (bool, error) {
	q := s.client.Category.Query().Where(entcategory.Name(name))
	if excludeID != uuid.Nil {
		q = q.Where(entcategory.IDNEQ(excludeID))
	}
	taken, err := q.Exist(ctx)
	if err != nil {
		return false, fmt.Errorf("category: check name %q: %w", name, err)
	}
	return taken, nil
}
