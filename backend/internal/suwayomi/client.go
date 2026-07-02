// Package suwayomi — typed HTTP client for the embedded Suwayomi-Server.
//
// This file (client.go) is the only surface the rest of the application uses
// to talk to Suwayomi. It is split across one concern per file per the
// engineering standard: the Client interface + DTO types live here; the concrete
// httpClient methods follow directly.
//
// API split (Suwayomi v2.2.2100):
//   - GraphQL (POST /api/graphql): sources, search (fetchSourceManga mutation),
//     chapters (chapters query with mangaId filter), chapter page URL list
//     (fetchChapterPages mutation).
//   - REST GET (absolute URL): raw page-image bytes.
//
// Source IDs are strings in the GraphQL SourceType (they are 64-bit integers
// serialised as strings in the Kotlin backend to avoid JS number overflow).
// Manga IDs and Chapter IDs are Go int (Kotlin Int = 32-bit signed).
package suwayomi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/technobecet/tsundoku/internal/config"
)

// --- DTO types ---------------------------------------------------------------

// Source represents a Suwayomi content source (a Tachiyomi/Suwayomi extension).
// ID is a string because Suwayomi serialises source IDs as 64-bit integers that
// exceed JS's safe integer range; the GraphQL layer returns them as strings.
type Source struct {
	// ID is the source's unique string identifier (a 64-bit integer as string).
	ID string
	// Name is the human-readable source name (e.g. "MangaDex").
	Name string
	// Lang is the BCP-47 language tag for the source (e.g. "en", "ja").
	Lang string
}

// LocalSourceID is Suwayomi's built-in "Local source" identifier. It is
// always present in the Sources() list, indexes files from Suwayomi's own
// on-disk localSourcePath rather than a real online content provider, and
// carries the fixed lang tag "localsourcelang" (see LocalSourceLang). Tsundoku
// never treats it as a real content source — see imports.Service.Sources,
// which filters it out of the Discover/Search source lists.
const LocalSourceID = "0"

// LocalSourceLang is the fixed BCP-47-shaped lang tag Suwayomi assigns to its
// built-in Local source. It is a more stable exclusion signal than
// LocalSourceID: if Suwayomi ever changes the id, the lang tag is what would
// otherwise leak into the UI as the literal string "LOCALSOURCELANG".
const LocalSourceLang = "localsourcelang"

// Manga is a search result or library entry from Suwayomi.
// ID is an int (Kotlin Int, 32-bit); ThumbnailURL, Author, Artist, and
// Description are nil when the source does not provide them; Genre is nil/empty
// under the same condition. These MangaType fields are read-only metadata —
// Suwayomi never contacts the upstream source again for them once cached.
type Manga struct {
	// ID is the Suwayomi-internal manga identifier.
	ID int
	// Title is the manga's display title.
	Title string
	// URL is the provider-canonical URL for this manga.
	URL string
	// ThumbnailURL is the cover image URL; nil if not available.
	ThumbnailURL *string
	// Author is the manga's writing credit; nil when the source omits it.
	Author *string
	// Artist is the manga's art credit; nil when the source omits it (some
	// sources set Author == Artist, or omit Artist entirely for a single-credit
	// work — callers decide how to display that, this struct just carries what
	// Suwayomi reports).
	Artist *string
	// Description is the synopsis/summary text; nil when the source omits it.
	Description *string
	// Genre is the source's genre/tag list; nil or empty when not provided.
	Genre []string
}

// Chapter represents a single chapter entry from Suwayomi.
// Number and UploadDate are pointers because Suwayomi may omit them (null).
// Index maps to Suwayomi's sourceOrder field — the source-assigned reading order.
type Chapter struct {
	// ID is the Suwayomi-internal chapter identifier.
	ID int
	// Index is the source-assigned reading order (Suwayomi: sourceOrder).
	Index int
	// Name is the chapter display name (e.g. "Chapter 1").
	Name string
	// Number is the parsed chapter number (e.g. 1.5); nil if not available.
	Number *float64
	// URL is the provider-canonical URL for this chapter.
	URL string
	// UploadDate is the chapter publication date; nil if not available.
	UploadDate *time.Time
	// PageCount is the number of pages in this chapter.
	PageCount int
}

