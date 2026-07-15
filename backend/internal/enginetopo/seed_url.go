// Package enginetopo holds one-shot, owner-triggered maintenance passes that
// prepare the library for an engine swap or topology change — as opposed to
// the recurring per-cycle work in internal/refresh/internal/download.
// Residents: BackfillProviderURLs (SeriesProvider.url), SeedSourcePreferences
// (per-source Tachiyomi/Mihon preferences), and SeedEngineConfig (the engine's
// FlareSolverr + SOCKS server settings).
package enginetopo

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/technobecet/tsundoku/internal/ent"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// backfillConcurrency bounds how many MangaMeta calls run in parallel. Small
// and fixed (unlike the tunable refresh/download concurrency settings)
// because this is a rare, owner-triggered one-shot, not a recurring sweep —
// there is no hot-reload knob worth adding for it.
const backfillConcurrency = 8

// BackfillProviderURLs fills SeriesProvider.url for every row missing it, by
// asking Suwayomi for the manga's current metadata (client.MangaMeta) — the
// EXACT same lookup suwayomi.Ingest.upsertSeriesProvider already performs on
// every AddSeries/refresh call. It exists because the refresh sweep only
// re-ingests monitored, non-completed series (refresh.Service.RefreshAll), so
// an old row belonging to an unmonitored or completed series can carry
// url="" forever; this pass is deliberately UNGATED — it does not filter by
// monitored/completed — so every stale row gets one chance to be filled
// regardless of the series' current state.
//
// It is idempotent: the query selects only rows with url=="" AND a known
// suwayomi_id, so a second run over an already-filled library does no work
// and calls MangaMeta zero times (filled=0, remaining=0).
//
// A per-row MangaMeta failure (upstream error, or a resolved-but-empty URL)
// is logged and skipped — the row is left untouched and counted in
// `remaining`, never `filled` — so one bad source can never abort the whole
// pass (partial success, matching the never-auto-delete/upsert-only
// conventions the rest of the ingest engine follows). err is non-nil only
// when the initial query enumerating candidate rows fails.
//
// PANIC-SAFE per worker: the rows are fanned out across errgroup goroutines, and
// errgroup does NOT recover a panic in a child goroutine — an unrecovered one
// (e.g. a nil deref on a malformed MangaMeta response) would escape RunSeed's
// top-level recover and crash the whole process. Each worker therefore recovers
// its own panic, logs it, and counts that row in `remaining` (a failed row), so
// a single bad row degrades to a gap instead of taking the process down.
func BackfillProviderURLs(ctx context.Context, client suwayomi.Client, db *ent.Client) (filled int, remaining int, err error) {
	rows, err := db.SeriesProvider.Query().
		Where(
			entseriesprovider.URL(""),
			entseriesprovider.SuwayomiIDNEQ(0),
		).
		All(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("enginetopo.BackfillProviderURLs: query rows missing url: %w", err)
	}

	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(backfillConcurrency)
	for _, row := range rows {
		g.Go(func() error {
			ok := false
			// Count exactly once, in the deferred func, so a recovered panic and a
			// normal return both funnel through the same single counting site.
			defer func() {
				if r := recover(); r != nil {
					slog.ErrorContext(gctx, "enginetopo: recovered panic in backfillOne",
						"series_provider", row.ID, "err", r)
					ok = false
				}
				mu.Lock()
				if ok {
					filled++
				} else {
					remaining++
				}
				mu.Unlock()
			}()
			ok = backfillOne(gctx, client, db, row)
			return nil
		})
	}
	// backfillOne never returns a non-nil error (failures are logged+counted
	// internally) and a worker panic is recovered above, so Wait never
	// propagates an error.
	_ = g.Wait()

	return filled, remaining, nil
}

// backfillOne resolves row's current URL via MangaMeta and writes it, unless
// the fetch fails or resolves to an empty URL (both logged, both leave the
// row untouched). Returns whether the row was filled.
func backfillOne(ctx context.Context, client suwayomi.Client, db *ent.Client, row *ent.SeriesProvider) bool {
	meta, err := client.MangaMeta(ctx, row.SuwayomiID)
	if err != nil {
		slog.WarnContext(ctx, "enginetopo: MangaMeta failed, leaving url empty",
			"series_provider", row.ID, "provider", row.Provider, "suwayomi_id", row.SuwayomiID, "err", err)
		return false
	}
	if meta.URL == "" {
		slog.WarnContext(ctx, "enginetopo: MangaMeta returned an empty url, leaving row empty",
			"series_provider", row.ID, "provider", row.Provider, "suwayomi_id", row.SuwayomiID)
		return false
	}
	if err := db.SeriesProvider.UpdateOne(row).SetURL(meta.URL).Exec(ctx); err != nil {
		// Defensive path: reachable only on DB connection loss between the query
		// and this update.
		slog.WarnContext(ctx, "enginetopo: failed to persist backfilled url",
			"series_provider", row.ID, "err", err)
		return false
	}
	return true
}
