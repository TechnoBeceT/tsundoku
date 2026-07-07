package library

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/series"
)

// SeriesDetail returns the refreshed series-detail DTO for id. The dedup handler
// renders this after DedupProviders (which returns only the merged/skipped
// counts) so the response still carries the up-to-date series shape (§16
// round-trip). It is a thin delegate to the underlying series read service.
func (s *Service) SeriesDetail(ctx context.Context, id uuid.UUID) (series.SeriesDetailDTO, error) {
	return s.series.GetSeries(ctx, id)
}

// DedupProviders is the owner-triggered cleanup for source-identity drift that
// ALREADY happened (before merge-at-attach existed): a series carrying both an
// unlinked disk-origin provider AND a real linked provider that are actually the
// same physical source (same display name + scanlator). It folds every such disk
// row into its linked twin via mergeDiskIntoLive — the same no-redownload engine
// Match uses — and reports how many pairs it merged and how many it skipped.
//
// A pair is SKIPPED (not merged) when the linked twin has an EMPTY
// ProviderChapter feed: merging into an unfetched source would relabel nothing
// and then delete the disk row, orphaning the disk chapters. The owner should
// let that source fetch (or refresh) first, then re-run dedup.
//
// The provider set is re-loaded each pass because a merge deletes the disk row
// and re-points chapters. Idempotent: with no drifted pairs it returns (0, 0)
// and changes nothing. trigger() fires only when at least one merge happened.
// ErrSeriesNotFound is returned for an unknown series id.
func (s *Service) DedupProviders(ctx context.Context, seriesID uuid.UUID) (merged, skipped int, err error) {
	// WithCategory so mergeDiskIntoLive → relabelOverlap can resolve the on-disk
	// series folder. The category never changes across merges, so this single
	// load is reused for every pass inside dedupDriftedPairs.
	row, err := s.db.Series.Query().
		Where(entseries.IDEQ(seriesID)).
		WithCategory().
		Only(ctx)
	if ent.IsNotFound(err) {
		return 0, 0, ErrSeriesNotFound
	}
	if err != nil {
		return 0, 0, fmt.Errorf("library.DedupProviders: load series %s: %w", seriesID, err)
	}

	merged, skipped, err = s.dedupDriftedPairs(ctx, row)
	if err != nil {
		return merged, skipped, err
	}

	if merged > 0 && s.trigger != nil {
		s.trigger()
	}
	return merged, skipped, nil
}

// dedupDriftedPairs folds every drifted (disk, linked-twin) pair of a loaded
// series into one row, one pair per pass. It re-loads the provider set each pass
// because a merge deletes the disk row and re-points chapters. A pair whose
// linked twin has an EMPTY feed is skipped and remembered so the loop never
// retries it (merging into an unfetched source would orphan the disk chapters).
func (s *Service) dedupDriftedPairs(ctx context.Context, row *ent.Series) (merged, skipped int, err error) {
	skippedDisk := map[uuid.UUID]bool{}
	for {
		providers, qErr := s.db.SeriesProvider.Query().
			Where(entseriesprovider.SeriesID(row.ID)).
			All(ctx)
		if qErr != nil {
			return merged, skipped, fmt.Errorf("library.DedupProviders: load providers: %w", qErr)
		}

		diskSP, liveSP := findDriftedPair(providers, skippedDisk)
		if diskSP == nil {
			return merged, skipped, nil
		}

		hasFeed, fErr := s.db.ProviderChapter.Query().
			Where(entproviderchapter.SeriesProviderID(liveSP.ID)).
			Exist(ctx)
		if fErr != nil {
			return merged, skipped, fmt.Errorf("library.DedupProviders: check feed: %w", fErr)
		}
		if !hasFeed {
			skippedDisk[diskSP.ID] = true
			skipped++
			continue
		}

		if _, mErr := s.mergeDiskIntoLive(ctx, row, diskSP, liveSP, max(liveSP.Importance, diskSP.Importance)); mErr != nil {
			return merged, skipped, mErr
		}
		merged++
	}
}

// findDriftedPair returns the first (unlinked disk provider, linked twin) pair
// in providers that are the same physical source — the disk row's identity name
// matches the linked row's display name (providerNameMatches) under the same
// scanlator — skipping any disk row in skip. Returns (nil, nil) when none drift.
func findDriftedPair(providers []*ent.SeriesProvider, skip map[uuid.UUID]bool) (disk, live *ent.SeriesProvider) {
	for _, d := range providers {
		if d.SuwayomiID != 0 || skip[d.ID] {
			continue
		}
		for _, l := range providers {
			if l.SuwayomiID == 0 || l.Scanlator != d.Scanlator {
				continue
			}
			if providerNameMatches(d.Provider, l.ProviderName) {
				return d, l
			}
		}
	}
	return nil, nil
}

// providerNameMatches reports whether a disk-origin provider's identity name
// and a live source's resolved display name refer to the same physical source.
// The comparison is case-insensitive and trims surrounding whitespace; two
// blank names never match (an empty display name is "unknown", not a wildcard),
// so a live source whose provider_name was never resolved is never merged.
func providerNameMatches(diskProviderName, liveDisplayName string) bool {
	a := strings.TrimSpace(diskProviderName)
	b := strings.TrimSpace(liveDisplayName)
	if a == "" || b == "" {
		return false
	}
	return strings.EqualFold(a, b)
}

// matchingUnlinkedDiskProvider returns the unlinked disk-origin provider
// (suwayomi_id == 0) in providers whose identity name matches liveDisplayName
// (providerNameMatches) AND whose scanlator equals scanlator, or nil when none
// qualifies. This is how a disk import — which stores the display NAME in the
// provider field (suwayomi_id == 0) — is recognised as the same physical source
// a live ingest just attached, which stores the numeric source id in provider
// and the display name in provider_name (suwayomi_id != 0). Matching lets the
// two be folded into one row instead of drifting apart (see mergeDiskIntoLive).
func matchingUnlinkedDiskProvider(providers []*ent.SeriesProvider, liveDisplayName, scanlator string) *ent.SeriesProvider {
	for _, p := range providers {
		if p.SuwayomiID != 0 {
			continue
		}
		if p.Scanlator != scanlator {
			continue
		}
		if providerNameMatches(p.Provider, liveDisplayName) {
			return p
		}
	}
	return nil
}
