package disk

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/technobecet/tsundoku/internal/chapter"
)

// knownCategories is the ordered set of top-level category directory names
// recognised by ScanLibrary.
var knownCategories = []string{
	CategoryManga,
	CategoryManhwa,
	CategoryManhua,
	CategoryComic,
	CategoryOther,
}

// SeriesFacts holds the on-disk facts for one series directory as reported
// by ScanLibrary. It is the input to Reconcile.
type SeriesFacts struct {
	// Title is the series display title from the sidecar (or the directory name
	// for uncategorized / sidecar-less series).
	Title string

	// Category is the library category (Manga, Manhwa, …) or "" if the series
	// directory sits directly under the storage root.
	Category string

	// Chapters is the ordered list of chapter facts for this series.
	Chapters []ChapterFact
}

// ChapterFact holds the per-chapter facts reconstructed from disk.
// Fields are sourced from tsundoku.json (primary) or ComicInfo.xml (orphan fallback).
type ChapterFact struct {
	// Key is the normalised chapter_key.
	Key string

	// Number is the chapter display/sort number; nil for un-numbered chapters.
	Number *float64

	// Provider is the source provider name.
	Provider string

	// Scanlator is the scanlation group name.
	Scanlator string

	// Importance is the provider importance rank.
	Importance int

	// Filename is the on-disk CBZ filename (basename only).
	Filename string

	// PageCount is the number of pages in the CBZ.
	PageCount int

	// FileExists reports whether the CBZ file is present on disk.
	// A sidecar entry whose CBZ has been deleted gets FileExists=false.
	FileExists bool
}

// seriesCandidate pairs a category name with the absolute path of a series directory.
type seriesCandidate struct {
	category  string
	seriesDir string
}

// ScanLibrary walks the storage root and returns one SeriesFacts per series
// directory found.
//
// Layout expected: <storage>/<Category>/<SeriesTitle>/ where Category ∈
// {Manga, Manhwa, Manhua, Comic, Other}. Series directories found directly
// under storage (uncategorized) are also included.
//
// For each series directory:
//   - If tsundoku.json exists it is the primary source: ChapterFacts are built
//     from its ChapterProvenance entries. FileExists is verified by stat.
//   - Each .cbz in the directory not covered by a sidecar entry (orphan) is
//     processed via ReadComicInfoFromCBZ; the chapter_key is taken from the
//     ComicInfo ChapterKey field, or recomputed via NormalizeChapterKey when
//     absent.
func ScanLibrary(storage string) ([]SeriesFacts, error) {
	candidates, err := collectCandidates(storage)
	if err != nil {
		return nil, err
	}

	var results []SeriesFacts
	for _, c := range candidates {
		sf, err := scanSeriesDir(c.seriesDir, c.category)
		if err != nil {
			return nil, err
		}
		if sf != nil {
			results = append(results, *sf)
		}
	}

	return results, nil
}

// collectCandidates enumerates all series directories under storage.
// It walks each known category subdirectory, then adds any non-category
// directory directly under storage as an uncategorized candidate.
func collectCandidates(storage string) ([]seriesCandidate, error) {
	var candidates []seriesCandidate

	catCandidates, err := categoryCandidates(storage)
	if err != nil {
		return nil, err
	}
	candidates = append(candidates, catCandidates...)

	uncatCandidates, err := uncategorizedCandidates(storage)
	if err != nil {
		return nil, err
	}
	candidates = append(candidates, uncatCandidates...)

	return candidates, nil
}

// categoryCandidates returns candidates from the known category subdirectories.
func categoryCandidates(storage string) ([]seriesCandidate, error) {
	var candidates []seriesCandidate
	for _, cat := range knownCategories {
		catDir := filepath.Join(storage, cat)
		entries, err := os.ReadDir(catDir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			// Defensive path: reachable only on OS-level I/O failure (permission denied /
			// fd exhausted) after ErrNotExist is already handled above.
			return nil, fmt.Errorf("disk.ScanLibrary: read category dir %q: %w", catDir, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				candidates = append(candidates, seriesCandidate{
					category:  cat,
					seriesDir: filepath.Join(catDir, e.Name()),
				})
			}
		}
	}
	return candidates, nil
}

// uncategorizedCandidates returns candidates for directories directly under
// storage that are not known category directories.
func uncategorizedCandidates(storage string) ([]seriesCandidate, error) {
	storageEntries, err := os.ReadDir(storage)
	if err != nil && !os.IsNotExist(err) {
		// Defensive path: reachable only on OS-level I/O failure (permission denied /
		// fd exhausted) when the storage root itself cannot be listed.
		return nil, fmt.Errorf("disk.ScanLibrary: read storage dir: %w", err)
	}

	catSet := make(map[string]struct{}, len(knownCategories))
	for _, c := range knownCategories {
		catSet[c] = struct{}{}
	}

	var candidates []seriesCandidate
	for _, e := range storageEntries {
		if !e.IsDir() {
			continue
		}
		if _, isKnownCat := catSet[e.Name()]; isKnownCat {
			continue
		}
		candidates = append(candidates, seriesCandidate{
			category:  "",
			seriesDir: filepath.Join(storage, e.Name()),
		})
	}
	return candidates, nil
}

