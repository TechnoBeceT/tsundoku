// Package sourceengine is the typed HTTP/JSON client for the Tsundoku
// engine-host — the Kotlin/Mihon process (engine-host/) that replaces
// Suwayomi as the download engine in the P2 migration. It speaks a small
// REST/JSON RPC surface (never GraphQL) documented in
// engine-host/RPC-CONTRACT.md and mirrored by engine-host/src/main/kotlin/
// enginehost/Dto.kt + RpcServer.kt.
//
// This file (client.go) is the ONE place that owns the HTTP plumbing: the
// Client interface every other package binds to, the unexported httpClient
// implementation, the constructor, and the single generic get/post helper
// every endpoint file (search.go, manga.go, chapters.go, ...) calls through.
// No endpoint file re-implements request building or error mapping — see §2
// (DRY) in ENGINEERING.md.
//
// Every request/response is addressed by STABLE (sourceId, url) pairs, never
// an engine-assigned opaque id — a DB rebuild + extension reinstall yields
// the same source ids and the same source-relative URLs, so a stored key
// always resolves to the same series.
package sourceengine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// HTTPDoer is the minimal interface the client needs to send a request. A
// *http.Client satisfies it directly; tests may inject any stand-in
// (typically an httptest.Server's own client).
type HTTPDoer interface {
	// Do sends req and returns the raw HTTP response, exactly like
	// (*http.Client).Do.
	Do(req *http.Request) (*http.Response, error)
}

// Client is the typed interface every engine-host RPC flows through. The
// concrete implementation (httpClient) is unexported; callers hold a Client.
//
// Method groups mirror the RPC surface in engine-host/RpcServer.kt:
//   - Health: liveness/loaded-source-count probe.
//   - Search/Popular/Latest/MangaDetails/Chapters/Pages/Image: url-addressed
//     source content calls.
//   - Sources/Preferences/SetPreferences: the loaded-source registry + each
//     source's configurable preferences.
//   - Extensions/InstallExtension/RefreshExtensions/UpdateExtension/
//     UninstallExtension/Repos/SetRepos: extension package management.
//   - SetFlareSolverr/SetSocks: the FlareSolverr + SOCKS-proxy config
//     passthrough (replaces the retired Suwayomi settings-proxy).
type Client interface {
	// Health reports the engine host's liveness and how many sources it has
	// loaded. It never fails on a healthy host.
	Health(ctx context.Context) (Health, error)

	// Search searches sourceID for query and returns one page of results.
	// page is 1-based.
	Search(ctx context.Context, sourceID int64, query string, page int) (SearchResult, error)

	// Popular fetches one page of sourceID's most-popular catalogue listing
	// (no query). page is 1-based.
	Popular(ctx context.Context, sourceID int64, page int) (SearchResult, error)

	// Latest fetches one page of sourceID's latest-updates catalogue listing
	// (no query). page is 1-based.
	Latest(ctx context.Context, sourceID int64, page int) (SearchResult, error)

	// MangaDetails fetches full metadata for the manga at url on sourceID.
	MangaDetails(ctx context.Context, sourceID int64, url string) (MangaDetails, error)

	// Chapters fetches the chapter list for the manga at url on sourceID,
	// running the engine host's chapter-number-recognition/scanlator-
	// normalization post-processing (mirrors Suwayomi's own Chapter.kt
	// service layer). mangaTitle improves number recognition (it is
	// stripped from a chapter name before number-matching) and is safe to
	// pass as "" when unknown.
	Chapters(ctx context.Context, sourceID int64, url string, mangaTitle string) ([]Chapter, error)

	// Pages fetches the page list for the chapter at chapterURL on sourceID.
	// Each Page's own (URL, ImageURL) address pair must be fed back to Image
	// verbatim — this call does not resolve image URLs itself.
	Pages(ctx context.Context, sourceID int64, chapterURL string) ([]Page, error)

	// Image downloads the raw bytes for one page, identified by the SAME
	// (pageURL, imageURL) pair a Pages call returned. imageURL may be empty
	// when the source only set url (some sources encode routing in url and
	// resolve the real image address server-side).
	Image(ctx context.Context, sourceID int64, pageURL, imageURL string) (data []byte, contentType string, err error)

	// Sources lists every source the engine host has loaded from its
	// installed extensions.
	Sources(ctx context.Context) ([]Source, error)

	// Preferences reads sourceID's configurable preferences (the union of
	// EditText/Switch/List/MultiSelect variants a source may expose).
	Preferences(ctx context.Context, sourceID int64) ([]Preference, error)

	// SetPreferences writes changes (preference key -> new value) for
	// sourceID and returns the full refreshed preference list.
	SetPreferences(ctx context.Context, sourceID int64, changes map[string]any) ([]Preference, error)

	// Extensions lists every extension the engine host knows about
	// (installed + available from the configured repos).
	Extensions(ctx context.Context) ([]Extension, error)

	// InstallExtension installs an extension either by pkgName (resolved
	// against the configured repos) or by a direct apkURL — exactly one of
	// the two must be non-empty. Returns the refreshed extension list.
	InstallExtension(ctx context.Context, pkgName, apkURL string) ([]Extension, error)

	// RefreshExtensions re-fetches the available-extensions list from the
	// configured repos ("check for updates") and returns the refreshed list.
	RefreshExtensions(ctx context.Context) ([]Extension, error)

	// UpdateExtension updates an already-installed extension identified by
	// pkgName and returns the refreshed extension list.
	UpdateExtension(ctx context.Context, pkgName string) ([]Extension, error)

	// UninstallExtension removes an installed extension identified by
	// pkgName and returns the refreshed extension list.
	UninstallExtension(ctx context.Context, pkgName string) ([]Extension, error)

	// Repos reads the configured extension-repo index URLs.
	Repos(ctx context.Context) ([]string, error)

	// SetRepos REPLACES the configured extension-repo index URL list and
	// returns it read back. An empty slice clears every repo.
	SetRepos(ctx context.Context, repos []string) ([]string, error)

	// SetFlareSolverr applies a PARTIAL update of the FlareSolverr
	// (Cloudflare-bypass) config — only patch's non-nil fields are sent, so
	// unset fields are never clobbered — and returns the config read back.
	SetFlareSolverr(ctx context.Context, patch FlareSolverrPatch) (FlareSolverrConfig, error)

	// SetSocks applies a PARTIAL update of the SOCKS-proxy config — only
	// patch's non-nil fields are sent — and returns the config read back
	// (the password is never echoed back by the host).
	SetSocks(ctx context.Context, patch SocksPatch) (SocksConfig, error)
}

