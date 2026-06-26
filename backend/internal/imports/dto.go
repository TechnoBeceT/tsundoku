// Package imports — DTOs for the import workflow API.
//
// This file defines the data-transfer objects that imports.Service returns to
// its callers (HTTP handlers). All types carry camelCase JSON tags so the
// generated TypeScript client matches the OpenAPI spec without renaming.
package imports

// SourceDTO is the read-only representation of a Suwayomi source (extension).
type SourceDTO struct {
	// ID is the Suwayomi source identifier (a 64-bit integer serialised as string).
	ID string `json:"id"`
	// Name is the human-readable source name (e.g. "MangaDex").
	Name string `json:"name"`
	// Lang is the BCP-47 content language tag for this source (e.g. "en", "ko").
	Lang string `json:"lang"`
}

// SearchCandidateDTO is one source's search hit within a grouped result.
// Callers use Source and MangaID together to identify the entry uniquely when
// adopting it into the library.
type SearchCandidateDTO struct {
	// Source is the Suwayomi source ID from which this candidate came.
	Source string `json:"source"`
	// SourceName is the human-readable name of the source.
	SourceName string `json:"sourceName"`
	// Lang is the content language of this source.
	Lang string `json:"lang"`
	// MangaID is the Suwayomi-internal manga identifier within this source.
	MangaID int `json:"mangaId"`
	// Title is the manga's display title as returned by the source.
	Title string `json:"title"`
	// URL is the provider-canonical URL for this manga; powers the "View on
	// source" external link. Empty string when the source does not provide one.
	URL string `json:"url"`
	// ThumbnailURL is the cover image URL; empty string when the source does
	// not provide one.
	ThumbnailURL string `json:"thumbnailUrl"`
}

// BrowseResultDTO is one page of a source's catalog browse (Popular/Latest).
// Unlike the grouped search response, browse is single-source and returns a flat
// candidate list in source order plus pagination metadata.
type BrowseResultDTO struct {
	// Manga holds the candidates on this page, in source order.
	Manga []SearchCandidateDTO `json:"manga"`
	// HasNextPage reports whether another page exists (drives FE pagination).
	HasNextPage bool `json:"hasNextPage"`
	// Page is the 1-based page number returned.
	Page int `json:"page"`
}

// SearchGroupDTO bundles all per-source candidates for the same logical series.
type SearchGroupDTO struct {
	// Title is the representative display title chosen by the grouping logic.
	Title string `json:"title"`
	// Candidates holds every source hit that belongs to this group.
	Candidates []SearchCandidateDTO `json:"candidates"`
}

// ChapterInspectDTO is a single chapter entry returned by InspectChapters.
// Number is a pointer because some chapters lack a numeric chapter number
// (e.g. "Special Volume", "One-shot").
type ChapterInspectDTO struct {
	// Number is the parsed chapter number (e.g. 1.5); nil if not available.
	Number *float64 `json:"number"`
	// Name is the chapter's display name (e.g. "Chapter 1").
	Name string `json:"name"`
}

// AdoptProvider identifies one source/manga pair within an adopt request.
// Importance controls the provider priority for this series: higher number =
// higher priority (Tsundoku convention). Callers are responsible for assigning
// unique importances across the providers in a single AdoptRequest.
type AdoptProvider struct {
	// Source is the Suwayomi source ID (e.g. "mangadex").
	Source string
	// MangaID is the Suwayomi-internal manga identifier within Source.
	MangaID int
	// Importance is the provider rank for this series (higher = better).
	Importance int
}

// AdoptRequest groups one or more (source, manga) candidates under a single
// canonical Title and merges them into one Series with N importance-ranked
// providers. Category is optional: when empty ("") the series category defaults
// to Other (the schema default); when non-empty it must be a valid
// Series.category enum value (Manga, Manhwa, Manhua, Comic, Other).
//
// Callers must supply at least one provider; the service assumes len(Providers) >= 1.
type AdoptRequest struct {
	// Title is the canonical series title. All providers are attached to the
	// Series whose slug equals disk.Slugify(Title).
	Title string
	// Category sets the Series.category when non-empty.
	Category string
	// Providers is the ordered list of (source, manga) pairs to adopt. Must
	// have at least one entry (validated by the HTTP handler, not the service).
	Providers []AdoptProvider
}
