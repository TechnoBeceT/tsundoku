// Package mal implements metadata.Provider against MyAnimeList's official
// REST API (https://api.myanimelist.net/v2). Unlike AniList/MangaDex, MAL
// requires a registered application identity on EVERY request — but it is a
// server-level app credential (an `X-MAL-CLIENT-ID` header), NOT per-user
// OAuth: per brief/komf-metadata-engine-reference, OAuth is only needed for
// MAL's tracker-SYNC half (out of scope here). The client-id is
// CONFIG-INJECTED by the caller (New's clientID param) — this package never
// reads it from the environment or hardcodes it (internal/config stays the
// sole env boundary).
package mal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/technobecet/tsundoku/internal/metadata"
	"github.com/technobecet/tsundoku/internal/metadata/httpx"
)

const (
	// Key is this provider's stable identity (metadata.Provider.Key()).
	Key = "mal"
	// providerID is this provider's numeric registry id
	// (metadata.Provider.ID()). Matches the tracker registry pinned in
	// ent/schema/trackerconnection.go (MAL=1, AniList=2, Kitsu=3,
	// MangaUpdates=7) — the same physical provider, one shared numbering
	// (see internal/metadata/anilist/client.go's identical note).
	providerID = 1
	// priority is this provider's merge-order weight
	// (metadata.Provider.Priority()) — LOWER runs earlier in a
	// primary-anchored gap-fill (AniList=0, MangaDex=1). MAL sits behind
	// both: it requires an app credential to reach at all and its field set
	// is the thinnest of the three (no tags, no links), so it is the
	// weakest default anchor.
	priority = 3

	apiBaseURL = "https://api.myanimelist.net/v2"

	// clientIDHeader is the header MAL's v2 API requires on every request in
	// lieu of per-user OAuth for read-only endpoints.
	clientIDHeader = "X-MAL-CLIENT-ID"

	// maxQueryLen is the longest query string MAL's search endpoint accepts;
	// longer input is truncated (rune-safe) rather than rejected, so a
	// caller-supplied long title still returns a best-effort result set.
	maxQueryLen = 64

	// defaultSearchLimit is used when a caller passes a non-positive limit.
	defaultSearchLimit = 10

	// httpTimeout bounds a single MAL request; the shared rate limiter (not
	// this timeout) is what keeps request PACE under a courteous cap.
	httpTimeout = 30 * time.Second

	// requestsPerMinute is a conservative courtesy cap: MAL's v2 API
	// publishes no documented per-minute limit (unlike AniList's 90/min),
	// so this mirrors the same shared httpx.NewRateLimited transport
	// AniList uses at a deliberately cautious ~1 req/sec to avoid tripping
	// any undocumented abuse threshold.
	requestsPerMinute = 60

	// searchFields / detailFields are MAL's `fields=` selections — see this
	// package's doc comment for the field lists this provider was
	// contracted to request.
	searchFields = "id,title,main_picture,start_date"
	detailFields = "id,title,synopsis,num_chapters,mean,main_picture,status,media_type,start_date,genres,authors{first_name,last_name},alternative_titles"
)

// Client implements metadata.Provider against the MyAnimeList v2 REST API.
type Client struct {
	http     *http.Client
	clientID string
}

// compile-time assertion that Client satisfies metadata.Provider.
var _ metadata.Provider = (*Client)(nil)

// New builds a mal Client. clientID is the MAL app's registered client id
// (config-injected by the caller — see the package doc comment; NEVER read
// from the environment or hardcoded here). A nil httpClient gets a default
// *http.Client whose Transport is httpx.NewRateLimited(nil,
// requestsPerMinute), mirroring anilist.New's shared-throttle default.
func New(clientID string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout:   httpTimeout,
			Transport: httpx.NewRateLimited(nil, requestsPerMinute),
		}
	}
	return &Client{http: httpClient, clientID: clientID}
}

// Key returns this provider's stable string identity.
func (c *Client) Key() string { return Key }

// ID returns this provider's numeric registry id.
func (c *Client) ID() int { return providerID }

// Priority returns this provider's default merge-order rank (lower = higher
// priority / earlier in Merge's gap-fill order).
func (c *Client) Priority() int { return priority }

