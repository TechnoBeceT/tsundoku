package library

import (
	"errors"

	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/imports"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// Sentinel errors returned by AddProvider (provider.go).
var (
	// ErrSeriesNotFound is returned when the target series id does not exist.
	ErrSeriesNotFound = errors.New("series not found")
	// ErrProviderAlreadyPresent is returned when the series already has a
	// SeriesProvider row for the requested source.
	ErrProviderAlreadyPresent = errors.New("provider already attached to series")
	// ErrSourceNotFound is returned when the Suwayomi source/manga fetch fails
	// (wrapped via errors.Join with the underlying client error).
	ErrSourceNotFound = errors.New("source not found")
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
// + importing them against a Suwayomi source without re-downloading.
type Service struct {
	db      *ent.Client
	ingest  *suwayomi.Ingest
	imports *imports.Service
	series  *series.Service
	trigger func()
	storage string
}

// NewService builds a library Service. ingest/imports/series/trigger are
// wired by later tasks (Match/Import) and may be nil/no-op for Scan-only use.
func NewService(db *ent.Client, ingest *suwayomi.Ingest, importsSvc *imports.Service, seriesSvc *series.Service, trigger func(), storage string) *Service {
	return &Service{db: db, ingest: ingest, imports: importsSvc, series: seriesSvc, trigger: trigger, storage: storage}
}
