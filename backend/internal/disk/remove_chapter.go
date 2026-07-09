package disk

import (
	"fmt"
	"os"
	"path/filepath"
)

// RemoveChapterFile removes ONE chapter's CBZ by exact filename from the series
// directory. A missing file (or missing series dir) is a no-op returning
// (false, nil); a real removal returns (true, nil); only a genuine filesystem
// error is returned. Used to delete a superseded split-part's file when its whole
// chapter is downloaded (fractional-part suppression); reversible — the Chapter
// row survives in the `superseded` state.
func RemoveChapterFile(storage, category, title, filename string) (removed bool, err error) {
	if filename == "" {
		return false, nil
	}
	path := filepath.Join(SeriesDir(storage, category, title), filename)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("disk.RemoveChapterFile: remove %q: %w", path, err)
	}
	return true, nil
}
