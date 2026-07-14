package tracker

// UpstreamError marks a failure that came from actually CALLING a
// tracker's own upstream API — a transport error, a rejected credential/
// OAuth exchange, or a non-2xx response (e.g. MangaUpdates' "returned HTTP
// 405: ..." — see internal/tracker/mangaupdates/client.go's doJSON) — as
// opposed to a genuine INTERNAL failure (a DB read/write, a malformed
// UUID, ...) that happens to occur in the same connect/bind/sync call.
//
// The distinction matters at the HTTP boundary (internal/handler/trackers.
// mapServiceError): this instance runs behind Cloudflare, which REPLACES
// any origin 5xx response body with its own generic "Bad gateway" page —
// so a bare 502/500 for an upstream tracker rejection hides the tracker's
// real message (an OAuth "invalid_grant", MangaUpdates' 405, ...) from the
// owner, who has no way to self-diagnose. Wrapping the error here lets the
// handler tell "surface this tracker message as a Cloudflare-visible 4xx"
// apart from "this is an opaque internal error, log it and return a safe
// 500" WITHOUT string-matching an error message (fragile) — a plain
// errors.As check instead.
//
// Every internal/tracker/connect, internal/tracker/bind, and
// internal/tracker/syncsvc call site that invokes a Tracker interface
// method (GetEntry/SaveEntry/UpdateEntry/DeleteEntry/Search/
// LoginCredentials/ExchangeCode/TokenFromImplicit) wraps that call's error
// with WrapUpstream before returning it, so mapServiceError's default
// branch only ever sees an *UpstreamError for a real tracker-call failure.
type UpstreamError struct {
	// Tracker is the failing tracker's stable Key() (e.g. "mangaupdates"),
	// for logging/diagnostics — Error() already embeds it via the wrapped
	// message (every concrete client prefixes its own errors, e.g.
	// "mangaupdates: ..."), so callers rendering Error() need not repeat it.
	Tracker string
	// Err is the underlying error returned by the Tracker implementation.
	Err error
}

// Error returns the wrapped tracker error's message verbatim — this IS the
// text surfaced to the owner (via httperr.BadRequest in mapServiceError),
// so it deliberately carries no extra "upstream error:" framing of its own.
func (e *UpstreamError) Error() string { return e.Err.Error() }

// Unwrap exposes the underlying error so errors.Is/errors.As (e.g. a
// caller checking for tracker.ErrTokenExpired) still see through the
// wrapper.
func (e *UpstreamError) Unwrap() error { return e.Err }

// WrapUpstream marks err as having come from trackerKey's own upstream API
// call (see UpstreamError's doc comment). A nil err returns nil, so
// call sites can wrap unconditionally: `return WrapUpstream(t.Key(), err)`.
func WrapUpstream(trackerKey string, err error) error {
	if err == nil {
		return nil
	}
	return &UpstreamError{Tracker: trackerKey, Err: err}
}
