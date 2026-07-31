package download

import (
	"errors"
	"net/http"
	"time"

	"github.com/technobecet/tsundoku/internal/pkg/errorclass"
	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// classifyFetchFailure implements the classification model (owner-ratified: "a
// fetch is a fetch — the SAME rule for downloads AND upgrades"). It answers the
// one question both the per-(chapter,source) counter and the circuit-breaker key
// off: is THIS chapter broken on THIS source, or is the SOURCE down/blocking
// everything?
//
//   - CHAPTER-SPECIFIC — this one chapter cannot be served by this
//     source, but the source is fine for every other series/chapter:
//     errorclass not_found / no_pages / parse, plus the engine sentinels
//     ErrBrokenPage (a page failed image validation), ErrNoPages (the chapter
//     resolved to zero pages), ErrNotLiveSource (a disk-origin provider that is
//     not a real source id — it structurally can never serve this or any chapter,
//     so it must EXHAUST rather than loop forever; see
//     TestDiskOriginProvider_ExhaustsNotLoops), and ErrImageFetch (a per-image byte
//     fetch that survived stagePages' transient retries — reaching the image stage
//     proves the source session is alive, so a persistent single-image failure is
//     about this page/chapter, not the source; one flaky page must never trip the
//     breaker).
//     Consequence: the per-source counter is BUMPED (bumpSourceFailure → attempts++,
//     so this source gives up on this chapter at max_retries) and the breaker is NOT
//     tripped (the source stays available for its other chapters).
//   - SOURCE-WIDE / ban — the whole source is down or blocking:
//     rate_limit / captcha / timeout / network / server_error / unknown.
//     Consequence: the per-source counter is only COOLED DOWN (cooldownSource →
//     next_attempt_at, attempts UNCHANGED, so a ban never exhausts/drains the queue)
//     and the breaker IS recorded (gateRecordFailure — pause the whole source; its
//     chapters WAIT, excluded from candidacy, while it is tripped).
//   - DEFERRED — the source is deliberately WITHHOLDING the chapter (paywall /
//     early access). Neither party is at fault, so it is the only arm that does
//     BOTH: leaves attempts untouched AND leaves the breaker closed, re-checking
//     on a day-scale horizon instead (deferSource).
//
// It is reused by BOTH the download path (chargeFetchFailure + tryCandidate's gate
// call) AND the upgrade path (handleUpgradeFailure + fetchAndRender's gate call),
// so the single definition is the one place the two axes (counter, breaker) and the
// two fetch paths (download, upgrade) agree on what a failure MEANS.
//
// A LOCAL render/persist fault is NOT a fetch failure and never reaches here: it
// charges nothing and trips nothing (⑥, the nil-failedPC path).

// failureKind is the three-way outcome of classifying a fetch failure. It
// replaced a bool once a third behaviour was needed (GAP-141): a paywalled
// chapter is neither "this chapter is broken" nor "this source is down".
type failureKind int

const (
	// failureSourceWide is the CONSERVATIVE default: the whole source looks
	// down/blocking. Cools the source down (attempts UNCHANGED) and trips the
	// breaker. Anything unrecognised lands here on purpose.
	failureSourceWide failureKind = iota
	// failureChapterSpecific means THIS chapter is broken on THIS source while the
	// source is fine for everything else. Bumps the (chapter,source) attempts
	// budget so it exhausts at max_retries; never trips the breaker.
	failureChapterSpecific
	// failureDeferred means the source is deliberately WITHHOLDING the chapter
	// (paywall / early access). Neither the chapter nor the source is faulty, so it
	// must trip NO breaker and burn NO attempts — it is simply re-checked on a
	// day-scale horizon, because the chapter becomes free on its own.
	failureDeferred
)

// classifyFetchFailure is THE fetch-failure classifier, shared by the download
// and upgrade paths. See failureKind for what each outcome costs.
//
// It classifies the SOURCE's error, not the transport that delivered it — see
// sourceErrorOf. Typed engine sentinels win outright, because they are
// unambiguous where a substring match can only approximate.
func classifyFetchFailure(err error) failureKind {
	if errors.Is(err, sourceengine.ErrBrokenPage) ||
		errors.Is(err, sourceengine.ErrNoPages) ||
		errors.Is(err, sourceengine.ErrNotLiveSource) ||
		errors.Is(err, sourceengine.ErrImageFetch) {
		return failureChapterSpecific
	}
	switch errorclass.Classify(sourceErrorOf(err)) {
	case errorclass.CategoryLocked:
		return failureDeferred
	case errorclass.CategoryNotFound, errorclass.CategoryNoPages, errorclass.CategoryParse:
		return failureChapterSpecific
	default:
		return failureSourceWide
	}
}

// sourceErrorOf strips the engine host's transport envelope so classification
// sees what the SOURCE said, not the status code that carried it (GAP-141).
//
// Engine-host answers 502 for ANY exception thrown inside an extension, and
// *UpstreamError's Error() renders that as "upstream error (status 502): <msg>".
// Classifying that flattened string let the envelope's own "502" match the
// server_error rule, so EVERY extension-level failure — a paywalled chapter, a
// parse error, a bad selector — read as SOURCE-WIDE and tripped the breaker for
// the whole source. UpstreamError already carries the source's message
// separately, so the fix is to classify that field instead of the rendered string.
//
// ONLY 502 is unwrapped. Any other status is engine-host infrastructure (a stray
// 404/405 route fault), where the status IS the real signal and the inner text is
// not the source speaking; those keep classifying exactly as they did before.
func sourceErrorOf(err error) error {
	var upstream *sourceengine.UpstreamError
	if errors.As(err, &upstream) && upstream.Status == http.StatusBadGateway {
		return errors.New(upstream.Msg)
	}
	return err
}

// minLockedHorizon is the floor lockedHorizon clamps to. It exists because a
// deferral has NO other backstop.
const minLockedHorizon = time.Hour

// lockedHorizon clamps the configured locked-chapter re-check interval to a sane
// floor (GAP-141).
//
// The jobs.locked_retry_interval >= 1h bound is enforced by the settings
// validator, which only guards an owner-set OVERRIDE — the env-injected default
// and settings.Static's zero value both reach the dispatcher unchecked. A zero
// interval would put next_attempt_at at `now`, making the source an immediate
// candidate again on the very next cycle.
//
// That is far worse here than for the ordinary retry backoff: a deferral burns NO
// attempts and trips NO breaker, so nothing else ever stops the loop. Where an
// exhausting failure self-limits at max_retries and a ban self-limits at the
// breaker, a zero-interval deferral would re-fetch the same withheld chapter every
// cycle, forever and invisibly. The clamp is the only backstop, so it lives at the
// point of use rather than in a validator a caller can bypass.
func lockedHorizon(d time.Duration) time.Duration {
	if d < minLockedHorizon {
		return minLockedHorizon
	}
	return d
}

// reclassifiedByUnwrap reports whether stripping the engine host's 502 envelope
// CHANGED the verdict from source-wide to chapter-specific.
//
// It exists purely to make a RATIFIED RESIDUAL RISK observable (GAP-141). The
// unwrap is what stops the envelope's own "502" masking the source's real error,
// and the owner ratified keeping it — but it also means any 502-wrapped
// parse / not-found / no-pages message now bumps the attempts budget instead of
// cooling the source down. If a source begins serving an HTML interstitial where
// JSON was expected (which extensions surface as exactly those classes), its
// queued chapters drain toward permanently_failed while the breaker never engages:
// no error, no health signal, nothing to notice until the library is already thin.
//
// This predicate is deliberately NARROW — only the source-wide → chapter-specific
// transition. The reverse direction is harmless, and a paywall classifies as
// locked with or without the unwrap, so a deferral raises no warning. A signal
// that fires on the ordinary case is a signal nobody reads.
func reclassifiedByUnwrap(err error) bool {
	var upstream *sourceengine.UpstreamError
	if !errors.As(err, &upstream) || upstream.Status != http.StatusBadGateway {
		return false
	}
	// What the flattened envelope WOULD have said, versus what the source itself says.
	enveloped := errorclass.Classify(errors.New(upstream.Error()))
	unwrapped := errorclass.Classify(errors.New(upstream.Msg))
	return isSourceWideCategory(enveloped) && !isSourceWideCategory(unwrapped)
}

// isSourceWideCategory mirrors classifyFetchFailure's category arms: everything
// that is not explicitly chapter-specific or locked is source-wide, the
// conservative default.
//
// The mirroring is ENFORCED, not merely intended — TestIsSourceWideCategoryMirrors-
// ClassifyFetchFailure walks every errorclass category and fails when the two
// disagree either way. It needs that pin because the arms are individually
// no-ops: dropping the locked arm here changes no verdict on its own (locked
// matches ahead of server_error regardless), so nothing else in the package
// notices the drift.
func isSourceWideCategory(category string) bool {
	switch category {
	case errorclass.CategoryLocked,
		errorclass.CategoryNotFound, errorclass.CategoryNoPages, errorclass.CategoryParse:
		return false
	default:
		return true
	}
}
