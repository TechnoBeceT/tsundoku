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

// ErrNoProviders is returned when a batch attach carries an empty list.
var ErrNoProviders = errors.New("no providers supplied")

// AddProviders attaches several real Suwayomi sources to an existing series in
// one call. It assigns each new source an importance strictly BELOW the
// series' current providers (decision E), in list order (refs[0] = highest of
// the new batch, via belowExistingImportances), then loops the AddProvider
// primitive. Mirrors imports.Adopt's partial-failure contract: sequential
// attach, and on the first failure it returns an error naming the providers
// attached so far (successful attaches are upsert-only and left in place —
// never rolled back). Returns the refreshed SeriesDetailDTO on full success
// (§16).
func (s *Service) AddProviders(ctx context.Context, seriesID uuid.UUID, refs []ProviderRef) (series.SeriesDetailDTO, error) {
	if len(refs) == 0 {
		return series.SeriesDetailDTO{}, ErrNoProviders
	}

	existing, err := s.db.SeriesProvider.Query().
		Where(seriesprovider.SeriesID(seriesID)).
		All(ctx)
	if err != nil {
		return series.SeriesDetailDTO{}, err
	}
	if len(existing) == 0 {
		// A series with zero providers is only reachable via a not-found id
		// here; verify existence so we return the not-found sentinel rather
		// than a silent below-existing computation over an empty set.
		if _, err := s.db.Series.Get(ctx, seriesID); err != nil {
			if ent.IsNotFound(err) {
				return series.SeriesDetailDTO{}, ErrSeriesNotFound
			}
			return series.SeriesDetailDTO{}, err
		}
	}
	existingImp := make([]int, len(existing))
	for i, sp := range existing {
		existingImp[i] = sp.Importance
	}
	importances := belowExistingImportances(existingImp, len(refs))

	var attached []string
	var last series.SeriesDetailDTO
	for i, ref := range refs {
		dto, err := s.AddProvider(ctx, seriesID, ref.Source, ref.MangaID, importances[i], ref.Scanlator)
		if err != nil {
			return series.SeriesDetailDTO{}, fmt.Errorf("attach %s (scanlator %q) failed after attaching %v: %w",
				ref.Source, ref.Scanlator, attached, err)
		}
		attached = append(attached, ref.Source)
		last = dto
	}
	return last, nil
}
