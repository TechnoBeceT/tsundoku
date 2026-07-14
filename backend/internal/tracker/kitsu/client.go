// Package kitsu implements tracker.Tracker against Kitsu's public JSON:API
// (kitsu.app/api/edge) — the tracker-SYNC half (credential login, search,
// get/save/update/delete a reading-progress library-entry) that needs an
// account. This is a SEPARATE client from internal/metadata/kitsu, which
// covers Kitsu's public METADATA-read half (no login at all); the two share
// a physical provider but not a package, per
// spec/trackers-and-rich-library-umbrella-v2 §1 (metadata vs tracker are
// different subsystems) — mirroring internal/tracker/anilist and
// internal/tracker/mal's own split from their internal/metadata siblings.
//
// Kitsu's tracker login is the OAuth2 PASSWORD grant (NeedsOAuth() ==
// false — there is no authorize redirect, so AuthURL/ExchangeCode always
// return tracker.ErrOAuthNotSupported; the owner enters their Kitsu
// username/password directly and this Client exchanges it via
// LoginCredentials, satisfying tracker.CredentialLogin). Kitsu's password
// grant — like every other Kitsu OAuth grant — requires a registered
// "native" OAuth client_id/client_secret pair even though the flow itself
// needs no owner app registration (spec §2): clientID/clientSecret below
// are Kitsu's own long-published PUBLIC native-client credentials (the same
// pair Tachiyomi/Mihon/Komikku and other open-source Kitsu trackers ship
// baked in — Kitsu treats them as public knowledge for open-source
// clients, not a per-instance secret), so there is no config field for
// them and no owner registration step. Kitsu DOES issue a refresh token
// (unlike AniList's implicit grant), so Refresh is fully implemented.
//
// CONFIRMED from production: kitsu.app now sits behind Cloudflare bot
// protection, and Go's default `net/http` User-Agent
// ("Go-http-client/1.1") is an instant bot signature — every request
// (including the token POST) came back a Cloudflare `403 "Just a
// moment…"` challenge page instead of Kitsu's real JSON:API response. Every
// outbound request this Client issues now carries a realistic browser
// browserUserAgent header, which is enough to pass Cloudflare's cheap
// User-Agent heuristic. This is BEST-EFFORT ONLY: if kitsu.app ever serves
// a full managed JS challenge (which a plain header swap cannot solve),
// requests will still 403 — the real fix at that point is routing Kitsu
// through FlareSolverr (the same headless-browser challenge solver Suwayomi
// already uses for anti-bot sources), which is a SEPARATE follow-up, not
// built here.
package kitsu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/technobecet/tsundoku/internal/tracker"
)

const (
	apiBaseURL = "https://kitsu.app/api/edge"
	tokenURL   = "https://kitsu.app/api/oauth/token" //nolint:gosec // Kitsu's public OAuth token ENDPOINT URL, not a credential

	// jsonAPIMediaType is the content type Kitsu's JSON:API documents for
	// every request/response body.
	jsonAPIMediaType = "application/vnd.api+json"

	// clientID/clientSecret are Kitsu's published PUBLIC native-app OAuth
	// credentials for the password grant — see the package doc comment.
	// They are NOT a per-owner secret; if Kitsu ever rotates them, update
	// here (never move to config.TrackerConfig, which holds per-instance
	// app registrations, not a shared public constant). Values re-confirmed
	// against Suwayomi-Server's KitsuApi.kt and Komikku's own KitsuApi.kt —
	// both proven-working reference implementations ship this exact pair;
	// Tsundoku's prior constants were a mistyped variant that Kitsu's token
	// endpoint had never actually accepted.
	clientID     = "dd031b32d2f56c990b1425efe6c42ad847e7fe3ab46bf1299f05ecd856bdb7dd"
	clientSecret = "54d7307928f63414defd96399fc31ba847961ceaecef3a5fd93144e960c0e151" //nolint:gosec // public native-client credential, see doc comment

	httpTimeout        = 30 * time.Second
	defaultSearchLimit = 10
	providerKey        = "kitsu"
	providerName       = "Kitsu"

	// browserUserAgent is set on EVERY outbound request this Client issues
	// (the token POST and every JSON:API request) — see the package doc
	// comment. A current desktop Chrome UA string is enough to clear
	// Cloudflare's cheap User-Agent bot check; it is NOT a guarantee against
	// a full JS challenge.
	browserUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
)

// Client implements tracker.Tracker against Kitsu's public JSON:API.
type Client struct {
	http *http.Client
}

