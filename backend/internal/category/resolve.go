package category

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entcategory "github.com/technobecet/tsundoku/internal/ent/category"
)

// FindOrCreate returns the Category with the given name, creating it (sort_order
// 0, unprotected) when none exists. It mirrors the find-or-create-SeriesProvider
// pattern: there is no in-application uniqueness lock, so it queries first and
// absorbs the unique-name constraint race by re-querying after a failed create.
//
// It does NOT run ValidateName: callers pass a name that already exists as a
// real on-disk folder (reconcile's dynamic scanner) or a name validated by the
// API layer (create/rename). This is the seam that lets a user-named category
// folder round-trip into a Category row after a DB-loss reconcile.
func FindOrCreate(ctx context.Context, client *ent.Client, name string) (*ent.Category, error) {
	existing, err := client.Category.Query().Where(entcategory.Name(name)).First(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return nil, fmt.Errorf("category.FindOrCreate: query %q: %w", name, err)
	}
	if existing != nil {
		return existing, nil
	}

	created, err := client.Category.Create().SetName(name).Save(ctx)
	if err == nil {
		return created, nil
	}
	if !ent.IsConstraintError(err) {
		return nil, fmt.Errorf("category.FindOrCreate: create %q: %w", name, err)
	}
	// Lost the unique-name race with a concurrent create — re-query for the row
	// the winner inserted.
	row, qErr := client.Category.Query().Where(entcategory.Name(name)).Only(ctx)
	if qErr != nil {
		return nil, fmt.Errorf("category.FindOrCreate: re-query %q after race: %w", name, qErr)
	}
	return row, nil
}

// IDByName returns the id of the Category with the given name, mapping a missing
// row to ErrCategoryNotFound. Used by callers that hold a category name (e.g. the
// import/adopt path resolving a chosen category, or tests linking a series to a
// seeded default) and need its id without the full row.
func IDByName(ctx context.Context, client *ent.Client, name string) (uuid.UUID, error) {
	row, err := client.Category.Query().Where(entcategory.Name(name)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return uuid.Nil, ErrCategoryNotFound
		}
		return uuid.Nil, fmt.Errorf("category.IDByName: query %q: %w", name, err)
	}
	return row.ID, nil
}

// byID loads a Category by id, mapping a missing row to ErrCategoryNotFound.
func (s *Service) byID(ctx context.Context, id uuid.UUID) (*ent.Category, error) {
	row, err := s.client.Category.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrCategoryNotFound
		}
		return nil, fmt.Errorf("category: load %s: %w", id, err)
	}
	return row, nil
}
