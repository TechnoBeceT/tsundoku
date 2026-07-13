// Package metadata defines the provider-agnostic contracts for cross-source
// series metadata (AniList, MangaDex, ...): the SeriesMetadata shape every
// provider maps its raw response into, the Provider port each metadata
// source implements, a name-similarity matcher (match.go), and a merge
// function that combines several providers' metadata into one record
// (merge.go). This package is PURE — no DB, no disk, no network — later
// tasks wire real providers and callers against these contracts.
package metadata

import "context"

// AltTitle is one alternate title a provider returns for a series, tagged
// with its kind and language.
type AltTitle struct {
	Name string
	// Type is one of ROMAJI, LOCALIZED, NATIVE, SYNONYM.
	Type string
	Lang string
}

// Author is one writing/art credit on a series.
type Author struct {
	Name string
	// Role is one of WRITER, ARTIST, STORY, ART, ... (provider-defined).
	Role string
}

// Link is an external reference URL (official site, MAL page, etc).
type Link struct {
	Label string
	URL   string
}

// SeriesMetadata is the normalized shape every Provider maps its raw
// response into. Zero values mean "unknown" (Year 0, Score 0, empty string
// fields) so Merge's scalar gap-fill can tell "unset" from "set".
type SeriesMetadata struct {
	Title       string
	AltTitles   []AltTitle
	Description string
	// Status is normalized: "ongoing"|"completed"|"hiatus"|"cancelled"|"".
	Status string
	Genres []string
	Tags   []string
	// Authors carries both writer and artist credits, distinguished by Role.
	Authors []Author
	// Year is the first-publication year; 0 = unknown.
	Year  int
	Links []Link
	// Score is normalized to a 0-100 scale; 0 = unknown.
	Score float64
	// CoverURL is set by an individual provider fetch but is NEVER merged
	// by Merge — the cover is chosen independently elsewhere (QCAT-228).
	CoverURL  string
	Publisher string
}

// SearchResult is one provider's search hit, returned by Provider.Search
// and Provider.Match.
type SearchResult struct {
	// Provider is the owning Provider's Key().
	Provider string
	RemoteID string
	Title    string
	URL      string
	CoverURL string
	Year     int
}

// CoverCandidate is one selectable cover option surfaced to the owner —
// either a metadata provider's cover or a library SeriesProvider's cover.
// Cover SELECTION itself is a later concern; this is only the shared shape
// a later chooser will operate over.
type CoverCandidate struct {
	// SourceKind is "metadata" or "source".
	SourceKind string
	// SourceRef is the metadata Provider's Key() when SourceKind is
	// "metadata", or the SeriesProvider UUID string when SourceKind is
	// "source".
	SourceRef string
	CoverURL  string
	Label     string
}

// MatchQuery is the title set NameSimilarity (match.go) compares a
// candidate title against: the canonical Title plus any known AltTitles.
type MatchQuery struct {
	Title     string
	AltTitles []string
}

// Provider is the port every metadata source (AniList, MangaDex, ...)
// implements. Fanning a query out across multiple providers is a LATER
// task (Registry below only holds the ordered set) — this package stays
// pure: no provider here talks to a real network.
type Provider interface {
	// Key is the provider's stable identifier (e.g. "anilist").
	Key() string
	// ID is the provider's own numeric identifier, if it has one (0 = none).
	ID() int
	// Priority ranks this provider against others absent an explicit Order
	// (mirrors the SeriesProvider.importance convention — higher wins).
	Priority() int
	// Search returns up to limit candidate matches for a free-text query.
	Search(ctx context.Context, q string, limit int) ([]SearchResult, error)
	// GetSeriesMetadata fetches the full metadata record for one remote series.
	GetSeriesMetadata(ctx context.Context, remoteID string) (SeriesMetadata, error)
	// GetSeriesCover fetches the raw cover image bytes plus file extension.
	GetSeriesCover(ctx context.Context, remoteID string) (data []byte, ext string, err error)
	// Match finds the provider's best confident match for q, or nil for
	// "no confident match".
	Match(ctx context.Context, q MatchQuery) (*SearchResult, error)
}

// Registry holds an ORDERED set of Providers. Fan-out/search/lookup logic
// over the registry is a later task — this is just the holder + accessor
// every later task builds on.
type Registry struct {
	providers []Provider
}

// NewRegistry builds a Registry over providers, preserving call order.
func NewRegistry(providers ...Provider) *Registry {
	return &Registry{providers: providers}
}

// Providers returns the registered providers in registration order.
func (r *Registry) Providers() []Provider {
	return r.providers
}
