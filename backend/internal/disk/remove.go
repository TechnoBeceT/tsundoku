package disk

import (
	"fmt"
	"os"
)

// RemoveSeriesDir deletes a series' library folder (<storage>/<category>/<title>)
// and everything under it — the owner-initiated "delete files" path. It is a
// no-op returning nil when the folder does not exist (a never-downloaded or
// zero-provider series has no folder), so callers need not pre-check existence.
func RemoveSeriesDir(storage, category, title string) error {
	dir := SeriesDir(storage, category, title)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("disk.RemoveSeriesDir: remove %q: %w", dir, err)
	}
	return nil
}
