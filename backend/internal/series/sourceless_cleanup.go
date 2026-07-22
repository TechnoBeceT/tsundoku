package series

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
)

// SourcelessCleanupChapterDTO is one removable sourceless chapter in the cleanup
// preview: a DOWNLOADED chapter whose chapter_key NO remaining source carries — the
// CBZ left behind when every source that supplied it was removed. Provider is the
// former satisfying source's label ("" once that source is gone, which is the normal
// case). Number is nullable (a sourceless chapter may lack a parsed number).
type SourcelessCleanupChapterDTO struct {
	ChapterID string   `json:"chapterId"`
	Number    *float64 `json:"number"`
	PageCount *int     `json:"pageCount"`
	Provider  string   `json:"provider"`
	Filename  string   `json:"filename"`
}

// SourcelessCleanupDTO is the sourceless-cleanup preview for one series. Chapters is
// always non-nil so the JSON renders [] rather than null.
type SourcelessCleanupDTO struct {
	Chapters []SourcelessCleanupChapterDTO `json:"chapters"`
}

// SourcelessCleanupPreview lists the series' REMOVABLE sourceless chapters — every
// DOWNLOADED chapter (filename != "") whose chapter_key NO remaining SeriesProvider
// feed carries. These are the CBZs stranded when a source that supplied them was
// removed (RemoveProvider keeps downloaded chapters by the keep-CBZs invariant, so
// they persist with no source that could ever re-supply them). It DELETES NOTHING.
//
// This is the exact INVERSE of the fractional-cleanup rule, which by design REFUSES
// zero-carrier chapters (fractional_cleanup.go isRemovableFractional): a sourceless
// chapter is irreplaceable by any current source, and this endpoint exists precisely
// to let the owner remove it deliberately. An unknown id yields ErrSeriesNotFound.
func (s *Service) SourcelessCleanupPreview(ctx context.Context, id uuid.UUID) (SourcelessCleanupDTO, error) {
	row, err := loadSeriesForCleanup(ctx, s.client, id)
	if err != nil {
		return SourcelessCleanupDTO{}, err
	}

	providers := providersByID(row.Edges.Providers)
	removable := removableSourceless(row)

	out := make([]SourcelessCleanupChapterDTO, 0, len(removable))
	for _, ch := range removable {
		out = append(out, SourcelessCleanupChapterDTO{
			ChapterID: ch.ID.String(),
			Number:    ch.Number,
			PageCount: ch.PageCount,
			Provider:  satisfyingLabel(ch, providers),
			Filename:  ch.Filename,
		})
	}
	return SourcelessCleanupDTO{Chapters: out}, nil
}

// RemoveSourcelessChapters deletes the selected removable sourceless chapters: each
// chapter's CBZ file AND its Chapter row. Returns how many were removed.
//
// 🔴 SANCTIONED OWNER DELETION PATH #6. Owner-triggered, per-chapter, confirmed in a
// dialog. NOTHING AUTOMATIC MAY EVER CALL IT — no sweep, no job, no reconcile. The
// removable set is RE-COMPUTED SERVER-SIDE (see SourcelessCleanupPreview) and any id
// outside it (a carried chapter, a chapter of another series, an unknown id) is
// rejected with ErrChapterNotRemovable (400) and NOTHING is deleted (all-or-nothing).
//
// There are NO ProviderChapter feed rows to keep — a sourceless chapter has no carrier
// by definition. Deletion reuses the fractional path's rollback-safe machinery
// (deleteRemovableTargets → removeCleanupFiles): rows are deleted in a tx, the CBZs are
// deleted BEFORE the commit, and a file failure rolls the rows back so no committed
// deletion strands an orphan CBZ for disk.Reconcile to re-import.
func (s *Service) RemoveSourcelessChapters(ctx context.Context, id uuid.UUID, chapterIDs []uuid.UUID) (int, error) {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return 0, fmt.Errorf("series.RemoveSourcelessChapters: begin tx: %w", err)
	}

	row, err := loadSeriesForCleanup(ctx, tx.Client(), id)
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	targets, err := selectSourceless(row, chapterIDs)
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	removed, err := s.deleteRemovableTargets(ctx, tx, id, row, targets)
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("series.RemoveSourcelessChapters: commit tx: %w", err)
	}
	return removed, nil
}

// selectSourceless resolves the caller's chapter ids against the server-recomputed
// removable-sourceless set. Any id outside it fails the WHOLE call with
// ErrChapterNotRemovable (all-or-nothing); duplicate ids are collapsed.
func selectSourceless(row *ent.Series, chapterIDs []uuid.UUID) ([]*ent.Chapter, error) {
	removable := make(map[uuid.UUID]*ent.Chapter, len(row.Edges.Chapters))
	for _, ch := range removableSourceless(row) {
		removable[ch.ID] = ch
	}
	targets := make([]*ent.Chapter, 0, len(chapterIDs))
	seen := make(map[uuid.UUID]struct{}, len(chapterIDs))
	for _, cid := range chapterIDs {
		ch, ok := removable[cid]
		if !ok {
			return nil, fmt.Errorf("series: chapter %s: %w", cid, ErrChapterNotRemovable)
		}
		if _, dup := seen[cid]; dup {
			continue
		}
		seen[cid] = struct{}{}
		targets = append(targets, ch)
	}
	return targets, nil
}

// removableSourceless applies the removable rule to one loaded series: a chapter with
// a downloaded CBZ (hasDownloadedFile) whose chapter_key NO source feed carries
// (len(carriers)==0). Purely in-memory over the eager-loaded edges.
func removableSourceless(row *ent.Series) []*ent.Chapter {
	carriers := carriersByKey(row.Edges.Providers)
	out := make([]*ent.Chapter, 0, len(row.Edges.Chapters))
	for _, ch := range row.Edges.Chapters {
		if hasDownloadedFile(ch) && len(carriers[ch.ChapterKey]) == 0 {
			out = append(out, ch)
		}
	}
	return out
}
