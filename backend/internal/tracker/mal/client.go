// Package mal implements tracker.Tracker against MyAnimeList's official v2
// REST API (https://api.myanimelist.net/v2) — the tracker-SYNC half
// (OAuth login, search, get/save/update/delete a reading-progress entry).
// This is a SEPARATE client from internal/metadata/mal, which covers MAL's
// public METADATA-read half (an app-level X-MAL-CLIENT-ID header, no
// per-user OAuth); the two share a physical provider but not a package, per
// spec/trackers-and-rich-library-umbrella-v2 §1.
//
// MAL's tracker OAuth is auth-code + PKCE-PLAIN: code_challenge is sent
// EQUAL to the raw code verifier (no code_challenge_method — MAL defaults
// to plain when it is omitted), and NO client secret is required for a
// self-hosted open app — see spec §5. Unlike AniList, MAL DOES issue a
// refresh token, so Refresh is fully implemented (not ErrNoRefresh).
package mal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/technobecet/tsundoku/internal/metadata/httpx"
	"github.com/technobecet/tsundoku/internal/tracker"
)

const (
	apiBaseURL   = "https://api.myanimelist.net/v2"
	authorizeURL = "https://myanimelist.net/v1/oauth2/authorize"
	tokenURL     = "https://myanimelist.net/v1/oauth2/token" //nolint:gosec // this is MAL's public OAuth token ENDPOINT URL, not a credential

	// requestsPerMinute is a conservative courtesy cap — MAL's v2 API
	// publishes no documented per-minute limit, so this mirrors
	// internal/metadata/mal's same cautious budget via the shared
	// internal/metadata/httpx rate limiter.
	requestsPerMinute = 60

	httpTimeout = 30 * time.Second

	// searchFields / detailFields are MAL's `fields=` selections for the
	// tracker surface — narrower than internal/metadata/mal's (this client
	// only needs enough to build TrackSearchResult / TrackEntry, not the
	// full rich-metadata record).
	searchFields = "id,title,main_picture,num_chapters,status"
	detailFields = "id,title,num_chapters,my_list_status{status,score,num_chapters_read,start_date,finish_date}"

	// defaultSearchLimit is used when a caller passes a non-positive limit
	// to Search.
	defaultSearchLimit = 10
)

// providerKey/providerName are this Client's fixed identity in the
// tracker.Tracker contract. ID() returns tracker.IDMAL (1) — the same
// numbering the ent schema and internal/metadata/mal both pin.
const (
	providerKey  = "mal"
	providerName = "MyAnimeList"
)

// Client implements tracker.Tracker against MyAnimeList's v2 REST API.
type Client struct {
	http         *http.Client
	clientID     string
	clientSecret string
}

// compile-time assert: Client satisfies the tracker.Tracker contract.
var _ tracker.Tracker = (*Client)(nil)

// New builds a Client. clientID is MAL's registered app client id
// (config-injected — see config.TrackerConfig.MALClientID; this package
// never reads it from the environment or hardcodes it — it may be the same
// app used by internal/metadata/mal, or a dedicated tracker app, per
// spec §2). clientSecret is MAL's registered app client SECRET
// (config.TrackerConfig.MALClientSecret) — a CONFIDENTIAL MAL app (the
// common "web" registration type) requires it at the token endpoint EVEN
// WITH PKCE; a PUBLIC/"other"-type app has none and this Client must then
// send no client_secret at all (see doToken's callers). A nil httpClient
// gets a default *http.Client whose Transport is
// httpx.NewRateLimited(nil, requestsPerMinute).
func New(clientID, clientSecret string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout:   httpTimeout,
			Transport: httpx.NewRateLimited(nil, requestsPerMinute),
		}
	}
	return &Client{http: httpClient, clientID: clientID, clientSecret: clientSecret}
}

// Key returns this tracker's stable string identity.
func (c *Client) Key() string { return providerKey }

// ID returns this tracker's numeric registry id (tracker.IDMAL).
func (c *Client) ID() int { return tracker.IDMAL }

// Name returns this tracker's human-display name.
func (c *Client) Name() string { return providerName }

// NeedsOAuth reports true — MAL connects via an OAuth redirect.
func (c *Client) NeedsOAuth() bool { return true }

