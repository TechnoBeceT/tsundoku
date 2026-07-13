// Package mangaupdates implements metadata.Provider against MangaUpdates'
// public REST API (api.mangaupdates.com/v1). Every read this provider needs
// — search and series detail — is anonymous; no API key or OAuth is
// involved (per brief/komf-metadata-engine-reference). This file holds the
// Client type + HTTP plumbing; mapper.go converts MangaUpdates' raw
// response shapes into metadata.SeriesMetadata / metadata.SearchResult.
package mangaupdates

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/technobecet/tsundoku/internal/metadata"
)

const (
	// Key is this provider's stable identity (metadata.Provider.Key()).
	Key = "mangaupdates"
	// providerID is this provider's numeric registry id
	// (metadata.Provider.ID()). Mirrors the tracker registry pinned in
	// ent/schema/trackerconnection.go: MAL=1, AniList=2, Kitsu=3,
	// MangaUpdates=7 — the same physical provider, one shared numbering
	// (see anilist/client.go's providerID doc comment for the sibling
	// convention).
	providerID = 7
	// priority is this provider's merge-order weight
	// (metadata.Provider.Priority()) — LOWER runs earlier in a
	// primary-anchored gap-fill (mirrors anilist=0, mangadex=1).
	// MangaUpdates ranks just behind MangaDex: a solid public source, but
	// its free-text status field and unlabeled alt-title list make it a
	// weaker anchor than AniList/MangaDex's structured shapes.
	priority = 2

	baseURL = "https://api.mangaupdates.com/v1"

	// defaultSearchLimit is used when a caller passes a non-positive limit.
	defaultSearchLimit = 10

	// httpTimeout bounds a single MangaUpdates request. MangaUpdates
	// publishes no documented per-minute rate cap (unlike AniList/
	// MangaDex), so no shared throttling transport is wired in here.
	httpTimeout = 30 * time.Second
)

// Client implements metadata.Provider for MangaUpdates.
type Client struct {
	http *http.Client
}

// compile-time assert: Client satisfies the metadata.Provider contract.
var _ metadata.Provider = (*Client)(nil)

// New builds a Client. A nil httpClient gets a default *http.Client with
// httpTimeout — MangaUpdates has no documented rate limit to throttle
// against, so (unlike anilist.New) no httpx.NewRateLimited transport is
// installed by default. Passing a non-nil httpClient (e.g. one backed by an
// httptest server) bypasses that default entirely.
func New(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: httpTimeout}
	}
	return &Client{http: httpClient}
}

// Key returns this provider's stable string identity.
func (c *Client) Key() string { return Key }

// ID returns this provider's numeric registry id (mirrors the tracker
// registry — see the providerID doc comment above).
func (c *Client) ID() int { return providerID }

// Priority returns this provider's default merge-order rank (lower = higher
// priority / earlier in Merge's gap-fill order).
func (c *Client) Priority() int { return priority }

// Search returns up to limit MangaUpdates series matching the free-text
// query q via POST /v1/series/search. A non-positive limit falls back to
// defaultSearchLimit.
func (c *Client) Search(ctx context.Context, q string, limit int) ([]metadata.SearchResult, error) {
	if limit <= 0 {
		limit = defaultSearchLimit
	}

	body, err := json.Marshal(searchRequest{Search: q, PerPage: limit})
	if err != nil {
		// Defensive path: searchRequest holds only JSON-safe scalars,
		// which json.Marshal never fails on; unreachable in practice.
		return nil, fmt.Errorf("mangaupdates: marshal search request: %w", err)
	}

	var page searchResponse
	if err := c.doPost(ctx, baseURL+"/series/search", body, &page); err != nil {
		return nil, err
	}

	out := make([]metadata.SearchResult, 0, len(page.Results))
	for _, r := range page.Results {
		out = append(out, toSearchResult(r.Record))
	}
	return out, nil
}

// GetSeriesMetadata fetches the full metadata record for one MangaUpdates
// series (remoteID is the decimal series_id, e.g. from a prior Search) via
// GET /v1/series/{id}.
func (c *Client) GetSeriesMetadata(ctx context.Context, remoteID string) (metadata.SeriesMetadata, error) {
	var detail seriesDetail
	reqURL := baseURL + "/series/" + url.PathEscape(remoteID)
	if err := c.doGet(ctx, reqURL, &detail); err != nil {
		return metadata.SeriesMetadata{}, err
	}
	return toSeriesMetadata(detail), nil
}

