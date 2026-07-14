// Package flaresolverr is a minimal client for the FlareSolverr Cloudflare-
// bypass proxy (https://github.com/FlareSolverr/FlareSolverr). It solves a
// Cloudflare managed challenge for one target URL and returns the
// cf_clearance cookie it earned (plus the browser User-Agent that earned it —
// cf_clearance is IP+UA-bound, so both must be replayed together against the
// real target).
//
// Mirrors Suwayomi's own solve flow (CFClearance.resolveWithFlareSolver in
// CloudflareInterceptor.kt): POST
// {"cmd":"request.get","url":<target>,"maxTimeout":<ms>,"session":<name?>}
// to <endpoint>/v1, then read solution.cookies (looking for cf_clearance) +
// solution.userAgent. This package is deliberately generic (not Kitsu-
// specific) — any future Tsundoku cf-clearance need (QCAT-238) reuses it.
package flaresolverr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// defaultTimeout is used when the caller passes a non-positive timeout.
const defaultTimeout = 60 * time.Second

// requestTimeoutMargin is added to timeout to build the HTTP call's own
// deadline, mirroring Suwayomi's CFClearance client (readTimeout =
// solve-timeout + 5s, callTimeout = solve-timeout + 10s) — the solve call
// itself must never be the thing that races FlareSolverr's own maxTimeout.
const requestTimeoutMargin = 10 * time.Second

// clearanceCookieName is the cookie FlareSolverr's headless browser earns by
// clearing a Cloudflare managed challenge. A solve response that carries no
// such cookie is treated as a failure — the caller has nothing usable.
const clearanceCookieName = "cf_clearance"

// Solution is the outcome of a successful solve: every cookie FlareSolverr's
// browser collected while clearing the challenge (cf_clearance among them,
// proven present by Solve) and the browser User-Agent that earned them.
type Solution struct {
	// Cookies are every cookie the solve produced, ready to attach to the
	// real request (a Cloudflare-cleared site commonly sets more than just
	// cf_clearance).
	Cookies []*http.Cookie
	// UserAgent is the browser identity FlareSolverr's headless browser used
	// — the real request MUST be replayed with this exact User-Agent, since
	// cf_clearance is bound to it.
	UserAgent string
}

// solveRequest is the FlareSolverr /v1 request.get command body.
type solveRequest struct {
	Cmd               string `json:"cmd"`
	URL               string `json:"url"`
	MaxTimeout        int    `json:"maxTimeout,omitempty"`
	Session           string `json:"session,omitempty"`
	SessionTTLMinutes int    `json:"session_ttl_minutes,omitempty"`
}

// solveCookie is one cookie in a FlareSolverr solution's cookie list.
type solveCookie struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain"`
	Path   string `json:"path"`
}

// solveSolution is the `solution` object of a FlareSolverr response.
type solveSolution struct {
	Status    int           `json:"status"`
	Cookies   []solveCookie `json:"cookies"`
	UserAgent string        `json:"userAgent"`
}

// solveResponse is the top-level FlareSolverr /v1 response shape. Status is
// "ok" on success; Message carries the failure reason otherwise (e.g. a
// challenge FlareSolverr itself could not solve).
type solveResponse struct {
	Status   string        `json:"status"`
	Message  string        `json:"message"`
	Solution solveSolution `json:"solution"`
}

// Solve asks FlareSolverr (at endpoint, e.g. "http://flaresolverr:8191" — the
// trailing "/v1" is appended automatically if absent) to fetch targetURL
// through its managed browser and clear any Cloudflare challenge in the way.
// sessionName, when non-empty, reuses a named FlareSolverr browser session
// across calls (FlareSolverr's OWN session cache — separate from whatever TTL
// cache the caller keeps around the returned Solution). timeout bounds both
// FlareSolverr's own solve budget (sent as maxTimeout, milliseconds) and this
// call's own HTTP deadline (timeout + requestTimeoutMargin); a non-positive
// timeout falls back to defaultTimeout.
//
// httpClient may be nil (a bare &http.Client{} is used) — callers that want a
// shared client for connection reuse should pass one; Solve never reuses the
// gate's own outbound-request client, since a challenge on THAT client is
// exactly what triggered this call.
//
// Returns an error on a non-2xx HTTP response, a FlareSolverr-reported
// non-"ok" status, or a solution carrying no cf_clearance cookie — Solve never
// returns a zero-value Solution alongside a nil error.
func Solve(ctx context.Context, httpClient *http.Client, endpoint, targetURL, sessionName string, timeout time.Duration) (Solution, error) {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	raw, err := postSolveRequest(ctx, httpClient, endpoint, targetURL, sessionName, timeout)
	if err != nil {
		return Solution{}, err
	}
	return parseSolveResponse(raw)
}

