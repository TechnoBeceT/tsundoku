package library

import (
	"errors"
	"sync"

	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/imports"
	"github.com/technobecet/tsundoku/internal/ingest"
	"github.com/technobecet/tsundoku/internal/series"
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
	// ErrSourceNotFound is returned when the Suwayomi source/manga fetch fails
	// (wrapped via errors.Join with the underlying client error).
	ErrSourceNotFound = errors.New("source not found")
	// ErrProviderNotInSeries is returned by MatchDiskProvider when the target
	// SeriesProvider id does not belong to the given series.
	ErrProviderNotInSeries = errors.New("provider does not belong to series")
)

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

	// autoIdentifier fires the Phase-1 native metadata engine's background
	// auto-identify pass after a successful Import (see autoidentify.go). Nil
	// ⇒ no auto-identify (every existing NewService call site is unaffected)
	// — attach it with WithAutoIdentifier.
	autoIdentifier AutoIdentifier
}

// NewService builds a library Service. ingest/imports/series/trigger are
// wired by later tasks (Match/Import) and may be nil/no-op for Scan-only use.
// hub is required — even the synchronous Scan path broadcasts scan.progress
// (see scan.go), so callers that don't care about the SSE stream should still
// pass a live *sse.Hub (broadcasting to zero subscribers is a harmless no-op).
func NewService(db *ent.Client, ingestSvc *ingest.Ingest, importsSvc *imports.Service, seriesSvc *series.Service, trigger func(), storage string, hub *sse.Hub) *Service {
	return &Service{db: db, ingest: ingestSvc, imports: importsSvc, series: seriesSvc, trigger: trigger, storage: storage, hub: hub}
}
