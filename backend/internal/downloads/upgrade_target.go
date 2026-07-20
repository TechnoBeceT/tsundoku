package downloads

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/pkg/chapterrange"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sourcegate"
)

// waiting-reason categories surfaced on a queued row's WaitingReason field: the
// two things that hold a chapter back from being fetched this cycle. "" means the
// row is not waiting on a persisted signal.
const (
	// waitBackoff — the waited-on source has a persisted per-source next_attempt_at
	// in the future (download.bumpSourceFailure / cooldownSource): this chapter
	// failed against that source and is inside its per-chapter backoff.
	waitBackoff = "backoff"
	// waitCoolingDown — the waited-on source's circuit-breaker is tripped
	// (SourceCircuitState.cooldown_until in the future): the WHOLE source is in
	// anti-ban cooldown, so no chapter of it is fetched until it reopens. This is the
	// signal the persisted-only deferral used to miss (see the closed KNOWN GAP).
	waitCoolingDown = "cooling_down"
)

// feedCarrier is one source that OFFERS a given chapter_key, paired with the
// ProviderChapter feed row through which it offers it. The feed row is not
// cosmetic on two counts: its ID is the SECONDARY sort key the engine ranks
// candidates by, so carrying it is what lets this read model order ties exactly as
// the engine does (see newUpgradeTargetIndex); and its next_attempt_at / last_error
// are the persisted per-source cooldown the waiting read surfaces (see
// waitedOnCarrier / waitingStatus). UNIQUE(series_provider_id, chapter_key) means a
// provider contributes exactly one feed row per key, so the pairing is unambiguous.
type feedCarrier struct {
	provider *ent.SeriesProvider
	pc       *ent.ProviderChapter
}

// upgradeTargetIndex maps a chapter_key to the series' sources that OFFER that key,
// ordered exactly as chapter.RankedLiveCandidates orders them: importance DESC,
// then ProviderChapter.ID (string form) ASC as the tiebreak. It is built ONCE per
// series from the already-batch-loaded provider feeds, so resolving a page's
// sources + upgrade targets adds ZERO queries (see newUpgradeTargetIndex).
type upgradeTargetIndex map[string][]feedCarrier

// newUpgradeTargetIndex builds the per-series key→carriers index from the
// providers' EAGER-LOADED ProviderChapter feeds (loadProviders already fetches
// them WithProviderChapters for the display/name resolution). It walks each feed
// once — O(total feed rows for the page's series) — and never touches the DB, so
// the downloads list stays at its bounded, page-size-independent query count.
//
// The ordering MIRRORS chapter.RankedLiveCandidates byte-for-byte: importance DESC,
// then ProviderChapter.ID.String() ASC. The secondary key is load-bearing, not
// decoration — EQUAL importances are routine (disk.Reconcile gives every
// disk-origin provider importance 1), and without it a tie would fall back to the
// order Postgres happened to return the providers in (the batch load has no ORDER
// BY), so the UI could name a different source than the scheduler picks, and even a
// different one on the next refresh.
//
// It also mirrors the engine's IGNORE-FRACTIONAL exclusion (see
// chapter.dropIgnoredFractionalSources): a source the owner flagged as a fractional
// re-uploader (SeriesProvider.ignore_fractional) contributes NONE of its
// fractional-numbered feed rows, so it can be named neither as a chapter's source
// nor as its upgrade target. Without this, ticking the box would leave a fractional
// chapter whose only carrier is that source sitting "Queued from Comic Asura"
// FOREVER while the engine, having dropped the source from candidacy, skips it every
// cycle — a row naming a source that is not fetching it, the exact lie this index
// was introduced to kill. A feed row with NO parsed number is KEPT: it cannot be
// judged fractional, and the engine fails open on it identically.
func newUpgradeTargetIndex(provs []*ent.SeriesProvider) upgradeTargetIndex {
	idx := upgradeTargetIndex{}
	for _, sp := range provs {
		for _, pc := range sp.Edges.ProviderChapters {
			if sp.IgnoreFractional && pc.Number != nil && chapterrange.IsFractional(*pc.Number) {
				continue
			}
			idx[pc.ChapterKey] = append(idx[pc.ChapterKey], feedCarrier{provider: sp, pc: pc})
		}
	}
	for key, carriers := range idx {
		slices.SortStableFunc(carriers, func(a, b feedCarrier) int {
			if c := cmp.Compare(b.provider.Importance, a.provider.Importance); c != 0 {
				return c // importance DESC
			}
			return cmp.Compare(a.pc.ID.String(), b.pc.ID.String()) // then feed-row id ASC
		})
		idx[key] = carriers
	}
	return idx
}

