package job

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/sse"
)

// sourcesSummaryTimeout bounds both detached summary refreshes and ordered
// transition publications, so a wedged DB read cannot block either indefinitely.
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

// SourcesSummaryHook pushes a current sources.summary snapshot asynchronously.
// It remains the direct no-argument entry point for callers that need an
// immediate refresh without a specific breaker transition.
//
// It returns immediately — the snapshot read + broadcast run on a detached,
// time-bounded goroutine and any panic is recovered — so a slow or broken alert
// push cannot hold the caller. Ordered breaker transitions use
// SourcesSummaryTransitionHook instead.
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

// SourcesSummaryTransitionHook is the ordered sourcegate transition hook. The
// durable transition carries the committed state of its physical source; the
// current snapshot is overwritten with that state before folding, so a later
// reset cannot erase an earlier trip alert. It completes the bounded snapshot
// and broadcast before returning because sourcegate holds the cross-process
// publication cursor for the callback's lifetime; detaching here would allow
// the observable SSE effects to reverse after the callbacks were ordered.
func (r *Runner) SourcesSummaryTransitionHook(ctx context.Context, transition sourcegate.BreakerTransition) (err error) {
	defer func() {
		if p := recover(); p != nil {
			slog.Warn("job.Runner: sources.summary transition hook panicked (recovered)", "panic", p)
			err = fmt.Errorf("sources.summary transition panic: %v", p)
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, sourcesSummaryTimeout)
	defer cancel()
	return r.broadcastTransitionSummary(ctx, transition)
}

func (r *Runner) broadcastTransitionSummary(ctx context.Context, transition sourcegate.BreakerTransition) error {
	if r.breakers == nil {
		return nil
	}
	snapshot, err := r.breakers.Snapshot(ctx)
	if err != nil {
		slog.WarnContext(ctx, "job.Runner: sources.summary snapshot failed (skipping)", "err", err)
		return fmt.Errorf("sources.summary snapshot: %w", err)
	}
	if transition.State == nil {
		delete(snapshot, transition.SourceKey)
	} else {
		snapshot[transition.SourceKey] = *transition.State
	}
	erroring, coolingDown := sourcegate.SummaryCounts(snapshot, time.Now())
	r.broadcastSourcesSummaryCounts(erroring, coolingDown)
	return nil
}

// broadcastSourcesSummary computes the current erroring / coolingDown source
// counts from the breaker snapshot and pushes them as a sources.summary SSE event.
// It is a no-op when no snapshotter is wired. Best-effort: a snapshot read failure
// is logged and swallowed (no event that pass), never propagated. It is the SINGLE
// current-snapshot emitter, shared by SourcesSummaryHook and the periodic refresh
// tick (runRefreshSweep), so the ordinary count rule lives once.
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
	r.broadcastSourcesSummaryCounts(erroring, coolingDown)
}

func (r *Runner) broadcastSourcesSummaryCounts(erroring, coolingDown int) {
	raw, err := json.Marshal(SourcesSummaryEvent{Erroring: erroring, CoolingDown: coolingDown})
	if err != nil {
		// Defensive path: two int fields cannot fail to marshal.
		return
	}
	r.hub.Broadcast(sse.Event{Type: "sources.summary", Data: json.RawMessage(raw)})
}
