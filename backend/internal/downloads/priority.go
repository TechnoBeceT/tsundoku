package downloads

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	"github.com/technobecet/tsundoku/internal/series"
)

// ErrSeriesNotFound identifies an unknown priority target.
var ErrSeriesNotFound = errors.New("series not found")

// PriorityStatus reports remaining fixed-snapshot membership for a series.
type PriorityStatus struct {
	Remaining int `json:"remaining"`
}

// SetPriority enables a fixed queued snapshot or explicitly cancels membership.
// Locking the owning series serializes competing clicks without locking source
// scheduling: already launched work retains its existing conditional ownership.
func (s *Service) SetPriority(ctx context.Context, id uuid.UUID, enabled bool) (PriorityStatus, error) {
	var status PriorityStatus
	err := withTx(ctx, s.client, func(tx *ent.Tx) error {
		if err := lockPrioritySeries(ctx, tx, id); err != nil {
			return err
		}
		marked := entchapter.And(entchapter.SeriesIDEQ(id), entchapter.DownloadPriority(true))
		count, err := tx.Chapter.Query().Where(marked).Count(ctx)
		if err != nil {
			return err
		}
		if !enabled {
			_, err = tx.Chapter.Update().Where(marked).SetDownloadPriority(false).Save(ctx)
			return err
		}
		if count > 0 {
			status.Remaining = count
			return nil
		}
		queued := entchapter.And(entchapter.SeriesIDEQ(id), entchapter.StateIn(entchapter.StateWanted, entchapter.StateFailed, entchapter.StateUpgradeAvailable))
		ids, err := tx.Chapter.Query().Where(queued).IDs(ctx)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		status.Remaining, err = tx.Chapter.Update().Where(queued, entchapter.IDIn(ids...)).SetDownloadPriority(true).Save(ctx)
		return err
	})
	return status, err
}

// PrioritySnapshot includes dormant members so owner cancellation remains
// available when no member is currently visible in the queued activity page.
type PrioritySnapshot struct {
	SeriesID    uuid.UUID `json:"seriesId"`
	SeriesTitle string    `json:"seriesTitle"`
	Remaining   int       `json:"remaining"`
}

// Priorities reads fixed-snapshot counts independently of activity pagination.
func (s *Service) Priorities(ctx context.Context) ([]PrioritySnapshot, error) {
	var counts []struct {
		SeriesID  uuid.UUID `json:"series_id"`
		Remaining int       `json:"remaining"`
	}
	err := s.client.Chapter.Query().Where(entchapter.DownloadPriority(true)).GroupBy(entchapter.FieldSeriesID).Aggregate(ent.As(ent.Count(), "remaining")).Scan(ctx, &counts)
	if err != nil {
		return nil, err
	}
	result := make([]PrioritySnapshot, 0, len(counts))
	if len(counts) == 0 {
		return result, nil
	}
	ids := make([]uuid.UUID, len(counts))
	byID := make(map[uuid.UUID]int, len(counts))
	for i, count := range counts {
		ids[i] = count.SeriesID
		byID[count.SeriesID] = count.Remaining
	}
	rows, err := s.client.Series.Query().Where(entseries.IDIn(ids...)).WithProviders().All(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		title, _ := series.SeriesDisplay(row, series.MetadataProvider(row))
		result = append(result, PrioritySnapshot{SeriesID: row.ID, SeriesTitle: title, Remaining: byID[row.ID]})
	}
	return result, nil
}

func lockPrioritySeries(ctx context.Context, tx *ent.Tx, id uuid.UUID) error {
	rows, err := tx.Client().QueryContext(ctx, `SELECT id FROM series WHERE id=$1 FOR UPDATE`, id)
	if err != nil {
		return fmt.Errorf("lock priority series: %w", err)
	}
	exists := rows.Next()
	rowErr := rows.Err()
	closeErr := rows.Close()
	if rowErr != nil {
		return rowErr
	}
	if closeErr != nil {
		return closeErr
	}
	if !exists {
		return ErrSeriesNotFound
	}
	return nil
}
