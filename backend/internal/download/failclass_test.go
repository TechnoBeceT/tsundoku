package download

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/technobecet/tsundoku/internal/pkg/errorclass"
	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// wrappedImageFetch mimics the error stagePages produces for a transient per-image
// failure that survived the retries: ErrImageFetch wrapping the underlying cause.
func wrappedImageFetch(underlying string) error {
	return fmt.Errorf("sourceengine fetcher: image: %w: %v", sourceengine.ErrImageFetch, errors.New(underlying))
}

// TestClassifyFetchFailure_ErrImageFetch proves a surviving per-image fetch
// failure (ErrImageFetch) is classified CHAPTER-SPECIFIC even though its underlying
// cause (a 502 server_error) is a source-wide errorclass category — the sentinel
// override is what keeps one flaky page off the source breaker.
func TestClassifyFetchFailure_ErrImageFetch(t *testing.T) {
	err := wrappedImageFetch("502 bad gateway")
	if got := classifyFetchFailure(err); got != failureChapterSpecific {
		t.Errorf("classifyFetchFailure(ErrImageFetch) = %v, want failureChapterSpecific (a flaky page is chapter-specific)", got)
	}
	// The whole point: shouldRecordGateFailure must be false so the breaker is not
	// tripped, while the caller still BUMPS the per-source budget (chapter-specific).
	if shouldRecordGateFailure(context.Background(), err) {
		t.Errorf("shouldRecordGateFailure(ErrImageFetch) = true, want false (one flaky page must not trip the source breaker)")
	}
}

// TestShouldRecordGateFailure_BanImageStaysSourceWide proves a ban-class image
// error (captcha / rate_limit) — which stagePages leaves UNWRAPPED — stays
// source-wide and DOES record a breaker failure, so a genuine block still pauses the
// whole source.
func TestShouldRecordGateFailure_BanImageStaysSourceWide(t *testing.T) {
	for _, msg := range []string{"sourceengine fetcher: image: cloudflare challenge", "sourceengine fetcher: image: 429 too many requests"} {
		err := errors.New(msg)
		if got := classifyFetchFailure(err); got != failureSourceWide {
			t.Errorf("classifyFetchFailure(%q) = %v, want failureSourceWide (a ban is source-wide)", msg, got)
		}
		if !shouldRecordGateFailure(context.Background(), err) {
			t.Errorf("shouldRecordGateFailure(%q) = false, want true (a ban must trip the breaker)", msg)
		}
	}
}

// TestShouldRecordGateFailure_PagesErrorStillTripsBreaker proves the ban-detection
// carve-out: a page-RESOLUTION (Client.Pages) source-wide failure is NOT wrapped in
// ErrImageFetch, stays source-wide, and still records a breaker failure — a real ban
// at the session stage blocks the whole source exactly as before.
func TestShouldRecordGateFailure_PagesErrorStillTripsBreaker(t *testing.T) {
	err := errors.New("sourceengine fetcher: pages: 502 bad gateway")
	if got := classifyFetchFailure(err); got != failureSourceWide {
		t.Errorf("classifyFetchFailure(pages error) = %v, want failureSourceWide (a page-resolution failure is source-wide)", got)
	}
	if !shouldRecordGateFailure(context.Background(), err) {
		t.Errorf("shouldRecordGateFailure(pages error) = false, want true (ban detection at the session stage must be preserved)")
	}
}

// TestClassifyFetchFailure_LockedIsDeferred pins the paywall/early-access class
// (GAP-141). Hive Scans withholds its newest chapters behind coins for a few
// days, then releases them for free. That is NOT a failure of the source, so it
// must be neither of the two pre-existing kinds:
//   - not SOURCE-WIDE, or one paid chapter trips the breaker and takes the whole
//     source offline (observed live: 36 locked errors in 6h left Hive stuck in a
//     cooldown loop for 3+ hours);
//   - not CHAPTER-SPECIFIC, or it burns the (chapter,source) attempts budget and
//     reaches permanently_failed days BEFORE the chapter actually goes free, after
//     which nothing ever retries it.
func TestClassifyFetchFailure_LockedIsDeferred(t *testing.T) {
	// The exact shape the engine host produces for Hive's paywalled chapters.
	err := fmt.Errorf("sourceengine: fetcher: pages: %w",
		&sourceengine.UpstreamError{Status: 502, Msg: "Exception: Chapter locked (coins required)"})
	if got := classifyFetchFailure(err); got != failureDeferred {
		t.Fatalf("classifyFetchFailure(locked) = %v, want failureDeferred", got)
	}
}

