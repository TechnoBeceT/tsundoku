package library

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/series"
)

// AddProvider attaches a Suwayomi source to an EXISTING series, upgrade-aware.
//
// Algorithm:
//  1. Load the series by id — ErrSeriesNotFound if it does not exist.
//  2. Reject if a SeriesProvider with provider==source is already attached —
//     ErrProviderAlreadyPresent.
//  3. Call s.ingest.AddSeries(ctx, source, mangaID, ser.Title): AddSeries
//     find-or-creates a Series by slug(title), so passing the EXISTING
//     series' canonical title attaches the new source to THIS series and
//     ingests its chapter feed (new chapters land as wanted). A Suwayomi
//     fetch failure is wrapped as ErrSourceNotFound.
//  4. Set importance on the just-created SeriesProvider(seriesID, source).
//  5. Call s.trigger() (if non-nil) to converge immediately: any on-disk
//     chapter whose satisfied_importance is lower than the new provider's
//     importance will be flagged upgrade_available by download.DetectUpgrades
//     on the next cycle, and the existing upgrade engine re-downloads it from
//     the better source.
//  6. Return the refreshed series.SeriesDetailDTO (§16 round-trip).
func (s *Service) AddProvider(ctx context.Context, seriesID uuid.UUID, source string, mangaID, importance int) (series.SeriesDetailDTO, error) {
	ser, err := s.db.Series.Get(ctx, seriesID)
	if ent.IsNotFound(err) {
		return series.SeriesDetailDTO{}, ErrSeriesNotFound
	}
	if err != nil {
		return series.SeriesDetailDTO{}, fmt.Errorf("library.AddProvider: get series %s: %w", seriesID, err)
	}

	dup, err := s.db.SeriesProvider.Query().
		Where(seriesprovider.SeriesID(seriesID), seriesprovider.Provider(source)).
		Exist(ctx)
	if err != nil {
		return series.SeriesDetailDTO{}, err
	}
	if dup {
		return series.SeriesDetailDTO{}, ErrProviderAlreadyPresent
	}

	if _, err := s.ingest.AddSeries(ctx, source, mangaID, ser.Title); err != nil {
		return series.SeriesDetailDTO{}, errors.Join(ErrSourceNotFound, err)
	}

	sp, err := s.db.SeriesProvider.Query().
		Where(seriesprovider.SeriesID(seriesID), seriesprovider.Provider(source)).
		Only(ctx)
	if err != nil {
		return series.SeriesDetailDTO{}, err
	}
	if _, err := sp.Update().SetImportance(importance).Save(ctx); err != nil {
		return series.SeriesDetailDTO{}, err
	}

	if s.trigger != nil {
		s.trigger()
	}

	return s.series.GetSeries(ctx, seriesID)
}