// --- Client interface --------------------------------------------------------

// Client is the typed interface that all Suwayomi operations flow through.
// The concrete implementation is unexported (httpClient); callers hold a Client.
//
// Method overview:
//   - Sources: list installed Suwayomi extensions/sources.
//   - Search: search a source for manga by query string.
//   - Browse: list a source's Popular/Latest catalog page (no query).
//   - FetchChapters: trigger a live chapter-list fetch from the source and return results.
//   - MangaChapters: list already-cached chapters for a manga (read-only; no source fetch).
//   - ChapterPages: trigger page-fetch and return the ordered page URLs.
//   - PageBytes: download a single page image and detect its file type.
type Client interface {
	// Sources returns all installed Suwayomi sources.
	Sources(ctx context.Context) ([]Source, error)

	// Search searches sourceID for manga matching query and returns the first
	// page of results.
	Search(ctx context.Context, sourceID, query string) ([]Manga, error)

	// Browse fetches one page of a source's catalog listing (Popular or Latest)
	// via the fetchSourceManga mutation with type=POPULAR|LATEST. Unlike Search
	// it carries no query and returns a BrowseResult that includes hasNextPage
	// for pagination. page is 1-based.
	Browse(ctx context.Context, sourceID string, t BrowseType, page int) (BrowseResult, error)

	// FetchChapters triggers the Suwayomi fetchChapters mutation, which fetches
	// the live chapter list from the source and populates Suwayomi's internal
	// cache. It returns the full chapter list for the manga. Call this when
	// ingesting a manga for the first time or to refresh the chapter list.
	//
	// Shape validated against Suwayomi v2.2.2100 (Task 7): mutation input
	// field is `mangaId: Int!`; result field is `chapters`.
	FetchChapters(ctx context.Context, mangaID int) ([]Chapter, error)

	// MangaChapters returns already-cached chapters for the given manga ID
	// using the chapters(filter:{mangaId:{equalTo:N}}) query. It does NOT
	// contact the upstream source; call FetchChapters first if the manga was
	// just added via Search.
	//
	// Shape validated against Suwayomi v2.2.2100 (Task 7): filter operator
	// `equalTo` is correct for the mangaId field.
	MangaChapters(ctx context.Context, mangaID int) ([]Chapter, error)

	// ChapterPages triggers a Suwayomi page-fetch for chapterID and returns
	// the ordered list of page-image URLs. The URLs are relative paths on the
	// Suwayomi server (e.g. /api/v1/manga/7/chapter/3/page/0).
	//
	// Shape validated against Suwayomi v2.2.2100 (Task 7): page URLs are
	// relative paths, not absolute URLs.
	ChapterPages(ctx context.Context, chapterID int) ([]string, error)

	// MangaMeta returns the stored metadata for the given mangaID using the
	// manga(id: $id) query. It does NOT contact the upstream source; the manga
	// must already exist in Suwayomi's library (added via Search/AddSeries).
	// Returns the full Manga struct including ThumbnailURL (nil when absent).
	MangaMeta(ctx context.Context, mangaID int) (Manga, error)

	// PageBytes downloads the image at pageURL (an absolute URL) and returns
	// the raw bytes and bare file extension (e.g. "jpg", "png") without a
	// leading dot. Extension detection uses http.DetectContentType on the first
	// 512 bytes of the response body.
	PageBytes(ctx context.Context, pageURL string) (data []byte, ext string, err error)

	// ServerSettings returns the FlareSolverr + SOCKS-proxy subset of Suwayomi's
	// server-global settings via the settings GraphQL query. See settings.go.
	ServerSettings(ctx context.Context) (SuwayomiSettings, error)

	// SetServerSettings applies a PARTIAL update of the FlareSolverr + SOCKS
	// subset via the setSettings mutation: only the patch's non-nil fields are
	// sent, so unset fields are never clobbered. See settings.go.
	SetServerSettings(ctx context.Context, patch SuwayomiSettingsPatch) error

	// Extensions lists every Suwayomi extension (installed + available) via the
	// extensions query. Identity is pkgName; the install/nsfw/obsolete flags use
	// the isInstalled/isNsfw/isObsolete casing. See extensions.go.
	Extensions(ctx context.Context) ([]Extension, error)

	// SetExtensionState installs / updates / uninstalls the extension identified
	// by pkgName via the updateExtension mutation (action selects the single
	// patch boolean). Re-read via Extensions for the authoritative state. See
	// extensions.go.
	SetExtensionState(ctx context.Context, pkgName string, action ExtensionAction) error

	// FetchExtensions refreshes the available-extensions list from the configured
	// repos ("check for updates") via the fetchExtensions mutation and returns the
	// refreshed list. See extensions.go.
	FetchExtensions(ctx context.Context) ([]Extension, error)

	// ExtensionRepos reads the configured extension repo URL list (a SettingsType
	// field). See extensions.go.
	ExtensionRepos(ctx context.Context) ([]string, error)

	// SetExtensionRepos REPLACES the extension repo URL list via a partial
	// setSettings mutation carrying only extensionRepos (no other setting is
	// clobbered). An empty slice clears all repos. See extensions.go.
	SetExtensionRepos(ctx context.Context, repos []string) error
}