// New constructs a Client that talks to the engine host at baseURL (e.g.
// "http://127.0.0.1:8181", no trailing slash required) using doer to send
// requests. doer is typically a *http.Client in production or an
// httptest.Server's own client in tests.
func New(baseURL string, doer HTTPDoer) Client {
	return &httpClient{baseURL: trimTrailingSlash(baseURL), doer: doer}
}

// trimTrailingSlash removes one trailing "/" from s, if present, so
// c.baseURL+path never produces a doubled slash.
func trimTrailingSlash(s string) string {
	if len(s) > 0 && s[len(s)-1] == '/' {
		return s[:len(s)-1]
	}
	return s
}

// httpClient is the unexported concrete implementation of Client.
type httpClient struct {
	baseURL string
	doer    HTTPDoer
}

// Health calls GET /health.
func (c *httpClient) Health(ctx context.Context) (Health, error) {
	return get[Health](ctx, c, "/health")
}

// --- error model ---------------------------------------------------------

// errorResponse is the wire shape of every non-2xx body: {"error": "..."}
// (engine-host's ErrorResponse). It is decoded once, inside newStatusError,
// and never exposed directly to callers.
type errorResponse struct {
	Error string `json:"error"`
}

// BadRequestError reports a 400 response from the engine host: a malformed
// request body, an unknown sourceId, or an invalid path/query parameter.
// Callers should treat this as a caller-side mistake — retrying the exact
// same request will fail again. Use errors.As to detect it.
type BadRequestError struct {
	// Msg is the engine host's own error message.
	Msg string
}

// Error implements the error interface for BadRequestError.
func (e *BadRequestError) Error() string {
	return "sourceengine: bad request: " + e.Msg
}

// UpstreamError reports any non-2xx response OTHER than 400 — most commonly
// a 502 raised when the underlying source fetch itself failed, but also any
// other unexpected status (e.g. a stray 404/405). Status carries the raw
// HTTP status code so callers can distinguish "source failed" from "route
// missing" if they need to. Use errors.As to detect it.
type UpstreamError struct {
	// Status is the HTTP status code the engine host responded with.
	Status int
	// Msg is the engine host's own error message.
	Msg string
}

