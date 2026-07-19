package reporting

import (
	"time"

	reportingsvc "github.com/technobecet/tsundoku/internal/reporting"
)

// OverviewDTO is the JSON shape of GET /api/reporting/overview: the period
// dashboard — headline KPIs, per-operation breakdown, the slowest + failing
// leaderboards, and a preview of the most recent errors. Every slice is non-nil
// so the JSON is [] (not null) when empty.
type OverviewDTO struct {
	Period         string             `json:"period"`
	Since          time.Time          `json:"since"`
	Kpis           KPIStatsDTO        `json:"kpis"`
	EventsByType   []TypeBreakdownDTO `json:"eventsByType"`
	SlowestSources []SlowSourceDTO    `json:"slowestSources"`
	FailingSources []FailingSourceDTO `json:"failingSources"`
	RecentErrors   []EventDTO         `json:"recentErrors"`
}

// KPIStatsDTO are the headline numbers for the window. successRate is a 0..1
// fraction (the FE renders it as a percentage).
type KPIStatsDTO struct {
	TotalEvents   int     `json:"totalEvents"`
	SuccessEvents int     `json:"successEvents"`
	FailedEvents  int     `json:"failedEvents"`
	SuccessRate   float64 `json:"successRate"`
	ActiveSources int     `json:"activeSources"`
}

// TypeBreakdownDTO is one operation type's success/fail tally — the "search
// works but downloads fail" signal.
type TypeBreakdownDTO struct {
	EventType string `json:"eventType"`
	Total     int    `json:"total"`
	Success   int    `json:"success"`
	Failed    int    `json:"failed"`
}

// SlowSourceDTO is one entry of the slowest-sources leaderboard (from the rolling
// EWMA snapshot, already ranked slowest-first).
type SlowSourceDTO struct {
	SourceKey     string `json:"sourceKey"`
	SourceName    string `json:"sourceName"`
	EwmaLatencyMs int    `json:"ewmaLatencyMs"`
}

// FailingSourceDTO is one entry of the currently-failing leaderboard (from the
// breaker state — authoritative "erroring since when").
type FailingSourceDTO struct {
	SourceKey           string     `json:"sourceKey"`
	FailingSince        *time.Time `json:"failingSince,omitempty"`
	ConsecutiveFailures int        `json:"consecutiveFailures"`
	LastError           string     `json:"lastError"`
	CooldownUntil       *time.Time `json:"cooldownUntil,omitempty"`
	IsCoolingDown       bool       `json:"isCoolingDown"`
}

// EventDTO is one audit-log row (camelCase mirror of ent.SourceEvent). The three
// pointer fields are omitted when the column is NULL (a success has no error; a
// warm has no items count). metadata is always a (possibly empty) object.
type EventDTO struct {
	ID            string            `json:"id"`
	SourceKey     string            `json:"sourceKey"`
	SourceID      string            `json:"sourceId"`
	SourceName    string            `json:"sourceName"`
	Language      string            `json:"language"`
	EventType     string            `json:"eventType"`
	Status        string            `json:"status"`
	DurationMs    int64             `json:"durationMs"`
	ErrorMessage  *string           `json:"errorMessage,omitempty"`
	ErrorCategory *string           `json:"errorCategory,omitempty"`
	ItemsCount    *int              `json:"itemsCount,omitempty"`
	Metadata      map[string]string `json:"metadata"`
	CreatedAt     time.Time         `json:"createdAt"`
}

// SourceReportDTO is one source's rollup for the window: identity, overall +
// per-operation tallies, joined latency, and the breaker's failure streak.
type SourceReportDTO struct {
	SourceKey           string             `json:"sourceKey"`
	SourceID            string             `json:"sourceId"`
	SourceName          string             `json:"sourceName"`
	Language            string             `json:"language"`
	TotalEvents         int                `json:"totalEvents"`
	SuccessEvents       int                `json:"successEvents"`
	FailedEvents        int                `json:"failedEvents"`
	SuccessRate         float64            `json:"successRate"`
	ByType              []TypeBreakdownDTO `json:"byType"`
	EwmaLatencyMs       int                `json:"ewmaLatencyMs"`
	LastLatencyMs       int                `json:"lastLatencyMs"`
	FailingSince        *time.Time         `json:"failingSince,omitempty"`
	ConsecutiveFailures int                `json:"consecutiveFailures"`
	LastError           string             `json:"lastError"`
	CooldownUntil       *time.Time         `json:"cooldownUntil,omitempty"`
	IsCoolingDown       bool               `json:"isCoolingDown"`
}

// SourceEventListDTO is a page of the event feed plus the total matching count,
// so the FE can paginate without a second call. items is always non-nil.
type SourceEventListDTO struct {
	Total int        `json:"total"`
	Items []EventDTO `json:"items"`
}

// SourceTimelineDTO is the bucketed success/fail series behind the stacked
// histogram. buckets is always non-nil, ordered ascending by bucket start.
type SourceTimelineDTO struct {
	Buckets []TimelineBucketDTO `json:"buckets"`
}

// TimelineBucketDTO is one time slot's tally. bucket is the slot's start.
type TimelineBucketDTO struct {
	Bucket  time.Time `json:"bucket"`
	Success int       `json:"success"`
	Failed  int       `json:"failed"`
	Total   int       `json:"total"`
}

