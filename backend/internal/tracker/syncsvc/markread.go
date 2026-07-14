package syncsvc

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	kernel "github.com/technobecet/tsundoku/internal/tracker/sync"
)

// markLocalRead is SyncNow's pull-direction half (spec/trackers-sync-phase4
// §2b): "when a native tracker reports the owner is FURTHER ahead than the
// local read-state, mark the series' local chapters read up to that point."
// converged is the just-computed sync.Converge target (the higher of local
// vs remote) — Converge's own doc comment names this exact caller
// obligation ("a caller that owns the local chapter rows marks every
// chapter numbered <= converged as read").
//
// 🔴 This writes Chapter rows DIRECTLY via ent (SetRead/SetReadAt) — it must
// NEVER route through series.Service.SetProgress. SetProgress fires the
// reading-triggered PushProgress hook (internal/series/tracksync.go
// firePushProgress) on every read=true transition; calling it from here
// would immediately push the chapter SyncNow just pulled straight back out
// to every bound tracker, a push↔pull loop. Chapter.last_read_page (page-
// level state no tracker reports) is never touched either.
//
// PAIRING: Chapter.number is nillable and kernel.SyncableNumbers can drop
// entries (the unparseable -1 sentinel, other negatives, NaN), so the number
// slice fed to kernel.MarkReadUpTo must stay index-aligned with the chapter
// rows it counts against. This filters the ROW slice with the identical
// per-element SyncableNumbers predicate used to build the number slice (the
// same "call SyncableNumbers on a lone element" idiom PushProgress already
// uses), so index i in both slices is always the same chapter — readCount
// (an index count into the filtered numbers) safely slices the SAME
// filtered row slice.
//
// Idempotent: an already-read chapter is skipped (read_at is never
// rewritten), and readCount only ever grows monotonically for a fixed
// converged value, so a repeat SyncNow marks nothing new.
func (s *Service) markLocalRead(ctx context.Context, seriesID uuid.UUID, converged float64) error {
	rows, err := s.client.Chapter.Query().
		Where(entchapter.SeriesID(seriesID), entchapter.NumberNotNil()).
		Order(entchapter.ByNumber()).
		All(ctx)
	if err != nil {
		return fmt.Errorf("syncsvc: markLocalRead: load chapters for series %s: %w", seriesID, err)
	}

	syncableRows, syncableNumbers := syncablePairs(rows)
	readCount := kernel.MarkReadUpTo(syncableNumbers, converged)

	now := time.Now().UTC()
	for _, ch := range syncableRows[:readCount] {
		if ch.Read {
			continue
		}
		if _, err := s.client.Chapter.UpdateOneID(ch.ID).
			SetRead(true).
			SetReadAt(now).
			Save(ctx); err != nil {
			return fmt.Errorf("syncsvc: markLocalRead: mark chapter %s read: %w", ch.ID, err)
		}
	}
	return nil
}

// syncablePairs filters rows (already ordered ascending by number) down to
// the syncable subset — reusing kernel.SyncableNumbers per element rather
// than re-deriving its unparseable/negative/NaN rule here — and returns the
// filtered rows alongside their numbers, index-aligned 1:1. rows must all
// have a non-nil Number (callers pre-filter via entchapter.NumberNotNil()).
func syncablePairs(rows []*ent.Chapter) (filteredRows []*ent.Chapter, numbers []float64) {
	filteredRows = make([]*ent.Chapter, 0, len(rows))
	numbers = make([]float64, 0, len(rows))
	for _, ch := range rows {
		n := *ch.Number
		if len(kernel.SyncableNumbers([]float64{n})) == 0 {
			continue
		}
		filteredRows = append(filteredRows, ch)
		numbers = append(numbers, n)
	}
	return filteredRows, numbers
}
