package disk

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// MoveSeriesCategory relocates a series directory from oldCat to newCat and
// updates its tsundoku.json sidecar to match.
//
// Layout: the series lives at <storage>/<oldCat>/<title> and is moved to
// <storage>/<newCat>/<title>. The move is a single os.Rename, so it is atomic on
// the same filesystem. CBZ filenames do not encode the category, so the archives
// are carried over untouched; only the sidecar's Category field is rewritten.
//
// Ordering and failure modes (so DB↔disk never drift — the caller updates the DB
// only after this succeeds):
//   - oldCat == newCat → no-op, returns nil.
//   - the source dir must exist (missing → error, nothing changed).
//   - the target dir must NOT already exist (collision → error, NO move).
//   - the target's parent category dir is created with MkdirAll before the rename.
//   - a cross-device rename → a clear error with NO partial move; there is no
//     copy+delete fallback in M3 (out of scope) — the error is surfaced as-is.
//   - a post-rename sidecar failure (ReadSidecar/WriteSidecar) triggers a
//     best-effort rollback that renames the folder back to src, so a non-nil
//     error always means NO net move happened (folder is back at src) — the
//     caller can trust DB == old. If the rollback itself fails (an externally
//     moved/cross-device dst), both errors are surfaced via errors.Join.
//
// Parameters: storage is the library root; oldCat/newCat are category folder
// names (e.g. CategoryOther, CategoryManhwa); title is the series directory name
// (already sanitised by the caller, matching SeriesDir's contract).
func MoveSeriesCategory(storage, oldCat, newCat, title string) error {
	if oldCat == newCat {
		return nil
	}

	src := SeriesDir(storage, oldCat, title)
	dst := SeriesDir(storage, newCat, title)

	if err := requireSourceExists(src); err != nil {
		return err
	}
	if err := requireTargetAbsent(dst); err != nil {
		return err
	}

	// Ensure the target category dir exists so the rename has a home.
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (permission denied /
		// a regular file blocking the path — ENOTDIR).
		return fmt.Errorf("disk.MoveSeriesCategory: create target category dir: %w", err)
	}

	// Atomic on the same filesystem. A cross-device rename surfaces here with no
	// partial move; M3 deliberately does not fall back to copy+delete.
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("disk.MoveSeriesCategory: rename %q -> %q: %w", src, dst, err)
	}

	// Update the sidecar at its new home to reflect the new category. If any
	// post-rename step fails, the folder is already at dst — roll it back to src
	// so the no-net-move invariant holds: a non-nil error from this function means
	// the folder is back where it started and the caller can keep DB == old.
	sidecar, err := ReadSidecar(dst)
	if err != nil {
		return rollbackRename(dst, src, fmt.Errorf("disk.MoveSeriesCategory: read sidecar: %w", err))
	}
	if sidecar == nil {
		// No sidecar yet (e.g. a series dir with no rendered chapters). The folder
		// move already succeeded; there is nothing to rewrite.
		return nil
	}

	sidecar.Category = newCat
	if err := WriteSidecar(dst, *sidecar); err != nil {
		return rollbackRename(dst, src, fmt.Errorf("disk.MoveSeriesCategory: write sidecar: %w", err))
	}

	return nil
}

// rollbackRename undoes a successful os.Rename(src, dst) by renaming dst back to
// src after a post-rename step failed (cause). It returns cause on a clean
// rollback (the net effect is no move); if the rollback itself fails, both errors
// are surfaced via errors.Join so neither is swallowed (§16). The rollback is
// best-effort: a dst that has since moved across a device boundary or been moved
// out from under us cannot be renamed back — that genuinely-unrecoverable case is
// reported as the joined error, never hidden.
func rollbackRename(dst, src string, cause error) error {
	// Defensive path: os.Rename(dst, src) fails only if dst was externally
	// moved/removed or src's parent vanished between the original rename and now —
	// not reachable in normal single-process operation, but surfaced (joined) if it
	// ever happens so the caller learns the folder is stranded at dst.
	if rbErr := os.Rename(dst, src); rbErr != nil {
		return errors.Join(
			cause,
			fmt.Errorf("disk.MoveSeriesCategory: rollback rename %q -> %q failed (folder left at %q): %w", dst, src, dst, rbErr),
		)
	}
	return cause
}

// requireSourceExists confirms the source series dir is present. A missing dir is
// a clear, expected error; any other stat failure is a defensive OS-level path.
func requireSourceExists(src string) error {
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("disk.MoveSeriesCategory: source dir %q does not exist", src)
		}
		// Defensive path: reachable only on OS-level stat failure (permission denied /
		// fd exhausted) after the not-exist case is handled above.
		return fmt.Errorf("disk.MoveSeriesCategory: stat source: %w", err)
	}
	return nil
}

// requireTargetAbsent confirms the target series dir does NOT already exist, so a
// recategorize never overwrites another series. A genuine not-exist is the allowed
// case; an existing dir is a collision error.
func requireTargetAbsent(dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("disk.MoveSeriesCategory: target dir %q already exists", dst)
	} else if !os.IsNotExist(err) {
		// Defensive path: reachable only on OS-level stat failure (permission denied /
		// fd exhausted) — a genuine not-exist is the expected, allowed case.
		return fmt.Errorf("disk.MoveSeriesCategory: stat target: %w", err)
	}
	return nil
}
