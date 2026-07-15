package engine

import (
	"fmt"

	"github.com/technobecet/tsundoku/internal/enginetopo"
)

// TopologyStatusDTO is the JSON shape of GET /api/engine/topology-status: the
// captured-topology counts grouped by concern, plus gaps — human-readable notes
// naming what is still missing (empty when nothing is outstanding).
//
// (QCAT-253, P2 Suwayomi-removal slice 5): the `urls` group is RETIRED along
// with the SeriesProvider.url backfill pass it reported on — see
// enginetopo.Status's doc comment.
type TopologyStatusDTO struct {
	Repos      int                `json:"repos"`
	Extensions ExtensionCountsDTO `json:"extensions"`
	Sources    SourceCountsDTO    `json:"sources"`
	Gaps       []string           `json:"gaps"`
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

// toTopologyStatusDTO maps the enginetopo.Status counts onto the wire DTO and
// derives the human-readable gap notes. gaps is always a non-nil slice so the
// field serializes as [] (never null) — a fully-captured or empty topology
// yields an empty list, not a missing field.
func toTopologyStatusDTO(s enginetopo.Status) TopologyStatusDTO {
	gaps := []string{}
	if missing := s.ExtensionsTotal - s.ExtensionsCached; missing > 0 {
		gaps = append(gaps, fmt.Sprintf("%d extensions not cached", missing))
	}
	if missing := s.SourcesTotal - s.SourcesPrefsCaptured; missing > 0 {
		gaps = append(gaps, fmt.Sprintf("%d sources without captured preferences", missing))
	}

	return TopologyStatusDTO{
		Repos:      s.Repos,
		Extensions: ExtensionCountsDTO{Total: s.ExtensionsTotal, Cached: s.ExtensionsCached},
		Sources:    SourceCountsDTO{Total: s.SourcesTotal, PrefsCaptured: s.SourcesPrefsCaptured},
		Gaps:       gaps,
	}
}
