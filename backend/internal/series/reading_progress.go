package series

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
)

// SetReadingProgress resets seriesID's LOCAL chapter read-state to "as if the
// owner had read up to (and including) target and nothing past it" — the
// local half of the QCAT-242 "set reading progress to N" action (the other
// half, force-setting every bound tracker, is
// syncsvc.Service.SetSeriesProgress; the two are orchestrated together by
// the HTTP handler, never coupled here).
//
// Rule (QCAT-242): a chapter numbered <= target is marked read; a chapter
// numbered > target is marked unread (read=false, last_read_page reset to 0,
// read_at cleared). target=0 means "re-read from scratch" — every chapter
// unread. A chapter that was ALREADY read and stays <= target keeps its
// original read_at (re-confirming a chapter someone already finished must
// not reset when they first finished it); only a chapter that TRANSITIONS
// unread->read on this call gets read_at stamped to now. A chapter whose
// number is nil (unparseable) is left entirely untouched — there is no
// number to compare against target.
//
// Deliberately does NOT go through SetProgress: SetProgress fires the
// reading-triggered tracker push (firePushProgress) on every read=true
// transition, which would immediately push the JUST-RESET chapters back out
// to every bound tracker — a push loop that would fight the explicit
// tracker force-set the caller is about to perform via
// syncsvc.Service.SetSeriesProgress. This method writes Chapter rows
// directly, mirroring syncsvc's own markLocalRead (which has the identical
// "never route through SetProgress" rule, for the same reason).
//
// TWO bulk UPDATE statements cover the whole series regardless of chapter
// count (no N+1): one marks the newly-read chapters (only rows currently
// unread, so an already-read chapter's read_at is never touched), one resets
// everything past target. The second statement is deliberately UNCONDITIONAL
// on the current read flag (not filtered to Read(true)): a chapter can be
// unread yet still carry a stray last_read_page from a page the owner
// scrolled past without finishing, and that must be cleared too, so every
// row past target is matched regardless of its current state. Returns the
// total number of Chapter rows EITHER statement matched (a chapter already
// unread with page 0 past target still counts, since the second statement
// touches it unconditionally — this is a matched-row count, not a strict
// "value actually changed" count). A missing seriesID yields ErrSeriesNotFound.
func (s *Service) SetReadingProgress(ctx context.Context, seriesID uuid.UUID, target float64) (affected int, err error) {
	exists, err := s.client.Series.Query().Where(entseries.IDEQ(seriesID)).Exist(ctx)
	if err != nil {
		return 0, fmt.Errorf("series.SetReadingProgress: check series %s exists: %w", seriesID, err)
	}
	if !exists {
		return 0, ErrSeriesNotFound
	}

	now := time.Now().UTC()

	newlyRead, err := s.client.Chapter.Update().
		Where(
			entchapter.SeriesID(seriesID),
			entchapter.NumberNotNil(),
			entchapter.NumberLTE(target),
			entchapter.Read(false),
		).
		SetRead(true).
		SetReadAt(now).
		Save(ctx)
	if err != nil {
		return 0, fmt.Errorf("series.SetReadingProgress: mark read up to %v for series %s: %w", target, seriesID, err)
	}

	reset, err := s.client.Chapter.Update().
		Where(
			entchapter.SeriesID(seriesID),
			entchapter.NumberNotNil(),
			entchapter.NumberGT(target),
		).
		SetRead(false).
		SetLastReadPage(0).
		ClearReadAt().
		Save(ctx)
	if err != nil {
		return 0, fmt.Errorf("series.SetReadingProgress: reset chapters past %v for series %s: %w", target, seriesID, err)
	}

	return newlyRead + reset, nil
}
