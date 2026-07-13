package metadata

import (
	metadatamodel "github.com/technobecet/tsundoku/internal/metadata"
)

// SearchResultDTO is one provider's search hit — the JSON shape returned by
// GET /api/metadata/search. Mirrors metadatamodel.SearchResult with camelCase
// JSON tags.
type SearchResultDTO struct {
	// Provider is the owning metadata Provider's Key() (e.g. "anilist").
	Provider string `json:"provider"`
	RemoteID string `json:"remoteId"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	CoverURL string `json:"coverUrl"`
	// Year is 0 when unknown.
	Year int `json:"year"`
}

// toSearchResultDTO maps one metadatamodel.SearchResult into its wire DTO.
func toSearchResultDTO(r metadatamodel.SearchResult) SearchResultDTO {
	return SearchResultDTO{
		Provider: r.Provider,
		RemoteID: r.RemoteID,
		Title:    r.Title,
		URL:      r.URL,
		CoverURL: r.CoverURL,
		Year:     r.Year,
	}
}

// toSearchResultDTOs maps a whole Search fan-out result. Always returns a
// non-nil slice so the JSON renders [] rather than null when no provider
// found anything.
func toSearchResultDTOs(results []metadatamodel.SearchResult) []SearchResultDTO {
	out := make([]SearchResultDTO, 0, len(results))
	for _, r := range results {
		out = append(out, toSearchResultDTO(r))
	}
	return out
}

// CoverCandidateDTO is one selectable cover option — the JSON shape returned
// by GET /api/series/:id/metadata/covers. Mirrors metadatamodel.CoverCandidate
// with camelCase JSON tags.
type CoverCandidateDTO struct {
	// SourceKind is "metadata" or "source".
	SourceKind string `json:"sourceKind"`
	// SourceRef is the metadata Provider's Key() when SourceKind is
	// "metadata", or the SeriesProvider UUID string when SourceKind is
	// "source".
	SourceRef string `json:"sourceRef"`
	CoverURL  string `json:"coverUrl"`
	Label     string `json:"label"`
}

// toCoverCandidateDTO maps one metadatamodel.CoverCandidate into its wire DTO.
func toCoverCandidateDTO(c metadatamodel.CoverCandidate) CoverCandidateDTO {
	return CoverCandidateDTO{
		SourceKind: c.SourceKind,
		SourceRef:  c.SourceRef,
		CoverURL:   c.CoverURL,
		Label:      c.Label,
	}
}

// toCoverCandidateDTOs maps a whole cover-candidate gallery. Always returns a
// non-nil slice so the JSON renders [] rather than null.
func toCoverCandidateDTOs(cands []metadatamodel.CoverCandidate) []CoverCandidateDTO {
	out := make([]CoverCandidateDTO, 0, len(cands))
	for _, c := range cands {
		out = append(out, toCoverCandidateDTO(c))
	}
	return out
}
