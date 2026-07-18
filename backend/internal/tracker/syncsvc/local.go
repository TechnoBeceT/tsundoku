package syncsvc

import (
	"context"
	"fmt"

	"entgo.io/ent/dialect/sql"
	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	kernel "github.com/technobecet/tsundoku/internal/tracker/sync"
)

// seriesLocalFurthest returns the highest SYNCABLE chapter number seriesID
// has actually been marked READ in the local library — the third leg of the
// max-wins convergence syncOneBinding folds together (alongside the
// binding's own stored value and the remote's reported progress). It is
// deliberately independent of the TrackBinding row: a series can be read
// locally far ahead of anything a tracker (or Tsundoku's own binding) yet
// claims — the exact "converge on add" gap this feature closes (Suwayomi/
// Komikku both converge the local read-count into a fresh bind; Tsundoku
// used to snapshot only the remote's progress and never look at local at
// all).
//
// Returns 0 (a safe floor for kernel.Converge — Converge only ever moves
// UP, so a floor can never regress anything) when the series has no read
// chapters at all, or its single highest-numbered read chapter is itself
// unsyncable (kernel.SyncableNumbers filters the chapter normaliser's
// unparseable -1 sentinel, any other negative, and NaN — see that
// function's own doc comment for why).
//
// ONE query: the ORDER BY number DESC + LIMIT 1 is done by the database, so
// this costs exactly one round-trip regardless of how many chapters the
// series has — no N+1, and no full-table load (contrast markLocalRead,
// which genuinely needs every syncable row to walk kernel.MarkReadUpTo's
// monotonic pairing; this caller only ever needs the single highest one).
func (s *Service) seriesLocalFurthest(ctx context.Context, seriesID uuid.UUID) (float64, error) {
	row, err := s.client.Chapter.Query().
		Where(entchapter.SeriesID(seriesID), entchapter.Read(true), entchapter.NumberNotNil()).
		Order(entchapter.ByNumber(sql.OrderDesc())).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("syncsvc: seriesLocalFurthest: load top read chapter for series %s: %w", seriesID, err)
	}

	syncable := kernel.SyncableNumbers([]float64{*row.Number})
	if len(syncable) == 0 {
		return 0, nil
	}
	// WHOLE-CHAPTER PROGRESS: a tracker stores an integer chapter COUNT, so the
	// local library's furthest-read value must be a whole chapter — reading the
	// 42.1 split when the highest whole chapter read is 42 reports 42, never
	// 42.1 and never 43 (kernel.TruncateForInteger floors, matching Suwayomi/
	// mihon's last_chapter_read.toInt()). Flooring here is the local-library →
	// tracker-progress boundary for SyncNow's three-way convergence, mirroring
	// PushProgress's reader-hook floor.
	return float64(kernel.TruncateForInteger(syncable[0])), nil
}