// compile-time assert: Client satisfies the tracker.Tracker contract plus
// the credential-login capability interface (Kitsu has no OAuth redirect).
var (
	_ tracker.Tracker         = (*Client)(nil)
	_ tracker.CredentialLogin = (*Client)(nil)
)

// New builds a Client. A nil httpClient gets a default *http.Client with
// httpTimeout — Kitsu documents no per-minute rate cap for these endpoints.
func New(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: httpTimeout}
	}
	return &Client{http: httpClient}
}

// WithFlareSolverrGate returns a NEW Client (the existing one is left
// untouched — mirrors the metadatasvc.Service.WithAutoIdentifyGate /
// series.Service.WithCoverFetcher optional-dep builder style) whose outbound
// transport is wrapped in the Cloudflare-clearing cfTransport (cfclearance.go):
// on a detected Cloudflare managed challenge it solves via FlareSolverr and
// retries once with the earned cf_clearance cookie + browser User-Agent.
//
// gate is called fresh on every request (via req.Context()) — it is expected
// to be backed by the Tsundoku settings overlay (settings.Service's
// FlareSolverr* accessors), so an owner's Settings-screen change hot-reloads
// without a restart (QCAT-238). gate returning FlareSolverrConfig{Enabled:
// false} (or a blank URL) makes every request a pure passthrough — today's
// exact behaviour before this feature.
func (c *Client) WithFlareSolverrGate(gate func(ctx context.Context) FlareSolverrConfig) *Client {
	base := c.http.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	wrapped := &http.Client{
		Transport: &cfTransport{
			base: base,
			gate: gate,
			// The FlareSolverr solve POST itself never goes through cfTransport
			// (that would be self-referential — a challenge on FlareSolverr's
			// OWN endpoint is not this transport's problem to solve). It gets a
			// plain client bounded generously; flaresolverr.Solve applies its
			// own tighter per-call timeout on top via the context it builds.
			solveClient: &http.Client{Timeout: 2 * time.Minute},
		},
		Timeout: c.http.Timeout,
	}
	return &Client{http: wrapped}
}

// Key returns this tracker's stable string identity.
func (c *Client) Key() string { return providerKey }

// ID returns this tracker's numeric registry id (tracker.IDKitsu).
func (c *Client) ID() int { return tracker.IDKitsu }

// Name returns this tracker's human-display name.
func (c *Client) Name() string { return providerName }

// NeedsOAuth reports false — Kitsu connects via a direct username/password
// login (LoginCredentials), never an OAuth redirect.
func (c *Client) NeedsOAuth() bool { return false }

// SupportsPrivate reports true — Kitsu's library-entries carry a `private`
// attribute (see libraryEntryAttrs) this client already reads/writes on
// every GetEntry/SaveEntry/UpdateEntry call.
func (c *Client) SupportsPrivate() bool { return true }

// AuthURL always returns tracker.ErrOAuthNotSupported — Kitsu has no
// authorize redirect; use LoginCredentials.
func (c *Client) AuthURL(_, _ string) (string, string, error) {
	return "", "", tracker.ErrOAuthNotSupported
}

// ExchangeCode always returns tracker.ErrOAuthNotSupported — Kitsu has no
// authorization code to exchange; use LoginCredentials.
func (c *Client) ExchangeCode(_ context.Context, _, _, _ string) (tracker.TokenSet, error) {
	return tracker.TokenSet{}, tracker.ErrOAuthNotSupported
}

// LoginCredentials exchanges a Kitsu username+password for a TokenSet via
// the OAuth2 password grant (POST /api/oauth/token, grant_type=password).
// Satisfies tracker.CredentialLogin.
func (c *Client) LoginCredentials(ctx context.Context, username, password string) (tracker.TokenSet, error) {
	form := url.Values{
		"grant_type":    {"password"},
		"username":      {username},
		"password":      {password},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
	}
	return c.doToken(ctx, form)
}

// Refresh mints a fresh TokenSet from a Kitsu refresh token (the password
// grant issues one, unlike AniList's implicit flow). Returns
// tracker.ErrNoRefresh (without a network call) when refresh is empty.
func (c *Client) Refresh(ctx context.Context, refresh string) (tracker.TokenSet, error) {
	if refresh == "" {
		return tracker.TokenSet{}, tracker.ErrNoRefresh
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
	}
	return c.doToken(ctx, form)
}

