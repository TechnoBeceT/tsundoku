package library

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/imports"
	"github.com/technobecet/tsundoku/internal/ingest"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sse"
)

// Sentinel errors returned by AddProvider (provider.go) and MatchDiskProvider
// (match_disk_provider.go).
var (
	// ErrSeriesNotFound is returned when the target series id does not exist.
	ErrSeriesNotFound = errors.New("series not found")
	// ErrProviderAlreadyPresent is returned when the series already has a
	// SeriesProvider row for the requested source.
	ErrProviderAlreadyPresent = errors.New("provider already attached to series")
	// ErrSourceNotFound is returned ONLY on a TRUE membership miss: the requested
	// source id does not even parse, or it is absent from the engine host's live
	// Sources() list. It maps to 404. It is NEVER used for an ingest/upstream fetch
	// failure any more (that was the phantom-404 bug — a cooled-down or
	// transiently-failing source was mislabelled "source not found"); those now
	// surface as ErrSourceUnavailable (503) or ErrSourceUpstream (502).
	ErrSourceNotFound = errors.New("source not found")
	// ErrSourceUnavailable is returned by AddProvider/MatchDiskProvider when the
	// requested source's circuit-breaker is in cooldown (ingest.ErrSourceCooledDown).
	// It maps to 503: the source really exists, it is just temporarily throttled,
	// so the owner should retry shortly. (In practice the owner attach uses the
	// UNGATED ingest path — AddSeriesUngated — which bypasses the cooldown, so this
	// is a defence-in-depth mapping rather than a routinely-hit branch.)
	ErrSourceUnavailable = errors.New("source temporarily unavailable, retry shortly")
	// ErrSourceUpstream is returned when the engine-host fetch itself fails (a
	// transport error, a source outage, or a Sources() read failure) — a genuine
	// gateway failure, distinct from a source that does not exist. It maps to 502
	// so the real reason surfaces instead of a lying 404.
	ErrSourceUpstream = errors.New("source fetch failed")
	// ErrProviderNotInSeries is returned by MatchDiskProvider when the target
	// SeriesProvider id does not belong to the given series.
	ErrProviderNotInSeries = errors.New("provider does not belong to series")
	// ErrTargetNoFeed is returned by ConsolidateProviders when the chosen
	// EXISTING target provider has an empty ProviderChapter feed. Merging the
	// selected disk providers into a feed-less target would relabel nothing and
	// then drain the disk rows — orphaning their downloaded chapters (the exact
	// guard DedupProviders/linkAttachedProvider already apply to a merge target).
	// It maps to 409: the request is well-formed but the target is not ready
	// (refresh it, then retry).
	ErrTargetNoFeed = errors.New("target provider has no chapter feed")
	// ErrMergeInFlight is returned by DedupProviders and by AddProvider's
	// merge-at-attach when another merge on the SAME series already holds the
	// per-series merge single-flight latch (an async Match, a Consolidation, the
	// library-wide sweep, the ignore-scanlator collapse, or the unattended
	// self-heal). The fold is SKIPPED, not queued and not blocked — nothing at
	// all was touched — so the caller can simply retry once the in-flight merge
	// lands. It maps to 409, matching the 409 the Match/Consolidate starts
	// already return for the same latch (GAP-120, GAP-122). Reporting it as an
	// error is deliberate: for dedup, a 200 carrying merged=0/skipped=0 would be
	// indistinguishable from "there was nothing to merge", the opposite
	// conclusion; for an attach, quietly skipping the fold would raise the live
	// twin above the disk chapters' watermark and re-download the whole imported
	// series.
	ErrMergeInFlight = errors.New("a merge is already running for this series, retry shortly")
)

// SourceLister lists the engine host's currently-loaded sources. AddProvider and
// MatchDiskProvider (the owner attach/match paths) use it to verify a requested
// source id really exists BEFORE ingesting it — a true membership check, distinct
// from an upstream fetch failure. sourceengine.Client satisfies it directly, so
// production wires the same engineClient threaded everywhere else (see
// WithSourceLister). A nil lister (the narrow test constructor) skips the
// membership check — the ingest fetch itself remains the real gate, mirroring the
// package's nil-gate/nil-cache seams.
type SourceLister interface {
	Sources(ctx context.Context) ([]sourceengine.Source, error)
}

