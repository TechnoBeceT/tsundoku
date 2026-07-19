// Package sourceengine — on-disk page-staging for the download Fetcher.
//
// A chapter's pages are streamed to a per-chapter staging directory
// (<stagingRoot>/<providerChapterID>/) as they download, one file per page named
// "<zero-padded-index>.<ext>". This is the anti-Suwayomi byte cache: disk-backed
// (not RAM), bounded to in-progress chapters, and actively cleaned by the caller
// once the CBZ is assembled. A retry re-uses the files already present and
// re-fetches only the missing pages (true partial resume); disk.WriteFileAtomic's
// temp→fsync→rename makes a killed process safe (a present staged file is always a
// complete, previously-validated image).
package sourceengine

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/fetcher"
)

// stagedPage records one page file already present in a staging dir: its base
// name (for reading) and the file extension (for the resulting PageImage.Ext).
type stagedPage struct {
	name string
	ext  string
}

// stagingDirFor returns the absolute staging directory for ref's chapter, one dir
// per ProviderChapter under the fetcher's staging root.
func (f *Fetcher) stagingDirFor(ref fetcher.FetchRef) string {
	return filepath.Join(f.stagingRoot, ref.ProviderChapterID.String())
}

// scanStagingDir reads dir and returns a map from page index to the staged file
// present for it. A non-existent dir is not an error — it yields an empty map (a
// first attempt). Entries that are not a "<index>.<ext>" page file (directories,
// half-written "*.tmp" temp files from an interrupted atomic write) are skipped,
// so only complete, resumable pages are reported.
func scanStagingDir(dir string) (map[int]stagedPage, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[int]stagedPage{}, nil
		}
		return nil, fmt.Errorf("sourceengine fetcher: read staging dir: %w", err)
	}
	staged := make(map[int]stagedPage, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// Split on the LAST dot: a "0003.jpg.tmp" temp file then parses its base as
		// "0003.jpg" (not an integer) and is skipped, so a partial write is never
		// mistaken for a complete staged page.
		dot := strings.LastIndex(name, ".")
		if dot <= 0 {
			continue
		}
		idx, convErr := strconv.Atoi(name[:dot])
		if convErr != nil {
			continue
		}
		staged[idx] = stagedPage{name: name, ext: name[dot+1:]}
	}
	return staged, nil
}

// stageWrite atomically writes one page's bytes to dir as
// "<zero-padded-index>.<ext>", creating dir if needed. The atomic temp→rename is
// what makes the file crash-safe to re-use on a later resume.
func stageWrite(dir string, index int, ext string, data []byte) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("sourceengine fetcher: create staging dir: %w", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("%04d.%s", index, ext))
	if err := disk.WriteFileAtomic(path, data); err != nil {
		return fmt.Errorf("sourceengine fetcher: stage page %d: %w", index, err)
	}
	return nil
}

// collectStagedPages reads every staged page back into an ordered PageImage slice
// for the CBZ render. It is called only after stagePages has confirmed all pages
// are present, so a missing index is a defensive error (a page vanished from the
// staging dir between staging and collection).
func collectStagedPages(links []fetcher.PageLink, dir string) ([]fetcher.PageImage, error) {
	staged, err := scanStagingDir(dir)
	if err != nil {
		return nil, err
	}
	images := make([]fetcher.PageImage, len(links))
	for i := range links {
		sp, ok := staged[i]
		if !ok {
			return nil, fmt.Errorf("sourceengine fetcher: staged page %d missing from %s", i, dir)
		}
		// G304: path is the staging root + a controlled "<index>.<ext>" file name.
		//nolint:gosec
		data, readErr := os.ReadFile(filepath.Join(dir, sp.name))
		if readErr != nil {
			return nil, fmt.Errorf("sourceengine fetcher: read staged page %d: %w", i, readErr)
		}
		images[i] = fetcher.PageImage{Data: data, Ext: sp.ext}
	}
	return images, nil
}
