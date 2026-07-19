package job

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/sse"
)

// sourcesSummaryTimeout bounds the detached snapshot + broadcast the breaker
// transition hook kicks off, so a wedged DB read can never leak the goroutine.
const sourcesSummaryTimeout = 10 * time.Second

// SourcesSummaryEvent is the sources.summary SSE payload: how many sources are
// currently in a failure streak (Erroring) and how many have a tripped breaker
// still in cooldown (CoolingDown). It drives the Health nav-rail danger badge —
// the immediate "a source broke, I need to KNOW" signal, distinct from the
// series-centric health.summary event.
type SourcesSummaryEvent struct {
	Erroring    int `json:"erroring"`
	CoolingDown int `json:"coolingDown"`
}

// BreakerSnapshotter reads every source's current circuit-breaker state in ONE
// batch read. *sourcegate.Service satisfies it. The Runner uses it only to
// compute the sources.summary alert counts; it stays nil (the summary a no-op)
// until SetBreakerSnapshotter wires the gate in.
type BreakerSnapshotter interface {
	Snapshot(ctx context.Context) (map[string]sourcegate.BreakerState, error)
}

// SetBreakerSnapshotter wires the source circuit-breaker store the sources.summary
// alert reads its counts from. Nil-safe: until it is called (or if nil is passed)
// the summary broadcast is a no-op. Kept a setter (not a NewRunner param) so the
// existing NewRunner call sites are untouched (mirrors SetNotifier).
func (r *Runner) SetBreakerSnapshotter(b BreakerSnapshotter) {
	r.breakers = b
}

// SourcesSummaryHook is the sourcegate breaker-transition hook (see
// sourcegate.Service.WithTransitionHook): it pushes an immediate sources.summary
// SSE the instant a source's breaker trips or clears, so the owner's danger badge
// updates without waiting for the 2h refresh tick.
//
// It returns immediately — the snapshot read + broadcast run on a DETACHED,
// time-bounded goroutine (the breaker path's own ctx ends the moment RecordFailure
// returns) and any panic is recovered — so a slow or broken alert push can NEVER
// slow or break the breaker record it fires from (best-effort posture, mirrors the
// metrics recorder). Pass it straight to WithTransitionHook.
func (r *Runner) SourcesSummaryHook() {
	go func() {
		defer func() {
			if p := recover(); p != nil {
				slog.Warn("job.Runner: sources.summary hook panicked (recovered)", "panic", p)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), sourcesSummaryTimeout)
		defer cancel()
		r.broadcastSourcesSummary(ctx)
	}()
}

// broadcastSourcesSummary computes the current erroring / coolingDown source
// counts from the breaker snapshot and pushes them as a sources.summary SSE event.
// It is a no-op when no snapshotter is wired. Best-effort: a snapshot read failure
// is logged and swallowed (no event that pass), never propagated. It is the SINGLE
// summary emitter, shared by BOTH the immediate transition hook (SourcesSummaryHook)
// and the periodic refresh tick (runRefreshSweep), so the count rule lives once.
func (r *Runner) broadcastSourcesSummary(ctx context.Context) {
	if r.breakers == nil {
		return
	}
	snap, err := r.breakers.Snapshot(ctx)
	if err != nil {
		slog.WarnContext(ctx, "job.Runner: sources.summary snapshot failed (skipping)", "err", err)
		return
	}
	erroring, coolingDown := sourcegate.SummaryCounts(snap, time.Now())
	raw, err := json.Marshal(SourcesSummaryEvent{Erroring: erroring, CoolingDown: coolingDown})
	if err != nil {
		// Defensive path: two int fields cannot fail to marshal.
		return
	}
	r.hub.Broadcast(sse.Event{Type: "sources.summary", Data: json.RawMessage(raw)})
}
