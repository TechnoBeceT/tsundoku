package library

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// DedupAllProviders runs the per-series provider dedup across EVERY series in
// the library, folding already-drifted disk/live source pairs into one row
// without re-downloading. It is the library-wide one-shot cleanup behind
// POST /api/library/dedup-providers.
//
// The sweep is resilient: a per-series error is logged and skipped (one bad
// series never aborts the whole sweep), and a series that vanished mid-sweep
// (concurrent DeleteSeries → ErrSeriesNotFound) is silently ignored. Each series
// is folded through dedupOneSeries, so the sweep holds the same per-series merge
// single-flight latch every other merge path holds; a series whose latch is
// already held is SKIPPED, never blocked and never queued (GAP-120). The sweep
// walks on to the next series, so one busy series never stalls or short-circuits
// the run, and re-running the sweep catches whatever was busy last time.
//
// THREE INDEPENDENT COUNTS, each meaning something different — do not merge them:
//
//   - seriesProcessed: series whose dedup RAN TO COMPLETION. Unchanged meaning:
//     a series that errored, vanished mid-sweep, or was busy is NOT counted. The
//     input set is still EVERY series (deliberately not narrowed to the drifted
//     ones — see driftedSeriesIDs), because this number is reported to the owner
//     as "how many series I looked at".
//   - skipped: drifted PAIRS left alone because the matching linked twin has an
//     empty chapter feed (merging would orphan the disk chapters). The owner acts
//     on this by refreshing that source.
//   - busy: SERIES skipped because another merge held their latch. The owner acts
//     on this by re-running the sweep.
//
// busy is reported through the sweep's terminal report (finishDedupSweep), not
// through the return tuple, so the three long-standing return values keep their
// exact meaning and arity for every existing caller. It reaches the owner where
// the counts actually have to land: the library.dedup.done SSE event the Settings
// dialog renders — this endpoint answers 202 and detaches, so the return values
// only ever reach the server log.
//
// dedupProvidersLocked itself fires s.trigger() once per merged series, so a
// successful sweep converges the affected series without an extra trigger here.
func (s *Service) DedupAllProviders(ctx context.Context) (seriesProcessed, merged, skipped int, err error) {
	busy := 0
	defer func() {
		s.finishDedupSweep(ctx, DedupSweepEvent{
			SeriesProcessed: seriesProcessed,
			Merged:          merged,
			Skipped:         skipped,
			Busy:            busy,
		}, err)
	}()

	ids, err := s.db.Series.Query().IDs(ctx)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("library.DedupAllProviders: list series: %w", err)
	}

	for _, id := range ids {
		if ctx.Err() != nil {
			return seriesProcessed, merged, skipped, ctx.Err()
		}
		m, sk, ok, derr := s.dedupOneSeries(ctx, id)
		if !ok {
			// Another merge holds this series' latch (an owner Match, a
			// consolidation, or the unattended self-heal). Yield and carry on —
			// re-running the sweep catches it.
			busy++
			slog.DebugContext(ctx, "library.DedupAllProviders: series skipped — a merge is already in flight",
				"series_id", id)
			continue
		}
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

// finishDedupSweep is the sweep's ONE terminal report, emitted on every exit
// (success, a failed series listing, or a cancelled/timed-out context) by
// DedupAllProviders' defer.
//
// It logs the busy skips — the only count the return tuple does not carry — and
// broadcasts library.dedup.done so the owner sees the outcome. The endpoint
// answers 202 and detaches, so without this push the whole sweep would be a
// silent operation: the dialog says "started" and never says what happened (§16).
//
// The event's error text is a fixed, caller-safe sentence (never the raw wrapped
// error) because it rides the SSE side-channel, which bypasses the central error
// middleware — the same hygiene rule safeMergeError applies to provider.merged.
func (s *Service) finishDedupSweep(ctx context.Context, ev DedupSweepEvent, err error) {
	if ev.Busy > 0 {
		slog.InfoContext(ctx, "library.DedupAllProviders: series skipped — a merge was already in flight",
			"busy", ev.Busy)
	}
	if err != nil {
		ev.Error = sweepErrorText(err)
	}
	s.broadcastDedupSweep(ev)
}

// sweepErrorText maps a sweep failure to the caller-safe sentence shown in the
// dialog. A context expiry is called out separately because it is the one
// failure the owner can act on directly (the counts up to that point are real
// work that landed — re-running continues from there); anything else is a server
// fault whose detail belongs in the log, not on the wire.
func sweepErrorText(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "the clean-up ran out of time before finishing — run it again to continue"
	}
	if errors.Is(err, context.Canceled) {
		return "the clean-up was stopped before finishing — run it again to continue"
	}
	return "the clean-up failed — see the server log for details"
}
