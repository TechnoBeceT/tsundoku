// This file (cfclearance.go) is Kitsu's Cloudflare-clearing http.RoundTripper
// — the "real fix" the package doc comment's browserUserAgent note points at.
// kitsu.app sits behind a Cloudflare MANAGED challenge that a browser
// User-Agent header alone cannot pass; this transport detects that challenge,
// solves it via FlareSolverr (internal/flaresolverr), and retries the
// request once with the earned cf_clearance cookie + browser User-Agent
// attached.
//
// QCAT-238 (owner-ratified): the FlareSolverr URL/timeout/session/etc. this
// transport uses is a TSUNDOKU-OWNED runtime setting (internal/settings),
// never an env var and never read live from Suwayomi — it is resolved via an
// injected gate func at REQUEST time, so a Settings-screen change hot-reloads
// without a restart. Any future Tsundoku cf-clearance need should reuse this
// same shape rather than growing a second implementation.
package kitsu

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/technobecet/tsundoku/internal/flaresolverr"
	"github.com/technobecet/tsundoku/internal/tracker"
)

// FlareSolverrConfig is the resolved snapshot of the Tsundoku-owned
// FlareSolverr settings a cfTransport needs for one request. The gate func
// passed to WithFlareSolverrGate returns a fresh one on every call, so a
// Settings-screen change (Enabled flipped, URL edited, ...) hot-reloads on
// the very next request.
type FlareSolverrConfig struct {
	// Enabled gates the whole transport: false ⇒ passthrough (no FlareSolverr
	// use, no challenge detection overhead beyond the initial request).
	Enabled bool
	// URL is the FlareSolverr endpoint (e.g. http://flaresolverr:8191). A blank
	// URL disables the transport regardless of Enabled — there is nothing to
	// solve against.
	URL string
	// Timeout bounds one solve call (FlareSolverr's own maxTimeout budget).
	Timeout time.Duration
	// SessionName, when non-empty, reuses a named FlareSolverr browser
	// session across solves (FlareSolverr's own session cache).
	SessionName string
	// SessionTTL is how long a solved clearance is trusted locally before
	// this transport re-solves, even if no challenge was re-detected — kept
	// in step with FlareSolverr's own session TTL so the two caches expire
	// together.
	SessionTTL time.Duration
}

// enabled reports whether cfg is usable at all: the owner flipped the toggle
// on AND configured a non-blank endpoint.
func (cfg FlareSolverrConfig) enabled() bool {
	return cfg.Enabled && cfg.URL != ""
}

// clearance is one cached, still-valid solve outcome.
type clearance struct {
	cookies   []*http.Cookie
	userAgent string
	expiresAt time.Time
}

// valid reports whether c is non-nil and its TTL has not elapsed.
func (c *clearance) valid(now time.Time) bool {
	return c != nil && now.Before(c.expiresAt)
}

// cfTransport wraps a base http.RoundTripper: when FlareSolverr is enabled
// (per the injected gate, resolved fresh per request), it attaches a cached
// cf_clearance cookie + the browser User-Agent that earned it, detects a
// Cloudflare challenge response, solves it via flaresolverr.Solve, caches the
// result for SessionTTL, and retries the request ONCE with the fresh
// clearance attached. Disabled or unconfigured ⇒ a pure passthrough to base
// (today's exact behaviour before this feature).
type cfTransport struct {
	base        http.RoundTripper
	gate        func(ctx context.Context) FlareSolverrConfig
	solveClient *http.Client

	mu    sync.Mutex
	cache *clearance
}

// RoundTrip implements http.RoundTripper.
func (t *cfTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cfg := t.gate(req.Context())
	if !cfg.enabled() {
		return t.base.RoundTrip(req)
	}

	attempt, err := attachClearance(req, t.currentClearance())
	if err != nil {
		return nil, err
	}
	resp, err := t.base.RoundTrip(attempt)
	if err != nil {
		return nil, err
	}
	if !isCloudflareChallenge(resp) {
		return resp, nil
	}
	_ = resp.Body.Close()

	sol, err := flaresolverr.Solve(req.Context(), t.solveClient, cfg.URL, req.URL.String(), cfg.SessionName, cfg.Timeout)
	if err != nil {
		// The challenge could not be cleared — return the ORIGINAL request's
		// outcome shape by retrying once through base unmodified, so the
		// caller still gets a real (if still-challenged) HTTP response rather
		// than this transport swallowing the failure into a transport error
		// the caller can't decode a status code from.
		retry, cloneErr := tracker.CloneRequestForRetry(req)
		if cloneErr != nil {
			return nil, cloneErr
		}
		return t.base.RoundTrip(retry)
	}
	t.storeClearance(sol, cfg.SessionTTL)

	retry, err := attachClearance(req, t.currentClearance())
	if err != nil {
		return nil, err
	}
	return t.base.RoundTrip(retry)
}

