// Package mangadex implements metadata.Provider against MangaDex's public
// REST API (api.mangadex.org). Every read this provider needs — search,
// series detail, and cover art — is anonymous; no API key or OAuth is
// involved. MangaDex is also the richest COVER source of the five
// providers in the Phase-1 engine (per-volume cover art, not just one
// thumbnail), which is why covers.go exposes a dedicated Covers gallery
// helper beyond the single metadata.Provider.GetSeriesCover.
package mangadex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/technobecet/tsundoku/internal/metadata"
)

const (
	// Key is this provider's stable identity (metadata.Provider.Key()).
	Key = "mangadex"
	// providerID is this provider's numeric registry id
	// (metadata.Provider.ID()). AniList=2, MAL=1, Kitsu=3, MangaUpdates=7
	// are already taken by sibling providers in the Phase-1 engine; 100 is
	// deliberately far outside that low range so a future provider slotted
	// between them can never collide with MangaDex by accident.
	providerID = 100
	// priority is this provider's merge-order weight
	// (metadata.Provider.Priority()) — lower runs earlier in a
	// primary-anchored gap-fill. MangaDex is public, fast, and reliable,
	// so it ranks just behind whatever the registry treats as primary.
	priority = 1

	apiBaseURL     = "https://api.mangadex.org"
	uploadsBaseURL = "https://uploads.mangadex.org"

	// minRequestGap enforces MangaDex's requested courtesy rate limit of
	// roughly 5 requests/second (1000ms / 5 = 200ms between calls).
	minRequestGap = 200 * time.Millisecond

	// searchLimit bounds how many candidates a Match search considers.
	searchLimit = 10
	// coverGalleryLimit bounds how many cover_art entries Covers fetches
	// per series — MangaDex allows up to 100 per page.
	coverGalleryLimit = 100
)

// Client implements metadata.Provider against the MangaDex REST API.
type Client struct {
	http    *http.Client
	limiter *rateLimiter
}

// New builds a MangaDex Client. A nil httpClient gets a default
// *http.Client{} (MangaDex sets no unusual timeout requirements beyond
// what the caller's context.Context already governs).
func New(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &Client{
		http:    httpClient,
		limiter: &rateLimiter{minGap: minRequestGap},
	}
}

// compile-time assertion that Client satisfies metadata.Provider.
var _ metadata.Provider = (*Client)(nil)

// Key returns this provider's stable identity.
func (c *Client) Key() string { return Key }

// ID returns this provider's numeric registry id.
func (c *Client) ID() int { return providerID }

// Priority returns this provider's merge-order weight.
func (c *Client) Priority() int { return priority }

// rateLimiter enforces a minimum gap between successive outbound requests
// — MangaDex's documented courtesy rate limit (~5 req/s). It is
// deliberately a plain mutex + sleep, not a token-bucket package: this
// package must not gain a shared internal/metadata/httpx dependency (a
// sibling slice owns that), and one client rarely issues bursts large
// enough to need more than this.
type rateLimiter struct {
	mu       sync.Mutex
	lastCall time.Time
	minGap   time.Duration
}

// wait blocks the calling goroutine, if needed, so at least minGap has
// elapsed since the previous call returned.
func (r *rateLimiter) wait() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if elapsed := time.Since(r.lastCall); elapsed < r.minGap {
		time.Sleep(r.minGap - elapsed)
	}
	r.lastCall = time.Now()
}

// doGet issues a rate-limited GET against reqURL and decodes a JSON body
// into out. Any non-200 response is reported as an error carrying the
// status and the URL for diagnosability.
func (c *Client) doGet(ctx context.Context, reqURL string, out any) error {
	c.limiter.wait()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return fmt.Errorf("mangadex: build request %s: %w", reqURL, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("mangadex: request %s: %w", reqURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("mangadex: %s returned %s", reqURL, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("mangadex: decode %s: %w", reqURL, err)
	}
	return nil
}

// doGetBytes issues a rate-limited GET against reqURL and returns the raw
// response body — used for cover-image downloads, which are not JSON.
func (c *Client) doGetBytes(ctx context.Context, reqURL string) ([]byte, error) {
	c.limiter.wait()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("mangadex: build request %s: %w", reqURL, err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mangadex: request %s: %w", reqURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mangadex: %s returned %s", reqURL, resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("mangadex: read body %s: %w", reqURL, err)
	}
	return data, nil
}

// Search returns up to limit MangaDex manga matching the free-text query
// q. It requests the cover_art relationship expanded (beyond the bare
// `GET /manga?title=` the search endpoint needs) so SearchResult.CoverURL
// — part of the shared metadata.SearchResult contract — is populated
// without a second round-trip per hit.
func (c *Client) Search(ctx context.Context, q string, limit int) ([]metadata.SearchResult, error) {
	if limit <= 0 {
		limit = searchLimit
	}

	reqURL := apiBaseURL + "/manga?" + url.Values{
		"title":      {q},
		"limit":      {strconv.Itoa(limit)},
		"includes[]": {"cover_art"},
	}.Encode()

	var page mangaCollectionResponse
	if err := c.doGet(ctx, reqURL, &page); err != nil {
		return nil, err
	}

	out := make([]metadata.SearchResult, 0, len(page.Data))
	for _, d := range page.Data {
		out = append(out, toSearchResult(d))
	}
	return out, nil
}

// GetSeriesMetadata fetches the full metadata record for remoteID,
// requesting author/artist/cover_art expanded so the mapper can populate
// Authors and CoverURL in one call.
func (c *Client) GetSeriesMetadata(ctx context.Context, remoteID string) (metadata.SeriesMetadata, error) {
	reqURL := apiBaseURL + "/manga/" + url.PathEscape(remoteID) + "?" + url.Values{
		"includes[]": {"author", "artist", "cover_art"},
	}.Encode()

	var entity mangaEntityResponse
	if err := c.doGet(ctx, reqURL, &entity); err != nil {
		return metadata.SeriesMetadata{}, err
	}
	return toSeriesMetadata(remoteID, entity.Data), nil
}

// GetSeriesCover fetches the raw bytes of remoteID's primary cover (the
// cover_art relationship MangaDex reports on the manga resource) at the
// .512.jpg thumbnail size. ext is always "jpg": MangaDex re-encodes every
// size variant to JPEG regardless of the original upload's format.
func (c *Client) GetSeriesCover(ctx context.Context, remoteID string) (data []byte, ext string, err error) {
	meta, err := c.GetSeriesMetadata(ctx, remoteID)
	if err != nil {
		return nil, "", err
	}
	if meta.CoverURL == "" {
		return nil, "", fmt.Errorf("mangadex: series %s has no cover", remoteID)
	}
	data, err = c.doGetBytes(ctx, meta.CoverURL)
	if err != nil {
		return nil, "", err
	}
	return data, "jpg", nil
}

// Match finds MangaDex's best confident match for q: it searches on
// q.Title, then scores every hit's Title against the full query (Title +
// AltTitles) via metadata.NameSimilarity, keeping the best-scoring result.
// Returns nil when no candidate clears metadata.MatchNone.
func (c *Client) Match(ctx context.Context, q metadata.MatchQuery) (*metadata.SearchResult, error) {
	results, err := c.Search(ctx, q.Title, searchLimit)
	if err != nil {
		return nil, err
	}

	var best *metadata.SearchResult
	bestScore := metadata.MatchNone
	for i := range results {
		score := metadata.NameSimilarity(q, results[i].Title)
		if score > bestScore {
			bestScore = score
			best = &results[i]
		}
	}
	if bestScore == metadata.MatchNone {
		return nil, nil
	}
	return best, nil
}
