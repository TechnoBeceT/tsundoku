package sourcegate

import (
	"log/slog"
	"time"

	"github.com/technobecet/tsundoku/internal/sourceevents"
)

// BreakerTransition is the committed state carried with one trip or reset hook.
// State is the source's breaker snapshot at that transition; it is nil for a
// reset. Consumers can combine it with a later full snapshot without allowing a
// newer transition for the same source to erase the alert being published.
type BreakerTransition struct {
	SourceKey string
	EventType sourceevents.EventType
	State     *BreakerState
}

// WithTransitionHook attaches a best-effort callback backed by one durable record
// for each breaker STATE TRANSITION: a trip (RecordFailure crossing the failure
// threshold) and a clear (RecordSuccess natural recovery + the owner Reset) —
// never on a routine success or a sub-threshold failure. The job.Runner attaches
// a closure that recomputes the erroring / coolingDown source counts and pushes a
// sources.summary SSE, so the owner learns a source broke the instant it does
// instead of waiting for the 2h refresh tick.
//
// sourcegate stays dependency-narrow: it never imports the SSE hub — the hook is
// an opaque callback the Runner owns. The callback receives the committed state
// for this transition, rather than rereading state a later reset may already have
// changed. fn runs inside the database-serialized publication window and MUST
// finish its observable notification before returning; otherwise a detached
// effect could still reverse after ordered callbacks. It must also be bounded so
// a broken downstream cannot indefinitely delay later publications. The Runner
// uses a 10-second context bound. A nil fn (the default) fires nothing. Returns
// the receiver for chaining off NewService.
func (s *Service) WithTransitionHook(fn func(BreakerTransition)) *Service {
	s.onTransition = fn
	return s
}

// fireTransition invokes the breaker-transition hook, if one is attached. It is
// nil-safe and panic-safe: a breaker record must NEVER be broken by a downstream
// alert push (best-effort posture, mirrors the metrics recorder), so a stray
// panic in the hook is recovered here rather than unwinding RecordFailure /
// RecordSuccess / Reset. Slowness is bounded by the WithTransitionHook contract,
// not here.
func (s *Service) fireTransition(transition BreakerTransition) {
	if s.onTransition == nil {
		return
	}
	defer func() {
		if p := recover(); p != nil {
			slog.Warn("sourcegate: transition hook panicked (recovered)", "panic", p)
		}
	}()
	s.onTransition(transition)
}

// SummaryCounts folds a breaker snapshot into the two counts the sources.summary
// alert carries:
//   - erroring:    sources currently in a failure streak (FailingSince set) — the
//     authoritative "a source broke" signal, the same rule as
//     reporting.failingSources uses;
//   - coolingDown: sources whose breaker is tripped and still in cooldown at now.
//
// It is PURE (no DB, no clock — now is passed in) so the interpretation of a
// snapshot lives in one place and is trivially testable. The Runner reads the
// snapshot via sourcegate.Snapshot and folds it here for both the immediate
// transition alert and the periodic refresh-tick alert.
func SummaryCounts(snapshot map[string]BreakerState, now time.Time) (erroring, coolingDown int) {
	for _, b := range snapshot {
		if b.FailingSince != nil {
			erroring++
		}
		if b.IsCoolingDown(now) {
			coolingDown++
		}
	}
	return erroring, coolingDown
}
