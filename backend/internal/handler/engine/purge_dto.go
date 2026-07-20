package engine

import "github.com/technobecet/tsundoku/internal/sourcepurge"

// PurgeSourceRequest is the JSON body of POST /api/engine/purge-source. Both
// fields are required: sourceId keys the metric row + live providers, sourceName
// keys the breaker row + disk-reconciled providers.
type PurgeSourceRequest struct {
	SourceID   string `json:"sourceId"`
	SourceName string `json:"sourceName"`
}

// PurgeExtensionRequest is the JSON body of POST /api/engine/purge-extension.
type PurgeExtensionRequest struct {
	PkgName string `json:"pkgName"`
}

// SourceSummaryDTO is the camelCase wire shape of a completed source purge — the
// exact counts of Tsundoku DB rows removed (never a CBZ or Chapter row).
type SourceSummaryDTO struct {
	SourceID         string `json:"sourceId"`
	SourceName       string `json:"sourceName"`
	SeriesAffected   int    `json:"seriesAffected"`
	ProvidersRemoved int    `json:"providersRemoved"`
	ChaptersDeleted  int    `json:"chaptersDeleted"`
	MetricsDeleted   int    `json:"metricsDeleted"`
	BreakerCleared   int    `json:"breakerCleared"`
}

// SourcePreviewDTO is the camelCase wire shape of a source purge dry run.
type SourcePreviewDTO struct {
	SourceID         string `json:"sourceId"`
	SourceName       string `json:"sourceName"`
	SeriesAffected   int    `json:"seriesAffected"`
	Providers        int    `json:"providers"`
	ProviderChapters int    `json:"providerChapters"`
	ChaptersDeleted  int    `json:"chaptersDeleted"`
	Metrics          int    `json:"metrics"`
	Breaker          int    `json:"breaker"`
}

// ExtensionSummaryDTO is the camelCase wire shape of a completed extension purge,
// aggregated across the extension's sources with a per-source breakdown.
type ExtensionSummaryDTO struct {
	PkgName          string             `json:"pkgName"`
	Sources          []SourceSummaryDTO `json:"sources"`
	SeriesAffected   int                `json:"seriesAffected"`
	ProvidersRemoved int                `json:"providersRemoved"`
	ChaptersDeleted  int                `json:"chaptersDeleted"`
	MetricsDeleted   int                `json:"metricsDeleted"`
	BreakerCleared   int                `json:"breakerCleared"`
	Errors           []string           `json:"errors"`
}

// ExtensionPreviewDTO is the camelCase wire shape of an extension purge dry run.
type ExtensionPreviewDTO struct {
	PkgName          string             `json:"pkgName"`
	Sources          []SourcePreviewDTO `json:"sources"`
	SeriesAffected   int                `json:"seriesAffected"`
	Providers        int                `json:"providers"`
	ProviderChapters int                `json:"providerChapters"`
	ChaptersDeleted  int                `json:"chaptersDeleted"`
	Metrics          int                `json:"metrics"`
	Breaker          int                `json:"breaker"`
}

// toSourceSummaryDTO maps a domain SourceSummary to its wire DTO.
func toSourceSummaryDTO(s sourcepurge.SourceSummary) SourceSummaryDTO {
	return SourceSummaryDTO{
		SourceID:         s.SourceID,
		SourceName:       s.SourceName,
		SeriesAffected:   s.SeriesAffected,
		ProvidersRemoved: s.ProvidersRemoved,
		ChaptersDeleted:  s.ChaptersDeleted,
		MetricsDeleted:   s.MetricsDeleted,
		BreakerCleared:   s.BreakerCleared,
	}
}

// toSourcePreviewDTO maps a domain SourcePreview to its wire DTO.
func toSourcePreviewDTO(p sourcepurge.SourcePreview) SourcePreviewDTO {
	return SourcePreviewDTO{
		SourceID:         p.SourceID,
		SourceName:       p.SourceName,
		SeriesAffected:   p.SeriesAffected,
		Providers:        p.Providers,
		ProviderChapters: p.ProviderChapters,
		ChaptersDeleted:  p.ChaptersDeleted,
		Metrics:          p.Metrics,
		Breaker:          p.Breaker,
	}
}

// toExtensionSummaryDTO maps a domain ExtensionSummary to its wire DTO. The
// per-source slice and the errors slice are always non-nil so the JSON renders
// [] (not null) for an empty list.
func toExtensionSummaryDTO(s sourcepurge.ExtensionSummary) ExtensionSummaryDTO {
	sources := make([]SourceSummaryDTO, 0, len(s.Sources))
	for _, src := range s.Sources {
		sources = append(sources, toSourceSummaryDTO(src))
	}
	errs := s.Errors
	if errs == nil {
		errs = []string{}
	}
	return ExtensionSummaryDTO{
		PkgName:          s.PkgName,
		Sources:          sources,
		SeriesAffected:   s.SeriesAffected,
		ProvidersRemoved: s.ProvidersRemoved,
		ChaptersDeleted:  s.ChaptersDeleted,
		MetricsDeleted:   s.MetricsDeleted,
		BreakerCleared:   s.BreakerCleared,
		Errors:           errs,
	}
}

// toExtensionPreviewDTO maps a domain ExtensionPreview to its wire DTO (non-nil
// per-source slice).
func toExtensionPreviewDTO(p sourcepurge.ExtensionPreview) ExtensionPreviewDTO {
	sources := make([]SourcePreviewDTO, 0, len(p.Sources))
	for _, src := range p.Sources {
		sources = append(sources, toSourcePreviewDTO(src))
	}
	return ExtensionPreviewDTO{
		PkgName:          p.PkgName,
		Sources:          sources,
		SeriesAffected:   p.SeriesAffected,
		Providers:        p.Providers,
		ProviderChapters: p.ProviderChapters,
		ChaptersDeleted:  p.ChaptersDeleted,
		Metrics:          p.Metrics,
		Breaker:          p.Breaker,
	}
}
