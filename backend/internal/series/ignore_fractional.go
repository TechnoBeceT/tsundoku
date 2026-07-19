package series

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/pkg/chapterrange"
)

// SetIgnoreFractionalForSeries flags EVERY one of the series' sources as a
// fractional re-uploader (ignore=true), or clears the flag on all of them
// (ignore=false) — the whole-series convenience behind the library Fractionals
// page's inline policy toggle. It is the bulk sibling of SetIgnoreFractional
// (which flips ONE source): that page's toggle reads ON only when every source
// already ignores, so flipping it must set them all together.
//
// Like the per-source form it DELETES NOTHING (never-auto-delete): already-ingested
// fractional feed rows and downloaded CBZs are kept. After writing the flags it
// runs the SAME reconcileIgnoredFractionals pass — parking a now-fully-ignored
// UNDOWNLOADED fractional into the terminal ignored state (out of the queue), or
// restoring one that regained a non-ignoring carrier back to wanted. Cleaning the
// already-downloaded files stays the explicit, previewed RemoveFractionalChapters
// action.
//
// An unknown series returns ErrSeriesNotFound (→404). A series with zero sources is
// a no-op (the bulk update matches no rows). The flag write is one bulk UPDATE — a
// single atomic statement, so it is all-or-nothing; the reconcile runs after it,
// mirroring SetIgnoreFractional's update-then-reconcile order.
func (s *Service) SetIgnoreFractionalForSeries(ctx context.Context, id uuid.UUID, ignore bool) error {
	exists, err := s.client.Series.Query().Where(entseries.IDEQ(id)).Exist(ctx)
	if err != nil {
		return fmt.Errorf("series.SetIgnoreFractionalForSeries: check series %s: %w", id, err)
	}
	if !exists {
		return ErrSeriesNotFound
	}

	if err := s.client.SeriesProvider.Update().
		Where(entseriesprovider.SeriesID(id)).
		SetIgnoreFractional(ignore).
		Exec(ctx); err != nil {
		// Defensive path: the series' existence was just confirmed and a predicate
		// update never errors on zero matches, so this is reachable only on a
		// DB-level failure — not forceable in a black-box test.
		return fmt.Errorf("series.SetIgnoreFractionalForSeries: update providers of series %s: %w", id, err)
	}

	if err := s.reconcileIgnoredFractionals(ctx, id); err != nil {
		return fmt.Errorf("series.SetIgnoreFractionalForSeries: reconcile ignored fractionals for series %s: %w", id, err)
	}
	return nil
}

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
