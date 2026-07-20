package sourcepurge

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/ent/predicate"
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	entsourcecircuitstate "github.com/technobecet/tsundoku/internal/ent/sourcecircuitstate"
	entsourcemetric "github.com/technobecet/tsundoku/internal/ent/sourcemetric"
	"github.com/technobecet/tsundoku/internal/series"
)

// sourceProviders selects every SeriesProvider row belonging to one physical
// source. A live-adopted provider stores the numeric source id in `provider`; a
// disk-reconciled provider stores the display NAME instead. Matching
// provider IN {id, name} covers both without over-matching on provider_name —
// which would risk purging a same-named source in another language (see the
// source-identity-drift note in the repo architecture notes). When sourceName
// is "" (not yet resolved) only the id is matched.
func sourceProviders(sourceID, sourceName string) predicate.SeriesProvider {
	if sourceName == "" || sourceName == sourceID {
		return entseriesprovider.Provider(sourceID)
	}
	return entseriesprovider.Or(
		entseriesprovider.Provider(sourceID),
		entseriesprovider.Provider(sourceName),
	)
}

// resolveName fills in a source's display NAME when the caller did not supply it
// (the extension cascade only knows numeric source ids). The name keys the
// circuit-breaker row, so a purge needs it to clear the breaker. It is read from
// the source's SourceMetric row (the denormalized name) first, then any of its
// SeriesProviders' provider_name, returning "" only when nothing knows the name
// (then there is no breaker row to clear anyway).
func (s *Service) resolveName(ctx context.Context, sourceID, given string) string {
	if given != "" {
		return given
	}
	if m, err := s.db.SourceMetric.Query().
		Where(entsourcemetric.SourceIDEQ(sourceID)).Only(ctx); err == nil && m.SourceName != "" {
		return m.SourceName
	}
	if p, err := s.db.SeriesProvider.Query().
		Where(entseriesprovider.Provider(sourceID), entseriesprovider.ProviderNameNEQ("")).
		First(ctx); err == nil {
		return p.ProviderName
	}
	return ""
}

// PreviewSource counts what PurgeSource WOULD remove without mutating anything —
// the figures behind the confirm dialog. It is safe to call repeatedly.
func (s *Service) PreviewSource(ctx context.Context, sourceID, sourceName string) (SourcePreview, error) {
	name := s.resolveName(ctx, sourceID, sourceName)
	providers, err := s.db.SeriesProvider.Query().
		Where(sourceProviders(sourceID, name)).All(ctx)
	if err != nil {
		return SourcePreview{}, fmt.Errorf("sourcepurge.PreviewSource: list providers for %q: %w", sourceID, err)
	}

	providerIDs := make([]uuid.UUID, len(providers))
	removed := make(map[uuid.UUID]struct{}, len(providers))
	seriesSet := make(map[uuid.UUID]struct{}, len(providers))
	for i, p := range providers {
		providerIDs[i] = p.ID
		removed[p.ID] = struct{}{}
		seriesSet[p.SeriesID] = struct{}{}
	}

	feedCount := 0
	if len(providerIDs) > 0 {
		feedCount, err = s.db.ProviderChapter.Query().
			Where(entproviderchapter.SeriesProviderIDIn(providerIDs...)).Count(ctx)
		if err != nil {
			return SourcePreview{}, fmt.Errorf("sourcepurge.PreviewSource: count feed for %q: %w", sourceID, err)
		}
	}

	// Dry-run phantom count: for each affected series, how many never-downloaded,
	// no-CBZ chapters would be left sourceless once the source's providers are gone.
	phantoms := 0
	for seriesID := range seriesSet {
		n, pErr := s.previewPhantoms(ctx, seriesID, removed)
		if pErr != nil {
			return SourcePreview{}, pErr
		}
		phantoms += n
	}

	metricRows, err := s.db.SourceMetric.Query().
		Where(entsourcemetric.SourceIDEQ(sourceID)).Count(ctx)
	if err != nil {
		return SourcePreview{}, fmt.Errorf("sourcepurge.PreviewSource: count metric for %q: %w", sourceID, err)
	}
	breakerRows := 0
	if name != "" {
		breakerRows, err = s.db.SourceCircuitState.Query().
			Where(entsourcecircuitstate.SourceKeyEQ(name)).Count(ctx)
		if err != nil {
			return SourcePreview{}, fmt.Errorf("sourcepurge.PreviewSource: count breaker for %q: %w", name, err)
		}
	}

	return SourcePreview{
		SourceID:         sourceID,
		SourceName:       name,
		SeriesAffected:   len(seriesSet),
		Providers:        len(providers),
		ProviderChapters: feedCount,
		ChaptersDeleted:  phantoms,
		Metrics:          metricRows,
		Breaker:          breakerRows,
	}, nil
}

