package series

import (
	"time"

	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/pkg/errorclass"
)

// EarlyAccessUntil reports when a source's deliberate WITHHOLDING of one chapter
// is expected to lapse, and nil when the feed row is not being withheld (GAP-141).
//
// Some sources publish their newest chapters behind coins / a subscription for a
// few days and then release them free. That is NOT a fault: the source is healthy
// and serving everything else, and the chapter arrives on its own once the window
// closes. The download engine therefore parks such a chapter on that source
// (download.deferSource) instead of charging it — attempts UNCHANGED, no breaker
// tripped, next_attempt_at pushed out to jobs.locked_retry_interval.
//
// This is the READ side of that park, and it is deliberately the conjunction of
// both halves the engine writes:
//   - the stored per-source last_error classifies as errorclass.CategoryLocked —
//     the message is what the SOURCE said, so it is the only record of WHY;
//   - the deferral is still in force (next_attempt_at in the future) — once it
//     lapses the engine re-checks on the next cycle, so the chapter is queued
//     again, not waiting on a paywall.
//
// Reading only the message would keep calling a chapter "early access" while the
// engine is actively retrying it; reading only the timestamp cannot tell a paywall
// apart from an ordinary backoff. The pairing also makes the returned instant
// meaningful: a caller can always render "free ~2d" from a non-nil result.
//
// It is exported because both read models over this state need the same answer —
// the per-series chapter list (series.GetSeries) and the cross-library activity
// list (internal/downloads) — and re-deriving it in either would let the two drift
// (§2 DRY). ProviderChapter carries no category column, so the classification is
// derived on read; nothing is persisted and no state is changed.
func EarlyAccessUntil(pc *ent.ProviderChapter, now time.Time) *time.Time {
	if pc == nil || pc.NextAttemptAt == nil || !pc.NextAttemptAt.After(now) {
		return nil
	}
	if errorclass.ClassifyMessage(pc.LastError) != errorclass.CategoryLocked {
		return nil
	}
	until := *pc.NextAttemptAt
	return &until
}

// chapterEarlyAccess indexes a series' chapter_key → when its early-access wait
// ends, over the provider feeds GetSeries has already eager-loaded (so it costs no
// extra query and no source call).
//
// A key is present exactly when at least one source carrying it is withholding it
// (see EarlyAccessUntil), and its value is the EARLIEST such instant: the chapter
// becomes fetchable as soon as the FIRST of its sources releases it, so the
// soonest re-check is the honest answer to "when could this arrive".
//
// CAVEAT, stated rather than hidden: this asks "is a source withholding this
// chapter", not "is the chapter blocked ONLY by that". A chapter one source
// withholds while another serves it freely is marked for the single cycle before
// the free source downloads it, after which it leaves both read models' queues.
func chapterEarlyAccess(provs []*ent.SeriesProvider, now time.Time) map[string]time.Time {
	out := map[string]time.Time{}
	for _, sp := range provs {
		for _, pc := range sp.Edges.ProviderChapters {
			until := EarlyAccessUntil(pc, now)
			if until == nil {
				continue
			}
			if seen, ok := out[pc.ChapterKey]; !ok || until.Before(seen) {
				out[pc.ChapterKey] = *until
			}
		}
	}
	return out
}

// lockedUntilFor projects one chapter_key out of a chapterEarlyAccess index as the
// nullable instant the DTO carries: a copy of the stored value when the key is
// present, else nil ("not withheld"). Taking the address of the loop-free local is
// what keeps every chapter's pointer independent.
func lockedUntilFor(idx map[string]time.Time, key string) *time.Time {
	until, ok := idx[key]
	if !ok {
		return nil
	}
	return &until
}

// EarlyAccessUnlessSettled suppresses an early-access wait for a chapter the
// library is no longer waiting on, and otherwise passes the wait through
// unchanged (GAP-141).
//
// A withheld feed row and a chapter that will never be fetched from it are not
// mutually exclusive, and the pair arises in TWO ways:
//
//   - the chapter ALREADY HAS A FILE. The reachable shape is a convergence
//     UPGRADE: the chapter arrives from a free mirror, a higher-importance source
//     is flagged as an upgrade, that upgrade fetch returns "Chapter locked (coins
//     required)", and download.deferSource parks the better source while the
//     chapter itself returns to `downloaded`. The feed row is genuinely withheld —
//     but the chapter is readable NOW, so presenting it as "waiting for early
//     access" (and, in the UI, replacing its state badge) hides a file the owner
//     can open.
//   - the chapter is PARKED — `superseded` or `ignored`. Both mean the library has
//     DELIBERATELY settled it and stopped fetching it, so the marker's promise
//     ("free in ~3d") is simply false: nothing will collect it when the window
//     lapses. Both are reachable while a stale withheld feed row survives — a split
//     part is superseded once its whole lands (download.supersedeOnePart), and a
//     `failed` fractional whose every carrier ignores fractionals is parked as
//     ignored (series.applyIgnoreReconcile) straight out of the state a withheld
//     chapter rests in.
//
// The FILE test alone cannot cover the second case, and that is the whole point:
// both parked states are states in which the chapter deliberately has NO file —
// superseded even DELETES the one it had and clears Chapter.filename. This does
// not walk back QCAT-343's rule that readability is a property of the file, never
// of the surrounding state: the file test still stands on its own and decides
// every readable chapter. The state test only adds the two states that mean "not
// waiting", and it is a DENY-list precisely so a state added later keeps the
// marker (cosmetic) rather than silently losing it.
//
// Both read models call this, so neither can drift from the other (§2 DRY).
func EarlyAccessUnlessSettled(state entchapter.State, filename string, until *time.Time) *time.Time {
	if filename != "" || isParkedState(state) {
		return nil
	}
	return until
}

// isParkedState reports whether a chapter state means the library has stopped
// fetching the chapter on purpose. Both members are terminal-until-reversed: the
// single escape from each is back to `wanted` (see chapter.legalTransitions), and
// only then does an early-access wait describe anything real again.
func isParkedState(state entchapter.State) bool {
	return state == entchapter.StateSuperseded || state == entchapter.StateIgnored
}
