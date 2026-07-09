package library

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// DedupAllProviders runs the per-series provider dedup (DedupProviders) across
// EVERY series in the library, folding already-drifted disk/live source pairs
// into one row without re-downloading. It is the library-wide one-shot cleanup
// behind POST /api/library/dedup-providers.
//
// The sweep is resilient: a per-series error is logged and skipped (one bad
// series never aborts the whole sweep), and a series that vanished mid-sweep
// (concurrent DeleteSeries → ErrSeriesNotFound) is silently ignored. Returns
// how many series were processed plus the aggregate merged/skipped counts.
// DedupProviders itself fires s.trigger() once per merged series, so a
// successful sweep converges the affected series without an extra trigger here.
func (s *Service) DedupAllProviders(ctx context.Context) (seriesProcessed, merged, skipped int, err error) {
	ids, err := s.db.Series.Query().IDs(ctx)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("library.DedupAllProviders: list series: %w", err)
	}

	for _, id := range ids {
		if ctx.Err() != nil {
			return seriesProcessed, merged, skipped, ctx.Err()
		}
		m, sk, derr := s.DedupProviders(ctx, id)
		if errors.Is(derr, ErrSeriesNotFound) {
			// Deleted mid-sweep — benign, skip.
			continue
		}
		if derr != nil {
			slog.WarnContext(ctx, "library.DedupAllProviders: series dedup failed, skipping",
				"series_id", id, "err", derr)
			continue
		}
		seriesProcessed++
		merged += m
		skipped += sk
		if m > 0 || sk > 0 {
			slog.InfoContext(ctx, "library.DedupAllProviders: series deduped",
				"series_id", id, "merged", m, "skipped", sk)
		}
	}
	return seriesProcessed, merged, skipped, nil
}
