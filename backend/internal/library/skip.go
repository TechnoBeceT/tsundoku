package library

import (
	"context"

	"github.com/technobecet/tsundoku/internal/ent/importentry"
)

// Skip marks the staged ImportEntry at path as "skipped" — the owner's "leave
// this on disk, don't import it" action. Idempotent (skipping an already-skipped
// row is a no-op success). Purely a status flip: no disk I/O, no row/CBZ deletion
// (never-auto-delete invariant). ErrEntryNotFound when no row matches path.
func (s *Service) Skip(ctx context.Context, path string) error {
	n, err := s.db.ImportEntry.Update().
		Where(importentry.Path(path)).
		SetStatus(statusSkipped).
		Save(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrEntryNotFound
	}
	return nil
}