// Error implements the error interface for UpstreamError.
func (e *UpstreamError) Error() string {
	return fmt.Sprintf("sourceengine: upstream error (status %d): %s", e.Status, e.Msg)
}

// newStatusError reads resp's body and maps a non-2xx response to a typed
// error: 400 -> *BadRequestError, anything else -> *UpstreamError. It always
// consumes resp.Body; callers must not read it afterwards.
func newStatusError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	msg := string(body)
	var parsed errorResponse
	if err := json.Unmarshal(body, &parsed); err == nil && parsed.Error != "" {
		msg = parsed.Error
	}
	if resp.StatusCode == http.StatusBadRequest {
		return &BadRequestError{Msg: msg}
	}
	return &UpstreamError{Status: resp.StatusCode, Msg: msg}
}

// --- shared HTTP/JSON plumbing --------------------------------------------

// newRequest builds an *http.Request for method+path against c.baseURL. When
// body is non-nil it is JSON-marshalled into the request body and
// Content-Type is set; a nil body sends an empty request body (used by
// endpoints like /extensions/refresh that take no input).
func (c *httpClient) newRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			// Defensive path: every body passed by this package is one of our
			// own DTOs/wire-request structs (structs, slices, maps of
			// string/bool/int/float) — json.Marshal never fails on those.
			// Unreachable in practice.
			return nil, fmt.Errorf("sourceengine: marshal %s %s request body: %w", method, path, err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		// Defensive path: reachable only with an invalid HTTP method string
		// (every call site here uses a http.Method* constant) or a nil ctx
		// (every caller passes a live context.Context). Unreachable in
		// practice.
		return nil, fmt.Errorf("sourceengine: build %s %s request: %w", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// send builds and executes method+path against c.baseURL, returning the raw
// response for the caller (doJSON or doRaw) to interpret. The caller owns
// closing resp.Body.
func (c *httpClient) send(ctx context.Context, method, path string, body any) (*http.Response, error) {
	req, err := c.newRequest(ctx, method, path, body)
	if err != nil {
		// Defensive path: see newRequest's own doc comments — both of its
		// error branches are unreachable with this package's controlled
		// inputs. Kept as a plain propagation, not a panic, in case that
		// ever changes.
		return nil, err
	}
	resp, err := c.doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sourceengine: %s %s: %w", method, path, err)
	}
	return resp, nil
}

// doJSON is the ONE shared JSON request/response helper every endpoint file
// calls through (directly, or via the get/post sugar below). It sends
// method+path with body (nil for no body), maps a non-2xx response to a
// typed error via newStatusError, and otherwise JSON-decodes the response
// into T.
func doJSON[T any](ctx context.Context, c *httpClient, method, path string, body any) (T, error) {
	var zero T
	resp, err := c.send(ctx, method, path, body)
	if err != nil {
		return zero, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return zero, newStatusError(resp)
	}

	var out T
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return zero, fmt.Errorf("sourceengine: decode %s %s response: %w", method, path, err)
	}
	return out, nil
}

// doRaw is the raw-bytes counterpart of doJSON, used only by /image (which
// returns the image bytes directly with a Content-Type header, not JSON). It
// shares the same request building and error mapping as doJSON.
func doRaw(ctx context.Context, c *httpClient, method, path string, body any) (data []byte, contentType string, err error) {
	resp, err := c.send(ctx, method, path, body)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", newStatusError(resp)
	}

	data, err = io.ReadAll(resp.Body)
	if err != nil {
		// Defensive path: reachable only on a connection dropped mid-body
		// (an OS-level read failure); not reproducible against an
		// httptest.Server, which always delivers a complete body.
		return nil, "", fmt.Errorf("sourceengine: read %s %s response: %w", method, path, err)
	}
	return data, resp.Header.Get("Content-Type"), nil
}

// get is sugar for doJSON with method GET and no request body.
func get[T any](ctx context.Context, c *httpClient, path string) (T, error) {
	return doJSON[T](ctx, c, http.MethodGet, path, nil)
}

// post is sugar for doJSON with method POST.
func post[T any](ctx context.Context, c *httpClient, path string, body any) (T, error) {
	return doJSON[T](ctx, c, http.MethodPost, path, body)
}
