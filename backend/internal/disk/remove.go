package disk

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// RemoveSeriesDir deletes a series' library folder (<storage>/<category>/<title>)
// and everything under it — the owner-initiated "delete files" path. Returns
// removed=false (nil error) when the folder does not exist — a never-downloaded
// / zero-provider series legitimately has none, and callers must still complete
// the DB delete. removed=true means a real folder was deleted. A genuine
// os.RemoveAll failure (permissions, cross-device) or a path collision (a file
// sitting where the series directory should be) is surfaced as an error.
func RemoveSeriesDir(storage, category, title string) (removed bool, err error) {
	dir := SeriesDir(storage, category, title)
	info, statErr := os.Stat(dir)
	if errors.Is(statErr, fs.ErrNotExist) {
		return false, nil
	}
	if statErr != nil {
		return false, fmt.Errorf("disk.RemoveSeriesDir: stat %q: %w", dir, statErr)
	}
	if !info.IsDir() {
		// A path collision (a file where the series dir should be) — surface it.
		return false, fmt.Errorf("disk.RemoveSeriesDir: %q is not a directory", dir)
	}
	if err := os.RemoveAll(dir); err != nil {
		return false, fmt.Errorf("disk.RemoveSeriesDir: remove %q: %w", dir, err)
	}
	return true, nil
}
