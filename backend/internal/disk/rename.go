package disk

import (
	"fmt"
	"os"
	"path/filepath"
)

// RenameCategory renames a whole category folder on disk in a SINGLE atomic
// os.Rename, carrying every series under it at once, then rewrites each child
// series' tsundoku.json Category field to the new name.
//
// Layout: the category lives at <storage>/<oldName> and is moved to
// <storage>/<newName>. Because it is one os.Rename of the parent directory, all
// child series + their CBZs move together atomically on the same filesystem —
// far cheaper and safer than N per-series moves.
//
// Ordering and failure modes (mirrors MoveSeriesCategory so DB↔disk never drift
// — the caller updates the DB only after this succeeds):
//   - oldName == newName → no-op, returns nil.
//   - the source dir absent → DB-only rename: returns nil with NO disk work (a
//     category with no downloaded series yet has no folder). The caller still
//     updates the DB name.
//   - the target dir must NOT already exist (collision → error, NO move).
//   - a post-rename sidecar failure on ANY child triggers a best-effort rollback
//     that renames the folder back to src, so a non-nil error always means NO net
//     change happened (folder is back at oldName) — the caller can trust DB ==
//     old. A failed rollback surfaces both errors via errors.Join (never
//     swallowed).
//
// Parameters: storage is the library root; oldName/newName are category folder
// names (already validated by the caller as filesystem-safe).
func RenameCategory(storage, oldName, newName string) error {
	if oldName == newName {
		return nil
	}

	src := filepath.Join(storage, oldName)
	dst := filepath.Join(storage, newName)

	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			// No folder on disk yet (no series rendered under this category) →
			// nothing to move; the caller does the DB-only rename.
			return nil
		}
		// Defensive path: reachable only on an OS-level stat failure other than
		// not-exist (permission denied / fd exhausted). Surfaced, not swallowed.
		return fmt.Errorf("disk.RenameCategory: stat source %q: %w", src, err)
	}

	if err := requireTargetAbsent(dst); err != nil {
		return err
	}

	if err := os.Rename(src, dst); err != nil {
		// A cross-device rename surfaces here with no partial move (the storage
		// root is one filesystem by contract).
		return fmt.Errorf("disk.RenameCategory: rename %q -> %q: %w", src, dst, err)
	}

	if err := rewriteChildSidecars(dst, newName); err != nil {
		// A child sidecar failed to update — roll the whole folder back so the
		// no-net-change invariant holds.
		return rollbackRename(dst, src, err)
	}
	return nil
}

// rewriteChildSidecars walks the (already-renamed) category dir and rewrites
// each child series' tsundoku.json Category field to newName. A series dir with
// no sidecar (no rendered chapters yet) is skipped — there is nothing to rewrite.
// The first failure aborts and is returned so the caller can roll the rename back.
func rewriteChildSidecars(categoryDir, newName string) error {
	entries, err := os.ReadDir(categoryDir)
	if err != nil {
		// Defensive path: the dir was just successfully renamed into place;
		// reachable only on an OS-level I/O failure (permission denied / fd
		// exhausted) immediately after.
		return fmt.Errorf("disk.RenameCategory: read category dir %q: %w", categoryDir, err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Through the shared per-series lock: a concurrent cover GET can write the
		// same sidecar. A series dir with no sidecar is skipped by the helper.
		seriesDir := filepath.Join(categoryDir, e.Name())
		if err := updateExistingSidecar(seriesDir, func(s *Sidecar) { s.Category = newName }); err != nil {
			return fmt.Errorf("disk.RenameCategory: update sidecar %q: %w", seriesDir, err)
		}
	}
	return nil
}
