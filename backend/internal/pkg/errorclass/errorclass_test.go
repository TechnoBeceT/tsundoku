package errorclass_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/technobecet/tsundoku/internal/pkg/errorclass"
)

// TestClassifyMessage_EachCategory pins one representative message per category,
// so every rule in the ordered taxonomy is exercised and a wording change that
// silently reclassifies an error fails a named test.
func TestClassifyMessage_EachCategory(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want string
	}{
		{"captcha cloudflare", "Cloudflare challenge: just a moment...", errorclass.CategoryCaptcha},
		{"captcha 403", "request failed with status 403 forbidden", errorclass.CategoryCaptcha},
		{"rate limit 429", "HTTP 429 Too Many Requests", errorclass.CategoryRateLimit},
		{"not found 404", "404 not found", errorclass.CategoryNotFound},
		{"server error 500", "500 internal server error", errorclass.CategoryServerError},
		{"server error 503", "upstream returned 503 service unavailable", errorclass.CategoryServerError},
		{"timeout", "context deadline exceeded (timeout)", errorclass.CategoryTimeout},
		{"network refused", "dial tcp 1.2.3.4:443: connection refused", errorclass.CategoryNetwork},
		{"parse", "invalid character 'x' looking for beginning of value", errorclass.CategoryParse},
		{"no pages", "chapter resolved to 0 pages", errorclass.CategoryNoPages},
		{"broken image incomplete", "sourceengine: page failed image validation: incomplete image — the download was truncated before the full image arrived (will retry)", errorclass.CategoryBrokenImage},
		{"broken image empty", "sourceengine: page failed image validation: empty response — the source returned no image data (transient; will retry)", errorclass.CategoryBrokenImage},
		{"broken image unrecognized", "sourceengine: page failed image validation: unrecognized image data — not a supported image format", errorclass.CategoryBrokenImage},
		{"unknown", "something entirely unclassifiable happened", errorclass.CategoryUnknown},
		{"empty", "", errorclass.CategoryUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := errorclass.ClassifyMessage(tc.msg); got != tc.want {
				t.Fatalf("ClassifyMessage(%q) = %q, want %q", tc.msg, got, tc.want)
			}
		})
	}
}

// TestClassifyMessage_OrderingFirstMatchWins proves the ordered "first match
// wins, most-actionable first" contract: a message that hits multiple rules is
// classified by the earliest one.
func TestClassifyMessage_OrderingFirstMatchWins(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want string
	}{
		// captcha outranks timeout + server_error (a challenge page is the cause).
		{"captcha beats timeout", "cloudflare challenge timed out", errorclass.CategoryCaptcha},
		{"captcha beats server error", "503 service unavailable behind cloudflare", errorclass.CategoryCaptcha},
		// rate_limit outranks not_found/server_error wording that co-occurs.
		{"rate limit beats server error", "429 too many requests, internal server error", errorclass.CategoryRateLimit},
		// timeout outranks network (a timed-out dial reads as both).
		{"timeout beats network", "dial tcp: i/o timeout: connection reset", errorclass.CategoryTimeout},
		// The anti-bot/HTML challenge validation message carries "challenge", so it
		// must classify as captcha, NOT broken_image — captcha is placed first, and
		// this pins that the broken_image rule (appended after it) never steals a
		// challenge page.
		{"challenge validation beats broken_image", "sourceengine: page failed image validation: not an image — the source returned an anti-bot/HTML challenge page instead of the image (will retry)", errorclass.CategoryCaptcha},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := errorclass.ClassifyMessage(tc.msg); got != tc.want {
				t.Fatalf("ClassifyMessage(%q) = %q, want %q", tc.msg, got, tc.want)
			}
		})
	}
}

// TestClassify_NilIsUnknown documents the defensive nil default.
func TestClassify_NilIsUnknown(t *testing.T) {
	if got := errorclass.Classify(nil); got != errorclass.CategoryUnknown {
		t.Fatalf("Classify(nil) = %q, want %q", got, errorclass.CategoryUnknown)
	}
}

// TestClassify_TypedSignals proves the typed-error fast paths win over any
// message wording: a context deadline is timeout, a net.Error timeout is
// timeout, and a cancellation is network.
func TestClassify_TypedSignals(t *testing.T) {
	if got := errorclass.Classify(context.DeadlineExceeded); got != errorclass.CategoryTimeout {
		t.Fatalf("Classify(DeadlineExceeded) = %q, want timeout", got)
	}
	if got := errorclass.Classify(fmt.Errorf("wrapped: %w", context.DeadlineExceeded)); got != errorclass.CategoryTimeout {
		t.Fatalf("Classify(wrapped DeadlineExceeded) = %q, want timeout", got)
	}
	if got := errorclass.Classify(context.Canceled); got != errorclass.CategoryNetwork {
		t.Fatalf("Classify(Canceled) = %q, want network", got)
	}
	var timeoutErr net.Error = &net.DNSError{IsTimeout: true, Err: "lookup failed"}
	if got := errorclass.Classify(timeoutErr); got != errorclass.CategoryTimeout {
		t.Fatalf("Classify(net timeout) = %q, want timeout", got)
	}
}

