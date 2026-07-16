// Package anilist implements tracker.Tracker against AniList's GraphQL API
// (https://graphql.anilist.co) — the tracker-SYNC half (search, get/save/
// update/delete a reading-progress entry) that needs OAuth. This is a
// SEPARATE client from internal/metadata/anilist, which covers AniList's
// public METADATA-read half; the two share a physical provider but not a
// package, per spec/trackers-and-rich-library-umbrella-v2 §1 (metadata vs
// tracker are different subsystems).
//
// AniList's tracker OAuth is the IMPLICIT grant (response_type=token): the
// authorize redirect delivers the access token in the callback URL's
// FRAGMENT, which browsers never send to a server. ExchangeCode is
// therefore N/A here (always ErrImplicitFlow) — the SPA reads the fragment
// client-side and this Client instead exposes TokenFromImplicit to wrap the
// already-extracted token into a TokenSet. AniList issues NO refresh token
// (Refresh always returns ErrNoRefresh) and forbids a client secret for a
// self-hosted open app — see spec §5.
package anilist

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

	"github.com/technobecet/tsundoku/internal/metadata/httpx"
	"github.com/technobecet/tsundoku/internal/tracker"
)

const (
	graphQLEndpoint = "https://graphql.anilist.co"
	authorizeURL    = "https://anilist.co/api/v2/oauth/authorize"

	// requestsPerMinute stays under AniList's documented 90 req/min cap
	// with headroom for clock-boundary jitter — same budget
	// internal/metadata/anilist uses, reused here via the shared
	// internal/metadata/httpx rate limiter (spec §5: "AniList client-side
	// rate-limit 85/min").
	requestsPerMinute = 85

	// defaultSearchLimit is used when a caller passes a non-positive limit
	// to Search — kept identical to internal/metadata/anilist's default so
	// behaviour is unsurprising across the two AniList clients.
	defaultSearchLimit = 10

	httpTimeout = 30 * time.Second

	// implicitTokenLifetime is the SYNTHETIC expiry this client assigns an
	// implicit-flow token — AniList's real implicit tokens last "about a
	// year" (spec §5) with no way to query the exact expiry from the token
	// itself, so this is a conservative floor: it makes the shared auth
	// RoundTripper's proactive expiry check meaningful (never expires
	// silently mid-session) while erring on the side of "ask the owner to
	// re-login a little early" rather than "claim a token is still good
	// when AniList has already revoked it".
	implicitTokenLifetime = 300 * 24 * time.Hour
)

// providerKey/providerID/providerName are this Client's fixed identity in
// the tracker.Tracker contract. providerID matches tracker.IDAniList (2),
// the same numbering the ent schema and internal/metadata/anilist both
// pin — one shared registry across subsystems.
const (
	providerKey  = "anilist"
	providerName = "AniList"
)

// Client implements tracker.Tracker for AniList.
type Client struct {
	http     *http.Client
	clientID string
}

// compile-time assert: Client satisfies the tracker.Tracker contract, plus
// the two optional capability interfaces it implements (implicit-flow token
// extraction + account info).
var (
	_ tracker.Tracker                = (*Client)(nil)
	_ tracker.ImplicitTokenExtractor = (*Client)(nil)
	_ tracker.AccountInfoProvider    = (*Client)(nil)
)

// New builds a Client. clientID is AniList's registered app client id
// (config-injected — see config.TrackerConfig.AniListClientID; this
// package never reads it from the environment). A nil httpClient gets a
// default *http.Client whose Transport is
// httpx.NewRateLimited(nil, requestsPerMinute), mirroring
// internal/metadata/anilist.New's shared-throttle default.
func New(clientID string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout:   httpTimeout,
			Transport: httpx.NewRateLimited(nil, requestsPerMinute),
		}
	}
	return &Client{http: httpClient, clientID: clientID}
}

// Key returns this tracker's stable string identity.
func (c *Client) Key() string { return providerKey }

// ID returns this tracker's numeric registry id (tracker.IDAniList).
func (c *Client) ID() int { return tracker.IDAniList }

