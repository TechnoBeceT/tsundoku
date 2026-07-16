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
// the new batch), then loops the AddProvider primitive. Every assigned
// importance is NON-NEGATIVE: when the existing providers occupy importances too
// small to fit the new batch beneath them (e.g. a disk-origin provider at
// importance 1), belowExistingImportances RENUMBERS the existing providers up
// onto a clean spread so the new batch still lands below them without any
// negative value — the renumber is applied here (as part of the same operation)
// before the attaches. Mirrors imports.Adopt's partial-failure contract:
// sequential attach, and on the first failure it returns an error naming the
// providers attached so far (successful attaches — and any existing-provider
// renumber — are upsert-only and left in place, never rolled back). Returns the
// refreshed SeriesDetailDTO on full success (§16).
func (s *Service) AddProviders(ctx context.Context, seriesID uuid.UUID, refs []ProviderRef) (series.SeriesDetailDTO, error) {
	if len(refs) == 0 {
		return series.SeriesDetailDTO{}, ErrNoProviders
	}

	importances, err := s.planBatchImportances(ctx, seriesID, len(refs))
	if err != nil {
		return series.SeriesDetailDTO{}, err
	}
	return s.attachRefs(ctx, seriesID, refs, importances)
}

// planBatchImportances resolves the series' existing providers, plans a
// non-negative importance for each of the `count` new sources (below the
// existing providers, decision E), and — when there was no room below without
// going negative — renumbers the existing providers up FIRST so the new batch
// stays non-negative. Returns the new sources' importances (index i = refs[i]).
func (s *Service) planBatchImportances(ctx context.Context, seriesID uuid.UUID, count int) ([]int, error) {
	// Existing providers, highest-importance first (id-tiebroken for a
	// deterministic renumber), so belowExistingImportances can lift them above
	// the new batch in a stable relative order.
	existing, err := s.db.SeriesProvider.Query().
		Where(seriesprovider.SeriesID(seriesID)).
		Order(ent.Desc(seriesprovider.FieldImportance), ent.Asc(seriesprovider.FieldID)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	if len(existing) == 0 {
		if err := s.requireSeriesExists(ctx, seriesID); err != nil {
			return nil, err
		}
	}
	existingImp := make([]int, len(existing))
	for i, sp := range existing {
		existingImp[i] = sp.Importance
	}
	renumbered, importances := belowExistingImportances(existingImp, count)
	if renumbered != nil {
		if err := s.renumberExistingProviders(ctx, existing, renumbered); err != nil {
			return nil, err
		}
	}
	return importances, nil
}

// requireSeriesExists distinguishes an unknown series id from a real series
// whose last source was removed (M6 leaves a 0-provider series in place): a
// missing row → ErrSeriesNotFound; a real 0-provider series → nil (the caller's
// belowExistingImportances then uses the Adopt-scale fallback, nothing to be
// "below").
func (s *Service) requireSeriesExists(ctx context.Context, seriesID uuid.UUID) error {
	if _, err := s.db.Series.Get(ctx, seriesID); err != nil {
		if ent.IsNotFound(err) {
			return ErrSeriesNotFound
		}
		return err
	}
	return nil
}

// attachRefs loops the AddProvider primitive over refs at the planned
// importances, mirroring imports.Adopt's partial-failure contract: on the first
// failure it returns an error naming the providers attached so far (never rolled
// back). Returns the refreshed SeriesDetailDTO on full success (§16).
func (s *Service) attachRefs(ctx context.Context, seriesID uuid.UUID, refs []ProviderRef, importances []int) (series.SeriesDetailDTO, error) {
	var attached []string
	var last series.SeriesDetailDTO
	for i, ref := range refs {
		dto, err := s.AddProvider(ctx, seriesID, ref.Source, ref.URL, importances[i], ref.Scanlator)
		if err != nil {
			return series.SeriesDetailDTO{}, fmt.Errorf("attach %s (scanlator %q) failed after attaching %v: %w",
				ref.Source, ref.Scanlator, attached, err)
		}
		attached = append(attached, ref.Source)
		last = dto
	}
	return last, nil
}

// renumberExistingProviders lifts each existing provider onto its planned
// importance (imps is aligned to existing, both highest-first). A row already at
// its target is skipped. Not transactional with the subsequent attaches by
// design — AddProviders is already documented as non-atomic (upsert-only,
// partial-failure-visible), and every planned importance is non-negative so a
// mid-batch failure leaves a coherent, self-healing state.
func (s *Service) renumberExistingProviders(ctx context.Context, existing []*ent.SeriesProvider, imps []int) error {
	for i, sp := range existing {
		if sp.Importance == imps[i] {
			continue
		}
		if err := s.db.SeriesProvider.UpdateOneID(sp.ID).SetImportance(imps[i]).Exec(ctx); err != nil {
			return fmt.Errorf("library.AddProviders: renumber existing provider %s: %w", sp.ID, err)
		}
	}
	return nil
}
