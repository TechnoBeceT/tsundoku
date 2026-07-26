package library

import (
	"context"
	"fmt"
	"strconv"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	"github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/series"
)

// parseSourceID parses the wire-form (stringified) engine-host source id back
// to the numeric id ingest.Ingest expects. Shared by AddProvider and
// attachRealSource (match_disk_provider.go) — the ONE place a malformed
// source string is translated to a caller-facing error, wrapped by the
// caller with ErrSourceNotFound (a source id that doesn't even parse can
// never resolve to a real source, so it is treated the same as "not found").
func parseSourceID(source string) (int64, error) {
	id, err := strconv.ParseInt(source, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid source id %q: %w", source, err)
	}
	return id, nil
}

// AddProvider attaches an engine-host source to an EXISTING series, upgrade-aware.
//
// Algorithm:
//  1. Load the series by id — ErrSeriesNotFound if it does not exist.
//  2. Reject if a SeriesProvider with provider==source AND the same scanlator
//     is already attached — ErrProviderAlreadyPresent (the same source MAY be
//     attached again under a DIFFERENT scanlator; see ingest.Ingest.AddSeries).
//  3. Verify sourceID is a real, loaded source via s.sourceExists (a true
//     membership check) — ErrSourceNotFound (404) ONLY on a genuine miss.
//     Then call s.ingest.AddSeriesUngated(ctx, source, url, ser.Title,
//     scanlator): the UNGATED variant, since a deliberate one-shot owner click
//     must not be refused by the anti-ban circuit-breaker that throttles bulk
//     sweeps. AddSeriesUngated find-or-creates a Series by slug(title), so
//     passing the EXISTING series' canonical title attaches the new source to
//     THIS series and ingests its chapter feed (new chapters land as wanted). A
//     fetch failure is classified honestly by classifyAttachError —
//     ErrSourceUnavailable (503) when the source is cooled down, else
//     ErrSourceUpstream (502) — NEVER a phantom ErrSourceNotFound.
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
//     (empty) is NEVER merged; the ordinary new-row path runs instead. That fold
//     runs under the per-series merge latch and yields ErrMergeInFlight (→ 409)
//     when another merge holds it (GAP-122) — see linkAttachedProvider.
//  6. Otherwise set importance on the just-created SeriesProvider and let the
//     upgrade engine converge: any on-disk chapter whose satisfied_importance is
//     lower than the new provider's importance is flagged upgrade_available by
//     download.DetectUpgrades on the next cycle and re-downloaded from it.
//  7. Call s.trigger() (if non-nil) to converge immediately, then return the
//     refreshed series.SeriesDetailDTO (§16 round-trip).
//
// source is the engine-host source ID, stringified (parsed to int64 before
// the ingest call); url is the source-relative manga URL (P2 Suwayomi-removal,
// slice 3b — this replaces the retired mangaID int parameter).
func (s *Service) AddProvider(ctx context.Context, seriesID uuid.UUID, source string, url string, importance int, scanlator string) (series.SeriesDetailDTO, error) {
	// Collapse the scanlator to "" when the source is flagged ignore-scanlator, so
	// the whole attach — the duplicate guard, the ingest, and the post-ingest
	// SeriesProvider lookup — agrees on a single [Source] provider key (see
	// ingest.Ingest.EffectiveScanlator; matching a stale per-uploader scanlator
	// would otherwise miss the collapsed row ingest creates). A non-numeric source
	// is left unchanged — resolveAndIngestSource surfaces the real 404.
	if sourceID, perr := parseSourceID(source); perr == nil {
		scanlator = s.ingest.EffectiveScanlator(ctx, sourceID, scanlator)
	}

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

	// Membership check + UNGATED owner-attach ingest with honest error taxonomy
	// (true 404 only on a real miss; 503 cooled-down / 502 upstream on a fetch
	// failure) — see resolveAndIngestSource.
	if _, err := s.resolveAndIngestSource(ctx, source, url, ser.Title, scanlator); err != nil {
		return series.SeriesDetailDTO{}, err
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

	// Immediate whole-library download trigger PLUS the per-series instant
	// refresh+detect layer (GAP-113): the newly attached, possibly higher-importance
	// source should download its backlog and supersede existing downloads now, not at
	// the 2h sweep. Both are nil-guarded no-ops when unwired.
	s.fireSeriesConvergence(ctx, seriesID)

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
//
// # The fold — and ONLY the fold — runs under the per-series merge latch (GAP-122)
//
// The fold is a mergeDiskIntoLive call like every other, so it must be mutually
// exclusive with them: two concurrent merges over one series corrupt it (the
// loser's relabels are idempotent, so it proceeds to a commitMatch that fails on
// the already-deleted disk row and then renames every CBZ BACK, out from under the
// winner's committed rows). Merge-at-attach was the last owner path that took no
// latch, and its window is the widest of all — it opens the moment the ingest
// commits the live twin's feed (exactly what makes the series heal-eligible) and
// stays open for the whole multi-minute relabel, while the unattended self-heal
// (GAP-120) merges after every refresh sweep.
//
// The latch is taken around the MERGE BRANCH ONLY, never around the attach: a
// series with no drifted disk row (the overwhelmingly common case) never touches
// the latch, so attaching a source is unaffected by any merge in flight.
//
// A busy series yields ErrMergeInFlight (→ 409) rather than silently falling back
// to the plain importance-set path. Skipping the fold quietly would be actively
// unsafe here: the live twin now HAS a feed, so raising it to the requested
// importance above the disk chapters' watermark is exactly what arms
// download.DetectUpgrades to re-download the entire imported series. Failing loud
// leaves the fresh row parked at ingest's importance 0 — the reserved park
// sentinel, <= every watermark, so nothing re-downloads — with the disk row
// intact, which the self-heal folds on the next sweep.
func (s *Service) linkAttachedProvider(ctx context.Context, ser *ent.Series, sp *ent.SeriesProvider, importance int, scanlator string) error {
	twin, err := s.foldableDiskTwin(ctx, ser.ID, sp, scanlator)
	if err != nil {
		return err
	}
	if twin == nil {
		return setProviderImportance(ctx, sp, importance)
	}
	if !s.acquireMerge(ser.ID) {
		return ErrMergeInFlight
	}
	defer s.releaseMerge(ser.ID)
	return s.foldDiskTwinLocked(ctx, ser, sp, importance, scanlator)
}

// foldDiskTwinLocked performs merge-at-attach with the series' merge latch
// ALREADY HELD by the caller (linkAttachedProvider is the only way in).
//
// It RE-RESOLVES the twin rather than trusting the probe that decided to take the
// latch: that probe ran outside the latch, so a merge which landed in between may
// already have folded and DELETED the row. Merging a stale row would relabel
// nothing (its chapters are re-pointed) and then fail in commitMatch on the
// missing row — a pointless hard error on an attach that actually succeeded. A
// twin that vanished simply means the fold is done, so the ordinary
// importance-set path runs.
func (s *Service) foldDiskTwinLocked(ctx context.Context, ser *ent.Series, sp *ent.SeriesProvider, importance int, scanlator string) error {
	twin, err := s.foldableDiskTwin(ctx, ser.ID, sp, scanlator)
	if err != nil {
		return err
	}
	if twin == nil {
		return setProviderImportance(ctx, sp, importance)
	}
	_, err = s.mergeDiskIntoLive(ctx, ser, twin, sp, importance)
	return err
}

// foldableDiskTwin returns the unlinked disk-origin provider that the
// just-ingested live row sp should absorb, or nil when there is nothing to fold —
// either no disk row names the same physical source under the same scanlator, or
// sp's own chapter feed came back EMPTY (the shared providerHasFeed orphan guard:
// folding into a source with no chapters would relabel nothing and then drain the
// disk row). Both callers of the merge branch resolve the twin through this one
// helper, so the probe and the re-check under the latch can never disagree.
func (s *Service) foldableDiskTwin(ctx context.Context, seriesID uuid.UUID, sp *ent.SeriesProvider, scanlator string) (*ent.SeriesProvider, error) {
	providers, err := s.db.SeriesProvider.Query().
		Where(seriesprovider.SeriesID(seriesID)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	twin := matchingUnlinkedDiskProvider(providers, sp.ProviderName, scanlator)
	if twin == nil {
		return nil, nil
	}
	hasFeed, err := s.providerHasFeed(ctx, sp.ID)
	if err != nil {
		return nil, err
	}
	if !hasFeed {
		return nil, nil
	}
	return twin, nil
}

// setProviderImportance writes the owner's requested importance onto a
// just-attached provider — the non-merge outcome of linkAttachedProvider, where
// the upgrade engine converges the series normally.
func setProviderImportance(ctx context.Context, sp *ent.SeriesProvider, importance int) error {
	return sp.Update().SetImportance(importance).Exec(ctx)
}