// toOverviewDTO maps the service overview report to its DTO, forcing every slice
// non-nil.
func toOverviewDTO(r reportingsvc.OverviewReport) OverviewDTO {
	return OverviewDTO{
		Period: string(r.Period),
		Since:  r.Since,
		Kpis: KPIStatsDTO{
			TotalEvents:   r.KPIs.TotalEvents,
			SuccessEvents: r.KPIs.SuccessEvents,
			FailedEvents:  r.KPIs.FailedEvents,
			SuccessRate:   r.KPIs.SuccessRate,
			ActiveSources: r.KPIs.ActiveSources,
		},
		EventsByType:   toTypeBreakdownDTOs(r.EventsByType),
		SlowestSources: toSlowSourceDTOs(r.SlowestSources),
		FailingSources: toFailingSourceDTOs(r.FailingSources),
		RecentErrors:   toEventDTOs(r.RecentErrors),
	}
}

// toTypeBreakdownDTOs maps the per-type tallies, always returning a non-nil slice.
func toTypeBreakdownDTOs(in []reportingsvc.TypeBreakdown) []TypeBreakdownDTO {
	out := make([]TypeBreakdownDTO, 0, len(in))
	for _, t := range in {
		out = append(out, TypeBreakdownDTO{
			EventType: t.EventType.String(),
			Total:     t.Total,
			Success:   t.Success,
			Failed:    t.Failed,
		})
	}
	return out
}

// toSlowSourceDTOs maps the slowest leaderboard, always non-nil.
func toSlowSourceDTOs(in []reportingsvc.SlowSource) []SlowSourceDTO {
	out := make([]SlowSourceDTO, 0, len(in))
	for _, s := range in {
		out = append(out, SlowSourceDTO{SourceKey: s.SourceKey, SourceName: s.SourceName, EwmaLatencyMs: s.EwmaLatencyMs})
	}
	return out
}

// toFailingSourceDTOs maps the failing leaderboard, always non-nil.
func toFailingSourceDTOs(in []reportingsvc.FailingSource) []FailingSourceDTO {
	out := make([]FailingSourceDTO, 0, len(in))
	for _, f := range in {
		out = append(out, FailingSourceDTO{
			SourceKey:           f.SourceKey,
			FailingSince:        f.FailingSince,
			ConsecutiveFailures: f.ConsecutiveFailures,
			LastError:           f.LastError,
			CooldownUntil:       f.CooldownUntil,
			IsCoolingDown:       f.IsCoolingDown,
		})
	}
	return out
}

// toEventDTO maps one service event record to its DTO, normalizing a nil metadata
// map to an empty object.
func toEventDTO(e reportingsvc.EventRecord) EventDTO {
	meta := e.Metadata
	if meta == nil {
		meta = map[string]string{}
	}
	return EventDTO{
		ID:            e.ID.String(),
		SourceKey:     e.SourceKey,
		SourceID:      e.SourceID,
		SourceName:    e.SourceName,
		Language:      e.Language,
		EventType:     e.EventType.String(),
		Status:        e.Status.String(),
		DurationMs:    e.DurationMs,
		ErrorMessage:  e.ErrorMessage,
		ErrorCategory: e.ErrorCategory,
		ItemsCount:    e.ItemsCount,
		Metadata:      meta,
		CreatedAt:     e.CreatedAt,
	}
}

// toEventDTOs maps a slice of event records, always returning a non-nil slice.
func toEventDTOs(in []reportingsvc.EventRecord) []EventDTO {
	out := make([]EventDTO, 0, len(in))
	for _, e := range in {
		out = append(out, toEventDTO(e))
	}
	return out
}

// toEventListDTO maps a feed page to its DTO (total + non-nil items).
func toEventListDTO(f reportingsvc.EventFeed) SourceEventListDTO {
	return SourceEventListDTO{Total: f.Total, Items: toEventDTOs(f.Items)}
}

// toSourceReportDTOs maps the per-source rollup, computing successRate per row
// and always returning a non-nil slice.
func toSourceReportDTOs(in []reportingsvc.SourceReport) []SourceReportDTO {
	out := make([]SourceReportDTO, 0, len(in))
	for _, r := range in {
		out = append(out, SourceReportDTO{
			SourceKey:           r.SourceKey,
			SourceID:            r.SourceID,
			SourceName:          r.SourceName,
			Language:            r.Language,
			TotalEvents:         r.TotalEvents,
			SuccessEvents:       r.SuccessEvents,
			FailedEvents:        r.FailedEvents,
			SuccessRate:         successRate(r.SuccessEvents, r.TotalEvents),
			ByType:              toTypeBreakdownDTOs(r.ByType),
			EwmaLatencyMs:       r.EwmaLatencyMs,
			LastLatencyMs:       r.LastLatencyMs,
			FailingSince:        r.FailingSince,
			ConsecutiveFailures: r.ConsecutiveFailures,
			LastError:           r.LastError,
			CooldownUntil:       r.CooldownUntil,
			IsCoolingDown:       r.IsCoolingDown,
		})
	}
	return out
}

// toTimelineDTO maps the bucket series to its DTO (non-nil buckets).
func toTimelineDTO(in []reportingsvc.TimelineBucket) SourceTimelineDTO {
	out := make([]TimelineBucketDTO, 0, len(in))
	for _, b := range in {
		out = append(out, TimelineBucketDTO{Bucket: b.Bucket, Success: b.Success, Failed: b.Failed, Total: b.Total})
	}
	return SourceTimelineDTO{Buckets: out}
}

// successRate returns success/total as a 0..1 fraction (0 when total is 0),
// mirroring the service's own statusCounts.successRate for the per-source row.
func successRate(success, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(success) / float64(total)
}