// postSolveRequest builds + sends the FlareSolverr /v1 request.get command and
// returns the raw response body on a 2xx HTTP response (parsing the
// FlareSolverr-level payload is parseSolveResponse's job — split out purely
// to keep Solve's own cyclomatic complexity low).
func postSolveRequest(ctx context.Context, httpClient *http.Client, endpoint, targetURL, sessionName string, timeout time.Duration) ([]byte, error) {
	body, err := json.Marshal(solveRequest{
		Cmd:        "request.get",
		URL:        targetURL,
		MaxTimeout: int(timeout / time.Millisecond),
		Session:    sessionName,
	})
	if err != nil {
		// Defensive path: solveRequest holds only JSON-safe scalars, which
		// json.Marshal never fails on; unreachable in practice.
		return nil, fmt.Errorf("flaresolverr: marshal request: %w", err)
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout+requestTimeoutMargin)
	defer cancel()

	solveURL := endpointV1(endpoint)
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, solveURL, bytes.NewReader(body))
	if err != nil {
		// Defensive path: reachable only with a nil context, which every
		// caller here always supplies a real one for; unreachable in
		// practice.
		return nil, fmt.Errorf("flaresolverr: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := httpClient
	if client == nil {
		client = &http.Client{}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("flaresolverr: request %s: %w", solveURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("flaresolverr: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("flaresolverr: %s returned HTTP %d: %s", solveURL, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

// parseSolveResponse decodes a FlareSolverr /v1 response body, rejecting a
// non-"ok" status or a solution carrying no cf_clearance cookie.
func parseSolveResponse(raw []byte) (Solution, error) {
	var parsed solveResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Solution{}, fmt.Errorf("flaresolverr: decode response: %w", err)
	}
	if parsed.Status != "ok" {
		return Solution{}, fmt.Errorf("flaresolverr: solve failed: %s", strings.TrimSpace(parsed.Message))
	}

	sol := toSolution(parsed.Solution)
	if !sol.hasClearance() {
		return Solution{}, fmt.Errorf("flaresolverr: solve response carried no %s cookie", clearanceCookieName)
	}
	return sol, nil
}

// toSolution maps the wire solution shape into the exported Solution,
// stripping a Cloudflare-style leading-dot domain (Go's cookie jar/Cookie
// matching treats "domain" and ".domain" identically for Set-Cookie, but a
// literal leading dot in a hand-attached header value is invalid syntax).
func toSolution(s solveSolution) Solution {
	cookies := make([]*http.Cookie, 0, len(s.Cookies))
	for _, c := range s.Cookies {
		// Secure/HttpOnly/SameSite are Set-Cookie RESPONSE attributes — these
		// cookies are attached to OUTBOUND requests via req.AddCookie (Cookie
		// header, name=value pairs only), so those fields are inapplicable
		// here, not omitted by oversight.
		cookies = append(cookies, &http.Cookie{ //nolint:gosec // outbound request cookie, not a Set-Cookie response
			Name:   c.Name,
			Value:  c.Value,
			Domain: strings.TrimPrefix(c.Domain, "."),
			Path:   c.Path,
		})
	}
	return Solution{Cookies: cookies, UserAgent: s.UserAgent}
}

// hasClearance reports whether sol carries the cf_clearance cookie — the one
// artifact that actually proves the challenge was solved.
func (sol Solution) hasClearance() bool {
	for _, c := range sol.Cookies {
		if c.Name == clearanceCookieName {
			return true
		}
	}
	return false
}

// endpointV1 appends FlareSolverr's "/v1" API path to base unless it is
// already present, tolerating a trailing slash either way.
func endpointV1(base string) string {
	trimmed := strings.TrimRight(base, "/")
	if strings.HasSuffix(trimmed, "/v1") {
		return trimmed
	}
	return trimmed + "/v1"
}
