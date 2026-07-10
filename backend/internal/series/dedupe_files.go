package series

import (
	"context"
	"fmt"
	"math"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
)

// DedupeFiles is the owner-triggered duplicate-CBZ sweep for a whole series. It
// runs two passes and returns the total number of files removed:
//
//  1. For every downloaded chapter (one that has a winning filename AND a
//     number) it calls disk.RemoveOtherChapterFiles to remove any OTHER CBZ in
//     the series folder that shares that chapter's number, keeping the
//     chapter's own winning filename.
//  2. For every SUPERSEDED fractional-part chapter (fractional-part
//     suppression clears its DB filename and best-effort removes its CBZ; if
//     that removal ever failed the CBZ is orphaned on disk and invisible to
//     pass 1, which only matches chapters WITH a winning filename — no
//     downloaded chapter carries a part's fractional number) it removes EVERY
//     .cbz matching that fractional number, with NO keeper: a superseded part
//     is redundant (the whole covers it), so there is no file to keep for that
//     number. Superseded chapters with a WHOLE-integer number are skipped
//     (defensive — only fractional parts are ever superseded, but a whole
//     number's file is a legitimate keeper elsewhere, never orphaned).
//
// This is the bulk counterpart to the automatic per-convergence cleanup in the
// upgrade path: it reconciles a library that accumulated duplicate CBZs before
// the convergence engine existed (e.g. an imported Kaizoku library, or chapters
// downloaded before this feature). It NEVER deletes a chapter's winning file, and
// it performs NO DB writes — only orphan/duplicate on-disk files are removed.
// A missing series folder yields 0 (nothing to sweep). An unknown id returns
// ErrSeriesNotFound (mapped to 404 by the handler).
func (s *Service) DedupeFiles(ctx context.Context, id uuid.UUID) (int, error) {
	ser, err := s.client.Series.Query().
		Where(entseries.IDEQ(id)).
		WithCategory().
		WithChapters().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return 0, ErrSeriesNotFound
		}
		return 0, fmt.Errorf("series.DedupeFiles: load series %s: %w", id, err)
	}

	categoryName := category.NameOf(ser)

	total, err := dedupeWinningChapters(s.storage, categoryName, ser)
	if err != nil {
		return total, err
	}

	supersededRemoved, err := dedupeSupersededPartOrphans(s.storage, categoryName, ser)
	total += supersededRemoved
	if err != nil {
		return total, err
	}

	return total, nil
}

// dedupeWinningChapters is DedupeFiles pass 1: for every chapter with a
// winning filename AND a number, remove any OTHER CBZ in the series folder
// sharing that number, keeping the winner. Returns the count removed.
func dedupeWinningChapters(storage, categoryName string, ser *ent.Series) (int, error) {
	total := 0
	for _, ch := range ser.Edges.Chapters {
		if ch.Filename == "" || ch.Number == nil {
			continue
		}
		removed, err := disk.RemoveOtherChapterFiles(
			storage, categoryName, ser.Title,
			chapter.FormatChapterNumber(*ch.Number), ch.Filename,
		)
		if err != nil {
			return total, fmt.Errorf("series.DedupeFiles: chapter %s: %w", ch.ID, err)
		}
		total += removed
	}
	return total, nil
}

// dedupeSupersededPartOrphans is DedupeFiles pass 2 (see the DedupeFiles doc
// comment): for every superseded FRACTIONAL-part chapter, remove EVERY CBZ
// matching its number with no keeper. Whole-integer superseded chapters are
// skipped. Returns the count removed.
func dedupeSupersededPartOrphans(storage, categoryName string, ser *ent.Series) (int, error) {
	total := 0
	for _, ch := range ser.Edges.Chapters {
		if ch.State != entchapter.StateSuperseded || ch.Number == nil {
			continue
		}
		n := *ch.Number
		if n == math.Trunc(n) {
			// Whole-integer "superseded" chapter: not a split part, skip —
			// its file (if any) is a legitimate keeper elsewhere.
			continue
		}
		removed, err := disk.RemoveOtherChapterFiles(
			storage, categoryName, ser.Title,
			chapter.FormatChapterNumber(n), "",
		)
		if err != nil {
			return total, fmt.Errorf("series.DedupeFiles: superseded chapter %s: %w", ch.ID, err)
		}
		total += removed
	}
	return total, nil
}
