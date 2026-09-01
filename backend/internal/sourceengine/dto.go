package sourceengine

import (
	"encoding/json"
	"fmt"
)

// This file holds the Go mirrors of engine-host's Dto.kt response shapes —
// the values callers actually work with. Per-endpoint REQUEST wire shapes
// (searchRequest, mangaRequest, ...) and thin response WRAPPERS
// (chaptersResponse, pagesResponse, ...) live beside the method that uses
// them (search.go, chapters.go, ...), not here — this file is only the
// public, reusable DTOs.

// AddressMode records how the engine reconstructs a source manga from its
// serialized URL. Its zero value is the compatibility state (unknown), so an
// omitted field from an older engine or API client remains valid without
// inventing provenance.
type AddressMode string

const (
	// AddressModeUnknown is the legacy/unresolved mode. It is represented by the
	// Go zero value while its JSON/database spelling remains "unknown".
	AddressModeUnknown AddressMode = ""
	// AddressModeDirect means URL is an extension-owned key that must be passed
	// straight back to the source.
	AddressModeDirect AddressMode = "direct"
	// AddressModeURLSearch means URL is rehydrated through the source's URL-search
	// path, optionally using WebURL as the browser-address witness.
	AddressModeURLSearch AddressMode = "url_search"
)

// Wire returns the engine/database spelling of m. The zero value deliberately
// serializes as "unknown" for additive compatibility.
func (m AddressMode) Wire() string {
	if m == AddressModeUnknown {
		return "unknown"
	}
	return string(m)
}

// IsKnown reports whether m carries resolved provenance.
func (m AddressMode) IsKnown() bool {
	return m == AddressModeDirect || m == AddressModeURLSearch
}

// IsValid reports whether m is one of the three supported modes. The zero value
// is the valid unknown mode.
func (m AddressMode) IsValid() bool {
	return m == AddressModeUnknown || m.IsKnown()
}

// ParseAddressMode converts a wire/database spelling to AddressMode.
func ParseAddressMode(wire string) (AddressMode, error) {
	switch wire {
	case "", "unknown":
		return AddressModeUnknown, nil
	case "direct":
		return AddressModeDirect, nil
	case "url_search":
		return AddressModeURLSearch, nil
	default:
		return AddressModeUnknown, fmt.Errorf("sourceengine: invalid address mode %q", wire)
	}
}

// MarshalJSON preserves the engine's exact enum spellings while mapping the Go
// zero value onto the compatibility spelling "unknown".
func (m AddressMode) MarshalJSON() ([]byte, error) {
	if !m.IsValid() {
		return nil, fmt.Errorf("sourceengine: invalid address mode %q", string(m))
	}
	return json.Marshal(m.Wire())
}

// UnmarshalJSON accepts exactly the engine's tri-state enum. Missing fields do
// not invoke this method and therefore retain the zero-value unknown mode.
func (m *AddressMode) UnmarshalJSON(data []byte) error {
	var wire string
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	parsed, err := ParseAddressMode(wire)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}

// ProviderRef is the complete manga address context required by details,
// chapters, and page warm-up calls. URL is never rewritten; AddressMode tells
// the engine how to interpret it and WebURL is only the optional browser URL
// witness used while resolving legacy unknown rows.
type ProviderRef struct {
	SourceID    int64
	URL         string
	AddressMode AddressMode
	WebURL      string
}

// Health is the engine host's liveness probe response.
type Health struct {
	// Status is a short human string; the host always reports "ok" when it
	// answers at all.
	Status string `json:"status"`
	// Sources is how many sources the host currently has loaded.
	Sources int `json:"sources"`
}

// EngineSourceStatus is one of the status endpoint's ten busiest source ids.
// It carries only bounded scheduler counts and never request content.
type EngineSourceStatus struct {
	SourceID int64 `json:"source_id"`
	Queued   int   `json:"queued"`
	Running  int   `json:"running"`
}

