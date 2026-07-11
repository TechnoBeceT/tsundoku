package disk

import (
	"fmt"
	"os"
)

// writeFileAtomic writes data to path atomically: the bytes go to a sibling
// temp file, are fsynced, then renamed over the final path. A failure never
// leaves a partial file at the final path (the caller's readers only ever see
// the previous complete file or the new complete one).
//
// It is the single atomic-write primitive for the small files this package owns
// (the tsundoku.json sidecar and the series cover image); the CBZ writer streams
// its archive and keeps its own temp/rename dance.
func writeFileAtomic(path string, data []byte) error {
	tmpPath := path + ".tmp"

	// G304: path is built from the storage root + sanitised series folder names —
	// not a path-traversal concern.
	//nolint:gosec
	f, err := os.Create(tmpPath)
	if err != nil {
		// Defensive path: reachable only on OS-level I/O failure (fd exhausted / permission denied).
		return fmt.Errorf("create temp file: %w", err)
	}

	if _, err := f.Write(data); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (disk full / fd exhausted).
		_ = f.Close()
		removeTmp(tmpPath)
		return fmt.Errorf("write: %w", err)
	}

	if err := f.Sync(); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (disk full / corrupt FS).
		_ = f.Close()
		removeTmp(tmpPath)
		return fmt.Errorf("fsync: %w", err)
	}

	if err := f.Close(); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (fd exhausted / corrupt FS).
		removeTmp(tmpPath)
		return fmt.Errorf("close file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (cross-device rename / permission).
		removeTmp(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}

	return nil
}
