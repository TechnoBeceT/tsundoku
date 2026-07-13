package library

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/ent/importentry"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	"github.com/technobecet/tsundoku/internal/series"
)

// ErrEntryNotFound is returned by Import when path does not match any
// ImportEntry staged by a prior Scan.
var ErrEntryNotFound = errors.New("import entry not found")

// Import registers one staged ImportEntry's on-disk chapters as already
// downloaded (no re-download), optionally attaching a LIST of owner-matched
// Suwayomi sources, and marks the entry imported.
//
// Algorithm:
//  1. Look up the ImportEntry by path — ErrEntryNotFound if unstaged.
//  2. Round-trip entry.Found (map[string]any) back into a disk.SeriesFacts
//     via storedFacts (the same JSON shape Scan wrote it in).
//  3. disk.ReconcileOne upserts the Series/SeriesProvider/Chapter rows for
//     the disk-only source, marking every chapter downloaded (importance 1,
//     no download work needed — the CBZs already exist).
//  4. Load the just-reconciled Series by slug.
//  5. If matches is non-empty, attach the owner-chosen Suwayomi sources via
//     AddProviders (Slice P) — each lands at an importance strictly below
//     the disk provider's (decision E: gap-fill, never outrank an
//     already-satisfied chapter), so ONLY the disk provider's SeriesDetailDTO
//     round-trip below is skipped in favor of AddProviders' own return,
//     which already re-fetches the series (§16).
//  6. When matches is empty, mark the entry "imported" and call s.trigger()
//     (if non-nil) so the plain disk-only import still converges, then
//     return the refreshed series.SeriesDetailDTO (§16 round-trip).
//
// Idempotent: re-importing the same path re-runs ReconcileOne, which
// find-or-updates the Series by slug (no duplicate row), and re-marks the
// entry imported.
func (s *Service) Import(ctx context.Context, path string, matches []ProviderRef) (series.SeriesDetailDTO, error) {
	entry, err := s.loadEntryByPath(ctx, path)
	if err != nil {
		return series.SeriesDetailDTO{}, err
	}

	sf, err := decodeStoredFacts(entry.Found)
	if err != nil {
		return series.SeriesDetailDTO{}, err
	}

	if _, err := disk.ReconcileOne(ctx, s.db, sf.Facts); err != nil {
		return series.SeriesDetailDTO{}, err
	}
	ser, err := s.db.Series.Query().Where(entseries.Slug(disk.Slugify(sf.Facts.Title))).Only(ctx)
	if err != nil {
		return series.SeriesDetailDTO{}, err
	}

	// Best-effort background rich-metadata identify (spec/metadata-engine-
	// phase1 §4) — fires detached, never delays this response, and applies
	// regardless of which branch below runs (a disk-only import is just as
	// eligible for rich metadata as a matched one). See autoidentify.go.
	s.fireAutoIdentify(ctx, ser.ID)

	if len(matches) > 0 {
		dto, err := s.AddProviders(ctx, ser.ID, matches)
		if err != nil {
			return series.SeriesDetailDTO{}, err
		}
		if err := s.markImported(ctx, entry); err != nil {
			return series.SeriesDetailDTO{}, err
		}
		// AddProviders (via AddProvider) already re-fetched the series after
		// attaching every ref, so its DTO is authoritative — no second
		// round-trip needed.
		return dto, nil
	}

	if err := s.markImported(ctx, entry); err != nil {
		return series.SeriesDetailDTO{}, err
	}
	return s.series.GetSeries(ctx, ser.ID)
}

// markImported flips entry to "imported" and fires s.trigger (if non-nil) —
// the shared tail of both Import branches (§2 DRY), so a matched import and
// a disk-only import agree on when the entry is considered done and when an
// immediate download-cycle convergence is requested.
func (s *Service) markImported(ctx context.Context, entry *ent.ImportEntry) error {
	if _, err := entry.Update().SetStatus(statusImported).Save(ctx); err != nil {
		return err
	}
	if s.trigger != nil {
		s.trigger()
	}
	return nil
}

// loadEntryByPath looks up the staged ImportEntry by path, translating a
// not-found result into the ErrEntryNotFound sentinel.
func (s *Service) loadEntryByPath(ctx context.Context, path string) (*ent.ImportEntry, error) {
	entry, err := s.db.ImportEntry.Query().Where(importentry.Path(path)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, ErrEntryNotFound
	}
	if err != nil {
		return nil, err
	}
	return entry, nil
}

// decodeStoredFacts round-trips an ImportEntry.found blob (map[string]any,
// as stored by Scan's foundBlob) back into the concrete storedFacts shape.
func decodeStoredFacts(found map[string]any) (storedFacts, error) {
	var sf storedFacts
	raw, err := json.Marshal(found)
	if err != nil {
		return storedFacts{}, err
	}
	if err := json.Unmarshal(raw, &sf); err != nil {
		return storedFacts{}, err
	}
	return sf, nil
}
