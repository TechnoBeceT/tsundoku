// Package kitsu implements metadata.Provider against Kitsu's public
// JSON:API (kitsu.io/api/edge). Every read this provider needs — search,
// series detail (plus categories, which double as genres — see mapper.go's
// doc comment), and cover image bytes — is anonymous; no API key or OAuth
// is involved (only Kitsu's tracker-SYNC half, out of scope here, needs
// per-user OAuth; see brief/komf-metadata-engine-reference).
//
// Kitsu has NO Komf reference (unlike anilist/mangadex, which port Komf's
// existing provider) — this package was BUILT FRESH. Its shapes were
// confirmed live against the real API before this file was written (see
// shape_test.go / TestShapeKitsu; the captured responses are checked into
// testdata/ and drive mapper_test.go offline).
package kitsu

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
	"sync"
	"time"

	"github.com/technobecet/tsundoku/internal/metadata"
)

const (
	// Key is this provider's stable identity (metadata.Provider.Key()).
	Key = "kitsu"
	// providerID is this provider's numeric registry id
	// (metadata.Provider.ID()), matching the tracker registry pinned in
	// ent/schema/trackerconnection.go (MAL=1, AniList=2, Kitsu=3,
	// MangaUpdates=7) — the same physical provider, one shared numbering.
	providerID = 3
	// priority is this provider's merge-order weight
	// (metadata.Provider.Priority()) — LOWER runs earlier in a
	// primary-anchored gap-fill (mirrors SeriesProvider.importance being
	// inverted for this package — see provider.go). AniList=0 and
	// MangaDex=1 already claim the top two slots (see their own doc
	// comments); Kitsu ranks behind both.
	priority = 4

	apiBaseURL = "https://kitsu.io/api/edge"

	// jsonAPIMediaType is the content type Kitsu's JSON:API documents for
	// the Accept header on every request.
	jsonAPIMediaType = "application/vnd.api+json"

	// minRequestGap is a courtesy throttle between successive outbound
	// requests. Kitsu documents no explicit rate limit (confirmed live —
	// TestShapeKitsu's real responses carry no RateLimit-* headers), but a
	// small fixed gap costs nothing and avoids hammering a public API, the
	// same courtesy internal/metadata/mangadex extends to its own
	// documented cap.
	minRequestGap = 200 * time.Millisecond

	// defaultSearchLimit is used when a caller passes a non-positive limit.
	defaultSearchLimit = 10
)

// Client implements metadata.Provider against the Kitsu JSON:API.
type Client struct {
	http    *http.Client
	limiter *rateLimiter
}

// New builds a Kitsu Client. A nil httpClient gets a default *http.Client{}
// (Kitsu sets no unusual timeout requirements beyond what the caller's
// context.Context already governs).
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

// rateLimiter enforces a minimum gap between successive outbound requests.
// Deliberately a plain mutex + sleep, mirroring internal/metadata/mangadex's
// own rateLimiter, rather than sharing internal/metadata/httpx's
// token-bucket transport — that package backs the providers with a
// documented per-minute cap (e.g. anilist's GraphQL endpoint); Kitsu
// documents no such cap, so a simple self-contained courtesy gap is enough.
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

// doGet issues a rate-limited GET against reqURL and decodes a JSON:API
// body into out. Any non-200 response is reported as an error carrying the
// status and the URL for diagnosability.
func (c *Client) doGet(ctx context.Context, reqURL string, out any) error {
	c.limiter.wait()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		// Defensive path: reachable only with a nil context, which every
		// caller here always supplies a real one for; unreachable in
		// practice.
		return fmt.Errorf("kitsu: build request %s: %w", reqURL, err)
	}
	req.Header.Set("Accept", jsonAPIMediaType)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("kitsu: request %s: %w", reqURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("kitsu: %s returned %s", reqURL, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("kitsu: decode %s: %w", reqURL, err)
	}
	return nil
}

