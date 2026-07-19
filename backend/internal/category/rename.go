package category

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
)

// Rename changes a category's name, keeping the DB and the on-disk category
// folder consistent — the category is disk-folder-determining (Komga contract),
// so the rename physically moves <storage>/<oldName> to <storage>/<newName>.
//
// newName is validated (filesystem-safe) and must be unique. ANY category is
// renameable, including the current default (QCAT-296: the fallback role is the
// is_default invariant, not a name-lock, so nothing is name-locked). A missing id
// returns ErrCategoryNotFound; a duplicate name returns ErrCategoryNameTaken.
//
// Disk↔DB coordination mirrors series.SetCategory: the folder is moved on disk
// FIRST, then the DB name is updated, with compensation (move the folder back)
// if the DB update fails — so the two never end in disagreement. A category with
// no folder yet (no series rendered) is a DB-only rename (nothing to move).
func (s *Service) Rename(ctx context.Context, id uuid.UUID, newName string) error {
	row, clean, noop, err := s.prepareRename(ctx, id, newName)
	if err != nil || noop {
		return err
	}

	moved, err := s.renameFolder(row.Name, clean)
	if err != nil {
		return err
	}

	if err := s.client.Category.UpdateOneID(id).SetName(clean).Exec(ctx); err != nil {
		dbErr := fmt.Errorf("category.Rename: update name for %s: %w", id, err)
		if !moved {
			return dbErr
		}
		// Compensate: the folder already moved but the DB update failed. Move it
		// back so disk matches the still-old DB name. Surface BOTH errors if the
		// compensation also fails — never swallow either (§16).
		if cErr := disk.RenameCategory(s.storage, clean, row.Name); cErr != nil {
			return errors.Join(dbErr, fmt.Errorf("category.Rename: compensating move-back failed: %w", cErr))
		}
		return dbErr
	}
	return nil
}

// prepareRename validates a rename and loads the target category. It returns the
// loaded row and the cleaned new name, or noop=true when the rename is a no-op
// (the name is unchanged). Errors: ErrInvalidCategoryName (bad name),
// ErrCategoryNotFound (unknown id), or ErrCategoryNameTaken (duplicate).
// Extracted so Rename stays within the
// cyclomatic-complexity budget (cyclop ≤ 10).
func (s *Service) prepareRename(ctx context.Context, id uuid.UUID, newName string) (row *ent.Category, clean string, noop bool, err error) {
	clean, err = ValidateName(newName)
	if err != nil {
		return nil, "", false, err
	}
	row, err = s.byID(ctx, id)
	if err != nil {
		return nil, "", false, err
	}
	if clean == row.Name {
		// No-op: same name. (A pure-whitespace edit normalises to the same value.)
		return row, clean, true, nil
	}
	taken, err := s.nameTaken(ctx, clean, id)
	if err != nil {
		return nil, "", false, err
	}
	if taken {
		return nil, "", false, ErrCategoryNameTaken
	}
	return row, clean, false, nil
}

// renameFolder moves the category folder on disk from oldName to newName, unless
// the category has no folder yet (no series rendered). It returns moved=true when
// a real move happened (so Rename knows whether to compensate on a later DB
// failure), moved=false when skipped because the source dir is genuinely absent.
// A real rename failure (collision, cross-device, permission) is returned as-is
// and never masked as "no folder".
func (s *Service) renameFolder(oldName, newName string) (moved bool, err error) {
	src := filepath.Join(s.storage, oldName)
	if _, statErr := os.Stat(src); statErr != nil {
		if os.IsNotExist(statErr) {
			// No folder yet → DB-only rename.
			return false, nil
		}
		// Defensive path: reachable only on an OS-level stat failure other than
		// not-exist (permission denied / fd exhausted).
		return false, fmt.Errorf("category.Rename: stat folder %q: %w", src, statErr)
	}
	if err := disk.RenameCategory(s.storage, oldName, newName); err != nil {
		return false, fmt.Errorf("category.Rename: move folder: %w", err)
	}
	return true, nil
}