// previewPhantoms counts, WITHOUT mutating, how many of seriesID's chapters would
// become sourceless phantoms once the providers in `removed` are gone: a chapter
// in a phantom state (series.phantomChapterStates) with no CBZ whose chapter_key
// no SURVIVING provider (a provider not in `removed`) carries. It mirrors
// series.deleteSourcelessPhantoms's rule, but excludes the to-be-removed providers
// in memory rather than reading a post-deletion DB state (nothing is deleted yet).
func (s *Service) previewPhantoms(ctx context.Context, seriesID uuid.UUID, removed map[uuid.UUID]struct{}) (int, error) {
	providers, err := s.db.SeriesProvider.Query().
		Where(entseriesprovider.SeriesID(seriesID)).
		WithProviderChapters().
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("sourcepurge.previewPhantoms: load providers for %s: %w", seriesID, err)
	}
	carried := make(map[string]struct{})
	for _, p := range providers {
		if _, isRemoved := removed[p.ID]; isRemoved {
			continue // this provider is being purged — it will not survive
		}
		for _, pc := range p.Edges.ProviderChapters {
			carried[pc.ChapterKey] = struct{}{}
		}
	}

	candidates, err := s.db.Chapter.Query().
		Where(
			entchapter.SeriesID(seriesID),
			entchapter.StateIn(series.PhantomChapterStates()...),
			entchapter.Filename(""),
		).All(ctx)
	if err != nil {
		return 0, fmt.Errorf("sourcepurge.previewPhantoms: load candidates for %s: %w", seriesID, err)
	}
	count := 0
	for _, ch := range candidates {
		if _, ok := carried[ch.ChapterKey]; !ok {
			count++
		}
	}
	return count, nil
}

// PurgeSource removes every trace of one physical source from Tsundoku's DB:
//
//  1. every SeriesProvider on the source, via the sanctioned
//     series.RemoveProvider cascade (clears satisfied_by keeping the
//     satisfied_importance watermark, deletes the ProviderChapter feed + the
//     SuwayomiSyncState + the SeriesProvider row, clears a dangling
//     metadata_provider_id, AND deletes any chapter it leaves a sourceless
//     phantom) — NO downloaded CBZ and NO downloaded Chapter row is ever deleted;
//  2. the source's advisory SourceMetric row (keyed by numeric id) and
//     SourceCircuitState / breaker row (keyed by name).
//
// The phantom cleanup is NOT re-run here: it lives inside series.RemoveProvider
// (so a plain remove-source cleans phantoms too), which returns the count this
// method sums into ChaptersDeleted.
//
// sourceName may be "" — it is resolved from the metric/provider rows (the
// extension cascade knows only numeric ids). It is best-effort with error
// aggregation: a single provider's removal failure (a DB-level fault) is collected
// and the cascade continues, so one stuck row never strands the rest. The returned
// SourceSummary always reflects what DID happen; the returned error is non-nil
// (errors.Join) when any sub-step failed.
func (s *Service) PurgeSource(ctx context.Context, sourceID, sourceName string) (SourceSummary, error) {
	name := s.resolveName(ctx, sourceID, sourceName)
	providers, err := s.db.SeriesProvider.Query().
		Where(sourceProviders(sourceID, name)).All(ctx)
	if err != nil {
		return SourceSummary{SourceID: sourceID, SourceName: name},
			fmt.Errorf("sourcepurge.PurgeSource: list providers for %q: %w", sourceID, err)
	}

	summary := SourceSummary{SourceID: sourceID, SourceName: name}
	seriesSet := make(map[uuid.UUID]struct{}, len(providers))
	var errs []error

	for _, p := range providers {
		// RemoveProvider deletes the provider AND sweeps the phantoms it orphans,
		// returning how many chapters it deleted.
		deleted, rmErr := s.series.RemoveProvider(ctx, p.SeriesID, p.ID)
		if rmErr != nil {
			errs = append(errs, fmt.Errorf("remove provider %s (series %s): %w", p.ID, p.SeriesID, rmErr))
			continue
		}
		summary.ProvidersRemoved++
		summary.ChaptersDeleted += deleted
		seriesSet[p.SeriesID] = struct{}{}
	}
	summary.SeriesAffected = len(seriesSet)

	// Advisory rows are independent of the providers — always attempt them.
	if n, mErr := s.metrics.Delete(ctx, sourceID); mErr != nil {
		errs = append(errs, mErr)
	} else {
		summary.MetricsDeleted = n
	}
	if name != "" {
		if n, bErr := s.gate.Clear(ctx, name); bErr != nil {
			errs = append(errs, bErr)
		} else {
			summary.BreakerCleared = n
		}
	}

	return summary, errors.Join(errs...)
}