// Staging statuses for ImportEntry.status — the single source of truth so
// Scan/List/Import/Skip never disagree on the literal spelling (§2 DRY).
const (
	statusPending  = "pending"
	statusImported = "imported"
	statusSkipped  = "skipped"
)

// Service implements the on-disk library-import workflow: scanning storage,
// staging found series into ImportEntry rows, and (in later tasks) matching
// + importing them against an engine-host source without re-downloading.
type Service struct {
	db      *ent.Client
	ingest  *ingest.Ingest
	imports *imports.Service
	series  *series.Service
	trigger func()
	storage string
	hub     *sse.Hub

	// scanMu guards scanning, the single-flight latch consumed by StartScan
	// (scanjob.go): only one background scan may run at a time, so a
	// double-click on "Scan" can't launch two concurrent NFS walks.
	scanMu   sync.Mutex
	scanning bool

	// mergeMu guards mergeRunning, the per-SERIES in-flight set SHARED by EVERY
	// path that folds CBZs through mergeDiskIntoLive — the single Match, the
	// multi-provider consolidation, the dedup core (owner endpoint, library-wide
	// sweep, unattended self-heal), merge-at-attach and the ignore-scanlator
	// collapse (GAP-122; mergeDiskIntoLive's doc enumerates all five). Two
	// concurrent folds leave a series' CBZs where the DB is not looking, and a
	// consolidation's finaliseSurvivorRanks rewrites EVERY provider's importance —
	// including one a concurrent Match DB-parked at 0 for its relabel window, which
	// would re-arm a re-download mid-window (QCAT-295 review). Keying the guard by
	// SERIES makes them all MUTUALLY EXCLUSIVE per series while leaving different
	// series fully concurrent. Lazily initialised under the lock so every
	// NewService call site is unaffected.
	mergeMu      sync.Mutex
	mergeRunning map[uuid.UUID]struct{}

	// autoIdentifier fires the Phase-1 native metadata engine's background
	// auto-identify pass after a successful Import (see autoidentify.go). Nil
	// ⇒ no auto-identify (every existing NewService call site is unaffected)
	// — attach it with WithAutoIdentifier.
	autoIdentifier AutoIdentifier

	// sources lists the engine host's loaded sources so AddProvider /
	// MatchDiskProvider can verify a requested source id exists before ingesting
	// it (a true membership check). Nil ⇒ the check is skipped (see SourceLister)
	// — attach it with WithSourceLister; production always wires it.
	sources SourceLister

	// seriesSync fires the per-series instant refresh+detect layer (GAP-113) after a
	// successful AddProvider, so a newly attached source's chapters download / upgrade
	// immediately instead of at the next whole-library sweep. Nil ⇒ the scoped layer
	// is skipped (existing NewService call sites unaffected) — attach it with
	// WithSeriesSync.
	seriesSync SeriesSyncer
}

// SeriesSyncer runs the per-series instant refresh+detect convergence for one
// series. *seriessync.Orchestrator satisfies it. A local interface so library never
// imports seriessync, and nil is valid (the scoped layer is skipped).
type SeriesSyncer interface {
	SyncSeries(ctx context.Context, seriesID uuid.UUID)
}

// WithSeriesSync attaches the per-series instant convergence orchestrator (GAP-113),
// fired after a successful AddProvider. Returns the receiver for chaining off
// NewService. A nil syncer leaves the base behaviour unchanged.
func (s *Service) WithSeriesSync(sync SeriesSyncer) *Service {
	s.seriesSync = sync
	return s
}

// fireSeriesConvergence kicks the post-mutation convergence for seriesID: the
// immediate whole-library download trigger AND the per-series instant refresh+detect
// layer (GAP-113). Both are nil-guarded no-ops when their dependency is unwired.
// Extracted so a caller's own control flow (e.g. AddProvider) stays within the
// cyclomatic-complexity budget.
func (s *Service) fireSeriesConvergence(ctx context.Context, seriesID uuid.UUID) {
	if s.trigger != nil {
		s.trigger()
	}
	if s.seriesSync != nil {
		s.seriesSync.SyncSeries(ctx, seriesID)
	}
}

