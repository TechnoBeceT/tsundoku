package disk

import (
	"fmt"
	"os"
)

// WriteFileAtomic writes data to path atomically: the bytes go to a sibling
// temp file, are fsynced, then renamed over the final path. A failure never
// leaves a partial file at the final path (the caller's readers only ever see
// the previous complete file or the new complete one).
//
// It is the single atomic-write primitive: the small files this package owns
// (the tsundoku.json sidecar and the series cover image) plus the download
// engine's per-page staging writes (internal/sourceengine.Fetcher). The atomic
// rename is what makes a staged page crash-safe — a killed process never leaves a
// half-written page at its final path, so a present staged file is always a
// complete, previously-validated image safe to re-use on resume. The CBZ writer
// streams its archive and keeps its own temp/rename dance.
func WriteFileAtomic(path string, data []byte) error {
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