// upgradeTargetCarrier picks the feed carrier a chapter is upgrading TO: the source
// the row shows it converging to, whose label (series.ProviderLabel) and per-source
// attempt count (its ProviderChapter) both feed the UI's "Comix → Asura Scans · N/max".
// It returns ok=false when the chapter is not upgrading, or no carrier clears the bar.
// This is what lets the UI show where the chapter is HEADED instead of only the source
// being REPLACED (satisfied_by, where the file came from).
//
// The rule — the INTENDED target, deliberately NOT the engine's authoritative pick:
// among the sources whose feed carries this chapter's chapter_key (ranked exactly as
// the engine ranks them — see upgradeTargetIndex), take the highest-importance one
// that is (a) NOT the chapter's current satisfier and (b) strictly higher than the
// satisfier's CURRENT importance (its frozen satisfied_importance watermark when the
// satisfier was removed or is parked at the importance-0 merge sentinel — mirroring
// download.effectiveSatisfiedImportance).
//
// GOTCHA — where this can disagree with the engine, and where it MUST NOT. The
// engine's STRUCTURAL exclusions are mirrored here, because they are permanent: a
// source flagged ignore_fractional offers no fractional chapters, and
// newUpgradeTargetIndex drops those feed rows exactly as
// chapter.dropIgnoredFractionalSources does — a permanently-excluded source must
// never be named, or the row would lie forever. What is NOT mirrored are the
// engine's TRANSIENT exclusions (download.bestUpgradeCandidate also skips a source
// that has exhausted its per-source retry budget, is inside its per-source cooldown,
// or whose politeness circuit-breaker is tripped): this DTO layer cannot see them
// without the very N+1 the index exists to avoid, and they clear on their own. So it
// names the source the chapter is MEANT to converge to; while that source is
// temporarily deferred the engine may fetch from a lower one — or defer the upgrade
// entirely — as the row still shows the intended target. Treat this as a UI hint,
// never as engine state.
//
// waitedOnCarrier reuses this same pick to read the target's cooldown (§2 DRY), so the
// two can never disagree on which source is the target.
func upgradeTargetCarrier(ch *ent.Chapter, idx upgradeTargetIndex, provByID map[uuid.UUID]*ent.SeriesProvider) (feedCarrier, bool) {
	if !isUpgrading(ch.State) {
		return feedCarrier{}, false
	}
	for _, c := range idx[ch.ChapterKey] {
		sp := c.provider
		if ch.SatisfiedByProviderID != nil && sp.ID == *ch.SatisfiedByProviderID {
			continue
		}
		// The list is importance-DESC, so the first candidate that fails the bar means
		// no remaining one can clear it either.
		if sp.Importance <= satisfiedImportanceOf(ch, provByID) {
			return feedCarrier{}, false
		}
		return c, true
	}
	return feedCarrier{}, false
}

// resolveUpgradeTarget returns the upgrade target's display label AND its own
// per-source attempt count against this chapter, both from the SINGLE carrier pick —
// so the badge names the source the chapter is converging TO (the one actually
// fetched), never the satisfier it replaces. Both are the zero value ("" / 0) when the
// chapter has no upgrade target, or the target carries no failed attempt yet. In memory
// over the already-loaded feed index — no query.
func resolveUpgradeTarget(ch *ent.Chapter, idx upgradeTargetIndex, provByID map[uuid.UUID]*ent.SeriesProvider) (label string, attempts int) {
	tc, ok := upgradeTargetCarrier(ch, idx, provByID)
	if !ok {
		return "", 0
	}
	if tc.pc != nil {
		attempts = tc.pc.Attempts
	}
	return series.ProviderLabel(tc.provider), attempts
}

