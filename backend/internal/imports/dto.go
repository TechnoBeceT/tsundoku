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
	// ThumbnailURL is the cover image URL; empty string when the source does
	// not provide one.
	ThumbnailURL string `json:"thumbnailUrl"`
}

// SearchGroupDTO bundles all per-source candidates for the same logical series.
// The Title is the representative title — the longest raw title among all
// candidates in the group (from the matcher).
type SearchGroupDTO struct {
	// Title is the representative title for this logical series group.
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