// EngineStatus is the engine host's bounded runtime snapshot. Counts describe
// physical source occupancy and public-result outcomes at one sampling point.
type EngineStatus struct {
	Ready               bool                 `json:"ready"`
	SourceWorkers       int                  `json:"source_workers"`
	PerSourceLimit      int                  `json:"per_source_limit"`
	Queued              int                  `json:"queued"`
	Running             int                  `json:"running"`
	CompletionSequence  int64                `json:"completion_sequence"`
	OldestRunningMillis int64                `json:"oldest_running_millis"`
	Completed           int64                `json:"completed"`
	Cancelled           int64                `json:"cancelled"`
	TimedOut            int64                `json:"timed_out"`
	Rejected            int64                `json:"rejected"`
	BusiestSources      []EngineSourceStatus `json:"busiest_sources"`
	ExtensionRunning    bool                 `json:"extension_running"`
	ExtensionQueued     int                  `json:"extension_queued"`
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

// MangaEntry is one search/browse result, identified by a source-owned
// serialized address rather than an engine-assigned id.
type MangaEntry struct {
	// URL is the stable ADDRESSING value every request sends back to identify
	// the manga. Depending on AddressMode, it may be a relative or opaque
	// extension key, or an absolute cross-origin URL retained for hydration.
	URL string `json:"url"`
	// Title is the manga's display title.
	Title string `json:"title"`
	// ThumbnailURL is the cover image URL; "" when the source omits it.
	ThumbnailURL string `json:"thumbnailUrl"`
	// RealURL is the fully-qualified, browser-clickable URL for this manga
	// (Mihon's HttpSource.getMangaUrl) — powers the "View on source" external
	// link. URL remains the source-owned addressing value; RealURL opens in a
	// browser and may be carried back only as the optional WebURL resolver
	// witness. The two may be equal for absolute URL-search addresses. "" when
	// the engine host could not resolve one (a
	// non-HttpSource source, or a source whose request-building throws).
	RealURL string `json:"realUrl"`
	// AddressMode is the engine-resolved provenance for URL. Search and browse
	// establish it before the candidate crosses the browser adopt flow.
	AddressMode AddressMode `json:"addressMode"`
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
	// URL is the source-owned serialized manga address. It may be relative,
	// opaque, or absolute; see AddressMode and RealURL.
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
	// AddressMode is the mode the engine successfully used for this details
	// fetch. Unknown is the compatibility default for older hosts.
	AddressMode AddressMode `json:"addressMode"`
}

// Chapter is one chapter of a manga, keyed by its source-owned address.
type Chapter struct {
	// URL is the source-owned chapter address — the stable key for this chapter
	// (NEVER an engine-assigned id). Its shape may be relative, opaque, or
	// absolute; see RealURL for the browser link.
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
	// RealURL is the fully-qualified, browser-clickable URL for this chapter
	// (Mihon's HttpSource.getChapterUrl) — feeds Komga's ComicInfo <Web>
	// field. Distinct from URL — never used for addressing. "" when the
	// engine host could not resolve one.
	RealURL string `json:"realUrl"`
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

// ImpersonatePatch is a PARTIAL update to the impersonate-gateway config (the
// Chrome-fingerprint image-fetch gateway, GAP-111). Every field is a pointer so
// only the caller's explicitly-set fields are marshalled onto the wire (via
// omitempty) — see FlareSolverrPatch's doc comment for the same no-clobber rule.
type ImpersonatePatch struct {
	// Enabled turns impersonate-gateway image routing on/off, if set. It is the
	// MASTER switch only — a source still has to be listed in SourceIDs.
	Enabled *bool `json:"enabled,omitempty"`
	// URL is the impersonate-gateway endpoint, if set. Global: one gateway
	// serves every gated source.
	URL *string `json:"url,omitempty"`
	// SourceIDs is the set of engine-host source ids allowed to use the gateway,
	// if set (GAP-131). A source absent from it NEVER reaches the gateway and so
	// keeps its own OkHttp interceptor chain — which is what actually
	// descrambles images. An explicitly EMPTY slice is a meaningful value ("no
	// source"), so callers that own this state always send it; nil omits the
	// field entirely (no-clobber, like every other patch field).
	SourceIDs *[]int64 `json:"sourceIds,omitempty"`
}

// ImpersonateConfig is the impersonate-gateway config read back after a
// SetImpersonate call. Every field always carries the host's current value.
type ImpersonateConfig struct {
	// Enabled reports whether impersonate-gateway image routing is on.
	Enabled bool `json:"enabled"`
	// URL is the configured impersonate-gateway endpoint.
	URL string `json:"url"`
	// SourceIDs is the set of source ids currently allowed to use the gateway
	// (ascending). Empty means no source uses it.
	SourceIDs []int64 `json:"sourceIds"`
}

// ImageTransportPatch is a PARTIAL update to the source image connection
// policy. ReuseSourceIDs selects sources that use their normal pooled client
// for cacheless image calls. A nil pointer omits the field (preserving the
// host selection); a pointer to an empty slice clears the selection.
type ImageTransportPatch struct {
	ReuseSourceIDs *[]int64 `json:"reuseSourceIds,omitempty"`
}

// ImageTransportConfig is the image connection policy read back after a
// SetImageTransport call. ReuseSourceIDs is normalized by the host into
// ascending unique source IDs.
type ImageTransportConfig struct {
	ReuseSourceIDs []int64 `json:"reuseSourceIds"`
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
