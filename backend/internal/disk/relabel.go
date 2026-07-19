package disk

import (
	"fmt"
	"os"
	"path/filepath"
)

// RelabelChapterFile re-attributes an already-rendered chapter CBZ to a NEW
// provenance identity (provider/scanlator/importance carried by meta) WITHOUT
// touching its page images or page count.
//
// Used by the Match workflow (library.MatchDiskProvider) to attach an
// already-downloaded, disk-imported chapter to a newly-linked real Suwayomi
// source: the archive's bytes never change, only its filename, its embedded
// ComicInfo.xml, and the series' tsundoku.json sidecar entry are updated to
// the source's clean identity (owner-picked) — so the chapter never needs to
// be re-fetched.
//
// oldFilename is the CURRENT basename inside
// <storage>/<meta.Category>/<meta.SeriesTitle>/. The new filename is derived
// from meta via GenerateCBZFilename; when it equals oldFilename only the
// ComicInfo + sidecar are rewritten (no rename). The page count carried into
// the new ComicInfo + sidecar entry is preserved from the file's EXISTING
// ComicInfo (a missing/unreadable one is not fatal — the operation proceeds
// with page count 0, a defensive corner not expected for a chapter that was
// actually downloaded).
//
// Returns the (possibly-unchanged) new filename and the ComicInfo the file
// carried BEFORE this call (oldCI) — the caller retains oldCI (together with
// oldFilename) to build a rollback via UndoRelabelChapterFile if a later step
// in a multi-chapter batch fails. On any error, the file and sidecar are left
// exactly as they were: no partial rename, no partial ComicInfo, no partial
// sidecar write (each underlying step is itself atomic or a plain no-op on
// failure).
//
// IDEMPOTENT / RETRY-SAFE (GAP-096): if the rename target already exists and the
// source is already gone — the file is ALREADY at its destination from a prior
// interrupted run of this same relabel — the rename is SKIPPED (not an
// ENOENT-500) and the ComicInfo/sidecar are (re)written to the new identity. This
// lets a retry of a partially-completed Match/Dedup merge succeed instead of
// hard-failing on "rename OLD -> NEW: no such file or directory", and it recovers
// a series already stranded by a mid-op disconnect. When the file is already at
// the destination oldCI is read from THERE (its page count is preserved even if
// the prior run had also rewritten its ComicInfo). A rename that fails while
// BOTH paths are missing (a genuinely vanished file) still errors.
func RelabelChapterFile(storage string, meta RenderMeta, oldFilename string) (newFilename string, oldCI ComicInfo, err error) {
	seriesDir := SeriesDir(storage, meta.Category, meta.SeriesTitle)
	oldPath := filepath.Join(seriesDir, oldFilename)

	newFilename = GenerateCBZFilename(meta)
	newPath := filepath.Join(seriesDir, newFilename)

	oldCI, didRename, err := relabelMoveIntoPlace(oldPath, newPath, newFilename != oldFilename)
	if err != nil {
		return "", ComicInfo{}, err
	}

	// undoRename reverses ONLY a rename this call itself performed — never one an
	// earlier interrupted run left behind (didRename is false in that case).
	undoRename := func() {
		if didRename {
			_ = os.Rename(newPath, oldPath)
		}
	}

	ci := newComicInfo(meta, oldCI.PageCount)
	if uErr := UpdateCBZComicInfo(newPath, ci); uErr != nil {
		undoRename()
		return "", ComicInfo{}, fmt.Errorf("disk.RelabelChapterFile: %w", uErr)
	}

	if sErr := upsertSidecar(seriesDir, meta, newFilename, oldCI.PageCount); sErr != nil {
		// WriteSidecar (called by upsertSidecar) is itself atomic — a failure here
		// means the sidecar was NOT modified, so only the CBZ side needs undoing.
		_ = UpdateCBZComicInfo(newPath, oldCI)
		undoRename()
		return "", ComicInfo{}, fmt.Errorf("disk.RelabelChapterFile: update sidecar: %w", sErr)
	}

	return newFilename, oldCI, nil
}

// relabelMoveIntoPlace reads the archive's pre-existing ComicInfo and, when a
// rename is needed, moves oldPath -> newPath, reporting whether THIS call
// performed the rename. It is idempotent (GAP-096): if renameNeeded but the
// archive is ALREADY at newPath (gone from oldPath) because a prior interrupted
// run moved it, it skips the rename and returns didRename=false, reading oldCI
// from newPath so the page count survives the retry. A rename with BOTH paths
// missing (a genuinely vanished archive) still errors.
func relabelMoveIntoPlace(oldPath, newPath string, renameNeeded bool) (oldCI ComicInfo, didRename bool, err error) {
	alreadyAtDest := renameNeeded && !pathExists(oldPath) && pathExists(newPath)
	didRename = renameNeeded && !alreadyAtDest

	readPath := oldPath
	if alreadyAtDest {
		readPath = newPath
	}
	if existing, rErr := ReadComicInfoFromCBZ(readPath); rErr == nil && existing != nil {
		oldCI = *existing
	}

	if didRename {
		if rErr := os.Rename(oldPath, newPath); rErr != nil {
			return ComicInfo{}, false, fmt.Errorf("disk.RelabelChapterFile: rename %q -> %q: %w", oldPath, newPath, rErr)
		}
	}
	return oldCI, didRename, nil
}

// pathExists reports whether p is a name that currently exists on disk. Used by
// relabelMoveIntoPlace to detect an already-relabeled (idempotent-retry) archive.
func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// UndoRelabelChapterFile reverses one successful RelabelChapterFile call,
// restoring the chapter to its ORIGINAL identity: it renames the file back to
// originalFilename (if RelabelChapterFile renamed it), restores oldCI as the
// embedded ComicInfo.xml, and rewrites the sidecar entry back to oldMeta's
// provenance.
//
// oldMeta is the RenderMeta describing the chapter's identity BEFORE the
// relabel (e.g. the disk-origin provider being matched away from); oldCI is
// the ComicInfo RelabelChapterFile returned for this same call.
//
// Used by library.MatchDiskProvider to unwind a partially-completed batch of
// relabels when a later chapter's relabel fails, so the whole Match operation
// leaves no net change on disk when it returns an error.
func UndoRelabelChapterFile(storage string, oldMeta RenderMeta, currentFilename, originalFilename string, oldCI ComicInfo) error {
	seriesDir := SeriesDir(storage, oldMeta.Category, oldMeta.SeriesTitle)
	currentPath := filepath.Join(seriesDir, currentFilename)
	originalPath := filepath.Join(seriesDir, originalFilename)

	if currentFilename != originalFilename {
		if err := os.Rename(currentPath, originalPath); err != nil {
			return fmt.Errorf("disk.UndoRelabelChapterFile: rename %q -> %q: %w", currentPath, originalPath, err)
		}
	}

	if err := UpdateCBZComicInfo(originalPath, oldCI); err != nil {
		return fmt.Errorf("disk.UndoRelabelChapterFile: restore ComicInfo: %w", err)
	}

	if err := upsertSidecar(seriesDir, oldMeta, originalFilename, oldCI.PageCount); err != nil {
		return fmt.Errorf("disk.UndoRelabelChapterFile: restore sidecar: %w", err)
	}

	return nil
}
