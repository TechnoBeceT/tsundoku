package trackers

import (
	"errors"
	"net/http"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/tracker"
)

// TestMapServiceError_UpstreamTrackerFailureIs4xxWithRealMessage pins the
// Cloudflare-visibility fix: a genuine tracker-CALL failure (wrapped with
// tracker.WrapUpstream at the connect/bind/syncsvc call site — e.g. what
// MangaUpdates' client returns on the production 405 this fix also
// corrects) must map to a 4xx carrying the tracker's own error text
// verbatim, NEVER the shared httperr.Upstream 502 — Cloudflare replaces any
// origin 5xx body with its own generic page, which would hide the real
// message from the owner exactly as it did before this fix.
func TestMapServiceError_UpstreamTrackerFailureIs4xxWithRealMessage(t *testing.T) {
	underlying := errors.New(`mangaupdates: https://api.mangaupdates.com/v1/lists/0/series/12345 returned HTTP 405: "Method not allowed. Must be one of: OPTIONS"`)
	wrapped := tracker.WrapUpstream("mangaupdates", underlying)

	got := mapServiceError(wrapped)

	var he *echo.HTTPError
	if !errors.As(got, &he) {
		t.Fatalf("mapServiceError(upstream error) = %v (%T), want an *echo.HTTPError", got, got)
	}
	if he.Code < 400 || he.Code >= 500 {
		t.Fatalf("mapServiceError(upstream error) status = %d, want a 4xx (never 502/500)", he.Code)
	}
	msg, ok := he.Message.(string)
	if !ok || msg != underlying.Error() {
		t.Fatalf("mapServiceError(upstream error) message = %v, want the tracker's real error text %q", he.Message, underlying.Error())
	}
}

// TestMapServiceError_GenuineInternalErrorStays500 confirms a plain error
// that was NEVER marked as a tracker.UpstreamError (a DB read/write
// failure, in real callers) is returned UNWRAPPED — not turned into any
// echo.HTTPError — so the central error middleware (internal/middleware/
// error.go ErrorHandler) renders the safe, opaque 500 "internal server
// error" and logs the real cause server-side, rather than leaking DB
// detail to the client via a 4xx.
func TestMapServiceError_GenuineInternalErrorStays500(t *testing.T) {
	dbErr := errors.New("bind: query track binding (series=... tracker=1): pq: connection reset")

	got := mapServiceError(dbErr)

	if got != dbErr {
		t.Fatalf("mapServiceError(internal error) = %v, want the SAME unwrapped error (so it renders as a plain 500)", got)
	}
	var he *echo.HTTPError
	if errors.As(got, &he) {
		t.Fatalf("mapServiceError(internal error) = %v, want NOT an echo.HTTPError (must fall through to the middleware's default 500)", got)
	}
}

// TestMapServiceError_UpstreamErrorUnwrapsToSentinel confirms an
// UpstreamError wrapping one of the package's own named sentinels (e.g. a
// tracker client that itself returns tracker.ErrTokenExpired) still
// satisfies errors.Is through the wrapper's Unwrap — proving WrapUpstream
// never breaks the EXISTING sentinel-based 404/400 branches for a caller
// that checks errors.Is deeper in the chain (mirrors
// s.markExpiredOnTokenFailure's own errors.Is(err, tracker.ErrTokenExpired)
// check in internal/tracker/bind and internal/tracker/syncsvc).
func TestMapServiceError_UpstreamErrorUnwrapsToSentinel(t *testing.T) {
	wrapped := tracker.WrapUpstream("fake", tracker.ErrTokenExpired)
	if !errors.Is(wrapped, tracker.ErrTokenExpired) {
		t.Fatalf("errors.Is(WrapUpstream(...), tracker.ErrTokenExpired) = false, want true")
	}

	got := mapServiceError(wrapped)
	var he *echo.HTTPError
	if !errors.As(got, &he) || he.Code != http.StatusBadRequest {
		t.Fatalf("mapServiceError(wrapped ErrTokenExpired) = %v, want a 400 echo.HTTPError", got)
	}
}