// Search returns up to limit MAL manga matches for a free-text query. A
// non-positive limit falls back to defaultSearchLimit; q longer than
// maxQueryLen runes is truncated (MAL's search endpoint rejects overlong
// queries outright).
func (c *Client) Search(ctx context.Context, q string, limit int) ([]metadata.SearchResult, error) {
	if limit <= 0 {
		limit = defaultSearchLimit
	}

	reqURL := apiBaseURL + "/manga?" + url.Values{
		"q":      {truncateQuery(q)},
		"nsfw":   {"true"},
		"limit":  {strconv.Itoa(limit)},
		"fields": {searchFields},
	}.Encode()

	var page mangaListResponse
	if err := c.doGet(ctx, reqURL, &page); err != nil {
		return nil, err
	}

	out := make([]metadata.SearchResult, len(page.Data))
	for i, d := range page.Data {
		out[i] = toSearchResult(d.Node)
	}
	return out, nil
}

// GetSeriesMetadata fetches the full metadata record for one MAL manga id
// (remoteID is the decimal MAL manga id, e.g. from a prior Search).
func (c *Client) GetSeriesMetadata(ctx context.Context, remoteID string) (metadata.SeriesMetadata, error) {
	reqURL := apiBaseURL + "/manga/" + url.PathEscape(remoteID) + "?" + url.Values{
		"fields": {detailFields},
	}.Encode()

	var detail mangaDetail
	if err := c.doGet(ctx, reqURL, &detail); err != nil {
		return metadata.SeriesMetadata{}, err
	}
	return toSeriesMetadata(detail), nil
}

// GetSeriesCover fetches the raw cover image bytes at main_picture.large,
// plus a bare file extension (no leading dot) derived from the URL. It does
// one metadata lookup (to resolve the current cover URL) then one plain GET
// of that URL — mirrors anilist.Client.GetSeriesCover / mangadex.Client.
// GetSeriesCover, since MAL has no separate "cover" endpoint either.
func (c *Client) GetSeriesCover(ctx context.Context, remoteID string) ([]byte, string, error) {
	meta, err := c.GetSeriesMetadata(ctx, remoteID)
	if err != nil {
		return nil, "", err
	}
	if meta.CoverURL == "" {
		return nil, "", fmt.Errorf("mal: no cover image for id %s", remoteID)
	}
	return c.fetchImage(ctx, meta.CoverURL)
}

// Match searches MAL for q.Title and returns the best NameSimilarity match
// across the results (comparing each result's title against every one of
// q's titles), or nil when no result clears MatchNone.
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

// truncateQuery rune-truncates q to at most maxQueryLen characters — MAL's
// search endpoint rejects an overlong q outright, so this keeps Search
// usable for a caller passing a long title rather than erroring.
func truncateQuery(q string) string {
	r := []rune(q)
	if len(r) <= maxQueryLen {
		return q
	}
	return string(r[:maxQueryLen])
}

// doGet issues a GET against reqURL carrying the required X-MAL-CLIENT-ID
// header and decodes a JSON body into out. Any non-200 response is reported
// as an error carrying the status and body for diagnosability.
func (c *Client) doGet(ctx context.Context, reqURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		// Defensive path: reqURL is always built from a valid base +
		// url.Values here; unreachable in practice.
		return fmt.Errorf("mal: build request: %w", err)
	}
	req.Header.Set(clientIDHeader, c.clientID)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("mal: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mal: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("mal: decode response: %w", err)
	}
	return nil
}

// fetchImage GETs url (no client-id header needed — MAL's CDN serves cover
// images publicly) and returns its raw bytes plus a bare file extension (no
// leading dot, "jpg" when the URL carries none).
func (c *Client) fetchImage(ctx context.Context, imgURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imgURL, nil)
	if err != nil {
		// Defensive path: reachable only with a nil context, which
		// GetSeriesCover always supplies a real one for; unreachable in
		// practice.
		return nil, "", fmt.Errorf("mal: build cover request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("mal: fetch cover: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("mal: cover HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("mal: read cover body: %w", err)
	}
	return data, extFromURL(imgURL), nil
}

// extFromURL returns the bare (no leading dot) file extension from a URL's
// path component, defaulting to "jpg" when the path carries none.
func extFromURL(rawURL string) string {
	ext := strings.TrimPrefix(path.Ext(rawURL), ".")
	if ext == "" {
		return "jpg"
	}
	return ext
}
