package library

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	"github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/series"
)

// AddProvider attaches a Suwayomi source to an EXISTING series, upgrade-aware.
//
// Algorithm:
//  1. Load the series by id — ErrSeriesNotFound if it does not exist.
//  2. Reject if a SeriesProvider with provider==source AND the same scanlator
//     is already attached — ErrProviderAlreadyPresent (the same source MAY be
//     attached again under a DIFFERENT scanlator; see suwayomi.Ingest.AddSeries).
//  3. Call s.ingest.AddSeries(ctx, source, mangaID, ser.Title, scanlator):
//     AddSeries find-or-creates a Series by slug(title), so passing the
//     EXISTING series' canonical title attaches the new source to THIS series
//     and ingests its chapter feed (new chapters land as wanted). A Suwayomi
//     fetch failure is wrapped as ErrSourceNotFound.
//  4. Set importance on the just-created SeriesProvider(seriesID, source,
//     scanlator) — matched by the full triple (same fix as
//     imports.Service.setImportances) so a second scanlator row for the same
//     source is never mistaken for the first.
//  5. MERGE-AT-ATTACH: if this newly-linked source is really the same physical
//     source as an existing UNLINKED disk-origin provider (its resolved
//     provider_name name-matches the disk row's provider, same scanlator — see
//     matchingUnlinkedDiskProvider), fold the disk group into the live row via
//     mergeDiskIntoLive instead of leaving TWO rows for one source. This
//     re-points the disk-satisfied chapters onto the live source at the
//     requested importance (no re-download) and deletes the drained disk row —
//     preventing the source-identity drift this feature exists to stop. The
//     strict name match means a live source whose provider_name never resolved
//     (empty) is NEVER merged; the ordinary new-row path runs instead.
//  6. Otherwise set importance on the just-created SeriesProvider and let the
//     upgrade engine converge: any on-disk chapter whose satisfied_importance is
//     lower than the new provider's importance is flagged upgrade_available by
//     download.DetectUpgrades on the next cycle and re-downloaded from it.
//  7. Call s.trigger() (if non-nil) to converge immediately, then return the
//     refreshed series.SeriesDetailDTO (§16 round-trip).
func (s *Service) AddProvider(ctx context.Context, seriesID uuid.UUID, source string, mangaID, importance int, scanlator string) (series.SeriesDetailDTO, error) {
	// WithCategory so a merge-at-attach fold (mergeDiskIntoLive → relabelOverlap)
	// can resolve the on-disk series folder <storage>/<Category>/<Title>/.
	ser, err := s.db.Series.Query().
		Where(entseries.IDEQ(seriesID)).
		WithCategory().
		Only(ctx)
	if ent.IsNotFound(err) {
		return series.SeriesDetailDTO{}, ErrSeriesNotFound
	}
	if err != nil {
		return series.SeriesDetailDTO{}, fmt.Errorf("library.AddProvider: get series %s: %w", seriesID, err)
	}

	dup, err := s.db.SeriesProvider.Query().
		Where(seriesprovider.SeriesID(seriesID), seriesprovider.Provider(source), seriesprovider.Scanlator(scanlator)).
		Exist(ctx)
	if err != nil {
		return series.SeriesDetailDTO{}, err
	}
	if dup {
		return series.SeriesDetailDTO{}, ErrProviderAlreadyPresent
	}

	if _, err := s.ingest.AddSeries(ctx, source, mangaID, ser.Title, scanlator); err != nil {
		return series.SeriesDetailDTO{}, errors.Join(ErrSourceNotFound, err)
	}

	sp, err := s.db.SeriesProvider.Query().
		Where(seriesprovider.SeriesID(seriesID), seriesprovider.Provider(source), seriesprovider.Scanlator(scanlator)).
		Only(ctx)
	if err != nil {
		return series.SeriesDetailDTO{}, err
	}

	if err := s.linkAttachedProvider(ctx, ser, sp, importance, scanlator); err != nil {
		return series.SeriesDetailDTO{}, err
	}

	if s.trigger != nil {
		s.trigger()
	}

	return s.series.GetSeries(ctx, seriesID)
}

// linkAttachedProvider finishes an AddProvider attach for the just-ingested live
// row sp: if an existing UNLINKED disk-origin provider is really the same
// physical source (matchingUnlinkedDiskProvider on sp.ProviderName + scanlator)
// AND sp actually ingested a non-empty chapter feed, it folds that disk group
// into sp (merge-at-attach — no re-download, disk row deleted); otherwise it
// just sets the requested importance on sp so the upgrade engine converges
// normally. Either way sp ends up carrying `importance`.
//
// The non-empty-feed condition MIRRORS DedupProviders' guard: merging into a
// live source that returned no chapters for the matched scanlator would relabel
// nothing and then delete the disk row — orphaning the downloaded chapters'
// provenance. In that case the ordinary new-row path runs, so the disk row and
// the (empty) live row coexist with no data loss; a later refresh + dedup can
// fold them once the source actually has chapters.
func (s *Service) linkAttachedProvider(ctx context.Context, ser *ent.Series, sp *ent.SeriesProvider, importance int, scanlator string) error {
	providers, err := s.db.SeriesProvider.Query().
		Where(seriesprovider.SeriesID(ser.ID)).
		All(ctx)
	if err != nil {
		return err
	}
	if diskSP := matchingUnlinkedDiskProvider(providers, sp.ProviderName, scanlator); diskSP != nil {
		hasFeed, err := s.providerHasFeed(ctx, sp.ID)
		if err != nil {
			return err
		}
		if hasFeed {
			_, err = s.mergeDiskIntoLive(ctx, ser, diskSP, sp, importance)
			return err
		}
	}
	_, err = sp.Update().SetImportance(importance).Save(ctx)
	return err
}
