package engine

import (
	"fmt"

	"github.com/technobecet/tsundoku/internal/enginetopo"
)

// TopologyStatusDTO is the JSON shape of GET /api/engine/topology-status: the
// captured-topology counts grouped by concern, plus gaps — human-readable notes
// naming what is still missing (empty when nothing is outstanding).
type TopologyStatusDTO struct {
	Repos      int                 `json:"repos"`
	Extensions ExtensionCountsDTO  `json:"extensions"`
	Sources    SourceCountsDTO     `json:"sources"`
	URLs       ProviderURLCountDTO `json:"urls"`
	Gaps       []string            `json:"gaps"`
}

// ExtensionCountsDTO reports harvested extensions vs. how many have their .apk
// bytes cached locally for offline recovery.
type ExtensionCountsDTO struct {
	Total  int `json:"total"`
	Cached int `json:"cached"`
}

// SourceCountsDTO reports the library's live-source universe vs. how many of
// those sources have had their preferences captured.
type SourceCountsDTO struct {
	Total         int `json:"total"`
	PrefsCaptured int `json:"prefsCaptured"`
}

// ProviderURLCountDTO reports how many SeriesProvider urls are resolved vs. still
// fillable (live rows still missing a url).
type ProviderURLCountDTO struct {
	Filled    int `json:"filled"`
	Remaining int `json:"remaining"`
}

// toTopologyStatusDTO maps the enginetopo.Status counts onto the wire DTO and
// derives the human-readable gap notes. gaps is always a non-nil slice so the
// field serializes as [] (never null) — a fully-captured or empty topology
// yields an empty list, not a missing field.
func toTopologyStatusDTO(s enginetopo.Status) TopologyStatusDTO {
	gaps := []string{}
	if missing := s.ExtensionsTotal - s.ExtensionsCached; missing > 0 {
		gaps = append(gaps, fmt.Sprintf("%d extensions not cached", missing))
	}
	if s.URLsRemaining > 0 {
		gaps = append(gaps, fmt.Sprintf("%d provider urls unresolved", s.URLsRemaining))
	}
	if missing := s.SourcesTotal - s.SourcesPrefsCaptured; missing > 0 {
		gaps = append(gaps, fmt.Sprintf("%d sources without captured preferences", missing))
	}

	return TopologyStatusDTO{
		Repos:      s.Repos,
		Extensions: ExtensionCountsDTO{Total: s.ExtensionsTotal, Cached: s.ExtensionsCached},
		Sources:    SourceCountsDTO{Total: s.SourcesTotal, PrefsCaptured: s.SourcesPrefsCaptured},
		URLs:       ProviderURLCountDTO{Filled: s.URLsFilled, Remaining: s.URLsRemaining},
		Gaps:       gaps,
	}
}
