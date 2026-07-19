// Package errorclass classifies a source-operation error into a small, stable
// taxonomy of human-meaningful categories (captcha, rate_limit, not_found, …).
//
// It is a shared kernel with exactly one job: turn a raw upstream error (a
// Cloudflare challenge, a 429, a dropped connection, a decode failure) into a
// single category string. The Source Health Console stores that category on every
// failed SourceEvent (SourceEvent.error_category) and its diagnosis engine renders
// a human explanation from it, so the same classification must live in ONE place
// (§2 DRY) — never re-derived inline at a call site.
//
// Ported from Kaizoku.GO's CategorizeError. The classification is DELIBERATELY
// ORDERED (see the categories slice): the first matching rule wins, most-specific
// first, so an error mentioning both "captcha" and "timeout" is a captcha (the
// actionable cause), not a timeout (the symptom).
package errorclass

import (
	"context"
	"errors"
	"net"
	"strings"
)

// The closed set of category strings this package emits. They are stable
// identifiers (stored in the DB, matched by the FE diagnosis engine) — never
// change an existing value without a migration + a coordinated FE change.
const (
	// CategoryCaptcha is an anti-bot / Cloudflare challenge (a CAPTCHA, a JS
	// challenge, an "attention required" interstitial) — the source is reachable
	// but is gating access.
	CategoryCaptcha = "captcha"
	// CategoryRateLimit is an explicit throttle (HTTP 429 / "too many requests").
	CategoryRateLimit = "rate_limit"
	// CategoryNotFound is a missing resource (HTTP 404 / "not found").
	CategoryNotFound = "not_found"
	// CategoryServerError is an upstream 5xx / "internal server error".
	CategoryServerError = "server_error"
	// CategoryNetwork is a transport-level failure (connection refused/reset, DNS,
	// no route) — the request never got a usable HTTP response.
	CategoryNetwork = "network"
	// CategoryTimeout is a deadline exceeded / timed-out request.
	CategoryTimeout = "timeout"
	// CategoryParse is a malformed / undecodable response body.
	CategoryParse = "parse"
	// CategoryNoPages is a source-specific empty result (a chapter that resolved
	// to zero pages) — distinct from not_found (the chapter exists, it just has no
	// readable images).
	CategoryNoPages = "no_pages"
	// CategoryUnknown is the fall-through: an error that matched no rule above.
	CategoryUnknown = "unknown"
)

// rule pairs a category with the lowercased substrings that identify it. A rule
// matches when the (lowercased) error message contains ANY of its substrings.
type rule struct {
	category   string
	substrings []string
}

// categories is the ORDERED rule list — first match wins, most-specific /
// most-actionable first. The order is load-bearing (see the package doc comment):
//   - captcha before everything (an anti-bot page is often ALSO a 5xx / timeout,
//     but the captcha is the actionable cause);
//   - rate_limit before server_error (a 429 is a 4xx, not a 5xx, but some sources
//     word it loosely);
//   - not_found before server_error (a clean 4xx);
//   - timeout before network (a timed-out dial reads as both, and "timeout" is
//     the more precise, more actionable label);
//   - no_pages / parse last among the specific rules (they are the least ambiguous
//     substrings, so ordering them earlier would never change an outcome — kept
//     late for readability).
var categories = []rule{
	{CategoryCaptcha, []string{"captcha", "cloudflare", "challenge", "attention required", "cf-", "just a moment", "access denied", "forbidden", "403"}},
	{CategoryRateLimit, []string{"rate limit", "rate-limit", "ratelimit", "too many requests", "429"}},
	{CategoryNotFound, []string{"not found", "404", "no such", "does not exist"}},
	{CategoryServerError, []string{"internal server error", "500", "502", "503", "504", "bad gateway", "service unavailable", "gateway timeout"}},
	{CategoryTimeout, []string{"timeout", "timed out", "deadline exceeded", "context deadline"}},
	{CategoryNetwork, []string{"connection refused", "connection reset", "no route to host", "network is unreachable", "broken pipe", "eof", "no such host", "dial tcp", "dns", "i/o timeout"}},
	{CategoryParse, []string{"parse", "unmarshal", "invalid character", "decode", "malformed", "unexpected end of json", "invalid json"}},
	{CategoryNoPages, []string{"no pages", "empty chapter", "zero pages", "0 pages"}},
}

// Classify returns the category of err. A nil error is CategoryUnknown (callers
// should not classify a success; this is a defensive default). It first checks
// typed sentinels via errors.Is / errors.As (the authoritative signals a
// substring match can only approximate), then falls back to an ordered
// substring match on the message. See ClassifyMessage for the message-only form.
func Classify(err error) string {
	if err == nil {
		return CategoryUnknown
	}
	// Typed signals first — these are unambiguous regardless of message wording.
	if errors.Is(err, context.DeadlineExceeded) {
		return CategoryTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return CategoryTimeout
	}
	if errors.Is(err, context.Canceled) {
		// A cancellation is a shutdown/navigation signal, not a source defect —
		// classify it as network (transport aborted) rather than inventing a
		// category; callers that care about clean cancels filter before logging.
		return CategoryNetwork
	}
	return ClassifyMessage(err.Error())
}

// ClassifyMessage classifies a bare error message (no typed error available — e.g.
// a message read back from a stored DB column). It applies the ordered substring
// rules, first match wins, and falls back to CategoryUnknown. Matching is
// case-insensitive.
func ClassifyMessage(msg string) string {
	lower := strings.ToLower(msg)
	if strings.TrimSpace(lower) == "" {
		return CategoryUnknown
	}
	for _, r := range categories {
		for _, sub := range r.substrings {
			if strings.Contains(lower, sub) {
				return r.category
			}
		}
	}
	return CategoryUnknown
}
