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

		diskSP, liveSP, feedPresent, fErr := s.findDriftedPair(ctx, providers, skippedDisk)
		if fErr != nil {
			return merged, skipped, fErr
		}
		if diskSP == nil {
			return merged, skipped, nil
		}
		if !feedPresent {
			// Every matching linked twin has an empty feed — merging would
			// relabel nothing then delete the disk row, orphaning its chapters.
			// Skip this disk row (remembered so the loop never retries it) and
			// let the owner refresh the source before re-running dedup.
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

// findDriftedPair returns the first actionable (unlinked disk provider, linked
// twin) pair — the disk row's identity name matches the twin's display name
// (providerNameMatches) under the same scanlator — skipping any disk row in
// skip. feedPresent reports whether the chosen twin has a non-empty chapter feed
// (a mergeable pair); among a disk row's matching twins it PREFERS a feed-bearing
// one, only reporting feedPresent=false when ALL of them are empty (so the caller
// skips rather than merging into an unfetched source). Returns a nil disk when no
// unlinked row has any matching twin.
func (s *Service) findDriftedPair(ctx context.Context, providers []*ent.SeriesProvider, skip map[uuid.UUID]bool) (disk, live *ent.SeriesProvider, feedPresent bool, err error) {
	for _, d := range providers {
		if d.SuwayomiID != 0 || skip[d.ID] {
			continue
		}
		twin, feed, pErr := s.pickTwin(ctx, d, providers)
		if pErr != nil {
			return nil, nil, false, pErr
		}
		if twin != nil {
			return d, twin, feed, nil
		}
	}
	return nil, nil, false, nil
}

// pickTwin finds the linked twin to fold the disk row into: among the linked
// providers matching the disk row's name + scanlator it returns the FIRST one
// with a non-empty feed (feedPresent=true); if none has a feed but at least one
// matches, it returns that empty-feed twin with feedPresent=false (the caller
// skips it); (nil, false, nil) when no twin matches at all.
func (s *Service) pickTwin(ctx context.Context, disk *ent.SeriesProvider, providers []*ent.SeriesProvider) (*ent.SeriesProvider, bool, error) {
	var emptyTwin *ent.SeriesProvider
	for _, l := range providers {
		if l.SuwayomiID == 0 || l.Scanlator != disk.Scanlator {
			continue
		}
		if !providerNameMatches(disk.Provider, l.ProviderName) {
			continue
		}
		hasFeed, err := s.providerHasFeed(ctx, l.ID)
		if err != nil {
			return nil, false, err
		}
		if hasFeed {
			return l, true, nil
		}
		if emptyTwin == nil {
			emptyTwin = l
		}
	}
	if emptyTwin != nil {
		return emptyTwin, false, nil
	}
	return nil, false, nil
}

// providerHasFeed reports whether a provider has at least one ProviderChapter
// row (a non-empty availability feed). Shared by merge-at-attach (provider.go)
// and DedupProviders so both gate a merge on the live source actually having
// chapters — merging into an empty feed would orphan the disk chapters (§2 DRY).
func (s *Service) providerHasFeed(ctx context.Context, providerID uuid.UUID) (bool, error) {
	return s.db.ProviderChapter.Query().
		Where(entproviderchapter.SeriesProviderID(providerID)).
		Exist(ctx)
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
