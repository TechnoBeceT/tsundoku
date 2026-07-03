package library

import (
	"context"
	"encoding/json"

	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/ent/importentry"
	"github.com/technobecet/tsundoku/internal/ent/series"
)

// Scan walks the storage root via disk.ScanLibrary, upserts one ImportEntry
// per series directory found (keyed by path), and returns the staging list.
// A series whose title already exists in the DB (by slug) is marked
// status="imported" so the owner can see it needs no further action; an
// entry already marked "imported" is never silently downgraded back to
// "pending" by a re-scan.
func (s *Service) Scan(ctx context.Context) ([]FoundSeriesDTO, error) {
	facts, err := disk.ScanLibrary(s.storage)
	if err != nil {
		return nil, err
	}

	out := make([]FoundSeriesDTO, 0, len(facts))
	for _, sf := range facts {
		path := disk.SeriesDir(s.storage, sf.Category, sf.Title)
		providers := distinctProviders(sf)

		exists, err := s.db.Series.Query().Where(series.Slug(disk.Slugify(sf.Title))).Exist(ctx)
		if err != nil {
			return nil, err
		}
		status := statusPending
		if exists {
			status = statusImported
		}

		found, err := foundBlob(sf)
		if err != nil {
			return nil, err
		}

		if err := s.upsertEntry(ctx, path, sf, len(sf.Chapters), status, found); err != nil {
			return nil, err
		}

		out = append(out, FoundSeriesDTO{
			Path:         path,
			Title:        sf.Title,
			Category:     sf.Category,
			ChapterCount: len(sf.Chapters),
			Providers:    providers,
			Status:       status,
			AlreadyInDB:  exists,
		})
	}
	return out, nil
}

// foundBlob marshals a SeriesFacts snapshot into the map[string]any shape
// ImportEntry.found (an Ent JSON field) expects.
func foundBlob(sf disk.SeriesFacts) (map[string]any, error) {
	blob, err := json.Marshal(storedFacts{Facts: sf})
	if err != nil {
		return nil, err
	}
	var found map[string]any
	if err := json.Unmarshal(blob, &found); err != nil {
		return nil, err
	}
	return found, nil
}

// upsertEntry creates or updates the ImportEntry for path. An existing row
// already marked "imported" keeps that status regardless of the freshly
// computed one — a re-scan never downgrades an imported series back to
// pending.
func (s *Service) upsertEntry(ctx context.Context, path string, sf disk.SeriesFacts, count int, status string, found map[string]any) error {
	existing, err := s.db.ImportEntry.Query().Where(importentry.Path(path)).Only(ctx)
	if ent.IsNotFound(err) {
		_, cerr := s.db.ImportEntry.Create().
			SetPath(path).SetTitle(sf.Title).SetCategory(sf.Category).
			SetChapterCount(count).SetStatus(status).SetFound(found).Save(ctx)
		return cerr
	}
	if err != nil {
		return err
	}

	upd := existing.Update().SetTitle(sf.Title).SetCategory(sf.Category).
		SetChapterCount(count).SetFound(found)
	if existing.Status != statusImported {
		upd = upd.SetStatus(status)
	}
	_, uerr := upd.Save(ctx)
	return uerr
}

// distinctProviders returns the unique, non-empty provider names present
// across a series' chapters, in first-seen order.
func distinctProviders(sf disk.SeriesFacts) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, c := range sf.Chapters {
		if c.Provider == "" {
			continue
		}
		if _, ok := seen[c.Provider]; ok {
			continue
		}
		seen[c.Provider] = struct{}{}
		out = append(out, c.Provider)
	}
	return out
}