// doGetBytes issues a rate-limited GET against reqURL and returns the raw
// response body — used for cover-image downloads, which are not JSON.
func (c *Client) doGetBytes(ctx context.Context, reqURL string) ([]byte, error) {
	c.limiter.wait()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("kitsu: build request %s: %w", reqURL, err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kitsu: request %s: %w", reqURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kitsu: %s returned %s", reqURL, resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("kitsu: read body %s: %w", reqURL, err)
	}
	return data, nil
}

// Search returns up to limit Kitsu manga matching the free-text query q,
// via `GET /manga?filter[text]=&page[limit]=`. A non-positive limit falls
// back to defaultSearchLimit. Search never requests `?include=categories`
// (that is a details-only concern — see GetSeriesMetadata), so its results
// carry no genre data; metadata.SearchResult has no field for it anyway.
func (c *Client) Search(ctx context.Context, q string, limit int) ([]metadata.SearchResult, error) {
	if limit <= 0 {
		limit = defaultSearchLimit
	}

	reqURL := apiBaseURL + "/manga?" + url.Values{
		"filter[text]": {q},
		"page[limit]":  {strconv.Itoa(limit)},
	}.Encode()

	var page mangaCollectionResponse
	if err := c.doGet(ctx, reqURL, &page); err != nil {
		return nil, err
	}

	out := make([]metadata.SearchResult, len(page.Data))
	for i, d := range page.Data {
		out[i] = toSearchResult(d)
	}
	return out, nil
}

// GetSeriesMetadata fetches the full metadata record for one Kitsu manga
// id, via `GET /manga/{id}?include=categories`. Categories is a JSON:API
// RELATIONSHIP: the manga resource's `relationships.categories.data[]`
// carries only bare {type,id} refs, and the include param makes the actual
// category resources (each carrying `attributes.title`) arrive in the
// response's top-level `included[]` array — resolveGenres (mapper.go)
// joins the two.
func (c *Client) GetSeriesMetadata(ctx context.Context, remoteID string) (metadata.SeriesMetadata, error) {
	reqURL := apiBaseURL + "/manga/" + url.PathEscape(remoteID) + "?" + url.Values{
		"include": {"categories"},
	}.Encode()

	var entity mangaEntityResponse
	if err := c.doGet(ctx, reqURL, &entity); err != nil {
		return metadata.SeriesMetadata{}, err
	}
	return toSeriesMetadata(entity), nil
}

// GetSeriesCover fetches the raw bytes of remoteID's poster image at its
// "original" size (`attributes.posterImage.original` — the
// highest-resolution variant Kitsu serves; there is no separate "cover"
// API the way AniList/MangaDex need one, mirroring anilist.Client's own
// GetSeriesCover shape). ext is derived from the URL, defaulting to "jpg"
// when the path carries none.
func (c *Client) GetSeriesCover(ctx context.Context, remoteID string) (data []byte, ext string, err error) {
	meta, err := c.GetSeriesMetadata(ctx, remoteID)
	if err != nil {
		return nil, "", err
	}
	if meta.CoverURL == "" {
		return nil, "", fmt.Errorf("kitsu: series %s has no cover", remoteID)
	}
	data, err = c.doGetBytes(ctx, meta.CoverURL)
	if err != nil {
		return nil, "", err
	}
	return data, extFromURL(meta.CoverURL), nil
}

// Match finds Kitsu's best confident match for q: it searches on q.Title,
// then scores every hit's Title against the full query (Title + AltTitles)
// via metadata.NameSimilarity, keeping the best-scoring result. Returns nil
// when no candidate clears metadata.MatchNone.
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

// extFromURL returns the bare (no leading dot) file extension from a URL's
// path component, defaulting to "jpg" when the path carries none — every
// Kitsu poster URL observed live ends in an image extension, but the
// fallback keeps GetSeriesCover from returning an empty ext on a future
// extension-less CDN path.
func extFromURL(rawURL string) string {
	ext := strings.TrimPrefix(path.Ext(rawURL), ".")
	if ext == "" {
		return "jpg"
	}
	return ext
}