// AuthURL builds MAL's auth-code authorize URL using PKCE-PLAIN: the
// generated verifier is sent AS code_challenge verbatim (no
// code_challenge_method parameter at all — MAL treats that omission as
// "plain"), and no client secret is included. MAL's real authorize endpoint
// takes ONLY client_id + code_challenge + response_type=code — confirmed
// against Suwayomi-Server's MyAnimeListApi.kt authUrl() and Komikku's own
// MyAnimeListApi.kt, neither of which sends redirect_uri OR state (MAL's
// registered-app model has no per-request redirect_uri to validate).
// state/redirectURI are therefore UNUSED here — kept only so Client still
// satisfies the shared tracker.Tracker interface every OAuth tracker
// implements; internal/tracker/connect now correlates a pending login by
// TRACKER ID rather than a returned state value (see that package's doc
// comment). Returns tracker.ErrClientIDNotConfigured when this Client's
// clientID is blank.
func (c *Client) AuthURL(_, _ string) (string, string, error) {
	if c.clientID == "" {
		return "", "", tracker.ErrClientIDNotConfigured
	}
	verifier, err := tracker.GeneratePKCEVerifier()
	if err != nil {
		return "", "", err
	}
	q := url.Values{
		"response_type":  {"code"},
		"client_id":      {c.clientID},
		"code_challenge": {verifier},
	}
	return authorizeURL + "?" + q.Encode(), verifier, nil
}

// ExchangeCode POSTs the authorization code plus the PKCE verifier AuthURL
// generated to MAL's token endpoint — PKCE-plain means code_verifier is
// sent as the SAME raw string code_challenge carried, no transform. The
// form carries client_id + code + code_verifier + grant_type=
// authorization_code — confirmed against Suwayomi-Server's
// MyAnimeListApi.kt getAccessToken()/Komikku's own equivalent, neither of
// which sends redirect_uri (MAL's token endpoint does not require it) — PLUS
// client_secret whenever c.clientSecret is non-blank (a CONFIDENTIAL MAL app
// requires it even alongside PKCE; a public app sends none at all, so its
// request shape is unchanged). redirectURI is therefore UNUSED — kept only
// so Client still satisfies the shared tracker.Tracker interface.
func (c *Client) ExchangeCode(ctx context.Context, code, pkceVerifier, _ string) (tracker.TokenSet, error) {
	form := url.Values{
		"client_id":     {c.clientID},
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {pkceVerifier},
	}
	c.addClientSecret(form)
	return c.doToken(ctx, form)
}