// WithSourceLister attaches the engine-host source lister used by AddProvider /
// MatchDiskProvider to verify a requested source id really exists (a true
// membership check → ErrSourceNotFound / 404) before attempting the ingest.
// Returns the receiver for chaining (mirrors WithAutoIdentifier). Without it the
// membership check is skipped (nil-lister seam) — production wires the shared
// engineClient.
func (s *Service) WithSourceLister(lister SourceLister) *Service {
	s.sources = lister
	return s
}

// sourceExists reports whether sourceID is among the engine host's
// currently-loaded sources. A nil lister returns true (membership check skipped —
// see SourceLister). A Sources() read failure is surfaced as ErrSourceUpstream
// (the engine host is unreachable — a gateway failure, never a "source not
// found"), so it maps to 502, not a lying 404.
func (s *Service) sourceExists(ctx context.Context, sourceID int64) (bool, error) {
	if s.sources == nil {
		return true, nil
	}
	all, err := s.sources.Sources(ctx)
	if err != nil {
		return false, fmt.Errorf("%w: list sources: %w", ErrSourceUpstream, err)
	}
	for _, src := range all {
		if src.ID == sourceID {
			return true, nil
		}
	}
	return false, nil
}

// resolveAndIngestSource is the shared owner-attach prelude for AddProvider and
// attachRealSource (§2 DRY — both had an identical block): parse the source id
// (ErrSourceNotFound on a non-numeric id — it can never resolve to a real
// source), verify it is a currently-loaded source (ErrSourceNotFound on a TRUE
// membership miss, ErrSourceUpstream if the engine host is unreachable), then
// ingest its feed via the UNGATED path (a deliberate one-shot owner click must
// bypass the anti-ban circuit-breaker that throttles bulk sweeps), classifying
// any fetch failure honestly (ErrSourceUnavailable / ErrSourceUpstream). It never
// returns the old phantom ErrSourceNotFound for a mere fetch failure. Returns the
// parsed numeric source id on success.
func (s *Service) resolveAndIngestSource(ctx context.Context, source, url, title, scanlator string) (int64, error) {
	sourceID, err := parseSourceID(source)
	if err != nil {
		return 0, errors.Join(ErrSourceNotFound, err)
	}
	exists, err := s.sourceExists(ctx, sourceID)
	if err != nil {
		return 0, err // ErrSourceUpstream (502) — engine host unreachable.
	}
	if !exists {
		return 0, ErrSourceNotFound // true miss (404).
	}
	if _, err := s.ingest.AddSeriesUngated(ctx, sourceID, url, title, scanlator); err != nil {
		return 0, classifyAttachError(source, err)
	}
	return sourceID, nil
}

// classifyAttachError maps an ingest.AddSeriesUngated failure to the honest
// library-level sentinel — replacing the old blanket errors.Join(ErrSourceNotFound,
// err) that produced the phantom 404. A cooled-down source becomes
// ErrSourceUnavailable (503, source id named); any other fetch/upstream failure
// becomes ErrSourceUpstream (502, real cause preserved via %w so httperr.Upstream
// surfaces it). It is NEVER ErrSourceNotFound — a membership miss is caught before
// the ingest is ever attempted.
func classifyAttachError(source string, err error) error {
	if errors.Is(err, ingest.ErrSourceCooledDown) {
		return fmt.Errorf("%w (source %s)", ErrSourceUnavailable, source)
	}
	return fmt.Errorf("%w: %w", ErrSourceUpstream, err)
}

// NewService builds a library Service. ingest/imports/series/trigger are
// wired by later tasks (Match/Import) and may be nil/no-op for Scan-only use.
// hub is required — even the synchronous Scan path broadcasts scan.progress
// (see scan.go), so callers that don't care about the SSE stream should still
// pass a live *sse.Hub (broadcasting to zero subscribers is a harmless no-op).
func NewService(db *ent.Client, ingestSvc *ingest.Ingest, importsSvc *imports.Service, seriesSvc *series.Service, trigger func(), storage string, hub *sse.Hub) *Service {
	return &Service{db: db, ingest: ingestSvc, imports: importsSvc, series: seriesSvc, trigger: trigger, storage: storage, hub: hub}
}