// TestClassifyFetchFailure_UnwrapsUpstreamEnvelope is the broader defect behind
// GAP-141 and the reason this is a classification fix rather than a paywall
// special-case. Engine-host wraps EVERY extension exception in an
// "upstream error (status 502)" envelope. Classifying the FLATTENED string made
// that envelope's own "502" match the server_error rule, so every extension-level
// failure — whatever it really was — read as SOURCE-WIDE and tripped the breaker.
// UpstreamError already carries the source's own message separately; classify THAT.
func TestClassifyFetchFailure_UnwrapsUpstreamEnvelope(t *testing.T) {
	err := fmt.Errorf("sourceengine: fetcher: pages: %w",
		&sourceengine.UpstreamError{Status: 502, Msg: "could not parse chapter page: unexpected end of json"})
	if got := classifyFetchFailure(err); got != failureChapterSpecific {
		t.Fatalf("classifyFetchFailure(parse inside 502) = %v, want failureChapterSpecific "+
			"(the envelope's own 502 must not decide the class)", got)
	}
}

// TestClassifyFetchFailure_NonBadGatewayKeepsEnvelope guards the unwrap's SCOPE.
// Only 502 means "the engine reached the source and the SOURCE call failed", so
// only 502 carries a source error worth re-judging. Any other status is an
// engine-host transport/routing fault — infrastructure, not the source — and must
// keep being classified on the envelope exactly as it was before GAP-141.
//
// The case is chosen so the two behaviours are DISTINGUISHABLE: unwrapped, the
// message is a clean `parse` (chapter-specific); left wrapped, the envelope's own
// "500" matches server_error first (source-wide). Asserting source-wide therefore
// proves the unwrap did NOT fire.
func TestClassifyFetchFailure_NonBadGatewayKeepsEnvelope(t *testing.T) {
	err := fmt.Errorf("sourceengine: %w",
		&sourceengine.UpstreamError{Status: 500, Msg: "unexpected end of json"})
	if got := classifyFetchFailure(err); got != failureSourceWide {
		t.Fatalf("classifyFetchFailure(500 envelope) = %v, want failureSourceWide "+
			"(only a 502 envelope may be unwrapped)", got)
	}
}

// TestClassifyFetchFailure_ContainmentStatusesStaySourceWide proves neither
// scheduler saturation nor a host execution deadline spends a chapter/source
// retry budget. Both are engine/source availability failures, while 504 remains
// distinguishable through the existing timeout category.
func TestClassifyFetchFailure_ContainmentStatusesStaySourceWide(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"queue full", &sourceengine.UpstreamError{Status: 503, Msg: "source queue full"}},
		{"deadline", &sourceengine.UpstreamError{Status: 504, Msg: "source call timed out"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyFetchFailure(tc.err); got != failureSourceWide {
				t.Fatalf("classifyFetchFailure = %v, want failureSourceWide", got)
			}
			if !shouldRecordGateFailure(context.Background(), tc.err) {
				t.Fatal("containment failure must retain source-wide breaker semantics")
			}
		})
	}
}

// TestClassifyFetchFailure_BanStaysSourceWide re-pins the drain-prevention
// behaviour across the rewrite: a ban must still be source-wide so it cools the
// source down without burning any chapter's attempts.
func TestClassifyFetchFailure_BanStaysSourceWide(t *testing.T) {
	for _, msg := range []string{"429 too many requests", "cloudflare challenge: just a moment"} {
		err := fmt.Errorf("sourceengine: fetcher: pages: %w",
			&sourceengine.UpstreamError{Status: 502, Msg: msg})
		if got := classifyFetchFailure(err); got != failureSourceWide {
			t.Fatalf("classifyFetchFailure(%q) = %v, want failureSourceWide", msg, got)
		}
	}
}

// TestShouldRecordGateFailure_LockedNeverTripsBreaker pins the property the Hive
// Scans incident was actually about (GAP-141). Classification is only half the
// fix — this is the half the owner sees. A paywalled chapter must never count
// toward the circuit breaker, because the source is healthy and serving every
// other chapter; 36 locked errors in six hours had left Hive stuck in a cooldown
// loop for 3+ hours, with warmup skipped every ~17 minutes.
func TestShouldRecordGateFailure_LockedNeverTripsBreaker(t *testing.T) {
	locked := fmt.Errorf("sourceengine: fetcher: pages: %w",
		&sourceengine.UpstreamError{Status: 502, Msg: "Exception: Chapter locked (coins required)"})
	if shouldRecordGateFailure(context.Background(), locked) {
		t.Fatal("shouldRecordGateFailure(locked) = true, want false — a paywalled chapter must not trip the breaker")
	}

	// Control: a genuine ban on the SAME envelope still trips it, so the test
	// above cannot pass merely because the unwrap disabled breaker recording.
	banned := fmt.Errorf("sourceengine: fetcher: pages: %w",
		&sourceengine.UpstreamError{Status: 502, Msg: "429 too many requests"})
	if !shouldRecordGateFailure(context.Background(), banned) {
		t.Fatal("shouldRecordGateFailure(rate limit) = false, want true — a real ban must still trip the breaker")
	}
}