// Search returns up to defaultSearchLimit Kitsu manga matches for a
// free-text query, via GET /manga?filter[text]=. token is attached when
// non-empty but is not required — Kitsu search works anonymously.
// fields[manga] is explicit (rather than accepting Kitsu's full default
// attribute bag) so the Search-Enrichment fields (subtype/startDate/
// averageRating/synopsis, confirmed against Suwayomi-Server's/Komikku's own
// KitsuApi.kt manga-search field selections) are requested by name in ONE
// place alongside the fields this client already read.
func (c *Client) Search(ctx context.Context, token, query string) ([]tracker.TrackSearchResult, error) {
	reqURL := apiBaseURL + "/manga?" + url.Values{
		"filter[text]":  {query},
		"page[limit]":   {strconv.Itoa(defaultSearchLimit)},
		"fields[manga]": {"slug,canonicalTitle,status,chapterCount,posterImage,subtype,startDate,averageRating,synopsis"},
	}.Encode()

	var page mangaCollectionResponse
	if err := c.doGet(ctx, token, reqURL, &page); err != nil {
		return nil, err
	}
	out := make([]tracker.TrackSearchResult, len(page.Data))
	for i, d := range page.Data {
		out[i] = toTrackSearchResult(d)
	}
	return out, nil
}

// selfUserID resolves the logged-in account's own JSON:API user id via
// GET /users?filter[self]=true — the primitive GetEntry/upsertEntry need to
// scope a library-entries request/write to the caller's own account, since
// Kitsu's library-entries endpoints are id-scoped, not "my"-scoped the way
// MAL's are (mirrors anilist.Client.viewerInfo's same role for AniList).
func (c *Client) selfUserID(ctx context.Context, token string) (string, error) {
	if token == "" {
		return "", fmt.Errorf("kitsu: this operation requires an account token")
	}
	reqURL := apiBaseURL + "/users?" + url.Values{"filter[self]": {"true"}}.Encode()
	var page userCollectionResponse
	if err := c.doGet(ctx, token, reqURL, &page); err != nil {
		return "", err
	}
	if len(page.Data) == 0 {
		return "", fmt.Errorf("kitsu: self lookup returned no account (token may be invalid)")
	}
	return page.Data[0].ID, nil
}

// GetEntry fetches the caller's own library-entry for remoteID (a Kitsu
// manga id), via GET /library-entries?filter[userId]=&filter[mangaId]=.
// Returns (nil, nil) when the manga is not yet on the account's library —
// Kitsu represents that as an empty data array, never an error.
func (c *Client) GetEntry(ctx context.Context, token, remoteID string) (*tracker.TrackEntry, error) {
	userID, err := c.selfUserID(ctx, token)
	if err != nil {
		return nil, err
	}
	reqURL := apiBaseURL + "/library-entries?" + url.Values{
		"filter[userId]":  {userID},
		"filter[mangaId]": {remoteID},
		"filter[kind]":    {"manga"},
		// include the bound manga so its canonicalTitle rides along and
		// TrackEntry.Title can be populated (a library-entry alone carries
		// only a manga relationship reference, not its title).
		"include": {"manga"},
	}.Encode()

	var page libraryEntryCollectionResponse
	if err := c.doGet(ctx, token, reqURL, &page); err != nil {
		return nil, err
	}
	if len(page.Data) == 0 {
		return nil, nil
	}
	entry := toTrackEntry(page.Data[0], page.Included)
	return &entry, nil
}

// SaveEntry creates a new library-entry (a bind) for entry.RemoteID, via
// POST /library-entries.
func (c *Client) SaveEntry(ctx context.Context, token string, entry tracker.TrackEntry) (tracker.TrackEntry, error) {
	if entry.RemoteID == "" {
		return tracker.TrackEntry{}, fmt.Errorf("kitsu: SaveEntry requires a remote id")
	}
	userID, err := c.selfUserID(ctx, token)
	if err != nil {
		return tracker.TrackEntry{}, err
	}
	body := buildLibraryEntryRequest("", entry, userID)
	var resp libraryEntryResponse
	if err := c.doJSONAPI(ctx, token, http.MethodPost, apiBaseURL+"/library-entries?include=manga", body, &resp); err != nil {
		return tracker.TrackEntry{}, err
	}
	return toTrackEntry(resp.Data, resp.Included), nil
}

