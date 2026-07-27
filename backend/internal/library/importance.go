package library

// importanceStep is the spacing between adjacent providers on a clean importance
// spread. Higher importance = higher priority (see the repo architecture notes,
// "Provider importance — higher number = higher priority").
const importanceStep = 10

// belowExistingImportances plans importances for `count` newly attached sources
// placed below a series' `existing` providers (whose current importances are
// passed HIGHEST-first). It returns:
//
//   - existingImps: the existing providers RENUMBERED (same highest-first order)
//     so they sit above the whole new batch — non-nil ONLY when the new batch
//     cannot fit below the existing providers while every slot stays a real rank
//     (e.g. a disk-origin provider sitting at importance 1 leaves no room below).
//     When nil, the existing providers keep their current importances untouched.
//   - newImps: the new sources' importances (index 0 = highest of the batch),
//     ALWAYS >= importanceStep and ALWAYS below the (possibly renumbered)
//     existing providers.
//
// Rationale: the original scheme returned minExisting-(i+1)*step, which went
// NEGATIVE whenever the existing providers occupied small importances (a
// disk-origin provider is importance 1, so new sources got -9, -19, …). Negative
// importances then made the reorder endpoint 400 ("importance must be
// non-negative") and could not be ranked coherently.
//
// Bounding the plan at zero was not enough, and that is what the >= importanceStep
// room test fixes (GAP-125). Importance 0 is RESERVED — it is the park sentinel a
// library merge writes onto a provider for the whole window in which it renames
// that series' CBZ files — so it is not a rank the planner may hand out. Whenever
// count*importanceStep landed exactly on minExisting the old test passed and the
// LOWEST new slot was planned at 0: existing {70, 60} plus a six-source batch gave
// 50, 40, 30, 20, 10, 0, stranding the last source of the batch on the sentinel,
// where download.effectiveSatisfiedImportance reads it as "parked, no rank" so it
// can never heal a chapter's satisfied-importance watermark and
// chapter.RankedLiveCandidates ranks it below every sibling. Requiring room for a
// full importanceStep instead sends that case down the renumber branch, where the
// whole set lands on a clean spread of real ranks.
//
// Keeping every value a real rank — renumbering the existing providers up when
// there is no room below them — keeps the reorder normalization and the upgrade
// engine coherent while still landing new sources below the existing ones by
// default (decision E's gap-fill intent; promoting a source above them is the
// owner's separate ReorderProviders action).
func belowExistingImportances(existing []int, count int) (existingImps, newImps []int) {
	if count <= 0 {
		return nil, []int{}
	}

	// No existing providers: fall back to the Adopt-wizard scale (count-i)*step.
	if len(existing) == 0 {
		return nil, descendingSpread(count, count)
	}

	minExisting := existing[0]
	for _, v := range existing[1:] {
		if v < minExisting {
			minExisting = v
		}
	}

	// Room below minExisting for `count` descending real ranks? The lowest new slot
	// would be minExisting - count*step; the batch fits underneath, untouched, only
	// while that is still at or above importanceStep. Testing >= 0 here let the
	// lowest slot land exactly on the reserved sentinel — see the doc comment.
	if minExisting-count*importanceStep >= importanceStep {
		newImps = make([]int, count)
		for i := range newImps {
			newImps[i] = minExisting - (i+1)*importanceStep
		}
		return nil, newImps
	}

	// Cramped: renumber the whole set onto one clean descending spread — existing
	// (highest-first) on top, new below — so every value is a real rank and the new
	// batch still ranks below the existing providers.
	total := len(existing) + count
	existingImps = descendingSpread(total, len(existing))
	newImps = make([]int, count)
	for j := range newImps {
		newImps[j] = (count - j) * importanceStep
	}
	return existingImps, newImps
}

// descendingSpread returns the first `n` values of a clean descending spread
// whose highest slot is `top`*step: top*step, (top-1)*step, … Every value is a
// positive multiple of step (callers pass top >= n >= 1).
func descendingSpread(top, n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = (top - i) * importanceStep
	}
	return out
}
