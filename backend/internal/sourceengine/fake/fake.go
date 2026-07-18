// Package fake provides the ONE configurable in-memory implementation of
// sourceengine.Client used by every downstream package's tests (the P2
// migration's later slices bind to this the same way M1's dispatcher tests
// bind to internal/fetcher/fake). Configure it with functional options,
// drive it exactly like the real client, and assert on call counts via
// CallCount — no network, no real engine host, no GraphQL.
package fake

import (
	"context"
	"sync"

	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// Compile-time assertion: Client must satisfy sourceengine.Client.
var _ sourceengine.Client = (*Client)(nil)

// contentKey addresses a piece of per-(source,url) content — MangaDetails,
// Chapters, or Pages — the same way the real engine host does.
type contentKey struct {
	sourceID int64
	url      string
}

// imageEntry is one WithImage-configured page: its raw bytes and content
// type.
type imageEntry struct {
	data        []byte
	contentType string
}

// Client is a configurable, concurrency-safe in-memory implementation of
// sourceengine.Client. The zero value is not usable — build one with New.
type Client struct {
	mu sync.Mutex

	sources       []sourceengine.Source
	searchResults map[int64]sourceengine.SearchResult
	mangaDetails  map[contentKey]sourceengine.MangaDetails
	chapters      map[contentKey][]sourceengine.Chapter
	pages         map[contentKey][]sourceengine.Page
	images        map[contentKey]imageEntry
	coverImages   map[contentKey]imageEntry
	extensions    []sourceengine.Extension
	preferences   map[int64][]sourceengine.Preference
	repos         []string
	flareSolverr  sourceengine.FlareSolverrConfig
	socks         sourceengine.SocksConfig

	lastInstallApkURL string

	errors map[string]error
	calls  map[string]int
}

// Option configures a Client at construction time.
type Option func(*Client)

// New builds a Client with the given options applied in order.
func New(opts ...Option) *Client {
	c := &Client{
		searchResults: map[int64]sourceengine.SearchResult{},
		mangaDetails:  map[contentKey]sourceengine.MangaDetails{},
		chapters:      map[contentKey][]sourceengine.Chapter{},
		pages:         map[contentKey][]sourceengine.Page{},
		images:        map[contentKey]imageEntry{},
		coverImages:   map[contentKey]imageEntry{},
		preferences:   map[int64][]sourceengine.Preference{},
		errors:        map[string]error{},
		calls:         map[string]int{},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// WithSources seeds the source registry returned by Sources and folded into
// Health's Sources count.
func WithSources(sources []sourceengine.Source) Option {
	return func(c *Client) { c.sources = sources }
}

// WithSearchResult seeds the SearchResult returned by Search, Popular, AND
// Latest for sourceID — the fake does not distinguish between the three
// call shapes, only the source being queried.
func WithSearchResult(sourceID int64, res sourceengine.SearchResult) Option {
	return func(c *Client) { c.searchResults[sourceID] = res }
}

// WithMangaDetails seeds the MangaDetails returned for (sourceID, url).
func WithMangaDetails(sourceID int64, url string, details sourceengine.MangaDetails) Option {
	return func(c *Client) { c.mangaDetails[contentKey{sourceID, url}] = details }
}

// WithChapters seeds the chapter list returned for (sourceID, url).
func WithChapters(sourceID int64, url string, chapters []sourceengine.Chapter) Option {
	return func(c *Client) { c.chapters[contentKey{sourceID, url}] = chapters }
}

// WithPages seeds the page list returned for (sourceID, chapterURL).
func WithPages(sourceID int64, chapterURL string, pages []sourceengine.Page) Option {
	return func(c *Client) { c.pages[contentKey{sourceID, chapterURL}] = pages }
}

// WithImage seeds the raw bytes + content type returned for (sourceID,
// pageURL) — keyed the same way the real engine host addresses a page,
// ignoring the imageURL argument Image() is called with (mirroring the real
// host, which resolves imageURL itself when the caller omits it).
func WithImage(sourceID int64, pageURL string, data []byte, contentType string) Option {
	return func(c *Client) {
		c.images[contentKey{sourceID, pageURL}] = imageEntry{data: data, contentType: contentType}
	}
}

// WithCoverImage seeds the raw bytes + content type returned for a COVER
// fetch: Image called with pageURL="" and the cover's own address in
// imageURL (the series/cover.go + handler/coverproxy.StreamEngine shape —
// pageURL is deliberately empty for a cover, so the real engine host's
// HttpSource.getImage uses imageURL directly). It is keyed on (sourceID,
// imageURL) rather than (sourceID, pageURL): every cover fetch shares the same
// empty pageURL, so re-using WithImage's key would collide every cover under
// one contentKey{sourceID, ""} regardless of which cover was actually asked
// for. WithImage (the page path, pageURL non-empty) is unaffected — Image
// dispatches to this map only when pageURL=="".
func WithCoverImage(sourceID int64, imageURL string, data []byte, contentType string) Option {
	return func(c *Client) {
		c.coverImages[contentKey{sourceID, imageURL}] = imageEntry{data: data, contentType: contentType}
	}
}

// WithExtensions seeds the extension list every extension-listing/management
// method reads and mutates.
func WithExtensions(extensions []sourceengine.Extension) Option {
	return func(c *Client) { c.extensions = extensions }
}

// WithPreferences seeds the preference list returned for sourceID.
func WithPreferences(sourceID int64, prefs []sourceengine.Preference) Option {
	return func(c *Client) { c.preferences[sourceID] = prefs }
}

// WithRepos seeds the configured extension-repo index URL list.
func WithRepos(repos []string) Option {
	return func(c *Client) { c.repos = repos }
}

// WithError forces the named Client method (e.g. "Pages", "Image",
// "SetPreferences" — the exported method name, verbatim) to return err
// instead of its configured result. The call is still recorded by
// CallCount.
func WithError(method string, err error) Option {
	return func(c *Client) { c.errors[method] = err }
}

// CallCount returns how many times method (the exported method name, e.g.
// "SetPreferences") has been called, letting tests assert exact-once /
// at-least-once expectations without a dedicated counter field per method.
func (c *Client) CallCount(method string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[method]
}

// record increments method's call counter. Every Client method calls this
// first, before checking errFor, so a forced error is still recorded.
func (c *Client) record(method string) {
	c.mu.Lock()
	c.calls[method]++
	c.mu.Unlock()
}

// errFor returns the WithError-configured error for method, or nil.
func (c *Client) errFor(method string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.errors[method]
}

// --- ops -------------------------------------------------------------------

// Health returns a status of "ok" and the count of WithSources-configured
// sources.
func (c *Client) Health(_ context.Context) (sourceengine.Health, error) {
	c.record("Health")
	if err := c.errFor("Health"); err != nil {
		return sourceengine.Health{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return sourceengine.Health{Status: "ok", Sources: len(c.sources)}, nil
}

// --- source calls ------------------------------------------------------------

// Search returns the WithSearchResult-configured result for sourceID.
func (c *Client) Search(_ context.Context, sourceID int64, _ string, _ int) (sourceengine.SearchResult, error) {
	return c.searchResultFor("Search", sourceID)
}

// Popular returns the WithSearchResult-configured result for sourceID.
func (c *Client) Popular(_ context.Context, sourceID int64, _ int) (sourceengine.SearchResult, error) {
	return c.searchResultFor("Popular", sourceID)
}

// Latest returns the WithSearchResult-configured result for sourceID.
func (c *Client) Latest(_ context.Context, sourceID int64, _ int) (sourceengine.SearchResult, error) {
	return c.searchResultFor("Latest", sourceID)
}

// searchResultFor is the shared lookup Search/Popular/Latest all call
// through (§2 DRY — one map, one miss-is-zero-value rule for all three).
func (c *Client) searchResultFor(method string, sourceID int64) (sourceengine.SearchResult, error) {
	c.record(method)
	if err := c.errFor(method); err != nil {
		return sourceengine.SearchResult{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.searchResults[sourceID], nil
}

// MangaDetails returns the WithMangaDetails-configured details for
// (sourceID, url).
func (c *Client) MangaDetails(_ context.Context, sourceID int64, url string) (sourceengine.MangaDetails, error) {
	c.record("MangaDetails")
	if err := c.errFor("MangaDetails"); err != nil {
		return sourceengine.MangaDetails{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mangaDetails[contentKey{sourceID, url}], nil
}

// Chapters returns the WithChapters-configured chapter list for (sourceID,
// url). mangaTitle is accepted (interface parity with the real client) but
// ignored — the fake never runs recognition, so it has no effect here.
func (c *Client) Chapters(_ context.Context, sourceID int64, url string, _ string) ([]sourceengine.Chapter, error) {
	c.record("Chapters")
	if err := c.errFor("Chapters"); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.chapters[contentKey{sourceID, url}], nil
}

// Pages returns the WithPages-configured page list for (sourceID,
// chapterURL).
func (c *Client) Pages(_ context.Context, sourceID int64, chapterURL string) ([]sourceengine.Page, error) {
	c.record("Pages")
	if err := c.errFor("Pages"); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pages[contentKey{sourceID, chapterURL}], nil
}

// Image returns the configured bytes + content type for one of two shapes,
// mirrored on pageURL: a PAGE fetch (pageURL non-empty) returns the
// WithImage-configured entry for (sourceID, pageURL), imageURL ignored (see
// WithImage's doc comment); a COVER fetch (pageURL=="", the shape
// series/cover.go and handler/coverproxy.StreamEngine use) returns the
// WithCoverImage-configured entry for (sourceID, imageURL) instead — see
// WithCoverImage's doc comment for why the two need separate keys.
func (c *Client) Image(_ context.Context, sourceID int64, pageURL, imageURL string) ([]byte, string, error) {
	c.record("Image")
	if err := c.errFor("Image"); err != nil {
		return nil, "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if pageURL == "" {
		entry := c.coverImages[contentKey{sourceID, imageURL}]
		return entry.data, entry.contentType, nil
	}
	entry := c.images[contentKey{sourceID, pageURL}]
	return entry.data, entry.contentType, nil
}

// --- registry + preferences -------------------------------------------------

// Sources returns the WithSources-configured source list.
func (c *Client) Sources(_ context.Context) ([]sourceengine.Source, error) {
	c.record("Sources")
	if err := c.errFor("Sources"); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sources, nil
}

// Preferences returns the WithPreferences-configured preference list for
// sourceID.
func (c *Client) Preferences(_ context.Context, sourceID int64) ([]sourceengine.Preference, error) {
	c.record("Preferences")
	if err := c.errFor("Preferences"); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.preferences[sourceID], nil
}

// SetPreferences applies changes (by Key) onto sourceID's stored preference
// list and returns the updated list. It never mutates a slice the caller
// passed to WithPreferences — a fresh copy is written back.
func (c *Client) SetPreferences(_ context.Context, sourceID int64, changes map[string]any) ([]sourceengine.Preference, error) {
	c.record("SetPreferences")
	if err := c.errFor("SetPreferences"); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	updated := append([]sourceengine.Preference(nil), c.preferences[sourceID]...)
	for i, pref := range updated {
		if v, ok := changes[pref.Key]; ok {
			updated[i].CurrentValue = v
		}
	}
	c.preferences[sourceID] = updated
	return append([]sourceengine.Preference(nil), updated...), nil
}

// --- extension management ---------------------------------------------------

// Extensions returns the current extension list.
func (c *Client) Extensions(_ context.Context) ([]sourceengine.Extension, error) {
	c.record("Extensions")
	if err := c.errFor("Extensions"); err != nil {
		return nil, err
	}
	return c.extensionsCopy(), nil
}

// InstallExtension marks the extension identified by pkgName installed and
// returns the refreshed list. When installed by apkURL (pkgName ""), it records
// the apkURL (LastInstallApkURL) so a test can assert the reinstall path passed
// the cached-apk path, and marks the matching-by-apk extension installed if one
// exists (best-effort: the fake has no real apk to inspect).
func (c *Client) InstallExtension(_ context.Context, pkgName, apkURL string) ([]sourceengine.Extension, error) {
	c.record("InstallExtension")
	c.mu.Lock()
	c.lastInstallApkURL = apkURL
	c.mu.Unlock()
	if err := c.errFor("InstallExtension"); err != nil {
		return nil, err
	}
	if pkgName == "" {
		// apkURL-only install: the fake cannot read the apk to learn its pkg, so it
		// leaves the list unchanged and just returns it (the reinstall test asserts
		// the apkURL + a re-read, not a state flip).
		return c.extensionsCopy(), nil
	}
	return c.setInstalled(pkgName, true), nil
}

// LastInstallApkURL returns the apkURL passed to the most recent
// InstallExtension call ("" when none, or when the last install was by pkgName).
func (c *Client) LastInstallApkURL() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastInstallApkURL
}

// RefreshExtensions returns the current extension list unchanged (the fake
// has no repo to re-fetch from).
func (c *Client) RefreshExtensions(_ context.Context) ([]sourceengine.Extension, error) {
	c.record("RefreshExtensions")
	if err := c.errFor("RefreshExtensions"); err != nil {
		return nil, err
	}
	return c.extensionsCopy(), nil
}

// UpdateExtension returns the current extension list unchanged (the fake has
// no version to bump).
func (c *Client) UpdateExtension(_ context.Context, _ string) ([]sourceengine.Extension, error) {
	c.record("UpdateExtension")
	if err := c.errFor("UpdateExtension"); err != nil {
		return nil, err
	}
	return c.extensionsCopy(), nil
}

// UninstallExtension marks the extension identified by pkgName NOT installed
// and returns the refreshed list.
func (c *Client) UninstallExtension(_ context.Context, pkgName string) ([]sourceengine.Extension, error) {
	c.record("UninstallExtension")
	if err := c.errFor("UninstallExtension"); err != nil {
		return nil, err
	}
	return c.setInstalled(pkgName, false), nil
}

// setInstalled flips IsInstalled for the extension matching pkgName and
// returns a defensive copy of the whole list.
func (c *Client) setInstalled(pkgName string, installed bool) []sourceengine.Extension {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, ext := range c.extensions {
		if ext.PkgName == pkgName {
			c.extensions[i].IsInstalled = installed
		}
	}
	out := make([]sourceengine.Extension, len(c.extensions))
	copy(out, c.extensions)
	return out
}

// extensionsCopy returns a defensive copy of the current extension list.
func (c *Client) extensionsCopy() []sourceengine.Extension {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]sourceengine.Extension, len(c.extensions))
	copy(out, c.extensions)
	return out
}

// Repos returns a defensive copy of the configured repo list.
func (c *Client) Repos(_ context.Context) ([]string, error) {
	c.record("Repos")
	if err := c.errFor("Repos"); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.repos...), nil
}

// SetRepos replaces the configured repo list and returns a defensive copy of
// it.
func (c *Client) SetRepos(_ context.Context, repos []string) ([]string, error) {
	c.record("SetRepos")
	if err := c.errFor("SetRepos"); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.repos = append([]string(nil), repos...)
	return append([]string(nil), c.repos...), nil
}

// --- config passthrough ------------------------------------------------------

// configCall is the shared record+errFor+lock envelope SetFlareSolverr and
// SetSocks both run their (structurally different, so not worth merging any
// further) field-patch logic through — the same record/errFor/mutex dance
// every other method above repeats, factored out once here (§2 DRY) so the
// two config setters read as just their patch logic.
func (c *Client) configCall(method string, apply func()) error {
	c.record(method)
	if err := c.errFor(method); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	apply()
	return nil
}

// SetFlareSolverr applies patch's non-nil fields onto the stored
// FlareSolverr config and returns the updated config.
//
// but over a genuinely different set of typed fields (bool/int/string on two
// unrelated DTOs) — there is no shared field to factor out without a
// reflection-based generic merge, which would trade this file's plain
// readability for opacity just to satisfy the duplication linter.
//
//nolint:dupl // SetSocks below has the identical if-non-nil-then-copy SHAPE,
func (c *Client) SetFlareSolverr(_ context.Context, patch sourceengine.FlareSolverrPatch) (sourceengine.FlareSolverrConfig, error) {
	var result sourceengine.FlareSolverrConfig
	err := c.configCall("SetFlareSolverr", func() {
		if patch.Enabled != nil {
			c.flareSolverr.Enabled = *patch.Enabled
		}
		if patch.URL != nil {
			c.flareSolverr.URL = *patch.URL
		}
		if patch.Session != nil {
			c.flareSolverr.Session = *patch.Session
		}
		if patch.SessionTTL != nil {
			c.flareSolverr.SessionTTL = *patch.SessionTTL
		}
		if patch.Timeout != nil {
			c.flareSolverr.Timeout = *patch.Timeout
		}
		if patch.AsResponseFallback != nil {
			c.flareSolverr.AsResponseFallback = *patch.AsResponseFallback
		}
		result = c.flareSolverr
	})
	return result, err
}

// SetSocks applies patch's non-nil fields onto the stored SOCKS config and
// returns the updated config.
//
//nolint:dupl // see the matching note on SetFlareSolverr above.
func (c *Client) SetSocks(_ context.Context, patch sourceengine.SocksPatch) (sourceengine.SocksConfig, error) {
	var result sourceengine.SocksConfig
	err := c.configCall("SetSocks", func() {
		if patch.Enabled != nil {
			c.socks.Enabled = *patch.Enabled
		}
		if patch.Version != nil {
			c.socks.Version = *patch.Version
		}
		if patch.Host != nil {
			c.socks.Host = *patch.Host
		}
		if patch.Port != nil {
			c.socks.Port = *patch.Port
		}
		if patch.Username != nil {
			c.socks.Username = *patch.Username
		}
		if patch.Password != nil {
			c.socks.Password = *patch.Password
		}
		result = c.socks
	})
	return result, err
}
