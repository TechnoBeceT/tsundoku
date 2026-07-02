package category

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entcategory "github.com/technobecet/tsundoku/internal/ent/category"
)

// SetDefault promotes the category with the given id to be THE default landing
// category (where new / uncategorized series are filed) and demotes whichever
// category was the default before. The is_default invariant — exactly one row
// true — is preserved by doing both writes in ONE transaction: unset the current
// default, then set the target. A missing id returns ErrCategoryNotFound.
//
// Promoting a category is a pure DB preference: it moves no folder and touches no
// series. Its one visible consequence beyond the badge is that the PREVIOUS
// default becomes deletable (the delete-guard keys on is_default) while the new
// default becomes undeletable.
func (s *Service) SetDefault(ctx context.Context, id uuid.UUID) error {
	// Confirm the target exists first so an unknown id is a clean 404 rather than a
	// silent no-op inside the transaction.
	if _, err := s.byID(ctx, id); err != nil {
		return err
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("category.SetDefault: begin tx: %w", err)
	}

	if err := setDefaultTx(ctx, tx, id); err != nil {
		// Roll back and surface the original error; a rollback failure is joined so
		// neither error is swallowed.
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("category.SetDefault: %w (rollback failed: %v)", err, rbErr)
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("category.SetDefault: commit: %w", err)
	}
	return nil
}

// setDefaultTx clears is_default on every currently-default category (there is
// exactly one under the invariant, but the predicate is set-based so a stray
// extra is also cleared) and sets it on the target — all inside the caller's tx.
func setDefaultTx(ctx context.Context, tx *ent.Tx, id uuid.UUID) error {
	if _, err := tx.Category.Update().
		Where(entcategory.IsDefault(true), entcategory.IDNEQ(id)).
		SetIsDefault(false).
		Save(ctx); err != nil {
		return fmt.Errorf("category.SetDefault: clear previous default: %w", err)
	}
	if err := tx.Category.UpdateOneID(id).SetIsDefault(true).Exec(ctx); err != nil {
		return fmt.Errorf("category.SetDefault: set default %s: %w", id, err)
	}
	return nil
}