// currentClearance returns the cached clearance if still valid, else nil.
func (t *cfTransport) currentClearance() *clearance {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cache.valid(time.Now()) {
		return t.cache
	}
	return nil
}

// storeClearance caches sol for ttl (defaulting to a sane floor when the
// owner configures 0 — see minCacheTTL).
func (t *cfTransport) storeClearance(sol flaresolverr.Solution, ttl time.Duration) {
	if ttl <= 0 {
		ttl = minCacheTTL
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cache = &clearance{
		cookies:   sol.Cookies,
		userAgent: sol.UserAgent,
		expiresAt: time.Now().Add(ttl),
	}
}

// minCacheTTL is the local clearance-cache floor used when the owner sets
// SessionTTL to 0 — a zero TTL would force a fresh Solve on literally every
// request, defeating the whole point of caching (and hammering FlareSolverr).
const minCacheTTL = time.Minute

// attachClearance clones req (fresh-body-safe, see
// tracker.CloneRequestForRetry) and, when c is non-nil, attaches its cookies
// + User-Agent. A nil c leaves the clone's headers untouched — the first-ever
// request on a cold cache goes out exactly as the caller built it.
func attachClearance(req *http.Request, c *clearance) (*http.Request, error) {
	clone, err := tracker.CloneRequestForRetry(req)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return clone, nil
	}
	for _, cookie := range c.cookies {
		clone.AddCookie(cookie)
	}
	if c.userAgent != "" {
		clone.Header.Set("User-Agent", c.userAgent)
	}
	return clone, nil
}

// cloudflareChallengeStatuses are the HTTP statuses Cloudflare's managed
// challenge answers with (mirrors Suwayomi's own CloudflareInterceptor
// ERROR_CODES: 403 and 503).
var cloudflareChallengeStatuses = map[int]bool{
	http.StatusForbidden:          true,
	http.StatusServiceUnavailable: true,
}

// challengeBodySignatures are substrings Cloudflare's own challenge HTML
// carries. Checked as a case-insensitive contains, since Cloudflare's markup
// casing is not a stable contract to depend on.
var challengeBodySignatures = []string{"just a moment", "cf-mitigated", "cf-chl-"}

// challengeBodyPeekLimit caps how much of a 403/503 body this transport reads
// while sniffing for a Cloudflare challenge signature — generous enough for
// Cloudflare's own challenge HTML (a few KB) or Kitsu's real JSON:API error
// bodies (routinely under 1KB), while bounding memory against an oversized or
// hostile response.
const challengeBodyPeekLimit = 1 << 20 // 1MB

// isCloudflareChallenge reports whether resp looks like a Cloudflare managed
// challenge rather than the real upstream response: a 403/503 status carrying
// one of Cloudflare's own challenge-page/header signatures. A response NOT
// flagged by status code alone is returned false WITHOUT touching the body
// (the overwhelmingly common case — a real 200 never reaches here). Only a
// 403/503 triggers a body peek, and that peek RESTORES resp.Body afterwards
// (via bodyLooksLikeChallenge) so a genuine non-Cloudflare 403/503 still
// decodes normally for the caller.
func isCloudflareChallenge(resp *http.Response) bool {
	if !cloudflareChallengeStatuses[resp.StatusCode] {
		return false
	}
	if resp.Header.Get("cf-mitigated") != "" {
		return true
	}
	server := strings.ToLower(resp.Header.Get("Server"))
	if strings.Contains(server, "cloudflare") {
		return true
	}
	return bodyLooksLikeChallenge(resp)
}

// bodyLooksLikeChallenge peeks up to challengeBodyPeekLimit bytes of resp's
// body looking for a Cloudflare challenge signature, then ALWAYS restores
// resp.Body to a fresh reader over exactly the bytes it read — a caller that
// goes on to treat this response as "not a challenge" (e.g. a genuine 403
// from Kitsu itself) must still be able to decode the original body.
func bodyLooksLikeChallenge(resp *http.Response) bool {
	if resp.Body == nil {
		return false
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, challengeBodyPeekLimit))
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	if err != nil {
		return false
	}
	lower := strings.ToLower(string(raw))
	for _, sig := range challengeBodySignatures {
		if strings.Contains(lower, sig) {
			return true
		}
	}
	return false
}