// scanSeriesDir scans a single series directory and returns SeriesFacts.
// Returns nil when the directory contains no relevant content.
func scanSeriesDir(dir, category string) (*SeriesFacts, error) {
	sidecar, err := ReadSidecar(dir)
	if err != nil {
		return nil, fmt.Errorf("disk.ScanLibrary: read sidecar %q: %w", dir, err)
	}

	facts, sidecarCovered, err := factsFromSidecar(dir, sidecar)
	if err != nil {
		return nil, err
	}

	orphanFacts, err := orphanChapterFacts(dir, sidecarCovered)
	if err != nil {
		return nil, err
	}
	facts = append(facts, orphanFacts...)

	if len(facts) == 0 && sidecar == nil {
		return nil, nil
	}

	return buildSeriesFacts(dir, category, sidecar, facts), nil
}

// factsFromSidecar builds ChapterFact entries from the sidecar's provenance
// records and returns a set of filenames covered by those entries.
func factsFromSidecar(dir string, sidecar *Sidecar) ([]ChapterFact, map[string]struct{}, error) {
	covered := make(map[string]struct{})
	if sidecar == nil {
		return nil, covered, nil
	}

	facts := make([]ChapterFact, 0, len(sidecar.Chapters))
	for _, cp := range sidecar.Chapters {
		_, statErr := os.Stat(filepath.Join(dir, cp.Filename))
		facts = append(facts, ChapterFact{
			Key:        cp.ChapterKey,
			Number:     cp.Number,
			Provider:   cp.Provider,
			Scanlator:  cp.Scanlator,
			Importance: cp.Importance,
			Filename:   cp.Filename,
			PageCount:  cp.PageCount,
			FileExists: statErr == nil,
		})
		covered[cp.Filename] = struct{}{}
	}
	return facts, covered, nil
}

// orphanChapterFacts scans dir for .cbz files not in sidecarCovered and
// builds ChapterFact entries from their embedded ComicInfo.xml provenance.
func orphanChapterFacts(dir string, sidecarCovered map[string]struct{}) ([]ChapterFact, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Defensive path: reachable only on OS-level I/O failure (permission denied /
		// fd exhausted) after the series directory was already successfully entered.
		return nil, fmt.Errorf("disk.ScanLibrary: read series dir %q: %w", dir, err)
	}

	var facts []ChapterFact
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".cbz") {
			continue
		}
		if _, covered := sidecarCovered[e.Name()]; covered {
			continue
		}
		cf, err := chapterFactFromOrphanCBZ(filepath.Join(dir, e.Name()), e.Name())
		if err != nil {
			return nil, err
		}
		if cf != nil {
			facts = append(facts, *cf)
		}
	}
	return facts, nil
}

// buildSeriesFacts assembles the final SeriesFacts struct from a directory's
// metadata, deriving the title and category from the sidecar when available.
func buildSeriesFacts(dir, category string, sidecar *Sidecar, facts []ChapterFact) *SeriesFacts {
	title := filepath.Base(dir)
	cat := category
	if sidecar != nil {
		if sidecar.Title != "" {
			title = sidecar.Title
		}
		if sidecar.Category != "" && cat == "" {
			cat = sidecar.Category
		}
	}
	return &SeriesFacts{Title: title, Category: cat, Chapters: facts}
}

// chapterFactFromOrphanCBZ reads provenance from the ComicInfo.xml inside a
// CBZ that is not covered by the series sidecar.
//
// Returns nil (no error) when the CBZ has no ComicInfo.xml — the file is
// treated as non-provenance content and skipped.
func chapterFactFromOrphanCBZ(cbzPath, filename string) (*ChapterFact, error) {
	ci, err := ReadComicInfoFromCBZ(cbzPath)
	if err != nil {
		return nil, fmt.Errorf("disk.ScanLibrary: read ComicInfo from orphan %q: %w", filename, err)
	}
	if ci == nil {
		// A CBZ without ComicInfo.xml has no provenance — skip it silently.
		// This path is reachable when third-party tools drop archives into the
		// library directory without embedding a ComicInfo.
		return nil, nil
	}

	key, num := provenanceKeyAndNumber(ci)
	return &ChapterFact{
		Key:        key,
		Number:     num,
		Provider:   ci.Provider,
		Scanlator:  ci.Scanlator,
		Importance: ci.Importance,
		Filename:   filename,
		PageCount:  ci.PageCount,
		FileExists: true, // we just successfully opened it
	}, nil
}

// provenanceKeyAndNumber extracts the chapter_key and number from a ComicInfo.
// When ChapterKey is absent in the ComicInfo it is recomputed via
// NormalizeChapterKey — the same normaliser used by the renderer, so there is
// no second source of truth.
func provenanceKeyAndNumber(ci *ComicInfo) (string, *float64) {
	var num *float64
	if ci.Number != "" {
		if n, err := parseNumber(ci.Number); err == nil {
			num = &n
		}
	}
	key := ci.ChapterKey
	if key == "" {
		key = chapter.NormalizeChapterKey(num, ci.Title)
	}
	return key, num
}

// parseNumber parses a chapter number string to float64.
// Returns an error when the string is empty or not a valid decimal number.
func parseNumber(s string) (float64, error) {
	if s == "" {
		return 0, fmt.Errorf("empty number string")
	}
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err
}
