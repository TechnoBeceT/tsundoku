package tracker

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// TokenSource is a caller-supplied read/write handle onto ONE tracker
// account's current TokenSet — e.g. a thin wrapper the connect/bind service
// (internal/tracker/connect, slice 3b) backs onto a single TrackerConnection
// ent row. NewAuthRoundTripper calls Token() before every request and
// SetToken() whenever it mints a fresh TokenSet via refresh, so the caller's
// backing store (the DB row) stays current without the RoundTripper itself
// knowing anything about persistence.
type TokenSource interface {
	Token() TokenSet
	SetToken(TokenSet)
}

// TokenRefresher is the one capability NewAuthRoundTripper needs from a
// Tracker: its Refresh method. Every Tracker implementation satisfies this
// automatically (Go structural typing — Tracker.Refresh has this exact
// shape), so callers can pass a Tracker directly; it is its own narrow
// interface so a test double need not implement the whole Tracker surface.
type TokenRefresher interface {
	Refresh(ctx context.Context, refresh string) (TokenSet, error)
}

// authRoundTripper is the shared per-tracker HTTP auth transport: it
// attaches "Authorization: Bearer <token>" to every request, proactively
// refreshes an expired token before sending, and — if the upstream still
// answers 401 with a token that looked fresh (clock skew, a server-side
// revoke, ...) — refreshes once more and retries. If neither the proactive
// nor the reactive refresh recovers a 401, it returns ErrTokenExpired so the
// caller can force a re-login rather than silently keep retrying.
//
// This is REUSABLE plumbing for a future caller that holds a full TokenSet
// (access + refresh) for one tracker account across MANY requests — e.g. the
// slice-3b/4 sync engine — and wants refresh handled transparently instead
// of manually checking expiry before every call. The Tracker port methods
// (Search/GetEntry/...) themselves take a raw token string per call and do
// their own one-shot Bearer attach; they do not use this RoundTripper
// internally in slice 3a.
type authRoundTripper struct {
	base      http.RoundTripper
	refresher TokenRefresher
	source    TokenSource
}

// NewAuthRoundTripper wraps base in an http.RoundTripper that authenticates
// every request against the account tracked by source, refreshing via
// refresher when the token is expired (checked proactively) or the upstream
// rejects it with 401 (checked reactively, one retry). base defaults to
// http.DefaultTransport when nil.
//
// A request retried after a reactive refresh needs its body re-sent from
// the start; this only works when req.GetBody is set, which
// http.NewRequestWithContext populates automatically for the common body
// types this codebase's tracker clients use (bytes.Reader, bytes.Buffer,
// strings.Reader) — see cloneWithFreshBody.
func NewAuthRoundTripper(base http.RoundTripper, refresher TokenRefresher, source TokenSource) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &authRoundTripper{base: base, refresher: refresher, source: source}
}

// RoundTrip implements http.RoundTripper. See the authRoundTripper doc
// comment for the proactive/reactive refresh strategy.
func (rt *authRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	tok := rt.source.Token()
	if TokenExpired(tok) {
		refreshed, err := rt.refresh(req, tok)
		if err != nil {
			return nil, ErrTokenExpired
		}
		tok = refreshed
	}

	resp, err := rt.send(req, tok)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}
	_ = resp.Body.Close()

	refreshed, err := rt.refresh(req, tok)
	if err != nil {
		return nil, ErrTokenExpired
	}
	resp2, err := rt.send(req, refreshed)
	if err != nil {
		return nil, err
	}
	if resp2.StatusCode == http.StatusUnauthorized {
		_ = resp2.Body.Close()
		return nil, ErrTokenExpired
	}
	return resp2, nil
}

// refresh mints a fresh TokenSet from tok's refresh token via rt.refresher,
// persisting it back through rt.source on success. Returns ErrNoRefresh
// (never calls the refresher) when tok carries no refresh token at all —
// e.g. an AniList TokenSet, which always has Refresh == "".
func (rt *authRoundTripper) refresh(req *http.Request, tok TokenSet) (TokenSet, error) {
	if tok.Refresh == "" {
		return TokenSet{}, ErrNoRefresh
	}
	refreshed, err := rt.refresher.Refresh(req.Context(), tok.Refresh)
	if err != nil {
		return TokenSet{}, err
	}
	rt.source.SetToken(refreshed)
	return refreshed, nil
}

// send clones req (rewinding its body via GetBody when present — see
// cloneWithFreshBody), attaches the Bearer header for tok, and delegates to
// the base transport.
func (rt *authRoundTripper) send(req *http.Request, tok TokenSet) (*http.Response, error) {
	clone, err := cloneWithFreshBody(req)
	if err != nil {
		return nil, err
	}
	clone.Header.Set("Authorization", "Bearer "+tok.Access)
	return rt.base.RoundTrip(clone)
}

// cloneWithFreshBody clones req for a (possibly repeat) send. When req
// carries a body AND a GetBody rewinder (set automatically by
// http.NewRequestWithContext for bytes.Reader/bytes.Buffer/strings.Reader
// bodies — every request this codebase's tracker clients build), the clone
// gets a FRESH, unconsumed reader so a retry after a reactive refresh can
// resend the same payload. A body with no GetBody (a raw io.Reader) is
// passed through unchanged — a retry of such a request risks sending an
// already-drained body, but no caller in this codebase constructs one.
func cloneWithFreshBody(req *http.Request) (*http.Request, error) {
	clone := req.Clone(req.Context())
	if req.Body != nil && req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, fmt.Errorf("tracker: rewind request body: %w", err)
		}
		clone.Body = body
	}
	return clone, nil
}

// TokenExpired reports whether tok's ExpiresAt has already passed. A nil
// ExpiresAt (unknown expiry) is treated as NOT expired — the reactive 401
// path is the only signal for a tracker whose expiry is unknowable
// up front.
//
// EXPORTED (not just this file's own RoundTrip caller) so
// internal/tracker/account.ResolveToken can reuse the exact same proactive
// expiry rule instead of re-deriving it — see that function's own doc
// comment for the pre-activation gap this closes (a stored token used to be
// returned verbatim, never checked, until it 401'd forever).
func TokenExpired(tok TokenSet) bool {
	return tok.ExpiresAt != nil && !tok.ExpiresAt.After(time.Now())
}