// --- Constructor -------------------------------------------------------------

// NewClient constructs a Client backed by cfg and httpc.
// httpc is the caller's *http.Client (allows test injection via httptest.Server).
// cfg.BaseURL() provides the base URL for all API requests.
func NewClient(cfg config.SuwayomiConfig, httpc *http.Client) Client {
	return &httpClient{
		baseURL: cfg.BaseURL(),
		http:    httpc,
	}
}

// --- Concrete implementation -------------------------------------------------

// httpClient is the unexported concrete type implementing Client.
type httpClient struct {
	baseURL string
	http    *http.Client
}

// --- GraphQL helper ----------------------------------------------------------

// gqlRequest is the JSON body sent to /api/graphql.
type gqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

// gqlResponse is the envelope returned by /api/graphql.
// errors is checked before data is decoded; a non-empty errors array is always
// surfaced as a Go error — never silently dropped.
type gqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// doGraphQL POSTs a GraphQL request to /api/graphql and decodes the data field
// into out. If the response errors array is non-empty, it returns an error
// containing each error message.
func (c *httpClient) doGraphQL(ctx context.Context, query string, vars map[string]any, out any) error {
	body, err := json.Marshal(gqlRequest{Query: query, Variables: vars})
	if err != nil {
		// Defensive path: gqlRequest contains only string/map[string]any which
		// json.Marshal never fails on; unreachable in practice.
		return fmt.Errorf("suwayomi: marshal GraphQL request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/graphql", bytes.NewReader(body))
	if err != nil {
		// Defensive path: reachable only with a malformed BaseURL (caught at
		// config validate time) or a nil context; unreachable in normal operation.
		return fmt.Errorf("suwayomi: build GraphQL request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("suwayomi: GraphQL request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("suwayomi: GraphQL HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var envelope gqlResponse
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("suwayomi: decode GraphQL envelope: %w", err)
	}

	// Surface ALL GraphQL application errors — never silently swallow them.
	if len(envelope.Errors) > 0 {
		msgs := make([]string, len(envelope.Errors))
		for i, e := range envelope.Errors {
			msgs[i] = e.Message
		}
		return fmt.Errorf("suwayomi: GraphQL errors: %s", strings.Join(msgs, "; "))
	}

	// out == nil is valid when the caller only needs error/success (no data).
	if out != nil {
		if err := json.Unmarshal(envelope.Data, out); err != nil {
			return fmt.Errorf("suwayomi: decode GraphQL data: %w", err)
		}
	}
	return nil
}

// --- Sources -----------------------------------------------------------------

// gqlSourcesData is the typed shape of the `data` field for the sources query.
type gqlSourcesData struct {
	Sources struct {
		Nodes []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Lang string `json:"lang"`
		} `json:"nodes"`
	} `json:"sources"`
}

const sourcesQuery = `
query {
  sources {
    nodes {
      id
      name
      lang
    }
  }
}`

// Sources returns all installed Suwayomi sources via GraphQL.
func (c *httpClient) Sources(ctx context.Context) ([]Source, error) {
	var data gqlSourcesData
	if err := c.doGraphQL(ctx, sourcesQuery, nil, &data); err != nil {
		return nil, err
	}
	nodes := data.Sources.Nodes
	out := make([]Source, len(nodes))
	for i, n := range nodes {
		out[i] = Source{ID: n.ID, Name: n.Name, Lang: n.Lang}
	}
	return out, nil
}

// --- Search ------------------------------------------------------------------

// gqlMangaNode is the common JSON shape for a manga node returned by the
// fetchSourceManga mutation (Search/Browse) and the manga(id) query
// (MangaMeta). All three operations select the identical field set, so this
// shared type avoids duplicating the struct and the Manga-conversion logic
// (§2 DRY — a dropped field here would surface in all three callers at once).
type gqlMangaNode struct {
	ID           int      `json:"id"`
	Title        string   `json:"title"`
	URL          string   `json:"url"`
	ThumbnailURL *string  `json:"thumbnailUrl"`
	Author       *string  `json:"author"`
	Artist       *string  `json:"artist"`
	Genre        []string `json:"genre"`
	Description  *string  `json:"description"`
}

// toManga converts a gqlMangaNode to the public Manga type.
func (n gqlMangaNode) toManga() Manga {
	return Manga{
		ID:           n.ID,
		Title:        n.Title,
		URL:          n.URL,
		ThumbnailURL: n.ThumbnailURL,
		Author:       n.Author,
		Artist:       n.Artist,
		Genre:        n.Genre,
		Description:  n.Description,
	}
}

// mangaFieldSelection is the GraphQL field set shared by every operation that
// returns manga metadata (Search, Browse, MangaMeta). author/artist/genre/
// description are Suwayomi MangaType fields (confirmed against the Kaizoku.GO
// reference client and pending live re-confirmation via TestShape4 — see
// e2e_test.go); a wrong field name here surfaces as a GraphQL validation error
// from doGraphQL, not a silent decode-to-zero-value.
const mangaFieldSelection = `
      id
      title
      url
      thumbnailUrl
      author
      artist
      genre
      description`

// gqlSearchData is the typed shape of the `data` field for the fetchSourceManga mutation.
type gqlSearchData struct {
	FetchSourceManga struct {
		Mangas []gqlMangaNode `json:"mangas"`
	} `json:"fetchSourceManga"`
}

// Shape validation (Task 7, live): the input field is `source` (not `sourceId`).
// Confirmed against Suwayomi v2.2.2100 by introspecting FetchSourceMangaInput.
const searchMutation = `
mutation SearchSource($sourceId: LongString!, $query: String!, $page: Int!) {
  fetchSourceManga(input: {
    source: $sourceId
    type: SEARCH
    query: $query
    page: $page
  }) {
    mangas {` + mangaFieldSelection + `
    }
  }
}`

// Search calls the fetchSourceManga GraphQL mutation with type=SEARCH.
func (c *httpClient) Search(ctx context.Context, sourceID, query string) ([]Manga, error) {
	vars := map[string]any{
		"sourceId": sourceID,
		"query":    query,
		"page":     1,
	}
	var data gqlSearchData
	if err := c.doGraphQL(ctx, searchMutation, vars, &data); err != nil {
		return nil, err
	}
	nodes := data.FetchSourceManga.Mangas
	out := make([]Manga, len(nodes))
	for i, n := range nodes {
		out[i] = n.toManga()
	}
	return out, nil
}

// --- Browse ------------------------------------------------------------------

// BrowseType selects which of a source's catalog listings to fetch.
type BrowseType string

const (
	// BrowsePopular fetches a source's most-popular listing.
	BrowsePopular BrowseType = "POPULAR"
	// BrowseLatest fetches a source's latest-updated listing.
	BrowseLatest BrowseType = "LATEST"
)

// BrowseResult is one page of a source's catalog browse (Popular/Latest).
type BrowseResult struct {
	// Mangas holds the candidates on this page, in source order.
	Mangas []Manga
	// HasNextPage reports whether another page exists (drives FE pagination).
	HasNextPage bool
}

// gqlBrowseData is the typed shape of the `data` field for the browse
// fetchSourceManga mutation. It mirrors gqlSearchData but additionally selects
// hasNextPage so the caller can paginate.
type gqlBrowseData struct {
	FetchSourceManga struct {
		Mangas      []gqlMangaNode `json:"mangas"`
		HasNextPage bool           `json:"hasNextPage"`
	} `json:"fetchSourceManga"`
}

// browseMutation calls the same fetchSourceManga mutation as searchMutation but
// drives it by listing $type (POPULAR/LATEST) instead of a SEARCH query, and
// additionally selects hasNextPage for pagination. There is no $query variable —
// browse is a query-less catalog listing.
const browseMutation = `
mutation BrowseSource($sourceId: LongString!, $type: FetchSourceMangaType!, $page: Int!) {
  fetchSourceManga(input: {
    source: $sourceId
    type: $type
    page: $page
  }) {
    mangas {` + mangaFieldSelection + `
    }
    hasNextPage
  }
}`

// Browse calls the fetchSourceManga GraphQL mutation with type=POPULAR|LATEST to
// fetch one page of sourceID's catalog listing. Unlike Search it carries no query
// string and returns a BrowseResult that includes hasNextPage so callers can
// paginate. page is 1-based.
func (c *httpClient) Browse(ctx context.Context, sourceID string, t BrowseType, page int) (BrowseResult, error) {
	vars := map[string]any{
		"sourceId": sourceID,
		"type":     string(t),
		"page":     page,
	}
	var data gqlBrowseData
	if err := c.doGraphQL(ctx, browseMutation, vars, &data); err != nil {
		return BrowseResult{}, err
	}
	nodes := data.FetchSourceManga.Mangas
	out := make([]Manga, len(nodes))
	for i, n := range nodes {
		out[i] = n.toManga()
	}
	return BrowseResult{Mangas: out, HasNextPage: data.FetchSourceManga.HasNextPage}, nil
}

// --- Chapter conversion helper -----------------------------------------------

// gqlChapterNode is the common JSON shape for a chapter node returned by both
// the fetchChapters mutation and the chapters query. Both operations return
// the same set of fields; this shared type avoids duplicating the struct and
// the conversion loop.
//
// uploadDate is typed as LongString! in the Suwayomi GraphQL schema — the same
// custom scalar used for sourceId. Suwayomi serialises 64-bit integers as JSON
// strings ("1782184812670") to avoid JavaScript float precision loss. We receive
// it as *string and parse it in mapChapterNodes.
type gqlChapterNode struct {
	ID            int      `json:"id"`
	URL           string   `json:"url"`
	Name          string   `json:"name"`
	ChapterNumber *float64 `json:"chapterNumber"`
	UploadDate    *string  `json:"uploadDate"`
	PageCount     int      `json:"pageCount"`
	SourceOrder   int      `json:"sourceOrder"`
}

// mapChapterNodes converts a slice of gqlChapterNode to []Chapter.
// UploadDate arrives as a LongString (string-encoded milliseconds-since-epoch);
// a missing, zero, or unparseable value is treated as nil.
func mapChapterNodes(nodes []gqlChapterNode) []Chapter {
	out := make([]Chapter, len(nodes))
	for i, n := range nodes {
		var uploadDate *time.Time
		if n.UploadDate != nil && *n.UploadDate != "" && *n.UploadDate != "0" {
			if ms, err := strconv.ParseInt(*n.UploadDate, 10, 64); err == nil && ms != 0 {
				t := time.UnixMilli(ms).UTC()
				uploadDate = &t
			}
		}
		out[i] = Chapter{
			ID:         n.ID,
			Index:      n.SourceOrder,
			Name:       n.Name,
			Number:     n.ChapterNumber,
			URL:        n.URL,
			UploadDate: uploadDate,
			PageCount:  n.PageCount,
		}
	}
	return out
}

// --- FetchChapters -----------------------------------------------------------

// gqlFetchChaptersData is the typed shape of the `data` field for fetchChapters.
type gqlFetchChaptersData struct {
	FetchChapters struct {
		Chapters []gqlChapterNode `json:"chapters"`
	} `json:"fetchChapters"`
}

const fetchChaptersMutation = `
mutation FetchChapters($mangaId: Int!) {
  fetchChapters(input: { mangaId: $mangaId }) {
    chapters {
      id
      url
      name
      chapterNumber
      uploadDate
      pageCount
      sourceOrder
    }
  }
}`

// FetchChapters calls the Suwayomi fetchChapters mutation to trigger a live
// chapter-list refresh from the upstream source and returns the results.
// Use this when ingesting a manga for the first time; for read-only queries
// on already-cached data use MangaChapters.
func (c *httpClient) FetchChapters(ctx context.Context, mangaID int) ([]Chapter, error) {
	vars := map[string]any{"mangaId": mangaID}
	var data gqlFetchChaptersData
	if err := c.doGraphQL(ctx, fetchChaptersMutation, vars, &data); err != nil {
		return nil, err
	}
	return mapChapterNodes(data.FetchChapters.Chapters), nil
}

// --- MangaChapters -----------------------------------------------------------

// gqlChaptersData is the typed shape of the `data` field for the chapters query.
type gqlChaptersData struct {
	Chapters struct {
		Nodes []gqlChapterNode `json:"nodes"`
	} `json:"chapters"`
}

const chaptersQuery = `
query MangaChapters($mangaId: Int!) {
  chapters(filter: { mangaId: { equalTo: $mangaId } }) {
    nodes {
      id
      url
      name
      chapterNumber
      uploadDate
      pageCount
      sourceOrder
    }
  }
}`

// MangaChapters returns already-cached chapters for the given manga ID.
// UploadDate is stored as milliseconds-since-epoch in Suwayomi; it is converted
// to *time.Time (UTC) for callers. A zero uploadDate is treated as nil.
func (c *httpClient) MangaChapters(ctx context.Context, mangaID int) ([]Chapter, error) {
	vars := map[string]any{"mangaId": mangaID}
	var data gqlChaptersData
	if err := c.doGraphQL(ctx, chaptersQuery, vars, &data); err != nil {
		return nil, err
	}
	return mapChapterNodes(data.Chapters.Nodes), nil
}

// --- MangaMeta ---------------------------------------------------------------

// gqlMangaMetaData is the typed shape of the `data` field for the manga(id) query.
type gqlMangaMetaData struct {
	Manga gqlMangaNode `json:"manga"`
}

const mangaMetaQuery = `
query MangaMeta($id: Int!) {
  manga(id: $id) {` + mangaFieldSelection + `
  }
}`

// MangaMeta returns the stored metadata for the given mangaID via the
// manga(id: $id) GraphQL query. The manga must already exist in Suwayomi's
// library (added via Search / Ingest). ThumbnailURL is nil when the server
// does not provide one.
func (c *httpClient) MangaMeta(ctx context.Context, mangaID int) (Manga, error) {
	vars := map[string]any{"id": mangaID}
	var data gqlMangaMetaData
	if err := c.doGraphQL(ctx, mangaMetaQuery, vars, &data); err != nil {
		return Manga{}, err
	}
	return data.Manga.toManga(), nil
}

// --- ChapterPages ------------------------------------------------------------

// gqlChapterPagesData is the typed shape of the `data` field for fetchChapterPages.
type gqlChapterPagesData struct {
	FetchChapterPages struct {
		Pages []string `json:"pages"`
	} `json:"fetchChapterPages"`
}

const chapterPagesMutation = `
mutation FetchChapterPages($chapterId: Int!) {
  fetchChapterPages(input: { chapterId: $chapterId }) {
    pages
  }
}`

// ChapterPages triggers a Suwayomi page-fetch for chapterID and returns the
// ordered list of relative page-image URL paths.
func (c *httpClient) ChapterPages(ctx context.Context, chapterID int) ([]string, error) {
	vars := map[string]any{"chapterId": chapterID}
	var data gqlChapterPagesData
	if err := c.doGraphQL(ctx, chapterPagesMutation, vars, &data); err != nil {
		return nil, err
	}
	return data.FetchChapterPages.Pages, nil
}

// --- PageBytes ---------------------------------------------------------------

// contentTypeToExt maps MIME types returned by http.DetectContentType (or the
// response Content-Type header) to bare extensions without a leading dot.
// This matches the M1 convention in fetcher.PageImage.Ext and disk.CreateCBZ.
var contentTypeToExt = map[string]string{
	"image/jpeg": "jpg",
	"image/png":  "png",
	"image/gif":  "gif",
	"image/webp": "webp",
	"image/avif": "avif",
}

// PageBytes downloads the image at pageURL and returns the raw bytes and bare
// file extension (e.g. "jpg"). Extension is detected via http.DetectContentType
// on the first 512 bytes of the response body. A non-2xx response status is
// returned as an error.
//
// pageURL may be an absolute URL (e.g. "http://host/path") or a server-relative
// path (e.g. "/api/v1/manga/1/chapter/1/page/0"). Suwayomi v2.2.2100 returns
// relative paths from the fetchChapterPages mutation (LongString scalar); this
// method prepends c.baseURL when the URL starts with "/".
func (c *httpClient) PageBytes(ctx context.Context, pageURL string) ([]byte, string, error) {
	fullURL := pageURL
	if strings.HasPrefix(pageURL, "/") {
		fullURL = c.baseURL + pageURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		// Defensive path: reachable only with a malformed pageURL (which
		// comes from Suwayomi's own server response) or a nil context.
		return nil, "", fmt.Errorf("suwayomi: build page request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("suwayomi: page request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("suwayomi: page HTTP %d for %s", resp.StatusCode, fullURL)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		// Defensive path: reachable only on OS-level read failure (connection
		// reset mid-stream); not reproducible in httptest without custom bodies.
		return nil, "", fmt.Errorf("suwayomi: read page body: %w", err)
	}

	// Detect the content type from the actual bytes (more reliable than
	// trusting the Content-Type header, which some Suwayomi sources omit).
	// Sniff at most 512 bytes (the limit http.DetectContentType uses internally).
	sniff := data
	if len(sniff) > 512 {
		sniff = sniff[:512]
	}
	detected := http.DetectContentType(sniff)
	// DetectContentType may include parameters (e.g. "image/jpeg; charset=...").
	mimeType, _, _ := strings.Cut(detected, ";")
	mimeType = strings.TrimSpace(mimeType)

	ext, ok := contentTypeToExt[mimeType]
	if !ok {
		// Fall back to content-type header if sniffing yields an unknown type.
		ctHeader := resp.Header.Get("Content-Type")
		headerMIME, _, _ := strings.Cut(ctHeader, ";")
		headerMIME = strings.TrimSpace(headerMIME)
		ext = contentTypeToExt[headerMIME]
		if ext == "" {
			ext = "bin"
		}
	}

	return data, ext, nil
}