// TestClassify_FallsBackToMessage proves Classify uses the message rules when no
// typed signal matches.
func TestClassify_FallsBackToMessage(t *testing.T) {
	if got := errorclass.Classify(errors.New("HTTP 429 too many requests")); got != errorclass.CategoryRateLimit {
		t.Fatalf("Classify(429 err) = %q, want rate_limit", got)
	}
}

// TestClassifyMessage_Locked pins the paywall/early-access category (GAP-135,
// GAP-141). A "locked" chapter is NOT a failure of the source: the source is
// healthy and serving every other chapter, and the chapter itself becomes free
// once its early-access window closes. It therefore must never be classified as
// server_error (which the download path treats as SOURCE-WIDE and trips the
// breaker on) nor as captcha.
func TestClassifyMessage_Locked(t *testing.T) {
	cases := []struct {
		name string
		msg  string
	}{
		{"hive coins", "Exception: Chapter locked (coins required)"},
		{"locked chapter", "this is a locked chapter"},
		{"premium", "premium chapter — not available"},
		{"paywall", "content is behind a paywall"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := errorclass.ClassifyMessage(tc.msg); got != errorclass.CategoryLocked {
				t.Fatalf("ClassifyMessage(%q) = %q, want %q", tc.msg, got, errorclass.CategoryLocked)
			}
		})
	}
}

// TestClassifyMessage_LockedOutranksTransportWording is the ORDERING guarantee
// and is the whole reason locked sits first in the taxonomy. Both wordings below
// really occur: engine-host wraps every extension exception in an
// "upstream error (status 502)" envelope (which contains "502", a server_error
// token), and a paywalled fetch can come back as a 403 (a captcha token). Either
// rule winning would send a healthy source's breaker down over one paid chapter.
func TestClassifyMessage_LockedOutranksTransportWording(t *testing.T) {
	cases := []struct {
		name string
		msg  string
	}{
		{"inside the 502 envelope", "sourceengine: upstream error (status 502): Exception: Chapter locked (coins required)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := errorclass.ClassifyMessage(tc.msg); got != errorclass.CategoryLocked {
				t.Fatalf("ClassifyMessage(%q) = %q, want %q", tc.msg, got, errorclass.CategoryLocked)
			}
		})
	}
}

// TestClassifyMessage_LockedDoesNotOverreach guards the false-positive risk the
// narrow token list exists to avoid: bare "locked" is a common word in unrelated
// infrastructure errors, and misreading one of those as a paywall would suppress
// a breaker trip that SHOULD happen.
func TestClassifyMessage_LockedDoesNotOverreach(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want string
	}{
		{"db lock is not a paywall", "database is locked", errorclass.CategoryUnknown},
		{"account lock is not a paywall", "account locked after too many attempts", errorclass.CategoryUnknown},
		// Dropped deliberately: these are LOGIN-STATE wordings as often as paywall
		// ones. Reading a lapsed session as a paywall would park every chapter for
		// 72h with no breaker trip and no health signal — the dangerous direction.
		{"subscription wording is not a paywall", "subscription required", errorclass.CategoryUnknown},
		{"purchase wording is not a paywall", "purchase required to read this chapter", errorclass.CategoryUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := errorclass.ClassifyMessage(tc.msg); got != tc.want {
				t.Fatalf("ClassifyMessage(%q) = %q, want %q", tc.msg, got, tc.want)
			}
		})
	}
}

// TestClassifyMessage_BanOutranksLockedWording pins the ordering that keeps the
// locked category SAFE. Locked sits after the ban rules and before server_error,
// and both halves of that placement are load-bearing:
//
//   - AFTER captcha / rate_limit / not_found — a block, challenge or 403 that
//     merely MENTIONS premium or paywall wording (an interstitial linking to a
//     "Premium Chapters" page, a sign-in wall) must stay source-wide. Reading it
//     as a paywall would park every chapter for 72h with no breaker trip and no
//     health signal, which is the dangerous direction.
//   - BEFORE server_error — engine-host wraps a real paywall in a 502 envelope
//     whose own "502" would otherwise win.
func TestClassifyMessage_BanOutranksLockedWording(t *testing.T) {
	bans := []struct {
		name string
		msg  string
	}{
		{"challenge page linking to premium", "just a moment... checking your browser — premium chapter"},
		{"sign-in wall served as 403", "403 forbidden: premium chapter — sign in to continue"},
		{"paywall wording inside a challenge", "cloudflare challenge served: this content is behind a paywall"},
	}
	for _, tc := range bans {
		t.Run(tc.name, func(t *testing.T) {
			if got := errorclass.ClassifyMessage(tc.msg); got != errorclass.CategoryCaptcha {
				t.Fatalf("ClassifyMessage(%q) = %q, want %q — a block must stay source-wide so the breaker still trips",
					tc.msg, got, errorclass.CategoryCaptcha)
			}
		})
	}

	// The production string carries no ban token, so it still classifies as locked
	// both bare and inside the 502 envelope it actually arrives in.
	for _, msg := range []string{
		"Exception: Chapter locked (coins required)",
		"sourceengine: upstream error (status 502): Exception: Chapter locked (coins required)",
	} {
		if got := errorclass.ClassifyMessage(msg); got != errorclass.CategoryLocked {
			t.Fatalf("ClassifyMessage(%q) = %q, want %q", msg, got, errorclass.CategoryLocked)
		}
	}
}
