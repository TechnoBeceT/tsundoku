package sourceengine

// This file holds the Go mirrors of engine-host's Dto.kt response shapes —
// the values callers actually work with. Per-endpoint REQUEST wire shapes
// (searchRequest, mangaRequest, ...) and thin response WRAPPERS
// (chaptersResponse, pagesResponse, ...) live beside the method that uses
// them (search.go, chapters.go, ...), not here — this file is only the
// public, reusable DTOs.

// Health is the engine host's liveness probe response.
type Health struct {
	// Status is a short human string; the host always reports "ok" when it
	// answers at all.
	Status string `json:"status"`
	// Sources is how many sources the host currently has loaded.
	Sources int `json:"sources"`
}

// Source is one content source loaded from an installed extension —
// identified by a STABLE numeric ID that survives a DB rebuild + extension
// reinstall as long as the same extension version is loaded.
type Source struct {
	// ID is the source's stable numeric identifier.
	ID int64 `json:"id"`
	// Name is the human-readable source name (e.g. "MangaDex").
	Name string `json:"name"`
	// Lang is the BCP-47-ish language tag the source reports (e.g. "en").
	Lang string `json:"lang"`
}

// MangaEntry is one search/browse result — addressed by its source-relative
// URL, never an opaque id.
type MangaEntry struct {
	// URL is the source-relative manga URL — the stable key for this manga.
	// This is the ADDRESSING url (what every request sends back to identify
	// the manga), NOT a clickable browser link — see RealURL.
	URL string `json:"url"`
	// Title is the manga's display title.
	Title string `json:"title"`
	// ThumbnailURL is the cover image URL; "" when the source omits it.
	ThumbnailURL string `json:"thumbnailUrl"`
	// RealURL is the fully-qualified, browser-clickable URL for this manga
	// (Mihon's HttpSource.getMangaUrl) — powers the "View on source" external
	// link. Distinct from URL: URL is source-relative addressing that may not
	// even be an absolute URL for every source; RealURL is always meant to
	// open in a browser. "" when the engine host could not resolve one (a
	// non-HttpSource source, or a source whose request-building throws).
	RealURL string `json:"realUrl"`
}

// SearchResult is one page of a search or catalogue-browse listing.
type SearchResult struct {
	// Manga holds the candidates on this page, in source order.
	Manga []MangaEntry `json:"manga"`
	// HasNextPage reports whether another page exists.
	HasNextPage bool `json:"hasNextPage"`
}

// MangaDetails is the full metadata for one manga, keyed by URL.
type MangaDetails struct {
	// URL is the source-relative manga URL. This is the ADDRESSING url, NOT a
	// clickable browser link — see RealURL.
	URL string `json:"url"`
	// Title is the manga's display title.
	Title string `json:"title"`
	// Author is the writing credit; "" when the source omits it.
	Author string `json:"author"`
	// Artist is the art credit; "" when the source omits it (some sources
	// only ever set Author).
	Artist string `json:"artist"`
	// Description is the synopsis/summary text; "" when the source omits it.
	Description string `json:"description"`
	// Genres is the source's genre/tag list; nil or empty when not provided.
	Genres []string `json:"genres"`
	// Status is the source's publication-status label (e.g. "ONGOING").
	Status string `json:"status"`
	// ThumbnailURL is the cover image URL; "" when the source omits it.
	ThumbnailURL string `json:"thumbnailUrl"`
	// RealURL is the fully-qualified, browser-clickable URL for this manga
	// (Mihon's HttpSource.getMangaUrl) — see MangaEntry.RealURL's doc comment
	// for the distinction from URL. "" when unresolved.
	RealURL string `json:"realUrl"`
}

// Chapter is one chapter of a manga, keyed by its source-relative URL.
type Chapter struct {
	// URL is the source-relative chapter URL — the stable key for this
	// chapter (NEVER an engine-assigned id).
	URL string `json:"url"`
	// Name is the chapter's display name (e.g. "Chapter 1").
	Name string `json:"name"`
	// Number is the parsed chapter number (e.g. 1.5).
	Number float64 `json:"number"`
	// Scanlator is the credited scanlation group; "" when the source omits
	// it or does not tag chapters by group.
	Scanlator string `json:"scanlator"`
	// UploadDate is the chapter's publication date as milliseconds since the
	// Unix epoch; 0 when the source omits it.
	UploadDate int64 `json:"uploadDate"`
}

// Page is one page of a chapter. The image address is the SOURCE's own page
// addressing — the (URL, ImageURL) pair — not an engine id. Both must be fed
// back to Image verbatim; most sources set only ImageURL, some (e.g.
// MangaDex) encode routing in URL and leave ImageURL "".
type Page struct {
	// Index is the page's position within the chapter (0-based).
	Index int `json:"index"`
	// URL is the source's own page address.
	URL string `json:"url"`
	// ImageURL is the resolved image address; "" when the source only sets
	// URL and resolves the real image server-side.
	ImageURL string `json:"imageUrl"`
}

// Preference is one configurable source preference, enough for Tsundoku to
// render a settings form. Type is the androidx.preference class name
// (EditTextPreference / SwitchPreferenceCompat / ListPreference /
// CheckBoxPreference / MultiSelectListPreference). Entries/EntryValues are
// populated only for list-style preferences.
type Preference struct {
	// Key identifies the preference for a SetPreferences write; "" is
	// possible but not meaningfully writable.
	Key string `json:"key"`
	// Type is the androidx.preference class name driving how to render it.
	Type string `json:"type"`
	// Title is the human-readable label.
	Title string `json:"title"`
	// Summary is the human-readable description; "" when absent.
	Summary string `json:"summary"`
	// CurrentValue is the preference's current value; its concrete JSON type
	// (bool/string/[]string) depends on Type.
	CurrentValue any `json:"currentValue"`
	// DefaultValue is the preference's default value, same shape rules as
	// CurrentValue.
	DefaultValue any `json:"defaultValue"`
	// Entries holds the display labels for a list-style preference; nil
	// otherwise.
	Entries []string `json:"entries"`
	// EntryValues holds the underlying values for a list-style preference,
	// parallel to Entries; nil otherwise.
	EntryValues []string `json:"entryValues"`
}

