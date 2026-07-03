package library

import (
	"context"

	"github.com/technobecet/tsundoku/internal/ent/importentry"
)

// ListImports returns a page of staged ImportEntry rows (optionally filtered
// by status: pending/imported/skipped — an empty status means no filter) as
// []FoundSeriesDTO, ordered by scanned_at for stable output. limit/offset
// page the result — the caller (handler/library) is responsible for
// resolving the default/cap (mirrors the downloads/series pagination
// convention); this method applies them as given.
//
// Providers is recomputed from the row's stored disk.SeriesFacts snapshot via
// decodeStoredFacts (import.go) + distinctProviders (scan.go) — the SAME
// helpers Scan uses to build the equivalent field, so the provider list is
// never derived twice (§2 DRY). AlreadyInDB mirrors the row's persisted
// status: true iff status == "imported".
func (s *Service) ListImports(ctx context.Context, status string, limit, offset int) ([]FoundSeriesDTO, error) {
	q := s.db.ImportEntry.Query().Order(importentry.ByScannedAt())
	if status != "" {
		q = q.Where(importentry.Status(status))
	}
	q = q.Limit(limit).Offset(offset)
	rows, err := q.All(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]FoundSeriesDTO, 0, len(rows))
	for _, row := range rows {
		sf, err := decodeStoredFacts(row.Found)
		if err != nil {
			return nil, err
		}
		out = append(out, FoundSeriesDTO{
			Path:         row.Path,
			Title:        row.Title,
			Category:     row.Category,
			ChapterCount: row.ChapterCount,
			Providers:    distinctProviders(sf.Facts),
			Status:       row.Status,
			AlreadyInDB:  row.Status == statusImported,
		})
	}
	return out, nil
}