// failingCarrier returns the feed carrier the FAILURES read-model surfaces for a
// chapter: the source with a CHAPTER-SPECIFIC per-source failure to report. The
// deterministic rule (documented) is the HIGHEST-IMPORTANCE carrier whose feed row
// has attempts>0 — ties broken by feed-row ID ASC, mirroring the engine's own
// candidate ordering (newUpgradeTargetIndex is already sorted importance DESC then
// ID ASC, so the FIRST attempts>0 carrier is that source). attempts>0 is exactly the
// chapter-specific signal: a SOURCE-WIDE/ban failure only cools the source down
// (attempts untouched) and already surfaces via waitingReason=cooling_down, so it is
// deliberately excluded here. Returns ok=false when no carrier has failed this
// chapter. Reuses the same in-memory index every other resolution uses — ZERO queries.
func failingCarrier(ch *ent.Chapter, idx upgradeTargetIndex) (feedCarrier, bool) {
	for _, c := range idx[ch.ChapterKey] {
		if c.pc != nil && c.pc.Attempts > 0 {
			return c, true
		}
	}
	return feedCarrier{}, false
}

// isSatisfier reports whether sp is the chapter's current satisfying source.
func isSatisfier(ch *ent.Chapter, sp *ent.SeriesProvider) bool {
	return ch.SatisfiedByProviderID != nil && sp != nil && sp.ID == *ch.SatisfiedByProviderID
}

// waitedOnCarrier returns the feed carrier (source + its ProviderChapter feed row)
// of the source the engine is WAITING ON to advance a queued chapter — the source
// whose cooldown, if any, is why the row is not moving:
//
//   - upgrade_available / upgrading → the UPGRADE TARGET (the source it is
//     converging to; the same carrier upgradeTargetCarrier names). While that target
//     is cooling down after a failed upgrade fetch (download.cooldownSource), the
//     upgrade is deferred and the row sits still.
//   - wanted → the PRIMARY live candidate (highest-importance source whose feed
//     carries the key — the same source chapterSource names). While that source is
//     inside its per-source backoff (download.bumpSourceFailure), the download is
//     deferred.
//
// It returns ok=false for every other state, and when no source can be named
// (nothing carries the key, or no valid upgrade target), so the caller surfaces no
// waiting reason. The provider on the returned carrier is what breakerKey keys the
// circuit-breaker snapshot by; its pc is where the persisted per-source backoff is
// read.
func waitedOnCarrier(ch *ent.Chapter, idx upgradeTargetIndex, provByID map[uuid.UUID]*ent.SeriesProvider) (feedCarrier, bool) {
	switch {
	case isUpgrading(ch.State):
		return upgradeTargetCarrier(ch, idx, provByID)
	case ch.State == entchapter.StateWanted:
		if carriers := idx[ch.ChapterKey]; len(carriers) > 0 {
			return carriers[0], true
		}
		return feedCarrier{}, false
	default:
		return feedCarrier{}, false
	}
}