// Extension is one extension the engine host knows about, merged across the
// installed working-set and the configured repos. IsInstalled reports
// whether it is present on the volume; HasUpdate reports whether a repo
// advertises a higher VersionCode.
type Extension struct {
	// PkgName is the extension's package name — its identity (there is no
	// separate id).
	PkgName string `json:"pkgName"`
	// Name is the extension's display name.
	Name string `json:"name"`
	// VersionName is the human-readable version string.
	VersionName string `json:"versionName"`
	// VersionCode is the monotonic version number used to detect updates.
	VersionCode int64 `json:"versionCode"`
	// Lang is the extension's language tag.
	Lang string `json:"lang"`
	// IsInstalled reports whether the extension is present on the volume.
	IsInstalled bool `json:"isInstalled"`
	// HasUpdate reports whether a configured repo advertises a newer
	// VersionCode than the installed one.
	HasUpdate bool `json:"hasUpdate"`
	// IsNsfw reports whether the extension is flagged not-safe-for-work.
	IsNsfw bool `json:"isNsfw"`
	// IconURL is the extension's icon image URL; "" when unavailable.
	IconURL string `json:"iconUrl"`
	// RepoURL is the configured repo this extension was resolved from; nil
	// when the extension is not associated with a repo (e.g. sideloaded).
	RepoURL *string `json:"repoUrl"`
	// Sources lists the content sources this extension provides (one per
	// language it supports).
	Sources []Source `json:"sources"`
}

// FlareSolverrPatch is a PARTIAL update to the FlareSolverr (Cloudflare
// Cloudflare-bypass) config. Every field is a pointer so that only the
// caller's explicitly-set fields are marshalled onto the wire (via
// omitempty) — an unset field is never sent, and therefore never clobbers
// the host's current value.
type FlareSolverrPatch struct {
	// Enabled turns FlareSolverr routing on/off, if set.
	Enabled *bool `json:"enabled,omitempty"`
	// URL is the FlareSolverr server URL, if set.
	URL *string `json:"url,omitempty"`
	// Session is the FlareSolverr session name to reuse, if set.
	Session *string `json:"session,omitempty"`
	// SessionTTL is the session lifetime in minutes, if set.
	SessionTTL *int `json:"sessionTtl,omitempty"`
	// Timeout is the request timeout in seconds, if set.
	Timeout *int `json:"timeout,omitempty"`
	// AsResponseFallback controls whether a failed direct fetch falls back
	// to FlareSolverr, if set.
	AsResponseFallback *bool `json:"asResponseFallback,omitempty"`
}

// FlareSolverrConfig is the FlareSolverr config read back after a
// SetFlareSolverr call (or a plain read). Every field always carries the
// host's current value.
type FlareSolverrConfig struct {
	// Enabled reports whether FlareSolverr routing is on.
	Enabled bool `json:"enabled"`
	// URL is the configured FlareSolverr server URL.
	URL string `json:"url"`
	// Session is the configured session name.
	Session string `json:"session"`
	// SessionTTL is the configured session lifetime in minutes.
	SessionTTL int `json:"sessionTtl"`
	// Timeout is the configured request timeout in seconds.
	Timeout int `json:"timeout"`
	// AsResponseFallback reports whether a failed direct fetch falls back to
	// FlareSolverr.
	AsResponseFallback bool `json:"asResponseFallback"`
}

// SocksPatch is a PARTIAL update to the SOCKS-proxy config. Every field is a
// pointer so that only the caller's explicitly-set fields are marshalled
// onto the wire — see FlareSolverrPatch's doc comment for the same
// no-clobber rule.
type SocksPatch struct {
	// Enabled turns SOCKS-proxy routing on/off, if set.
	Enabled *bool `json:"enabled,omitempty"`
	// Version selects SOCKS4 or SOCKS5 (4 or 5), if set.
	Version *int `json:"version,omitempty"`
	// Host is the proxy host, if set.
	Host *string `json:"host,omitempty"`
	// Port is the proxy port (a numeric string, per the engine host's wire
	// shape), if set.
	Port *string `json:"port,omitempty"`
	// Username is the proxy auth username, if set.
	Username *string `json:"username,omitempty"`
	// Password is the proxy auth password, if set. It is write-only — the
	// host never echoes it back in SocksConfig.
	Password *string `json:"password,omitempty"`
}

// SocksConfig is the SOCKS-proxy config read back after a SetSocks call.
// Password is always "" — the host deliberately omits it from every
// response so a stored password is never echoed back.
type SocksConfig struct {
	// Enabled reports whether SOCKS-proxy routing is on.
	Enabled bool `json:"enabled"`
	// Version is the configured SOCKS version (4 or 5).
	Version int `json:"version"`
	// Host is the configured proxy host.
	Host string `json:"host"`
	// Port is the configured proxy port.
	Port string `json:"port"`
	// Username is the configured proxy auth username.
	Username string `json:"username"`
	// Password is always "" — see the type doc comment.
	Password string `json:"password"`
}
