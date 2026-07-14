// Package mangaupdates implements tracker.Tracker against MangaUpdates'
// public REST API (api.mangaupdates.com/v1) — the tracker-SYNC half
// (session login, search, get/save/update/delete a reading-list entry) that
// needs an account. This is a SEPARATE client from
// internal/metadata/mangaupdates, which covers MangaUpdates' public
// METADATA-read half (no login at all); the two share a physical provider
// but not a package, per spec/trackers-and-rich-library-umbrella-v2 §1 —
// mirroring internal/tracker/anilist and internal/tracker/mal's own split
// from their internal/metadata siblings.
//
// MangaUpdates' tracker login is a SESSION login (NeedsOAuth() == false —
// there is no authorize redirect, so AuthURL/ExchangeCode always return
// tracker.ErrOAuthNotSupported; the owner enters their MangaUpdates
// username/password directly and this Client exchanges it via
// LoginCredentials, satisfying tracker.CredentialLogin): PUT
// /v1/account/login returns a session_token used as a Bearer credential on
// every subsequent authenticated call. MangaUpdates issues no refresh
// grant — Refresh always returns tracker.ErrNoRefresh; a session simply
// re-logs-in when it expires.
//
// Reading-progress is modeled through MangaUpdates' LISTS API: a series is
// tracked by belonging to one of the account's numbered lists (0=Reading,
// 1=Wish List, 2=Complete, 3=Unfinished, 4=On Hold — MangaUpdates' own
// well-known, undocumented-in-name-but-stable numbering). This client
// always targets defaultListID (Reading) — the natural fit for "a series
// Tsundoku is actively downloading" — never lets the caller choose a
// different list; a future sync-slice can widen that if the owner wants
// finer control.
//
// The lists-API endpoints do NOT carry the list id as a URL segment —
// list_id travels in the REQUEST BODY instead (write calls) or is simply
// absent (the read call, which is scoped to the caller's account, not one
// list). Confirmed against two independent, long-running client ports of
// this same API (Komikku's + Suwayomi-Server's MangaUpdatesApi.kt): GET
// /v1/lists/series/{id}, POST /v1/lists/series, POST
// /v1/lists/series/update, POST /v1/lists/series/delete. An earlier version
// of this client wrongly injected a "/{defaultListID}/" URL segment
// (e.g. /v1/lists/0/series/{id}), which MangaUpdates answers with
// HTTP 405 "Method not allowed. Must be one of: OPTIONS" — confirmed from
// production logs when binding a tracker.
package mangaupdates

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
	baseURL = "https://api.mangaupdates.com/v1"

	// defaultListID is MangaUpdates' numeric "Reading List" id (see the
	// package doc comment). SaveEntry sends it as the target list_id when
	// binding a series; UpdateEntry sends it as the list_id the progress
	// write applies to. GetEntry and DeleteEntry never reference it — the
	// read call is list-agnostic (returns whatever list the series is
	// actually on) and the delete call identifies the series alone.
	defaultListID = 0

	httpTimeout        = 30 * time.Second
	defaultSearchLimit = 10
	providerKey        = "mangaupdates"
	providerName       = "MangaUpdates"
)

// Client implements tracker.Tracker against MangaUpdates' REST API.
type Client struct {
	http *http.Client
}

// compile-time assert: Client satisfies the tracker.Tracker contract plus
// the credential-login capability interface (MangaUpdates has no OAuth
// redirect).
var (
	_ tracker.Tracker         = (*Client)(nil)
	_ tracker.CredentialLogin = (*Client)(nil)
)

// New builds a Client. A nil httpClient gets a default *http.Client with
// httpTimeout — MangaUpdates publishes no documented per-minute rate cap.
func New(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: httpTimeout}
	}
	return &Client{http: httpClient}
}

// Key returns this tracker's stable string identity.
func (c *Client) Key() string { return providerKey }

// ID returns this tracker's numeric registry id (tracker.IDMangaUpdates).
func (c *Client) ID() int { return tracker.IDMangaUpdates }

// Name returns this tracker's human-display name.
func (c *Client) Name() string { return providerName }

// NeedsOAuth reports false — MangaUpdates connects via a direct
// username/password session login (LoginCredentials), never an OAuth
// redirect.
func (c *Client) NeedsOAuth() bool { return false }

// SupportsPrivate reports false — MangaUpdates' list-series model has no
// `private`/visibility concept at all; a Bind/UpdateTrack `private` request
// field is harmlessly ignored for this tracker.
func (c *Client) SupportsPrivate() bool { return false }

// AuthURL always returns tracker.ErrOAuthNotSupported — MangaUpdates has no
// authorize redirect; use LoginCredentials.
func (c *Client) AuthURL(_, _ string) (string, string, error) {
	return "", "", tracker.ErrOAuthNotSupported
}

