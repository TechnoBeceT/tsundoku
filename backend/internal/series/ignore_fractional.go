package series

import (
	"context"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/pkg/chapterrange"
)

// reconcileIgnoredFractionals brings a series' UNDOWNLOADED fractional chapters
// into agreement with its sources' current ignore_fractional flags, in BOTH
// directions, in one in-memory pass over the already-loaded chapters + provider
// feeds. It is called after SetIgnoreFractional flips a flag (either way):
//
//   - PARK: a wanted/failed fractional whose EVERY carrier now has
//     ignore_fractional=true (and that has at least one carrier) → ignored. No
//     live source will ever fetch it, so leaving it wanted clogs the download
//     queue and the chapter list forever; ignored is its terminal hidden state.
//   - RESTORE: an ignored fractional that now has at least one NON-ignoring
//     carrier → wanted. Un-ticking the toggle (or adding a source) makes the
//     chapter fetchable again, so it rejoins the queue. This is the genuine undo
//     that keeping the feed rows (never-auto-delete) makes possible.
//
// The PARK guard is EVERY-carrier-ignored — the resurrection guard, identical to
// series.RemoveFractionalChapters and chapter.IsIgnorableFractional: a fractional
// a non-ignored source also carries is a live target and is never parked. The
// RESTORE guard is "has a non-ignoring carrier", NOT merely "not all-ignored": a
// carrier-LESS ignored fractional (every source removed) has nothing to fetch it,
// so it stays ignored rather than stranding back in wanted — mirroring the
// removable rule's carrier-less handling.
//
// It NEVER touches downloaded/downloading/upgrading/superseded/permanently_failed
// chapters (those are not queue clutter, and a downloaded ignored fractional is
// cleaned up by the owner-triggered DedupeFiles, never automatically). It writes
// only state transitions — no deletion, no disk I/O. A per-chapter transition
// failure aborts the pass (the flag is already persisted; the download cycle's own
// handleSourcelessChapter finishes the parking on its next run either way).
func (s *Service) reconcileIgnoredFractionals(ctx context.Context, id uuid.UUID) error {
	row, err := loadSeriesForCleanup(ctx, s.client, id)
	if err != nil {
		return err
	}

	carriers := carriersByKey(row.Edges.Providers)
	for _, ch := range row.Edges.Chapters {
		if ch.Number == nil || !chapterrange.IsFractional(*ch.Number) {
			continue
		}
		if err := s.applyIgnoreReconcile(ctx, ch, carriers[ch.ChapterKey]); err != nil {
			return err
		}
	}
	return nil
}

// applyIgnoreReconcile applies the park/restore rule (see reconcileIgnoredFractionals)
// to ONE fractional chapter given every source whose feed carries its key.
func (s *Service) applyIgnoreReconcile(ctx context.Context, ch *ent.Chapter, carriers []*ent.SeriesProvider) error {
	switch ch.State {
	case entchapter.StateWanted, entchapter.StateFailed:
		if allCarriersIgnore(carriers) {
			return chapter.SetState(ctx, s.client, ch.ID, entchapter.StateIgnored)
		}
	case entchapter.StateIgnored:
		if hasNonIgnoringCarrier(carriers) {
			return chapter.SetState(ctx, s.client, ch.ID, entchapter.StateWanted)
		}
	}
	return nil
}

// allCarriersIgnore reports whether the chapter has at least one carrier AND every
// carrier has ignore_fractional=true — the resurrection-safe park condition.
func allCarriersIgnore(carriers []*ent.SeriesProvider) bool {
	if len(carriers) == 0 {
		return false
	}
	for _, sp := range carriers {
		if !sp.IgnoreFractional {
			return false
		}
	}
	return true
}

// hasNonIgnoringCarrier reports whether at least one carrier does NOT ignore
// fractionals — the restore condition (a live source can fetch the chapter again).
func hasNonIgnoringCarrier(carriers []*ent.SeriesProvider) bool {
	for _, sp := range carriers {
		if !sp.IgnoreFractional {
			return true
		}
	}
	return false
}
