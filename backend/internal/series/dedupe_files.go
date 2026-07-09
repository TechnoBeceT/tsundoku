package series

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
)

// DedupeFiles is the owner-triggered duplicate-CBZ sweep for a whole series. For
// every downloaded chapter (one that has a winning filename AND a number) it calls
// disk.RemoveOtherChapterFiles to remove any OTHER CBZ in the series folder that
// shares that chapter's number, keeping the chapter's own winning filename. It
// returns the total number of files removed.
//
// This is the bulk counterpart to the automatic per-convergence cleanup in the
// upgrade path: it reconciles a library that accumulated duplicate CBZs before
// the convergence engine existed (e.g. an imported Kaizoku library, or chapters
// downloaded before this feature). It NEVER deletes a chapter's winning file, and
// it performs NO DB writes — only superseded on-disk duplicates are removed.
// Chapters with no winning filename (never downloaded) or no number are skipped so
// there is always a keeper. A missing series folder yields 0 (nothing to sweep).
// An unknown id returns ErrSeriesNotFound (mapped to 404 by the handler).
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
	total := 0
	for _, ch := range ser.Edges.Chapters {
		if ch.Filename == "" || ch.Number == nil {
			continue
		}
		removed, err := disk.RemoveOtherChapterFiles(
			s.storage, categoryName, ser.Title,
			chapter.FormatChapterNumber(*ch.Number), ch.Filename,
		)
		if err != nil {
			return total, fmt.Errorf("series.DedupeFiles: chapter %s: %w", ch.ID, err)
		}
		total += removed
	}
	return total, nil
}