// GetSeriesCover fetches the raw cover image bytes MangaUpdates' image.url.
// original field points at for remoteID, plus a bare file extension (no
// leading dot) derived from the URL. It does one metadata lookup (to
// resolve the current cover URL) then one plain GET of that URL — no
// separate "cover" API on MangaUpdates.
func (c *Client) GetSeriesCover(ctx context.Context, remoteID string) ([]byte, string, error) {
	meta, err := c.GetSeriesMetadata(ctx, remoteID)
	if err != nil {
		return nil, "", err
	}
	if meta.CoverURL == "" {
		return nil, "", fmt.Errorf("mangaupdates: no cover image for id %s", remoteID)
	}
	data, err := c.fetchBytes(ctx, meta.CoverURL)
	if err != nil {
		return nil, "", err
	}
	return data, extFromURL(meta.CoverURL), nil
}

// Match searches MangaUpdates for q.Title and returns the best
// NameSimilarity match across the results (comparing each result's title
// against every one of q's titles), or nil when no result clears
// MatchNone. Mirrors anilist.Client.Match / mangadex.Client.Match.
func (c *Client) Match(ctx context.Context, q metadata.MatchQuery) (*metadata.SearchResult, error) {
	results, err := c.Search(ctx, q.Title, defaultSearchLimit)
	if err != nil {
		return nil, err
	}

	var best *metadata.SearchResult
	bestType := metadata.MatchNone
	for i := range results {
		if mt := metadata.NameSimilarity(q, results[i].Title); mt > bestType {
			bestType = mt
			best = &results[i]
		}
	}
	if bestType == metadata.MatchNone {
		return nil, nil
	}
	return best, nil
}

// doGet issues a GET against reqURL and decodes a JSON body into out. Any
// non-200 response is reported as an error carrying the status and URL for
// diagnosability.
func (c *Client) doGet(ctx context.Context, reqURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		// Defensive path: reachable only with a nil context, which every
		// caller here always supplies a real one for; unreachable in
		// practice.
		return fmt.Errorf("mangaupdates: build request %s: %w", reqURL, err)
	}
	req.Header.Set("Accept", "application/json")
	return c.do(req, out)
}

// doPost issues a POST of body (already-marshaled JSON) against reqURL and
// decodes the JSON response into out.
func (c *Client) doPost(ctx context.Context, reqURL string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		// Defensive path: reachable only with a nil context; unreachable in
		// practice (every caller here always supplies a real ctx).
		return fmt.Errorf("mangaupdates: build request %s: %w", reqURL, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return c.do(req, out)
}

// do executes req and decodes its JSON body into out. Any non-200 response
// is reported as an error carrying the status and body for diagnosability.
func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("mangaupdates: request %s: %w", req.URL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mangaupdates: %s returned HTTP %d: %s", req.URL, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("mangaupdates: decode %s: %w", req.URL, err)
	}
	return nil
}

// fetchBytes GETs reqURL and returns its raw response body — used for
// cover-image downloads, which are not JSON.
func (c *Client) fetchBytes(ctx context.Context, reqURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		// Defensive path: reachable only with a nil context; unreachable in
		// practice (GetSeriesCover always passes the caller's ctx through).
		return nil, fmt.Errorf("mangaupdates: build cover request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mangaupdates: fetch cover: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mangaupdates: cover HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("mangaupdates: read cover body: %w", err)
	}
	return data, nil
}

// extFromURL returns the bare (no leading dot) file extension from a URL's
// path component, defaulting to "jpg" when the path carries none — every
// MangaUpdates cover URL observed live ends in ".jpg", but the fallback
// keeps GetSeriesCover from returning an empty ext on a future
// extension-less CDN path. Mirrors anilist.extFromURL.
func extFromURL(rawURL string) string {
	ext := strings.TrimPrefix(path.Ext(rawURL), ".")
	if ext == "" {
		return "jpg"
	}
	return ext
}
