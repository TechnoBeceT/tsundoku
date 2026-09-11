package library

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/ingest"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sourceengine"
)

func validateRematchRef(storedSource string, ref ProviderRef) (int64, error) {
	if strings.TrimSpace(ref.URL) == "" || !ref.AddressMode.IsValid() {
		return 0, ErrInvalidProviderAddress
	}
	sourceID, err := parseSourceID(storedSource)
	if err != nil {
		return 0, errors.Join(ErrSourceNotFound, err)
	}
	if ref.Source != storedSource {
		return 0, ErrProviderSourceMismatch
	}
	return sourceID, nil
}

// RematchProvider replaces one linked provider's manga address and reconciles
// its fetched feed in place, preserving provider, chapter, and file identity.
func (s *Service) RematchProvider(ctx context.Context, seriesID, providerID uuid.UUID, ref ProviderRef) (series.SeriesDetailDTO, error) {
	provider, err := s.rematchProviderRow(ctx, seriesID, providerID)
	if err != nil {
		return series.SeriesDetailDTO{}, err
	}
	sourceID, err := validateRematchRef(provider.Provider, ref)
	if err != nil {
		return series.SeriesDetailDTO{}, err
	}
	exists, err := s.sourceExists(ctx, sourceID)
	if err != nil {
		return series.SeriesDetailDTO{}, err
	}
	if !exists {
		return series.SeriesDetailDTO{}, ErrSourceNotFound
	}
	providerName, err := s.rematchProviderName(ctx, sourceID, provider.ProviderName)
	if err != nil {
		return series.SeriesDetailDTO{}, err
	}
	engineRef := sourceengine.ProviderRef{SourceID: sourceID, URL: ref.URL, AddressMode: ref.AddressMode, WebURL: ref.WebURL}
	resolved, err := s.ingest.ResolveProvider(ctx, engineRef, provider.Edges.Series.Title, providerName)
	if err != nil {
		return series.SeriesDetailDTO{}, classifyAttachError(ref.Source, err)
	}
	if len(resolved.Chapters) == 0 {
		return series.SeriesDetailDTO{}, fmt.Errorf("%w: source returned no chapters", ErrSourceUpstream)
	}
	if err := s.applyProviderRematch(ctx, seriesID, providerID, resolved); err != nil {
		return series.SeriesDetailDTO{}, err
	}
	s.fireSeriesConvergence(ctx, seriesID)
	return s.series.GetSeries(ctx, seriesID)
}

func (s *Service) applyProviderRematch(ctx context.Context, seriesID, providerID uuid.UUID, resolved ingest.ProviderReconcileInput) error {
	if !s.acquireMerge(seriesID) {
		return ErrMergeInFlight
	}
	defer s.releaseMerge(seriesID)
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return fmt.Errorf("library.RematchProvider: begin transaction: %w", err)
	}
	if _, err = s.ingest.ReconcileProvider(ctx, tx, providerID, resolved, ingest.ReplaceProviderAddress); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("library.RematchProvider: commit: %w", err)
	}
	return nil
}

func (s *Service) rematchProviderName(ctx context.Context, sourceID int64, stored string) (string, error) {
	if stored != "" || s.sources == nil {
		return stored, nil
	}
	all, err := s.sources.Sources(ctx)
	if err != nil {
		return "", fmt.Errorf("%w: list sources: %w", ErrSourceUpstream, err)
	}
	for _, src := range all {
		if src.ID == sourceID {
			return src.Name, nil
		}
	}
	return "", ErrSourceNotFound
}

func (s *Service) rematchProviderRow(ctx context.Context, seriesID, providerID uuid.UUID) (*ent.SeriesProvider, error) {
	provider, err := s.db.SeriesProvider.Query().Where(seriesprovider.IDEQ(providerID), seriesprovider.SeriesID(seriesID)).WithSeries().Only(ctx)
	if ent.IsNotFound(err) {
		return nil, ErrProviderNotInSeries
	}
	if err != nil {
		return nil, fmt.Errorf("library.RematchProvider: load provider: %w", err)
	}
	return provider, nil
}