// Refresh POSTs a refresh_token grant to MAL's token endpoint — carrying
// client_secret too whenever c.clientSecret is non-blank (see ExchangeCode's
// doc comment; a confidential app's refresh grant needs it exactly like its
// auth-code grant does). Returns tracker.ErrNoRefresh (without a network
// call) when refresh is empty — never sends an obviously-invalid request.
func (c *Client) Refresh(ctx context.Context, refresh string) (tracker.TokenSet, error) {
	if refresh == "" {
		return tracker.TokenSet{}, tracker.ErrNoRefresh
	}
	form := url.Values{
		"client_id":     {c.clientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
	}
	c.addClientSecret(form)
	return c.doToken(ctx, form)
}

// addClientSecret adds client_secret to form ONLY when this Client was
// built with a non-blank clientSecret — a public/"other"-type MAL app must
// never send an empty client_secret field (that is a different, and
// possibly rejected, request shape than omitting the field entirely).
// Shared by ExchangeCode and Refresh so the confidential-client behavior
// lives in exactly one place.
func (c *Client) addClientSecret(form url.Values) {
	if c.clientSecret != "" {
		form.Set("client_secret", c.clientSecret)
	}
}

// Search returns up to defaultSearchLimit MAL manga matches for a free-text
// query.
func (c *Client) Search(ctx context.Context, token, query string) ([]tracker.TrackSearchResult, error) {
	reqURL := apiBaseURL + "/manga?" + url.Values{
		"q":      {query},
		"limit":  {strconv.Itoa(defaultSearchLimit)},
		"fields": {searchFields},
	}.Encode()

	var page mangaListResponse
	if err := c.doGet(ctx, token, reqURL, &page); err != nil {
		return nil, err
	}
	out := make([]tracker.TrackSearchResult, len(page.Data))
	for i, d := range page.Data {
		out[i] = toTrackSearchResult(d.Node)
	}
	return out, nil
}

// GetEntry fetches the caller's own my_list_status for remoteID (a MAL
// manga id). Returns (nil, nil) when the manga is not yet on the account's
// list — MAL omits my_list_status entirely rather than erroring.
func (c *Client) GetEntry(ctx context.Context, token, remoteID string) (*tracker.TrackEntry, error) {
	reqURL := apiBaseURL + "/manga/" + url.PathEscape(remoteID) + "?" + url.Values{
		"fields": {detailFields},
	}.Encode()

	var detail mangaDetail
	if err := c.doGet(ctx, token, reqURL, &detail); err != nil {
		return nil, err
	}
	if detail.MyListStatus == nil {
		return nil, nil
	}
	entry := toTrackEntry(remoteID, detail.MyListStatus, detail.NumChapters)
	return &entry, nil
}

// SaveEntry creates a tracking entry for entry.RemoteID. MAL's
// my_list_status endpoint UPSERTS on PUT, so this is the same wire call as
// UpdateEntry.
func (c *Client) SaveEntry(ctx context.Context, token string, entry tracker.TrackEntry) (tracker.TrackEntry, error) {
	return c.upsertEntry(ctx, token, entry)
}

// UpdateEntry writes progress/status/score/dates to entry.RemoteID's
// my_list_status. MAL has no separate list-entry id (unlike AniList's
// LibraryID) — every write is keyed by the manga id alone.
func (c *Client) UpdateEntry(ctx context.Context, token string, entry tracker.TrackEntry) (tracker.TrackEntry, error) {
	return c.upsertEntry(ctx, token, entry)
}

// upsertEntry is the shared PUT .../my_list_status call behind both
// SaveEntry and UpdateEntry.
func (c *Client) upsertEntry(ctx context.Context, token string, entry tracker.TrackEntry) (tracker.TrackEntry, error) {
	if entry.RemoteID == "" {
		return tracker.TrackEntry{}, fmt.Errorf("mal: entry requires a remote id")
	}
	form := url.Values{
		"status":            {entry.Status},
		"score":             {strconv.Itoa(int(entry.Score))},
		"num_chapters_read": {strconv.Itoa(int(entry.Progress))},
		"start_date":        {formatMALDate(entry.StartDate)},
		"finish_date":       {formatMALDate(entry.FinishDate)},
	}
	reqURL := apiBaseURL + "/manga/" + url.PathEscape(entry.RemoteID) + "/my_list_status"

	var status myListStatus
	if err := c.doForm(ctx, token, http.MethodPut, reqURL, form, &status); err != nil {
		return tracker.TrackEntry{}, err
	}
	// MAL's PUT .../my_list_status response carries only the my_list_status
	// fields — no manga id or num_chapters of its own — so TotalChapters is
	// carried through from the caller's OWN entry (typically already
	// populated from an earlier GetEntry/Search) rather than lost on every
	// save/update round-trip.
	return toTrackEntry(entry.RemoteID, &status, entry.TotalChapters), nil
}

// DeleteEntry removes entry.RemoteID's my_list_status from the account.
func (c *Client) DeleteEntry(ctx context.Context, token string, entry tracker.TrackEntry) error {
	if entry.RemoteID == "" {
		return fmt.Errorf("mal: DeleteEntry requires a remote id")
	}
	reqURL := apiBaseURL + "/manga/" + url.PathEscape(entry.RemoteID) + "/my_list_status"
	return c.doForm(ctx, token, http.MethodDelete, reqURL, nil, nil)
}

// tokenResponse is MAL's OAuth token endpoint's JSON response shape.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

// doToken POSTs a form-encoded grant to MAL's token endpoint and parses the
// resulting TokenSet, computing ExpiresAt from the response's expires_in
// (seconds) relative to now.
func (c *Client) doToken(ctx context.Context, form url.Values) (tracker.TokenSet, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		// Defensive path: reachable only with a nil context, which every
		// caller here always supplies a real one for; unreachable in
		// practice.
		return tracker.TokenSet{}, fmt.Errorf("mal: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return tracker.TokenSet{}, fmt.Errorf("mal: token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return tracker.TokenSet{}, fmt.Errorf("mal: token HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var tok tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return tracker.TokenSet{}, fmt.Errorf("mal: decode token response: %w", err)
	}

	var expiresAt *time.Time
	if tok.ExpiresIn > 0 {
		t := time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
		expiresAt = &t
	}
	return tracker.TokenSet{Access: tok.AccessToken, Refresh: tok.RefreshToken, ExpiresAt: expiresAt}, nil
}

// doGet issues an authenticated GET against reqURL and decodes a JSON body
// into out.
func (c *Client) doGet(ctx context.Context, token, reqURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		// Defensive path: reqURL is always built from a valid base +
		// url.Values here; unreachable in practice.
		return fmt.Errorf("mal: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	return c.doAndDecode(req, out)
}

// doForm issues an authenticated form-encoded request (PUT/DELETE) against
// reqURL and decodes a JSON body into out (skipped when out is nil — MAL's
// DELETE returns no useful body).
func (c *Client) doForm(ctx context.Context, token, method, reqURL string, form url.Values, out any) error {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		// Defensive path: reqURL/method are always constructed from fixed,
		// valid inputs here; unreachable in practice.
		return fmt.Errorf("mal: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	return c.doAndDecode(req, out)
}

// doAndDecode executes req and, on a 200 response, decodes the JSON body
// into out when out is non-nil. Any non-200 response is reported as an
// error carrying the status and body for diagnosability.
func (c *Client) doAndDecode(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("mal: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mal: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("mal: decode response: %w", err)
	}
	return nil
}
