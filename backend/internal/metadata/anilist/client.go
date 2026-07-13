// Package anilist implements metadata.Provider against AniList's public
// GraphQL API (https://graphql.anilist.co). No authentication is required
// for search / metadata / cover reads — only AniList's tracker-SYNC half
// (out of scope here) needs OAuth (see
// brief/komf-metadata-engine-reference). This file holds the Client type +
// GraphQL wire plumbing; mapper.go converts the raw response shapes into
// metadata.SeriesMetadata / metadata.SearchResult.
package anilist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/technobecet/tsundoku/internal/metadata"
	"github.com/technobecet/tsundoku/internal/metadata/httpx"
)

const (
	graphQLEndpoint = "https://graphql.anilist.co"

	// requestsPerMinute stays under AniList's documented 90 req/min cap
	// (see plan/metadata-engine-phase1) with headroom for clock-boundary
	// jitter.
	requestsPerMinute = 85

	// providerKey/providerID/providerPriority are this Client's fixed
	// identity in the metadata.Provider contract. providerID=2 matches the
	// existing tracker registry pinned in ent/schema/trackerconnection.go
	// (MAL=1, AniList=2, Kitsu=3, MangaUpdates=7) — the same physical
	// provider, one shared numbering. providerPriority=0 makes AniList the
	// default primary/anchor provider (plan/metadata-engine-phase1 Registry
	// default order).
	providerKey      = "anilist"
	providerID       = 2
	providerPriority = 0

	// defaultSearchLimit is used when a caller passes a non-positive limit.
	defaultSearchLimit = 10

	// httpTimeout bounds a single AniList request; the shared rate limiter
	// (not this timeout) is what keeps request PACE under the API's cap.
	httpTimeout = 30 * time.Second
)

// Client implements metadata.Provider for AniList.
type Client struct {
	http *http.Client
}

// compile-time assert: Client satisfies the metadata.Provider contract.
var _ metadata.Provider = (*Client)(nil)

// New builds a Client. A nil httpClient gets a default *http.Client whose
// Transport is httpx.NewRateLimited(nil, requestsPerMinute) — every request
// this Client issues shares one token-bucket throttle under AniList's
// documented per-minute cap. Passing a non-nil httpClient (e.g. one backed
// by an httptest server, or a shared rate-limited transport a Registry
// hands out to several providers) bypasses that default entirely, so tests
// never pay the live throttle.
func New(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout:   httpTimeout,
			Transport: httpx.NewRateLimited(nil, requestsPerMinute),
		}
	}
	return &Client{http: httpClient}
}

// Key returns this provider's stable string identity.
func (c *Client) Key() string { return providerKey }

// ID returns this provider's numeric registry id (mirrors the tracker
// registry — see the providerID doc comment above).
func (c *Client) ID() int { return providerID }

// Priority returns this provider's default merge-order rank (lower = higher
// priority / earlier in Merge's gap-fill order).
func (c *Client) Priority() int { return providerPriority }

// Search returns up to limit AniList manga matches for a free-text query. A
// non-positive limit falls back to defaultSearchLimit.
func (c *Client) Search(ctx context.Context, q string, limit int) ([]metadata.SearchResult, error) {
	if limit <= 0 {
		limit = defaultSearchLimit
	}

	var data searchPageData
	vars := map[string]any{"search": q, "perPage": limit}
	if err := c.do(ctx, searchQuery, vars, &data); err != nil {
		return nil, err
	}

	out := make([]metadata.SearchResult, len(data.Page.Media))
	for i, m := range data.Page.Media {
		out[i] = toSearchResult(m)
	}
	return out, nil
}

// GetSeriesMetadata fetches the full metadata record for one AniList manga
// id (remoteID is the decimal AniList Media id, e.g. from a prior Search).
func (c *Client) GetSeriesMetadata(ctx context.Context, remoteID string) (metadata.SeriesMetadata, error) {
	id, err := strconv.Atoi(remoteID)
	if err != nil {
		return metadata.SeriesMetadata{}, fmt.Errorf("anilist: invalid remote id %q: %w", remoteID, err)
	}

	var data mediaData
	if err := c.do(ctx, byIDQuery, map[string]any{"id": id}, &data); err != nil {
		return metadata.SeriesMetadata{}, err
	}
	return toSeriesMetadata(data.Media), nil
}

// GetSeriesCover fetches the raw cover image bytes AniList's coverImage
// points at for remoteID, plus a bare file extension (no leading dot)
// derived from the URL. It does one metadata lookup (to resolve the current
// cover URL) then one plain GET of that URL — no separate "cover" API on
// AniList's schema.
func (c *Client) GetSeriesCover(ctx context.Context, remoteID string) ([]byte, string, error) {
	meta, err := c.GetSeriesMetadata(ctx, remoteID)
	if err != nil {
		return nil, "", err
	}
	if meta.CoverURL == "" {
		return nil, "", fmt.Errorf("anilist: no cover image for id %s", remoteID)
	}
	return fetchImage(ctx, c.http, meta.CoverURL)
}

// Match searches AniList for q.Title and returns the best NameSimilarity
// match across the results (comparing each result's display title against
// every one of q's titles), or nil when no result clears MatchClosest.
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
// kept raw so each query's do() call can unmarshal it into its own typed
// shape (searchPageData / mediaData).
type gqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []gqlError      `json:"errors"`
}

// do POSTs a GraphQL request to AniList and decodes the "data" field into
// out (skipped when out is nil). Any non-empty "errors" array is surfaced
// as a single joined error — AniList's GraphQL layer can return partial
// data alongside errors, but a metadata provider has no use for a
// partially-populated series record, so any error fails the whole call.
func (c *Client) do(ctx context.Context, query string, vars map[string]any, out any) error {
	body, err := json.Marshal(gqlRequest{Query: query, Variables: vars})
	if err != nil {
		// Defensive path: gqlRequest holds only a string and a
		// map[string]any of JSON-safe scalars, which json.Marshal never
		// fails on; unreachable in practice.
		return fmt.Errorf("anilist: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, graphQLEndpoint, bytes.NewReader(body))
	if err != nil {
		// Defensive path: reachable only with a nil context, which every
		// caller here always supplies a real one for; unreachable in
		// practice.
		return fmt.Errorf("anilist: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("anilist: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("anilist: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var envelope gqlResponse
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
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

// fetchImage GETs url and returns its raw bytes plus a bare file extension
// (no leading dot, "jpg" when the URL carries none) derived from the URL
// path. Shared by GetSeriesCover.
func fetchImage(ctx context.Context, client *http.Client, url string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		// Defensive path: reachable only with a nil context; unreachable in
		// practice (GetSeriesCover always passes the caller's ctx through).
		return nil, "", fmt.Errorf("anilist: build cover request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("anilist: fetch cover: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("anilist: cover HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("anilist: read cover body: %w", err)
	}
	return data, extFromURL(url), nil
}

// extFromURL returns the bare (no leading dot) file extension from a URL's
// path component, defaulting to "jpg" when the path carries none — every
// AniList cover URL observed live ends in an image extension, but the
// fallback keeps GetSeriesCover from returning an empty ext on a future
// extension-less CDN path.
func extFromURL(rawURL string) string {
	ext := strings.TrimPrefix(path.Ext(rawURL), ".")
	if ext == "" {
		return "jpg"
	}
	return ext
}