// chapterSource resolves the source a chapter is ACTUALLY coming from, returning its
// SeriesProvider (for the display id + label) AND its ProviderChapter feed row (for
// the per-source attempt count), by a three-step rule:
//
//  1. the source that SATISFIED it (satisfied_by), when set and still present — true
//     provenance, where the file on disk came from;
//  2. else the highest-importance source whose FEED CARRIES this chapter_key, ranked
//     exactly as the engine ranks candidates (importance DESC, then feed-row ID ASC —
//     see newUpgradeTargetIndex). That is the scheduler's own primary-source rule, so
//     the row names the source the engine really fetches from — NOT the series' top
//     source, which lies whenever the top source has a feed gap (prod: rows labelled
//     "Asura Scans" while the engine fetched from "Comic Asura"). Covers every
//     unsatisfied chapter (wanted/downloading/failed) AND a downloaded chapter whose
//     satisfier was cleared (series.RemoveProvider nulls satisfied_by, keeps the CBZ);
//  3. else nil — no source carries the key, so nothing is fetching it (the UI renders
//     an em-dash). A fractional chapter whose only carrier is an ignore_fractional
//     source lands here too — newUpgradeTargetIndex drops that source's fractional
//     feed rows exactly as the engine drops it from candidacy.
//
// Returning the feed row is what lets the row's N/max badge read the SAME source's
// ProviderChapter.attempts, so the badge and the provider label it sits next to
// always describe one source. The satisfier's feed row is looked up in the
// eager-loaded edge (providerChapterForKey) and may be nil (a satisfier need not
// still carry the key) — attempts then reads 0, never a query. Step 2 is a UI hint,
// not engine state: the engine additionally skips retry-exhausted / cooling-down /
// breaker-tripped sources, which this read model cannot see without an N+1.
func chapterSource(ch *ent.Chapter, provByID map[uuid.UUID]*ent.SeriesProvider, idx upgradeTargetIndex) (*ent.SeriesProvider, *ent.ProviderChapter) {
	if ch.SatisfiedByProviderID != nil {
		if sp, ok := provByID[*ch.SatisfiedByProviderID]; ok {
			return sp, providerChapterForKey(sp, ch.ChapterKey)
		}
	}
	if carriers := idx[ch.ChapterKey]; len(carriers) > 0 {
		return carriers[0].provider, carriers[0].pc
	}
	return nil, nil
}

// providerChapterForKey returns sp's eager-loaded ProviderChapter feed row for the
// given chapter_key, or nil when the source does not carry it. In-memory over the
// already-loaded edge — no query. UNIQUE(series_provider_id, chapter_key) means at
// most one match.
func providerChapterForKey(sp *ent.SeriesProvider, key string) *ent.ProviderChapter {
	if sp == nil {
		return nil
	}
	for _, pc := range sp.Edges.ProviderChapters {
		if pc.ChapterKey == key {
			return pc
		}
	}
	return nil
}

// breakerKey is the circuit-breaker snapshot key for a source: the canonical
// physical-source NAME the sourcegate keys SourceCircuitState by. It mirrors
// download.canonicalSourceKey byte-for-byte — TrimSpace(provider_name, else
// provider) — so a row's resolved source joins the breaker snapshot correctly (a
// disk-reconciled "Comix " must collapse onto the live "Comix" the breaker stores).
func breakerKey(sp *ent.SeriesProvider) string {
	return strings.TrimSpace(series.ProviderLabel(sp))
}

// waitingStatus classifies WHY a queued chapter is not moving and when it will next
// be eligible, unifying the two cooldown signals on the waited-on source:
//
//   - backoff — the source's persisted per-source next_attempt_at is in the future
//     (download.bumpSourceFailure on a failed download / cooldownSource on a failed
//     upgrade fetch): this chapter is inside that source's per-chapter backoff.
//   - cooling_down — the source's circuit-breaker is tripped (breaker.CooldownUntil
//     in the future): the WHOLE source is in anti-ban cooldown. This is the signal
//     the persisted-only read used to miss (the now-closed KNOWN GAP): the breaker
//     writes SourceCircuitState.cooldown_until, a different table from
//     ProviderChapter.next_attempt_at, so a chapter held back purely by it showed no
//     reason. The breaker snapshot is batch-loaded once per List (no N+1).
//
// retryAt is the EFFECTIVE next-eligible time = the LATER of the two whenever both
// apply (the engine cannot fetch until BOTH the per-chapter backoff AND the
// source-wide breaker have elapsed), and reason names the binding constraint (the
// later timestamp; the breaker wins a tie, being the broader block). detail is that
// constraint's recorded message (the breaker's last_error for cooling_down, the feed
// row's last_error for backoff), preferring the binding one. All three are the zero
// value when the source is ready to try next cycle — a ready row is never mislabelled
// as waiting. breaker is nil when the source has no breaker row (or the snapshot was
// skipped), collapsing cleanly to the backoff-only behaviour.
func waitingStatus(fc feedCarrier, ok bool, breaker *sourcegate.BreakerState, now time.Time) (reason string, retryAt *time.Time, detail string) {
	if !ok {
		return "", nil, ""
	}
	backoff, hasBackoff := backoffSignal(fc, now)
	cooling, hasCooling := coolingSignal(breaker, now)
	switch {
	case hasBackoff && hasCooling:
		s := laterSignal(backoff, cooling)
		return s.reason, s.until, s.detail
	case hasCooling:
		return cooling.reason, cooling.until, cooling.detail
	case hasBackoff:
		return backoff.reason, backoff.until, backoff.detail
	default:
		return "", nil, ""
	}
}

