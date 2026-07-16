package engine

import (
	"fmt"
	"strings"

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
// those sources have had their preferences captured, plus the positive
// reached/failed read outcomes recorded per source by the seed.
//
// PrefsCaptured (≥1 stored preference) and Reached (the read succeeded) differ:
// a source can be reached yet carry zero non-default preferences (benign). Failed
// counts sources whose read errored (a real gap); FailedSources names them
// (always non-nil so it serializes as [] never null).
type SourceCountsDTO struct {
	Total         int      `json:"total"`
	PrefsCaptured int      `json:"prefsCaptured"`
	Reached       int      `json:"reached"`
	Failed        int      `json:"failed"`
	FailedSources []string `json:"failedSources"`
}

// toTopologyStatusDTO maps the enginetopo.Status counts onto the wire DTO and
// derives the human-readable gap notes. gaps is always a non-nil slice so the
// field serializes as [] (never null) — a fully-captured or empty topology
// yields an empty list, not a missing field.
func toTopologyStatusDTO(s enginetopo.Status) TopologyStatusDTO {
	gaps := []string{}
	// Defensive non-nil: enginetopo.TopologyStatus always returns a non-nil slice,
	// but a hand-built Status must still serialize failedSources as [] not null.
	failedSources := s.FailedSources
	if failedSources == nil {
		failedSources = []string{}
	}
	if missing := s.ExtensionsTotal - s.ExtensionsCached; missing > 0 {
		gaps = append(gaps, fmt.Sprintf("%d extensions not cached", missing))
	}
	// A REAL gap: sources whose preference READ failed (not merely a missing-count
	// inferred from sources-without-a-stored-pref, which conflates benign-empty
	// with read-failed). Emitted only when at least one source actually failed.
	if s.SourcesFailed > 0 {
		note := fmt.Sprintf("%d sources' preferences could not be read", s.SourcesFailed)
		if len(s.FailedSources) > 0 {
			note += ": " + strings.Join(s.FailedSources, ", ")
		}
		gaps = append(gaps, note)
	}

	return TopologyStatusDTO{
		Repos:      s.Repos,
		Extensions: ExtensionCountsDTO{Total: s.ExtensionsTotal, Cached: s.ExtensionsCached},
		Sources: SourceCountsDTO{
			Total:         s.SourcesTotal,
			PrefsCaptured: s.SourcesPrefsCaptured,
			Reached:       s.SourcesReached,
			Failed:        s.SourcesFailed,
			FailedSources: failedSources,
		},
		Gaps: gaps,
	}
}
