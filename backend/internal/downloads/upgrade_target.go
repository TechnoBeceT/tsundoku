package downloads

import (
	"cmp"
	"slices"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/series"
)

// feedCarrier is one source that OFFERS a given chapter_key, paired with the id of
// the ProviderChapter feed row through which it offers it. The feed-row id is not
// cosmetic: it is the SECONDARY sort key the engine ranks candidates by, so
// carrying it is what lets this read model order ties exactly as the engine does
// (see newUpgradeTargetIndex). UNIQUE(series_provider_id, chapter_key) means a
// provider contributes exactly one feed row per key, so pcID is unambiguous.
type feedCarrier struct {
	provider *ent.SeriesProvider
	pcID     uuid.UUID
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
func newUpgradeTargetIndex(provs []*ent.SeriesProvider) upgradeTargetIndex {
	idx := upgradeTargetIndex{}
	for _, sp := range provs {
		for _, pc := range sp.Edges.ProviderChapters {
			idx[pc.ChapterKey] = append(idx[pc.ChapterKey], feedCarrier{provider: sp, pcID: pc.ID})
		}
	}
	for key, carriers := range idx {
		slices.SortStableFunc(carriers, func(a, b feedCarrier) int {
			if c := cmp.Compare(b.provider.Importance, a.provider.Importance); c != 0 {
				return c // importance DESC
			}
			return cmp.Compare(a.pcID.String(), b.pcID.String()) // then feed-row id ASC
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
// GOTCHA — where this can disagree with the engine: the engine (download.
// bestUpgradeCandidate) additionally excludes a source that has exhausted its
// per-source retry budget, is inside its per-source cooldown, or whose politeness
// circuit-breaker is tripped. This DTO layer knows none of that (reading it would
// cost the very N+1 this index exists to avoid), so it names the source the chapter
// is MEANT to converge to. When that source is temporarily excluded, the engine may
// fetch from a lower one — or defer the upgrade entirely — while the row still shows
// the intended target. Treat this as a UI hint, never as engine state.
func upgradeTargetLabel(ch *ent.Chapter, idx upgradeTargetIndex, provByID map[uuid.UUID]*ent.SeriesProvider) string {
	if !isUpgrading(ch.State) {
		return ""
	}
	for _, c := range idx[ch.ChapterKey] {
		sp := c.provider
		if ch.SatisfiedByProviderID != nil && sp.ID == *ch.SatisfiedByProviderID {
			continue
		}
		// The list is importance-DESC, so the first candidate that fails the bar means
		// no remaining one can clear it either.
		if sp.Importance <= satisfiedImportanceOf(ch, provByID) {
			return ""
		}
		return series.ProviderLabel(sp)
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