// waitSignal is one cooldown reason on the waited-on source: the reason category, the
// time it clears, and its recorded message.
type waitSignal struct {
	reason string
	until  *time.Time
	detail string
}

// backoffSignal reports the persisted per-source backoff on the waited-on feed row —
// a future next_attempt_at (download.bumpSourceFailure / cooldownSource). ok=false
// when there is no future backoff.
func backoffSignal(fc feedCarrier, now time.Time) (waitSignal, bool) {
	if fc.pc != nil && fc.pc.NextAttemptAt != nil && fc.pc.NextAttemptAt.After(now) {
		return waitSignal{waitBackoff, fc.pc.NextAttemptAt, fc.pc.LastError}, true
	}
	return waitSignal{}, false
}

// coolingSignal reports the source-wide circuit-breaker cooldown — a tripped breaker
// whose cooldown_until is still in the future. ok=false when the source has no
// breaker row, or it is not currently tripped.
func coolingSignal(breaker *sourcegate.BreakerState, now time.Time) (waitSignal, bool) {
	if breaker != nil && breaker.IsCoolingDown(now) {
		return waitSignal{waitCoolingDown, breaker.CooldownUntil, breaker.LastError}, true
	}
	return waitSignal{}, false
}

// laterSignal picks the binding wait signal when BOTH a backoff and a breaker
// cooldown apply: the one with the LATER timestamp (the engine must wait out both, so
// the later is the effective retry ETA; the breaker wins a tie as the broader block),
// carrying its own detail and falling back to the other's when it has none.
func laterSignal(backoff, cooling waitSignal) waitSignal {
	if backoff.until.After(*cooling.until) {
		backoff.detail = firstNonEmpty(backoff.detail, cooling.detail)
		return backoff
	}
	cooling.detail = firstNonEmpty(cooling.detail, backoff.detail)
	return cooling
}

// firstNonEmpty returns the first non-empty string, or "". Mirrors the identically
// named helper in the download engine — kept local so this read model does not
// import the engine package.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// isUpgrading reports whether a chapter state is one where an upgrade TARGET is
// meaningful: flagged for upgrade (upgrade_available) or mid-upgrade (upgrading).
// Every other state — including downloaded and wanted — has no target.
func isUpgrading(st entchapter.State) bool {
	return st == entchapter.StateUpgradeAvailable || st == entchapter.StateUpgrading
}

// satisfiedImportanceOf is the importance bar an upgrade target must BEAT: the
// CURRENT importance of the source that satisfies the chapter while it is still
// attached and ranked, else the frozen satisfied_importance watermark (the source
// was removed by the owner, or is PARKED at importance 0 by a library merge — 0 is
// a sentinel, not a rank). This mirrors download.effectiveSatisfiedImportance's
// rule, minus its healing write (a read model never writes). A chapter with neither
// (never downloaded) has a bar of 0, so any feed-bearing source can be its target.
func satisfiedImportanceOf(ch *ent.Chapter, provByID map[uuid.UUID]*ent.SeriesProvider) int {
	frozen := 0
	if ch.SatisfiedImportance != nil {
		frozen = *ch.SatisfiedImportance
	}
	if ch.SatisfiedByProviderID == nil {
		return frozen
	}
	sp, ok := provByID[*ch.SatisfiedByProviderID]
	if !ok || sp.Importance == 0 {
		return frozen
	}
	return sp.Importance
}
