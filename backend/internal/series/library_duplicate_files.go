package series

import (
	"cmp"
	"context"
	"log/slog"
	"os"
	"slices"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
)

// SeriesDuplicateFilesDTO is one row of the library-wide Duplicates page: a series
// whose folder holds CBZs the per-series "Remove duplicate files" action would
// delete, with how many files that is and how much disk they hold.
//
// FileCount and ReclaimableBytes cover the FILE-ONLY removals and nothing else —
// leftover copies of a chapter that already has a winning file, and orphaned CBZs
// of a superseded fractional part. The row-deleting passes of the per-series
// dedupe (the engine-switch merge and the ignored-fractional cleanup) are
// deliberately EXCLUDED: this page exists to answer "where is disk being wasted",
// and a row-deleting action is not something to advertise from a list view.
type SeriesDuplicateFilesDTO struct {
	SeriesID         string `json:"seriesId"`
	Title            string `json:"title"`
	DisplayName      string `json:"displayName"`
	Category         string `json:"category"`
	CoverURL         string `json:"coverUrl"`
	FileCount        int    `json:"fileCount"`
	ReclaimableBytes int64  `json:"reclaimableBytes"`
}

// LibraryDuplicateFilesDTO is the library-wide Duplicates page envelope: every
// series with at least one removable duplicate CBZ, sorted most-actionable first,
// plus the library totals the page header shows. Series is always non-nil so the
// JSON renders [] not null (mirrors LibrarySourcelessDTO).
type LibraryDuplicateFilesDTO struct {
	Series     []SeriesDuplicateFilesDTO `json:"series"`
	TotalFiles int                       `json:"totalFiles"`
	TotalBytes int64                     `json:"totalBytes"`
}

// seriesDirReader lists one series' library folder. It is a parameter of
// duplicateFilesRollup rather than a hard call so the "one directory read per
// series" claim is PINNED by a counting test instead of asserted — the filesystem
// counterpart of the counting Ent driver that pins the SQL side.
type seriesDirReader func(storage, category, title string) ([]os.DirEntry, error)

// LibraryDuplicateFiles lists every series carrying removable duplicate CBZs, so
// the owner can see WHICH series need the per-series "Remove duplicate files"
// action instead of opening them one by one. It is READ-ONLY: it deletes nothing,
// writes nothing, and there is no execute counterpart — each row links to its
// series, where the existing owner-triggered button lives.
//
// A series is listed only when the removals are REAL under the strict full-token
// number rule (disk.strictChapterKey). Files whose final token is not a plain
// number — "… (Chapter 0 - Prologue).cbz" — are refused by that rule, so a series
// carrying only such files is correctly ABSENT: the page shows what is actually
// removable, not everything that looks odd on disk.
//
// COST: one bounded query set (loadAllSeriesForCleanup) + ONE directory read per
// series + one stat per PLANNED file. The reads are the load-bearing part — the
// per-series plan reads the folder once per CHAPTER, which is fine for one series
// and unusable across a whole library, so this path reads each folder once and
// matches every chapter against the retained listing. Pinned by
// TestDuplicateFilesRollupReadsEachSeriesDirectoryOnce.
func (s *Service) LibraryDuplicateFiles(ctx context.Context) (LibraryDuplicateFilesDTO, error) {
	rows, err := s.loadAllSeriesForCleanup(ctx)
	if err != nil {
		return LibraryDuplicateFilesDTO{}, err
	}
	return s.duplicateFilesRollup(rows, disk.ReadSeriesDir)
}

// duplicateFilesRollup builds the page from already-loaded series rows, reading
// each series folder EXACTLY ONCE through readDir and resolving that series' whole
// removal plan against the retained listing (resolveDedupePlan — the same passes
// the per-series preview runs, so the two can never disagree about a series).
//
// A genuine directory-read failure fails the whole page rather than silently
// under-reporting: a page that quietly omits a series the owner is hunting for is
// worse than an error they can retry. A MISSING folder is not a failure — a
// never-rendered series simply has nothing to clean.
func (s *Service) duplicateFilesRollup(rows []*ent.Series, readDir seriesDirReader) (LibraryDuplicateFilesDTO, error) {
	out := LibraryDuplicateFilesDTO{Series: []SeriesDuplicateFilesDTO{}}
	for _, row := range rows {
		entries, err := readDir(s.storage, category.NameOf(row), row.Title)
		if err != nil {
			return LibraryDuplicateFilesDTO{}, err
		}
		if len(entries) == 0 {
			continue
		}

		plan, err := s.resolveDedupePlan(row, entriesDuplicateLister(entries))
		if err != nil {
			return LibraryDuplicateFilesDTO{}, err
		}
		if len(plan.fileOnly) == 0 {
			continue // list criterion: only series with something removable
		}

		bytes := reclaimableBytes(entries, plan.fileOnly)
		name, coverURL := SeriesDisplay(row, MetadataProvider(row))
		out.Series = append(out.Series, SeriesDuplicateFilesDTO{
			SeriesID:         row.ID.String(),
			Title:            row.Title,
			DisplayName:      name,
			Category:         category.NameOf(row),
			CoverURL:         coverURL,
			FileCount:        len(plan.fileOnly),
			ReclaimableBytes: bytes,
		})
		out.TotalFiles += len(plan.fileOnly)
		out.TotalBytes += bytes
	}
	sortLibraryDuplicateFiles(out.Series)
	return out, nil
}

// reclaimableBytes sums the on-disk size of the PLANNED removals only — one stat
// per file the page offers to reclaim, never a walk of the library. The sizes come
// from the directory listing this series was already planned against, so a file
// that vanished between the read and the stat contributes 0 bytes and is logged;
// it is still counted, because the plan listed it and the count is what the
// per-series action would act on.
func reclaimableBytes(entries []os.DirEntry, items []dedupeFileItem) int64 {
	byName := make(map[string]os.DirEntry, len(entries))
	for _, e := range entries {
		byName[e.Name()] = e
	}

	var total int64
	for _, it := range items {
		entry, ok := byName[it.filename]
		if !ok {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			slog.Warn("series.LibraryDuplicateFiles: could not size a planned duplicate — counted as 0 bytes",
				"filename", it.filename, "err", err)
			continue
		}
		total += info.Size()
	}
	return total
}

// sortLibraryDuplicateFiles orders the page most-actionable first: the most files
// to remove on top, then the biggest reclaim, then title A→Z as a stable,
// deterministic tiebreak.
func sortLibraryDuplicateFiles(rows []SeriesDuplicateFilesDTO) {
	slices.SortStableFunc(rows, func(a, b SeriesDuplicateFilesDTO) int {
		if d := cmp.Compare(b.FileCount, a.FileCount); d != 0 {
			return d
		}
		if d := cmp.Compare(b.ReclaimableBytes, a.ReclaimableBytes); d != 0 {
			return d
		}
		return cmp.Compare(a.Title, b.Title)
	})
}
