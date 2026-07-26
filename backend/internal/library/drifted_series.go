package library

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/series"
)

// driftedSeriesIDs returns the ids of the series that carry at least one UNLINKED
// disk-origin SeriesProvider — a row whose `provider` column holds a display NAME
// instead of the engine-host's numeric source id (it fails
// series.IsLinkedProvider). Those are the ONLY series a provider-dedup pass can
// ever change: every merge candidate is, by definition, a (disk-origin row, live
// twin) pair, so a series whose providers are all linked has nothing to fold and
// DedupProviders is a guaranteed no-op on it.
//
// This is the targeting query for the recurring self-heal (HealDriftedProviders,
// GAP-120). DedupAllProviders — the one-shot owner sweep — deliberately still
// walks EVERY series id: its `seriesProcessed` count is reported to the owner as
// "how many series I looked at", and narrowing its input would silently change
// that number's meaning. A recurring pass has no such contract, so it targets.
//
// Cost: ONE query, no joins, no per-series fan-out, and only the two identity
// columns are read (a projection — nothing is hydrated). When it finds no
// disk-origin row the caller returns immediately, so a healthy library pays
// exactly this one query per sweep and nothing else.
//
// The linked/disk-origin decision is NOT pushed into SQL. It is a Go-side
// numeric parse (series.LinkedProviderSourceID) and it is ratified — a SQL
// regex would be a SECOND, divergent implementation of the same rule, and the
// two would drift. Reading the two columns and applying the one true predicate
// keeps a single source of truth.
func (s *Service) driftedSeriesIDs(ctx context.Context) ([]uuid.UUID, error) {
	var rows []struct {
		SeriesID uuid.UUID `json:"series_id"`
		Provider string    `json:"provider"`
	}
	err := s.db.SeriesProvider.Query().
		Order(entseriesprovider.ByID()).
		Select(entseriesprovider.FieldSeriesID, entseriesprovider.FieldProvider).
		Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("library.driftedSeriesIDs: read provider identities: %w", err)
	}

	seen := make(map[uuid.UUID]struct{}, len(rows))
	ids := make([]uuid.UUID, 0)
	for _, r := range rows {
		if _, linked := series.LinkedProviderSourceID(r.Provider); linked {
			continue // a real live source — never the disk half of a drifted pair
		}
		if _, dup := seen[r.SeriesID]; dup {
			continue // a series with two disk-origin rows is still one candidate
		}
		seen[r.SeriesID] = struct{}{}
		ids = append(ids, r.SeriesID)
	}
	return ids, nil
}