// Name returns this tracker's human-display name.
func (c *Client) Name() string { return providerName }

// NeedsOAuth reports true — AniList connects via an OAuth redirect.
func (c *Client) NeedsOAuth() bool { return true }

// SupportsPrivate reports true — AniList's MediaList entries carry a
// `private` flag (see mediaListEntrySelection) this client already reads/
// writes on every GetEntry/SaveEntry/UpdateEntry call.
func (c *Client) SupportsPrivate() bool { return true }

// AuthURL builds AniList's implicit-grant authorize URL. AniList's implicit
// flow has no PKCE, so pkceVerifier is always "". AniList's real authorize
// endpoint accepts ONLY client_id + response_type=token — confirmed against
// Suwayomi-Server's AnilistApi.kt authUrl() and Komikku's own AnilistApi.kt,
// neither of which sends redirect_uri OR state (AniList's app-registration
// model has no per-request redirect_uri to validate, and rejects an
// unrecognized state-shaped param with "unsupported_grant_type" — the bug
// this alignment fixes). Both of this method's parameters are therefore
// UNUSED — kept only so Client still satisfies the shared tracker.Tracker
// interface every OAuth tracker implements. Login-correlation without a
// CSRF state value is handled one layer up: internal/tracker/connect now
// keys its pending-login stash by TRACKER ID instead (see that package's
// doc comment). Returns tracker.ErrClientIDNotConfigured when this Client's
// clientID is blank (fail-closed — an owner who hasn't registered an
// AniList app yet gets a clear error, never a URL that can never work).
func (c *Client) AuthURL(_, _ string) (string, string, error) {
	if c.clientID == "" {
		return "", "", tracker.ErrClientIDNotConfigured
	}
	q := url.Values{
		"client_id":     {c.clientID},
		"response_type": {"token"},
	}
	return authorizeURL + "?" + q.Encode(), "", nil
}

// ExchangeCode always returns tracker.ErrImplicitFlow — AniList's implicit
// grant delivers the token in a URL fragment the server never sees; use
// TokenFromImplicit instead.
func (c *Client) ExchangeCode(_ context.Context, _, _, _ string) (tracker.TokenSet, error) {
	return tracker.TokenSet{}, tracker.ErrImplicitFlow
}

// TokenFromImplicit wraps an access token the SPA already extracted from
// the callback URL's fragment (browsers never forward a fragment to a
// server — see the package doc comment) into a TokenSet with a synthetic
// expiry (implicitTokenLifetime). Returns an error on an empty
// accessToken — never fabricates a TokenSet the caller could mistake for a
// real login.
func (c *Client) TokenFromImplicit(accessToken string) (tracker.TokenSet, error) {
	if accessToken == "" {
		return tracker.TokenSet{}, fmt.Errorf("anilist: empty implicit access token")
	}
	expires := time.Now().Add(implicitTokenLifetime)
	return tracker.TokenSet{Access: accessToken, ExpiresAt: &expires}, nil
}

// Refresh always returns tracker.ErrNoRefresh — AniList's implicit grant
// issues no refresh token; recovery is a fresh AuthURL/TokenFromImplicit
// round-trip (re-login).
func (c *Client) Refresh(_ context.Context, _ string) (tracker.TokenSet, error) {
	return tracker.TokenSet{}, tracker.ErrNoRefresh
}

// Search returns up to defaultSearchLimit AniList manga matches for a
// free-text query, attaching token (when non-empty) as a Bearer
// credential — AniList's search works unauthenticated, but an owner's
// account token still narrows out adult content per that account's
// preferences.
func (c *Client) Search(ctx context.Context, token, query string) ([]tracker.TrackSearchResult, error) {
	var data searchPageData
	vars := map[string]any{"search": query, "perPage": defaultSearchLimit}
	if err := c.do(ctx, token, searchQuery, vars, &data); err != nil {
		return nil, err
	}
	out := make([]tracker.TrackSearchResult, len(data.Page.Media))
	for i, m := range data.Page.Media {
		out[i] = toTrackSearchResult(m)
	}
	return out, nil
}

