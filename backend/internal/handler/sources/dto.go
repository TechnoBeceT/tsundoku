package sources

import (
	"time"

	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/metrics"
)

// SourceMetricDTO is the JSON shape of one source's rolling performance snapshot
// (camelCase mirror of ent.SourceMetric) plus the derived isSlow flag. The three
// timestamps are optional (omitted when the source has never errored / succeeded /
// been warmed).
type SourceMetricDTO struct {
	SourceID      string     `json:"sourceId"`
	SourceName    string     `json:"sourceName"`
	EwmaLatencyMs int        `json:"ewmaLatencyMs"`
	LastLatencyMs int        `json:"lastLatencyMs"`
	SearchCount   int        `json:"searchCount"`
	SuccessCount  int        `json:"successCount"`
	FailCount     int        `json:"failCount"`
	LastError     string     `json:"lastError"`
	LastErrorAt   *time.Time `json:"lastErrorAt,omitempty"`
	LastSuccessAt *time.Time `json:"lastSuccessAt,omitempty"`
	LastWarmedAt  *time.Time `json:"lastWarmedAt,omitempty"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	// IsSlow is derived at read time (ewma > threshold), never stored — so the
	// threshold can be re-tuned without a migration.
	IsSlow bool `json:"isSlow"`
}

// WarmResultDTO is the JSON body of POST /api/sources/warmup: how many sources
// were warmed successfully in the pass.
type WarmResultDTO struct {
	Warmed int `json:"warmed"`
}

// toSourceMetricDTO maps one ent.SourceMetric to its DTO, computing isSlow
// against thresholdMs. It is the SINGLE mapper the metrics endpoint routes
// through, so no field is dropped.
func toSourceMetricDTO(m *ent.SourceMetric, thresholdMs int) SourceMetricDTO {
	return SourceMetricDTO{
		SourceID:      m.SourceID,
		SourceName:    m.SourceName,
		EwmaLatencyMs: m.EwmaLatencyMs,
		LastLatencyMs: m.LastLatencyMs,
		SearchCount:   m.SearchCount,
		SuccessCount:  m.SuccessCount,
		FailCount:     m.FailCount,
		LastError:     m.LastError,
		LastErrorAt:   m.LastErrorAt,
		LastSuccessAt: m.LastSuccessAt,
		LastWarmedAt:  m.LastWarmedAt,
		UpdatedAt:     m.UpdatedAt,
		IsSlow:        metrics.IsSlow(m, thresholdMs),
	}
}

// toSourceMetricDTOs maps a slice of metrics through toSourceMetricDTO. The
// result is always non-nil so the JSON body is [] (not null) for an empty list.
func toSourceMetricDTOs(rows []*ent.SourceMetric, thresholdMs int) []SourceMetricDTO {
	out := make([]SourceMetricDTO, 0, len(rows))
	for _, m := range rows {
		out = append(out, toSourceMetricDTO(m, thresholdMs))
	}
	return out
}
