package sourcethroughput

import (
	"sort"
	"time"

	policy "github.com/technobecet/tsundoku/internal/sourcethroughput"
)

type defaultsDTO struct {
	DownloadConcurrency int    `json:"downloadConcurrency"`
	ImageRequestDelay   string `json:"imageRequestDelay"`
}

type intPolicyDTO struct {
	Override  *int `json:"override"`
	Effective int  `json:"effective"`
}

type durationPolicyDTO struct {
	Override  *string `json:"override"`
	Effective string  `json:"effective"`
}

type sourcePolicyDTO struct {
	SourceID            int64             `json:"sourceId"`
	DownloadConcurrency intPolicyDTO      `json:"downloadConcurrency"`
	ImageRequestDelay   durationPolicyDTO `json:"imageRequestDelay"`
}

type listResponse struct {
	Defaults defaultsDTO       `json:"defaults"`
	Sources  []sourcePolicyDTO `json:"sources"`
}

func newListResponse(defaults policy.Effective, snapshot map[int64]policy.Override) listResponse {
	ids := make([]int64, 0, len(snapshot))
	for id := range snapshot {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	sources := make([]sourcePolicyDTO, 0, len(ids))
	for _, id := range ids {
		sources = append(sources, newSourcePolicyDTO(id, defaults, snapshot[id]))
	}
	return listResponse{
		Defaults: defaultsDTO{DownloadConcurrency: defaults.DownloadConcurrency, ImageRequestDelay: defaults.ImageRequestDelay.String()},
		Sources:  sources,
	}
}

func newSourcePolicyDTO(sourceID int64, defaults policy.Effective, stored policy.Override) sourcePolicyDTO {
	effective := policy.ApplyDefaults(defaults, stored)
	return sourcePolicyDTO{
		SourceID:            sourceID,
		DownloadConcurrency: intPolicyDTO{Override: stored.DownloadConcurrency, Effective: effective.DownloadConcurrency},
		ImageRequestDelay:   durationPolicyDTO{Override: durationString(stored.ImageRequestDelay), Effective: effective.ImageRequestDelay.String()},
	}
}

func durationString(value *time.Duration) *string {
	if value == nil {
		return nil
	}
	formatted := value.String()
	return &formatted
}