// UpdateEntry writes progress/status/score/dates to the EXISTING
// library-entry identified by entry.LibraryID, via
// PATCH /library-entries/{id}. Returns an error when LibraryID is blank
// rather than silently creating a duplicate entry via SaveEntry.
func (c *Client) UpdateEntry(ctx context.Context, token string, entry tracker.TrackEntry) (tracker.TrackEntry, error) {
	if entry.LibraryID == "" {
		return tracker.TrackEntry{}, fmt.Errorf("kitsu: UpdateEntry requires a library id")
	}
	userID, err := c.selfUserID(ctx, token)
	if err != nil {
		return tracker.TrackEntry{}, err
	}
	body := buildLibraryEntryRequest(entry.LibraryID, entry, userID)
	reqURL := apiBaseURL + "/library-entries/" + url.PathEscape(entry.LibraryID) + "?include=manga"
	var resp libraryEntryResponse
	if err := c.doJSONAPI(ctx, token, http.MethodPatch, reqURL, body, &resp); err != nil {
		return tracker.TrackEntry{}, err
	}
	return toTrackEntry(resp.Data, resp.Included), nil
}

// DeleteEntry removes the library-entry identified by entry.LibraryID from
// the account, via DELETE /library-entries/{id}. Returns an error when
// LibraryID is blank.
func (c *Client) DeleteEntry(ctx context.Context, token string, entry tracker.TrackEntry) error {
	if entry.LibraryID == "" {
		return fmt.Errorf("kitsu: DeleteEntry requires a library id")
	}
	reqURL := apiBaseURL + "/library-entries/" + url.PathEscape(entry.LibraryID)
	return c.doJSONAPI(ctx, token, http.MethodDelete, reqURL, nil, nil)
}

// doToken POSTs a form-encoded grant to Kitsu's OAuth token endpoint and
// parses the resulting TokenSet, computing ExpiresAt from the response's
// expires_in (seconds) relative to now. Mirrors mal.Client.doToken.
func (c *Client) doToken(ctx context.Context, form url.Values) (tracker.TokenSet, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		// Defensive path: reachable only with a nil context, which every
		// caller here always supplies a real one for; unreachable in
		// practice.
		return tracker.TokenSet{}, fmt.Errorf("kitsu: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", browserUserAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return tracker.TokenSet{}, fmt.Errorf("kitsu: token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return tracker.TokenSet{}, fmt.Errorf("kitsu: token HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var tok tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return tracker.TokenSet{}, fmt.Errorf("kitsu: decode token response: %w", err)
	}

	var expiresAt *time.Time
	if tok.ExpiresIn > 0 {
		t := time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
		expiresAt = &t
	}
	return tracker.TokenSet{Access: tok.AccessToken, Refresh: tok.RefreshToken, ExpiresAt: expiresAt}, nil
}

// doGet issues an authenticated (when token != "") GET against reqURL and
// decodes a JSON:API body into out.
func (c *Client) doGet(ctx context.Context, token, reqURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		// Defensive path: reachable only with a nil context; unreachable in
		// practice (every caller here always supplies a real ctx).
		return fmt.Errorf("kitsu: build request %s: %w", reqURL, err)
	}
	req.Header.Set("Accept", jsonAPIMediaType)
	req.Header.Set("User-Agent", browserUserAgent)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return c.doAndDecode(req, out)
}

// doJSONAPI issues an authenticated JSON:API request (POST/PATCH/DELETE)
// against reqURL, marshaling body (skipped when nil) as the request payload
// and decoding a JSON:API response into out (skipped when out is nil —
// DELETE returns no useful body).
func (c *Client) doJSONAPI(ctx context.Context, token, method, reqURL string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			// Defensive path: every body this package builds holds only
			// JSON-safe scalars/structs, which json.Marshal never fails on;
			// unreachable in practice.
			return fmt.Errorf("kitsu: marshal request: %w", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, reqURL, reader)
	if err != nil {
		// Defensive path: reachable only with a nil context; unreachable in
		// practice.
		return fmt.Errorf("kitsu: build request %s: %w", reqURL, err)
	}
	req.Header.Set("Accept", jsonAPIMediaType)
	if body != nil {
		req.Header.Set("Content-Type", jsonAPIMediaType)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", browserUserAgent)
	return c.doAndDecode(req, out)
}

// doAndDecode executes req and, on a 2xx response, decodes the JSON body
// into out when out is non-nil. Any non-2xx response is reported as an
// error carrying the status and body for diagnosability.
func (c *Client) doAndDecode(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("kitsu: request %s: %w", req.URL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("kitsu: %s returned HTTP %d: %s", req.URL, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("kitsu: decode response %s: %w", req.URL, err)
	}
	return nil
}