// GetEntry fetches the caller's own MediaList entry for remoteID (an
// AniList Media id). Returns (nil, nil) when the manga is not yet on the
// account's list — AniList represents that as a null MediaList, not an
// error. Requires a non-empty token (there is no "my" entry without an
// account).
func (c *Client) GetEntry(ctx context.Context, token, remoteID string) (*tracker.TrackEntry, error) {
	if token == "" {
		return nil, fmt.Errorf("anilist: GetEntry requires an account token")
	}
	mediaID, err := strconv.Atoi(remoteID)
	if err != nil {
		return nil, fmt.Errorf("anilist: invalid remote id %q: %w", remoteID, err)
	}
	viewerID, _, _, err := c.viewerInfo(ctx, token)
	if err != nil {
		return nil, err
	}

	ml, err := c.fetchMediaListEntry(ctx, token, viewerID, mediaID)
	if err != nil {
		return nil, err
	}
	if ml == nil {
		return nil, nil
	}
	entry := toTrackEntry(ml)
	return &entry, nil
}

// fetchMediaListEntry issues the MediaList query and TOLERATES AniList's real
// "not on the account's list" response shape. Production AniList answers a
// MediaList the caller has NOT added with HTTP 404 whose GraphQL body STILL
// carries `data` present and MediaList explicitly null — e.g.
// {"errors":[{"message":"Not Found","status":404}],"data":{"MediaList":null}}.
// do()'s generic non-200-is-error path mistook that for a transport failure,
// so Bind aborted with an error instead of falling through to SaveEntry
// (creating the entry) — the bug this fixes. That exact shape returns
// (nil, nil) here ("not tracked"); a 200 whose MediaList is null (AniList's
// other documented not-tracked encoding) returns nil the same way. Any OTHER
// non-200 — and a 404 that ISN'T the data-present-MediaList-null shape — is
// still surfaced as a real error (isMediaListNotTracked is deliberately
// narrow so a genuine 404 is never swallowed).
func (c *Client) fetchMediaListEntry(ctx context.Context, token string, viewerID, mediaID int) (*mediaListEntry, error) {
	resp, err := c.postGraphQL(ctx, token, getEntryQuery, map[string]any{"userId": viewerID, "mediaId": mediaID})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("anilist: read response: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		var data getEntryData
		if err := decodeGraphQLBody(body, &data); err != nil {
			return nil, err
		}
		return data.MediaList, nil
	case http.StatusNotFound:
		if isMediaListNotTracked(body) {
			return nil, nil
		}
		return nil, fmt.Errorf("anilist: HTTP 404: %s", strings.TrimSpace(string(body)))
	case http.StatusUnauthorized:
		// Expired/revoked implicit token — surface tracker.ErrTokenExpired (see
		// do()'s own 401 branch for the rationale).
		return nil, fmt.Errorf("anilist: HTTP 401: %s: %w", strings.TrimSpace(string(body)), tracker.ErrTokenExpired)
	default:
		return nil, fmt.Errorf("anilist: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

// SaveEntry creates a new MediaList entry (a bind) for entry.RemoteID,
// returning the tracker-assigned LibraryID the caller must keep for
// subsequent UpdateEntry/DeleteEntry calls.
func (c *Client) SaveEntry(ctx context.Context, token string, entry tracker.TrackEntry) (tracker.TrackEntry, error) {
	mediaID, err := strconv.Atoi(entry.RemoteID)
	if err != nil {
		return tracker.TrackEntry{}, fmt.Errorf("anilist: invalid remote id %q: %w", entry.RemoteID, err)
	}
	vars := map[string]any{
		"mediaId":     mediaID,
		"progress":    int(entry.Progress),
		"status":      entry.Status,
		"scoreRaw":    int(entry.Score),
		"startedAt":   timeToFuzzyDateInput(entry.StartDate),
		"completedAt": timeToFuzzyDateInput(entry.FinishDate),
		"private":     entry.Private,
	}
	var data saveEntryData
	if err := c.do(ctx, token, saveEntryMutation, vars, &data); err != nil {
		return tracker.TrackEntry{}, err
	}
	if data.SaveMediaListEntry == nil {
		return tracker.TrackEntry{}, fmt.Errorf("anilist: SaveMediaListEntry returned no entry")
	}
	return toTrackEntry(data.SaveMediaListEntry), nil
}

// UpdateEntry writes progress/status/score/dates to the EXISTING MediaList
// entry identified by entry.LibraryID (not RemoteID — AniList updates are
// keyed by the list-entry's own id). Returns an error when LibraryID is
// blank rather than silently creating a duplicate entry via SaveEntry's
// mediaId-keyed upsert.
func (c *Client) UpdateEntry(ctx context.Context, token string, entry tracker.TrackEntry) (tracker.TrackEntry, error) {
	if entry.LibraryID == "" {
		return tracker.TrackEntry{}, fmt.Errorf("anilist: UpdateEntry requires a library id")
	}
	libraryID, err := strconv.Atoi(entry.LibraryID)
	if err != nil {
		return tracker.TrackEntry{}, fmt.Errorf("anilist: invalid library id %q: %w", entry.LibraryID, err)
	}
	vars := map[string]any{
		"id":          libraryID,
		"progress":    int(entry.Progress),
		"status":      entry.Status,
		"scoreRaw":    int(entry.Score),
		"startedAt":   timeToFuzzyDateInput(entry.StartDate),
		"completedAt": timeToFuzzyDateInput(entry.FinishDate),
		"private":     entry.Private,
	}
	var data saveEntryData
	if err := c.do(ctx, token, updateEntryMutation, vars, &data); err != nil {
		return tracker.TrackEntry{}, err
	}
	if data.SaveMediaListEntry == nil {
		return tracker.TrackEntry{}, fmt.Errorf("anilist: SaveMediaListEntry returned no entry")
	}
	return toTrackEntry(data.SaveMediaListEntry), nil
}

// DeleteEntry removes the MediaList entry identified by entry.LibraryID
// from the account. Returns an error when LibraryID is blank.
func (c *Client) DeleteEntry(ctx context.Context, token string, entry tracker.TrackEntry) error {
	if entry.LibraryID == "" {
		return fmt.Errorf("anilist: DeleteEntry requires a library id")
	}
	libraryID, err := strconv.Atoi(entry.LibraryID)
	if err != nil {
		return fmt.Errorf("anilist: invalid library id %q: %w", entry.LibraryID, err)
	}
	var data deleteEntryData
	return c.do(ctx, token, deleteEntryMutation, map[string]any{"id": libraryID}, &data)
}

// AccountInfo fetches the logged-in account's id/name/score-format via the
// Viewer query (spec §4: captured once at login into
// TrackerConnection.username / .score_format).
func (c *Client) AccountInfo(ctx context.Context, token string) (tracker.AccountInfo, error) {
	id, name, scoreFormat, err := c.viewerInfo(ctx, token)
	if err != nil {
		return tracker.AccountInfo{}, err
	}
	return tracker.AccountInfo{
		RemoteUserID: strconv.Itoa(id),
		Username:     name,
		ScoreFormat:  scoreFormat,
	}, nil
}

// viewerInfo is the shared primitive behind AccountInfo and GetEntry (which
// needs the viewer's numeric id to resolve "my" MediaList entry — AniList
// has no direct "give me my entry for media X" query).
func (c *Client) viewerInfo(ctx context.Context, token string) (id int, name string, scoreFormat string, err error) {
	if token == "" {
		return 0, "", "", fmt.Errorf("anilist: viewer lookup requires an account token")
	}
	var data viewerData
	if err := c.do(ctx, token, viewerQuery, nil, &data); err != nil {
		return 0, "", "", err
	}
	if data.Viewer == nil {
		return 0, "", "", fmt.Errorf("anilist: viewer query returned no account (token may be invalid)")
	}
	return data.Viewer.ID, data.Viewer.Name, data.Viewer.MediaListOptions.ScoreFormat, nil
}

// gqlRequest is the standard GraphQL-over-HTTP POST envelope.
type gqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

// gqlError is one entry in a GraphQL response's top-level "errors" array.
type gqlError struct {
	Message string `json:"message"`
}

// gqlResponse is the standard GraphQL-over-HTTP response envelope; Data is
// kept raw so each call's do() unmarshal into its own typed shape.
type gqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []gqlError      `json:"errors"`
}

// postGraphQL marshals and POSTs a GraphQL request to AniList (attaching a
// Bearer token when non-empty) and returns the raw HTTP response — the CALLER
// owns resp.Body.Close(). Shared by do() (the generic decode path) and
// GetEntry's fetchMediaListEntry, which additionally needs the raw status +
// body do() hides so it can recognise AniList's 404 not-tracked shape.
func (c *Client) postGraphQL(ctx context.Context, token, query string, vars map[string]any) (*http.Response, error) {
	body, err := json.Marshal(gqlRequest{Query: query, Variables: vars})
	if err != nil {
		// Defensive path: gqlRequest holds only a string and a
		// map[string]any of JSON-safe scalars, which json.Marshal never
		// fails on; unreachable in practice.
		return nil, fmt.Errorf("anilist: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, graphQLEndpoint, bytes.NewReader(body))
	if err != nil {
		// Defensive path: reachable only with a nil context, which every
		// caller here always supplies a real one for; unreachable in
		// practice.
		return nil, fmt.Errorf("anilist: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("anilist: request: %w", err)
	}
	return resp, nil
}

// do POSTs a GraphQL request to AniList and decodes the "data" field into out
// (skipped when out is nil). Any non-200 HTTP status fails the whole call; see
// GetEntry/fetchMediaListEntry for the ONE endpoint that instead tolerates a
// 404 not-tracked shape.
func (c *Client) do(ctx context.Context, token, query string, vars map[string]any, out any) error {
	resp, err := c.postGraphQL(ctx, token, query, vars)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("anilist: read response: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		// AniList's implicit token was revoked/expired. AniList issues no
		// refresh grant, so there is no automatic recovery — surface
		// tracker.ErrTokenExpired so the orchestration layer flags the
		// connection token_expired (re-auth needed) instead of a silent hard
		// fail (STEP 4 / gap-report guidance).
		return fmt.Errorf("anilist: HTTP 401: %s: %w", strings.TrimSpace(string(body)), tracker.ErrTokenExpired)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("anilist: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return decodeGraphQLBody(body, out)
}

// decodeGraphQLBody decodes a GraphQL-over-HTTP envelope from body: a
// non-empty "errors" array fails the whole call — AniList's GraphQL layer can
// return partial data alongside errors, but a tracker operation has no use for
// a partially-populated result — otherwise the "data" field is unmarshaled
// into out (skipped when out is nil).
func decodeGraphQLBody(body []byte, out any) error {
	var envelope gqlResponse
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("anilist: decode response: %w", err)
	}

	if len(envelope.Errors) > 0 {
		msgs := make([]string, len(envelope.Errors))
		for i, e := range envelope.Errors {
			msgs[i] = e.Message
		}
		return fmt.Errorf("anilist: GraphQL errors: %s", strings.Join(msgs, "; "))
	}

	if out == nil {
		return nil
	}
	return json.Unmarshal(envelope.Data, out)
}

// isMediaListNotTracked reports whether body is AniList's "this manga is not on
// the account's list" response: a GraphQL body whose top-level `data` object is
// PRESENT (not absent, not JSON null) and whose MediaList field is explicitly
// JSON null. Production AniList serves this alongside HTTP 404 + an
// {"errors":[{"message":"Not Found","status":404}]} array. Being this narrow —
// data present AND MediaList literally null — means a genuine 404 (no data key,
// data:null, or any other query's error shape) is never mistaken for
// "not tracked" and silently swallowed.
func isMediaListNotTracked(body []byte) bool {
	var env struct {
		Data *struct {
			MediaList json.RawMessage `json:"MediaList"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return false
	}
	if env.Data == nil {
		return false
	}
	return string(bytes.TrimSpace(env.Data.MediaList)) == "null"
}
