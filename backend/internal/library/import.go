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
// downloaded (no re-download), optionally attaching an owner-matched
// Suwayomi source, and marks the entry imported.
//
// Algorithm:
//  1. Look up the ImportEntry by path — ErrEntryNotFound if unstaged.
//  2. Round-trip entry.Found (map[string]any) back into a disk.SeriesFacts
//     via storedFacts (the same JSON shape Scan wrote it in).
//  3. disk.ReconcileOne upserts the Series/SeriesProvider/Chapter rows for
//     the disk-only source, marking every chapter downloaded (importance 1,
//     no download work needed — the CBZs already exist).
//  4. Load the just-reconciled Series by slug.
//  5. If match is non-nil, attach the owner-chosen Suwayomi source via
//     AddProvider (Task 5) — this ranks the disk copy against a live feed
//     and can flag upgrades on the next dispatch cycle.
//  6. Mark the entry "imported" and call s.trigger() (if non-nil) so a
//     freshly-attached source converges immediately.
//  7. Return the refreshed series.SeriesDetailDTO (§16 round-trip).
//
// Idempotent: re-importing the same path re-runs ReconcileOne, which
// find-or-updates the Series by slug (no duplicate row), and re-marks the
// entry imported.
func (s *Service) Import(ctx context.Context, path string, match *MatchInput) (series.SeriesDetailDTO, error) {
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

	if match != nil {
		// MatchInput carries no scanlator field (out of scope for this pass —
		// see library.Service.AddProvider); "" attaches the whole source, all
		// scanlators, matching the pre-existing behavior of this path.
		if _, err := s.AddProvider(ctx, ser.ID, match.Source, match.MangaID, match.Importance, ""); err != nil {
			return series.SeriesDetailDTO{}, err
		}
	}

	if _, err := entry.Update().SetStatus(statusImported).Save(ctx); err != nil {
		return series.SeriesDetailDTO{}, err
	}
	if s.trigger != nil {
		s.trigger()
	}
	return s.series.GetSeries(ctx, ser.ID)
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