// ExchangeCode always returns tracker.ErrOAuthNotSupported — MangaUpdates
// has no authorization code to exchange; use LoginCredentials.
func (c *Client) ExchangeCode(_ context.Context, _, _, _ string) (tracker.TokenSet, error) {
	return tracker.TokenSet{}, tracker.ErrOAuthNotSupported
}

// LoginCredentials exchanges a MangaUpdates username+password for a session
// TokenSet via PUT /v1/account/login. Satisfies tracker.CredentialLogin.
// The returned TokenSet carries no refresh token and no known expiry
// (MangaUpdates sessions are opaque; re-login is the only recovery — see
// Refresh).
func (c *Client) LoginCredentials(ctx context.Context, username, password string) (tracker.TokenSet, error) {
	//nolint:gosec // G117: this Marshal sends the credential TO MangaUpdates' own
	// login endpoint (the whole point of LoginCredentials) — not a leak.
	body, err := json.Marshal(loginRequest{Username: username, Password: password})
	if err != nil {
		// Defensive path: loginRequest holds only JSON-safe strings, which
		// json.Marshal never fails on; unreachable in practice.
		return tracker.TokenSet{}, fmt.Errorf("mangaupdates: marshal login request: %w", err)
	}

	var resp loginResponse
	if err := c.doJSON(ctx, "", http.MethodPut, baseURL+"/account/login", body, &resp); err != nil {
		return tracker.TokenSet{}, err
	}
	if resp.Status != "success" || resp.Context.SessionToken == "" {
		return tracker.TokenSet{}, fmt.Errorf("mangaupdates: login failed (status %q)", resp.Status)
	}
	return tracker.TokenSet{Access: resp.Context.SessionToken}, nil
}

// Refresh always returns tracker.ErrNoRefresh — MangaUpdates issues no
// refresh grant; recovery is a fresh LoginCredentials call.
func (c *Client) Refresh(_ context.Context, _ string) (tracker.TokenSet, error) {
	return tracker.TokenSet{}, tracker.ErrNoRefresh
}

// Search returns up to defaultSearchLimit MangaUpdates series matching the
// free-text query q, via POST /v1/series/search. token is attached when
// non-empty but is not required — MangaUpdates search works anonymously.
func (c *Client) Search(ctx context.Context, token, query string) ([]tracker.TrackSearchResult, error) {
	body, err := json.Marshal(searchRequest{Search: query, PerPage: defaultSearchLimit})
	if err != nil {
		// Defensive path: searchRequest holds only JSON-safe scalars, which
		// json.Marshal never fails on; unreachable in practice.
		return nil, fmt.Errorf("mangaupdates: marshal search request: %w", err)
	}

	var page searchResponse
	if err := c.doJSON(ctx, token, http.MethodPost, baseURL+"/series/search", body, &page); err != nil {
		return nil, err
	}
	out := make([]tracker.TrackSearchResult, 0, len(page.Results))
	for _, r := range page.Results {
		out = append(out, toTrackSearchResult(r.Record))
	}
	return out, nil
}

