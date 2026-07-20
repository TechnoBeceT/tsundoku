package download

import (
	"errors"

	"github.com/technobecet/tsundoku/internal/pkg/errorclass"
	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// isChapterSpecificFailure classifies a FETCH failure (owner-ratified model: "a
// fetch is a fetch — the SAME rule for downloads AND upgrades"). It answers the
// one question both the per-(chapter,source) counter and the circuit-breaker key
// off: is THIS chapter broken on THIS source, or is the SOURCE down/blocking
// everything?
//
//   - CHAPTER-SPECIFIC (returns true) — this one chapter cannot be served by this
//     source, but the source is fine for every other series/chapter:
//     errorclass not_found / no_pages / parse, plus the engine sentinels
//     ErrBrokenPage (a page failed image validation), ErrNoPages (the chapter
//     resolved to zero pages), and ErrNotLiveSource (a disk-origin provider that is
//     not a real source id — it structurally can never serve this or any chapter,
//     so it must EXHAUST rather than loop forever; see
//     TestDiskOriginProvider_ExhaustsNotLoops).
//     Consequence: the per-source counter is BUMPED (bumpSourceFailure → attempts++,
//     so this source gives up on this chapter at max_retries) and the breaker is NOT
//     tripped (the source stays available for its other chapters).
//   - SOURCE-WIDE / ban (returns false) — the whole source is down or blocking:
//     rate_limit / captcha / timeout / network / server_error / unknown.
//     Consequence: the per-source counter is only COOLED DOWN (cooldownSource →
//     next_attempt_at, attempts UNCHANGED, so a ban never exhausts/drains the queue)
//     and the breaker IS recorded (gateRecordFailure — pause the whole source; its
//     chapters WAIT, excluded from candidacy, while it is tripped).
//
// It is reused by BOTH the download path (chargeFetchFailure + tryCandidate's gate
// call) AND the upgrade path (handleUpgradeFailure + fetchAndRender's gate call),
// so the single definition is the one place the two axes (counter, breaker) and the
// two fetch paths (download, upgrade) agree on what a failure MEANS.
//
// A LOCAL render/persist fault is NOT a fetch failure and never reaches here: it
// charges nothing and trips nothing (⑥, the nil-failedPC path).
func isChapterSpecificFailure(err error) bool {
	if errors.Is(err, sourceengine.ErrBrokenPage) ||
		errors.Is(err, sourceengine.ErrNoPages) ||
		errors.Is(err, sourceengine.ErrNotLiveSource) {
		return true
	}
	switch errorclass.Classify(err) {
	case errorclass.CategoryNotFound, errorclass.CategoryNoPages, errorclass.CategoryParse:
		return true
	default:
		return false
	}
}
