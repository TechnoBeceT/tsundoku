package series

import (
	"context"

	"github.com/google/uuid"
)

// SeriesSyncer is the narrow port the provider-mutating routes use to fire the
// per-series INSTANT convergence layer (GAP-113) — satisfied by
// *seriessync.Orchestrator. Depending on this local interface (rather than
// importing internal/seriessync) keeps the handler package decoupled and mirrors
// ViewSyncer / TrackerProgressSetter's exact shape.
//
//   - SyncSeries: re-fetch the series' feeds THEN re-detect upgrades — used after
//     REMOVING a provider (the feed set changed).
//   - DetectSeries: re-detect only, no feed re-fetch — used after a source RE-RANK
//     (the feeds are already current; only the winning source changed).
//
// Both are async + single-flight per series; the handler never waits on them.
type SeriesSyncer interface {
	SyncSeries(ctx context.Context, seriesID uuid.UUID)
	DetectSeries(ctx context.Context, seriesID uuid.UUID)
}

// WithSeriesSync attaches the per-series instant convergence orchestrator and
// returns the handler, so production wires it fluently onto the constructor
// (mirrors WithViewSyncer / WithTrackerProgressSetter). It is OPTIONAL: a Handler
// with no SeriesSyncer attached (the default — every existing NewHandler call site,
// including every pre-existing handler/series test) fires no scoped convergence, so
// those mutations fall back to the whole-library sweep exactly as before.
func (h *Handler) WithSeriesSync(s SeriesSyncer) *Handler {
	h.seriesSync = s
	return h
}