// GetEntry fetches the caller's own list-series entry for remoteID (a
// MangaUpdates series id), via GET /v1/lists/series/{remoteID} — NO list-id
// URL segment (see the package doc comment). Returns (nil, nil) when the
// series is on none of the caller's lists — MangaUpdates answers that with
// 404, which this client treats as the valid empty state rather than an
// error (mirrors AniList's null MediaList / MAL's absent my_list_status
// carve-outs).
func (c *Client) GetEntry(ctx context.Context, token, remoteID string) (*tracker.TrackEntry, error) {
	if token == "" {
		return nil, fmt.Errorf("mangaupdates: GetEntry requires an account token")
	}
	reqURL := baseURL + "/lists/series/" + url.PathEscape(remoteID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		// Defensive path: reachable only with a nil context; unreachable in
		// practice.
		return nil, fmt.Errorf("mangaupdates: build request %s: %w", reqURL, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mangaupdates: request %s: %w", reqURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("mangaupdates: %s returned HTTP %d: %s", reqURL, resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var e listSeriesEntry
	if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
		return nil, fmt.Errorf("mangaupdates: decode %s: %w", reqURL, err)
	}
	entry := toTrackEntry(e)
	return &entry, nil
}

// SaveEntry adds entry.RemoteID to the Reading List (a bind), via
// POST /v1/lists/series. The write carries NO status/chapter object — an
// add is bind-only; progress is written separately by a later UpdateEntry
// (mirrors both reference ports' addSeriesToList, which never sends
// last_chapter_read on bind).
func (c *Client) SaveEntry(ctx context.Context, token string, entry tracker.TrackEntry) (tracker.TrackEntry, error) {
	seriesID, err := parseSeriesID(entry.RemoteID)
	if err != nil {
		return tracker.TrackEntry{}, err
	}

	body, err := json.Marshal([]listSeriesAdd{{
		Series: muSeriesRef{ID: seriesID},
		ListID: defaultListID,
	}})
	if err != nil {
		// Defensive path: listSeriesAdd holds only JSON-safe scalars, which
		// json.Marshal never fails on; unreachable in practice.
		return tracker.TrackEntry{}, fmt.Errorf("mangaupdates: marshal add request: %w", err)
	}

	var results []listSeriesEntry
	if err := c.doJSON(ctx, token, http.MethodPost, baseURL+"/lists/series", body, &results); err != nil {
		return tracker.TrackEntry{}, err
	}
	return finishUpsert(entry, results), nil
}

// UpdateEntry writes progress to an EXISTING list entry, via POST
// /v1/lists/series/update. UNLIKE SaveEntry, the write names both the
// target list_id AND status.chapter (mirrors both reference ports'
// updateSeriesListItem). MangaUpdates has no separate list-entry id
// (unlike AniList's LibraryID) — every write is keyed by the series id
// alone, same as MAL.
func (c *Client) UpdateEntry(ctx context.Context, token string, entry tracker.TrackEntry) (tracker.TrackEntry, error) {
	seriesID, err := parseSeriesID(entry.RemoteID)
	if err != nil {
		return tracker.TrackEntry{}, err
	}

	body, err := json.Marshal([]listSeriesWrite{{
		Series: muSeriesRef{ID: seriesID},
		ListID: defaultListID,
		Status: muStatus{Chapter: int(entry.Progress)},
	}})
	if err != nil {
		// Defensive path: listSeriesWrite holds only JSON-safe scalars,
		// which json.Marshal never fails on; unreachable in practice.
		return tracker.TrackEntry{}, fmt.Errorf("mangaupdates: marshal update request: %w", err)
	}

	var results []listSeriesEntry
	if err := c.doJSON(ctx, token, http.MethodPost, baseURL+"/lists/series/update", body, &results); err != nil {
		return tracker.TrackEntry{}, err
	}
	return finishUpsert(entry, results), nil
}

// finishUpsert maps the first echoed row from a SaveEntry/UpdateEntry
// response, falling back to the caller's own input when MangaUpdates
// echoes nothing back — this keeps the caller's write from looking like it
// silently vanished when the wire response is thinner than expected.
func finishUpsert(entry tracker.TrackEntry, results []listSeriesEntry) tracker.TrackEntry {
	if len(results) == 0 {
		return entry
	}
	return toTrackEntry(results[0])
}

// DeleteEntry removes entry.RemoteID from every list it belongs to, via
// POST /v1/lists/series/delete. UNLIKE Save/UpdateEntry, the body is a
// bare JSON array of series ids — no wrapping object, no list_id (mirrors
// both reference ports' deleteSeriesFromList).
func (c *Client) DeleteEntry(ctx context.Context, token string, entry tracker.TrackEntry) error {
	seriesID, err := parseSeriesID(entry.RemoteID)
	if err != nil {
		return err
	}
	body, err := json.Marshal([]int64{seriesID})
	if err != nil {
		// Defensive path: a single-element []int64 slice, which
		// json.Marshal never fails on; unreachable in practice.
		return fmt.Errorf("mangaupdates: marshal delete request: %w", err)
	}
	return c.doJSON(ctx, token, http.MethodPost, baseURL+"/lists/series/delete", body, nil)
}

// parseSeriesID validates + parses a TrackEntry.RemoteID into MangaUpdates'
// numeric series id, shared by SaveEntry/UpdateEntry/DeleteEntry so the
// "blank or non-numeric remote id" guard lives in exactly one place.
func parseSeriesID(remoteID string) (int64, error) {
	if remoteID == "" {
		return 0, fmt.Errorf("mangaupdates: entry requires a remote id")
	}
	seriesID, err := strconv.ParseInt(remoteID, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("mangaupdates: invalid remote id %q: %w", remoteID, err)
	}
	return seriesID, nil
}

// doJSON POSTs/PUTs body (already-marshaled JSON) to reqURL, attaching
// Bearer token when non-empty, and decodes the JSON response into out
// (skipped when out is nil). Any non-2xx response is reported as an error
// carrying the status and body for diagnosability.
func (c *Client) doJSON(ctx context.Context, token, method, reqURL string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, reqURL, bytes.NewReader(body))
	if err != nil {
		// Defensive path: reachable only with a nil context; unreachable in
		// practice.
		return fmt.Errorf("mangaupdates: build request %s: %w", reqURL, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("mangaupdates: request %s: %w", reqURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mangaupdates: %s returned HTTP %d: %s", reqURL, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("mangaupdates: decode response %s: %w", reqURL, err)
	}
	return nil
}
