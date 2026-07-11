package disk

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/technobecet/tsundoku/internal/fetcher"
)

// sidecarLocks serialises the per-series tsundoku.json read-modify-write. Now
// that a source's chapters download in parallel (the download dispatcher runs up
// to DownloadConcurrency chapters of the SAME series at once), two RenderChapter
// calls can update one series' sidecar concurrently. Without serialisation they
// race on the shared JSON — losing a provenance entry (last-writer-wins on the
// read-modify-write) and colliding on the fixed "tsundoku.json.tmp" path, which
// intermittently fails the render. The lock is keyed by series directory, so
// different series never contend.
var sidecarLocks sync.Map // seriesDir(string) -> *sync.Mutex

// lockSidecar acquires the per-series sidecar mutex and returns its unlock func.
func lockSidecar(seriesDir string) (unlock func()) {
	m, _ := sidecarLocks.LoadOrStore(seriesDir, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// RenderRequest carries everything needed to render one chapter to disk.
type RenderRequest struct {
	// Storage is the root library directory (e.g. "/data/library").
	Storage string

	// Meta holds all chapter and series metadata required for filename
	// generation, ComicInfo construction, and sidecar provenance.
	Meta RenderMeta

	// Pages is the ordered slice of decoded page images to store in the CBZ.
	Pages []fetcher.PageImage
}

// RenderChapter renders a chapter to the categorized library layout and
// updates the per-series tsundoku.json sidecar.
//
// It:
//  1. Generates the CBZ filename from Meta via GenerateCBZFilename.
//  2. Builds the ComicInfo with provenance via newComicInfo.
//  3. Writes the CBZ atomically (temp → fsync → rename) at
//     <Storage>/<Category>/<SeriesTitle>/<filename>.cbz.
//  4. Reads the existing tsundoku.json (if any), upserts the chapter's
//     provenance entry, and writes the sidecar atomically.
//
// Returns the on-disk filename (basename only) so Task 5 can store it on the
// Chapter row. The filename is guaranteed to be non-empty on success.
//
// On any error, no partial CBZ is left at the final path and the sidecar is
// not modified.
func RenderChapter(req RenderRequest) (filename string, err error) {
	filename = GenerateCBZFilename(req.Meta)
	seriesDir := SeriesDir(req.Storage, req.Meta.Category, req.Meta.SeriesTitle)
	cbzPath := filepath.Join(seriesDir, filename)

	// Ensure the series directory exists.
	if mkErr := os.MkdirAll(seriesDir, 0o750); mkErr != nil {
		return "", fmt.Errorf("disk.RenderChapter: create series dir: %w", mkErr)
	}

	// Build ComicInfo with provenance.
	ci := newComicInfo(req.Meta, len(req.Pages))

	// Write CBZ atomically.
	if err := CreateCBZ(cbzPath, req.Pages, ci); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (disk full / fd exhausted /
		// permission denied) after MkdirAll already succeeded.
		return "", fmt.Errorf("disk.RenderChapter: %w", err)
	}

	// Update sidecar.
	if err := upsertSidecar(seriesDir, req.Meta, filename, len(req.Pages)); err != nil {
		// The CBZ was written successfully; sidecar failure is still an error
		// but does not leave a partial CBZ.
		return "", fmt.Errorf("disk.RenderChapter: update sidecar: %w", err)
	}

	return filename, nil
}

// upsertSidecar reads the existing tsundoku.json (if any), upserts the given
// chapter provenance entry (matching by ChapterKey), and writes the result
// atomically. It is the sole place responsible for sidecar mutation.
func upsertSidecar(seriesDir string, m RenderMeta, filename string, pageCount int) error {
	// Serialise the whole read-modify-write for this series so concurrent renders
	// of sibling chapters (and a concurrent SaveCover) never lose an entry or
	// collide on the shared temp file.
	defer lockSidecar(seriesDir)()

	def := Sidecar{Title: m.SeriesTitle, Category: m.Category}

	return mutateSidecar(seriesDir, def, func(sidecar *Sidecar) {
		prov := ChapterProvenance{
			ChapterKey: m.ChapterKey,
			Number:     m.Number,
			Provider:   m.Provider,
			Scanlator:  m.Scanlator,
			Importance: m.Importance,
			Filename:   filename,
			PageCount:  pageCount,
			UploadDate: m.UploadDate,
		}

		// Upsert: replace existing entry with the same ChapterKey, or append.
		updated := false
		for i, ch := range sidecar.Chapters {
			if ch.ChapterKey == m.ChapterKey {
				sidecar.Chapters[i] = prov
				updated = true
				break
			}
		}
		if !updated {
			sidecar.Chapters = append(sidecar.Chapters, prov)
		}

		sidecar.ProviderOrder = buildProviderOrder(sidecar.Chapters)
	})
}

// buildProviderOrder builds a unique, importance-ordered list of provider names
// from all chapter provenance records.
//
// Sorted by Importance DESC — in Tsundoku a HIGHER importance number means
// HIGHER priority/quality (opposite of legacy Kaizoku.GO, where lower was better).
// Index 0 is the highest-priority provider. Ties broken by provider name ASC.
// Deduplication keeps the first occurrence (highest importance) of each provider name.
func buildProviderOrder(chapters []ChapterProvenance) []string {
	type pair struct {
		provider   string
		importance int
	}
	pairs := make([]pair, len(chapters))
	for i, ch := range chapters {
		pairs[i] = pair{provider: ch.Provider, importance: ch.Importance}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].importance != pairs[j].importance {
			return pairs[i].importance > pairs[j].importance
		}
		return pairs[i].provider < pairs[j].provider
	})
	seen := make(map[string]struct{}, len(pairs))
	order := make([]string, 0, len(pairs))
	for _, p := range pairs {
		if _, ok := seen[p.provider]; !ok {
			seen[p.provider] = struct{}{}
			order = append(order, p.provider)
		}
	}
	return order
}