// TestReclassifiedByUnwrap pins the OBSERVABILITY signal for the residual risk the
// owner ratified when keeping the 502 unwrap (GAP-141).
//
// The unwrap flips some 502-wrapped failures from SOURCE-WIDE to CHAPTER-SPECIFIC.
// That direction is the dangerous one: chapter-specific bumps the attempts budget
// and does NOT trip the breaker, so if a source starts serving an HTML interstitial
// where JSON was expected — which extensions throw as parse/not-found — its queued
// chapters drain toward permanently_failed with no breaker, no error and no health
// signal. This predicate is what makes that visible in the log instead of surfacing
// a week later as a drained library.
//
// It must fire ONLY on that transition, or the warning becomes noise and stops
// being read.
func TestReclassifiedByUnwrap(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			// Envelope alone reads server_error (source-wide); unwrapped it is a clean
			// parse (chapter-specific). THIS is the transition worth warning about.
			"parse inside a 502 flips the verdict",
			&sourceengine.UpstreamError{Status: 502, Msg: "could not parse chapter page: unexpected end of json"},
			true,
		},
		{
			// Locked matches first either way, so the verdict never changes — a paywall
			// must not generate a warning on every deferral.
			"locked is unchanged by the unwrap",
			&sourceengine.UpstreamError{Status: 502, Msg: "Exception: Chapter locked (coins required)"},
			false,
		},
		{
			// Source-wide before and after: no transition, no warning.
			"a real ban stays source-wide",
			&sourceengine.UpstreamError{Status: 502, Msg: "429 too many requests"},
			false,
		},
		{
			// Only 502 is ever unwrapped, so nothing can flip.
			"a non-502 envelope is never unwrapped",
			&sourceengine.UpstreamError{Status: 500, Msg: "unexpected end of json"},
			false,
		},
		{
			// A plain error carries no envelope to strip.
			"an unwrapped error has nothing to strip",
			errors.New("chapter not found"),
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := reclassifiedByUnwrap(fmt.Errorf("sourceengine: fetcher: pages: %w", tc.err)); got != tc.want {
				t.Fatalf("reclassifiedByUnwrap = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestIsSourceWideCategoryMirrorsClassifyFetchFailure PINS the mirroring
// isSourceWideCategory's doc comment claims. Without it the claim was unenforced:
// deleting errorclass.CategoryLocked from that switch left the whole package green,
// because the arm is a no-op in isolation (locked matches ahead of server_error
// either way) and nothing compared the two functions against each other.
//
// The property under test is the only one that matters: for EVERY category, the
// predicate must agree with what classifyFetchFailure actually does with an error
// of that category. It fails on divergence in both directions — a category dropped
// from the false-list (locked / not_found / no_pages / parse) and a category
// wrongly added to it.
//
// MAINTENANCE: errorclass exports no list of its categories, so this table is the
// enumeration. A new errorclass category needs a row here, or it goes unmirrored.
func TestIsSourceWideCategoryMirrorsClassifyFetchFailure(t *testing.T) {
	// One representative message per category. Each is asserted to actually
	// classify as its category first, so a reworded errorclass rule cannot quietly
	// turn this into a test of the wrong arm.
	cases := []struct{ category, message string }{
		{errorclass.CategoryCaptcha, "cloudflare challenge"},
		{errorclass.CategoryRateLimit, "too many requests"},
		{errorclass.CategoryNotFound, "chapter not found"},
		{errorclass.CategoryLocked, "chapter locked"},
		{errorclass.CategoryServerError, "internal server error"},
		{errorclass.CategoryTimeout, "request timed out"},
		{errorclass.CategoryNetwork, "connection refused"},
		{errorclass.CategoryParse, "malformed response body"},
		{errorclass.CategoryNoPages, "empty chapter"},
		{errorclass.CategoryBrokenImage, "incomplete image"},
		{errorclass.CategoryUnknown, "something odd happened"},
	}
	for _, tc := range cases {
		t.Run(tc.category, func(t *testing.T) {
			if got := errorclass.ClassifyMessage(tc.message); got != tc.category {
				t.Fatalf("fixture drifted: ClassifyMessage(%q) = %q, want %q", tc.message, got, tc.category)
			}
			wantSourceWide := classifyFetchFailure(errors.New(tc.message)) == failureSourceWide
			if got := isSourceWideCategory(tc.category); got != wantSourceWide {
				t.Errorf("isSourceWideCategory(%q) = %v, but classifyFetchFailure treats it as source-wide = %v — the two have drifted",
					tc.category, got, wantSourceWide)
			}
		})
	}
}
