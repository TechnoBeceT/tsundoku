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
