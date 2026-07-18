package downloads

import (
	"cmp"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/pkg/chapterrange"
	"github.com/technobecet/tsundoku/internal/series"
)

// feedCarrier is one source that OFFERS a given chapter_key, paired with the
// ProviderChapter feed row through which it offers it. The feed row is not
// cosmetic on two counts: its ID is the SECONDARY sort key the engine ranks
// candidates by, so carrying it is what lets this read model order ties exactly as
// the engine does (see newUpgradeTargetIndex); and its next_attempt_at / last_error
// are the persisted per-source cooldown the deferral read surfaces (see
// waitedOnCarrier / deferral). UNIQUE(series_provider_id, chapter_key) means a
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

// upgradeTargetLabel resolves the display label of the source a chapter is
// upgrading TO, or "" when the chapter is not upgrading (or no better source can be
// named). It is what lets the UI render "Comix → Asura Scans" instead of showing
// only the source being REPLACED (satisfied_by, which is where the file came from).
//
// The rule — the INTENDED target, deliberately NOT the engine's authoritative pick:
// among the sources whose feed carries this chapter's chapter_key (ranked exactly as
// the engine ranks them — see upgradeTargetIndex), take the highest-importance one
// that is (a) NOT the chapter's current satisfier and (b)
// strictly higher than the satisfier's CURRENT importance (its frozen
// satisfied_importance watermark when the satisfier was removed or is parked at the
// importance-0 merge sentinel — mirroring download.effectiveSatisfiedImportance).
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
func upgradeTargetLabel(ch *ent.Chapter, idx upgradeTargetIndex, provByID map[uuid.UUID]*ent.SeriesProvider) string {
	if c, ok := upgradeTargetCarrier(ch, idx, provByID); ok {
		return series.ProviderLabel(c.provider)
	}
	return ""
}

// upgradeTargetCarrier picks the feed carrier a chapter is upgrading TO, applying
// the exact rule upgradeTargetLabel documents: the highest-importance carrier that
// is (a) not the current satisfier and (b) strictly outranks the satisfier's
// current importance (frozen watermark when removed / parked at the merge sentinel).
// It returns ok=false when the chapter is not upgrading, or no carrier clears the
// bar. Extracted so upgradeTargetLabel (which wants the NAME) and waitedOnCarrier
// (which wants the ProviderChapter row, to read its cooldown) share one definition
// (§2 DRY) and can never disagree on which source is the target.
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

// waitedOnCarrier returns the ProviderChapter feed row of the source the engine is
// WAITING ON to advance a queued chapter — the source whose persisted cooldown, if
// any, is why the row is not moving:
//
//   - upgrade_available / upgrading → the UPGRADE TARGET (the source it is
//     converging to; the same carrier upgradeTargetLabel names). While that target
//     is cooling down after a failed upgrade fetch (download.cooldownSource), the
//     upgrade is deferred and the row sits still.
//   - wanted → the PRIMARY live candidate (highest-importance source whose feed
//     carries the key — the same source chapterProvider names). While that source is
//     inside its per-source backoff (download.bumpSourceFailure), the download is
//     deferred.
//
// It returns nil for every other state, and when no source can be named (nothing
// carries the key, or no valid upgrade target), so the caller surfaces no deferral.
func waitedOnCarrier(ch *ent.Chapter, idx upgradeTargetIndex, provByID map[uuid.UUID]*ent.SeriesProvider) *ent.ProviderChapter {
	switch {
	case isUpgrading(ch.State):
		if c, ok := upgradeTargetCarrier(ch, idx, provByID); ok {
			return c.pc
		}
		return nil
	case ch.State == entchapter.StateWanted:
		if carriers := idx[ch.ChapterKey]; len(carriers) > 0 {
			return carriers[0].pc
		}
		return nil
	default:
		return nil
	}
}

// deferral reports the persisted per-source cooldown the engine is waiting out on a
// queued chapter's source: it returns (next_attempt_at, last_error) ONLY when that
// source has a next_attempt_at genuinely in the FUTURE relative to now — the backoff
// the engine records on a failed download (download.bumpSourceFailure) or a failed
// upgrade fetch (download.cooldownSource). A nil carrier, or a nil / past
// next_attempt_at, means the source is ready to try next cycle, so it returns
// (nil, "") — a ready row is never mislabelled as waiting. reason is the carrier's
// last_error and travels with the deferral (it is meaningless without one).
//
// KNOWN GAP (v1, owner-ratified): this surfaces only the PERSISTED cooldown. The
// engine's in-memory source-politeness circuit-breaker (download/upgrade.go) can
// defer a source WITHOUT writing next_attempt_at; a chapter held back purely by it
// has no persisted timestamp, so it correctly shows no fabricated reason and reads
// as plain "ready" until the next cycle. Threading engine internals into this read
// path is deliberately out of scope.
func deferral(pc *ent.ProviderChapter, now time.Time) (until *time.Time, reason string) {
	if pc == nil || pc.NextAttemptAt == nil || !pc.NextAttemptAt.After(now) {
		return nil, ""
	}
	return pc.NextAttemptAt, pc.LastError
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
