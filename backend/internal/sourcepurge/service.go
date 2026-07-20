// Package sourcepurge removes ALL of Tsundoku's DB footprint for a physical
// source (or an extension's sources) in one owner-initiated cascade.
//
// Uninstalling an extension used to strand every trace of its source(s) in
// Tsundoku: dangling SeriesProviders, the source's per-chapter feed, its
// SourceMetric row (Sources pane), its SourceCircuitState row (breaker), and any
// chapter left pinned failed/permanently_failed because that source was its only
// carrier. The owner had to clean these up series-by-series by hand. This package
// is the one cascade that does it honestly.
//
// It NEVER deletes a CBZ or a Chapter row — the never-auto-delete invariant is
// load-bearing here (a purge that erased downloaded files would be a
// catastrophe). The ONLY rows it deletes are the exact same set
// series.RemoveProvider already deletes (SeriesProvider + ProviderChapter +
// SuwayomiSyncState) plus the source's advisory SourceMetric + SourceCircuitState
// rows. Every downloaded file stays on disk and can be re-attributed later by
// re-adopting/reconciling the source.
//
// The per-provider work is delegated to the sanctioned series.RemoveProvider
// primitive (not re-implemented), so the FK-safe ordering + satisfied_importance
// watermark handling live in exactly one place.
//
// The pkgName→source-ids map for the extension cascade comes from the DURABLE
// enginetopo store (HarvestedExtension.source_ids), NOT a live engine call: a
// purge frequently runs when the extension is already uninstalled, so the engine
// can no longer report its sources — only Tsundoku's own durable record can.
package sourcepurge

import (
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/metrics"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sourcegate"
)

// Service purges a source's (or an extension's sources') Tsundoku DB state. It
// composes the sanctioned per-provider removal (series.RemoveProvider), the
// metric-row and breaker-row deletions, and reads the durable HarvestedExtension
// store directly (via the Ent client) for the extension→sources map.
type Service struct {
	db      *ent.Client
	series  *series.Service
	metrics *metrics.Service
	gate    *sourcegate.Service
}

// NewService builds a sourcepurge Service over the Ent client and the
// collaborators whose primitives it reuses: the series service (per-provider
// removal) and the metrics + breaker services (their row-deleting Delete/Clear).
func NewService(db *ent.Client, seriesSvc *series.Service, metricsSvc *metrics.Service, gate *sourcegate.Service) *Service {
	return &Service{db: db, series: seriesSvc, metrics: metricsSvc, gate: gate}
}

// SourceSummary reports what a source purge actually removed. Every count is of
// Tsundoku DB rows ONLY — never a CBZ file or a downloaded Chapter row
// (never-auto-delete). ChaptersDeleted counts the sourceless PHANTOM chapters the
// purge cleaned up (never-downloaded, no-CBZ rows no remaining source can supply —
// deleted by series.RemoveProvider's phantom sweep). It is a domain result; the
// HTTP wire shape is handler/engine's DTO.
type SourceSummary struct {
	SourceID         string
	SourceName       string
	SeriesAffected   int
	ProvidersRemoved int
	ChaptersDeleted  int
	MetricsDeleted   int
	BreakerCleared   int
}

// SourcePreview reports what a source purge WOULD remove — the dry run that backs
// the confirm dialog. It performs no mutation. Providers is the SeriesProvider
// row count; ProviderChapters is the source's total feed rows across those
// providers (the "chapters this source supplies" figure); ChaptersDeleted is the
// number of sourceless phantom chapters the purge would clean up.
type SourcePreview struct {
	SourceID         string
	SourceName       string
	SeriesAffected   int
	Providers        int
	ProviderChapters int
	ChaptersDeleted  int
	Metrics          int
	Breaker          int
}

// ExtensionSummary aggregates a PurgeExtension across every source the extension
// provides (one per language). The totals are SUMMED across the extension's
// sources (a series that carried two of the extension's language-sources counts
// once per source). Errors carries any per-source failure text — a failing
// source never aborts the others (fault isolation).
type ExtensionSummary struct {
	PkgName          string
	Sources          []SourceSummary
	SeriesAffected   int
	ProvidersRemoved int
	ChaptersDeleted  int
	MetricsDeleted   int
	BreakerCleared   int
	Errors           []string
}

// ExtensionPreview aggregates the dry-run counts across every source an
// extension provides — the confirm-dialog figures for an extension-level purge.
type ExtensionPreview struct {
	PkgName          string
	Sources          []SourcePreview
	SeriesAffected   int
	Providers        int
	ProviderChapters int
	ChaptersDeleted  int
	Metrics          int
	Breaker          int
}
