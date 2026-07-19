package sources

import (
	"strings"
	"time"

	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/metrics"
	"github.com/technobecet/tsundoku/internal/sourcegate"
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
	// Breaker is this source's anti-ban circuit-breaker state, joined in from the
	// sourcegate store by source name. It is null (omitted) when the source has no
	// breaker row — the common healthy case. When present, isCoolingDown says
	// whether the breaker is currently refusing background fetches.
	Breaker *BreakerDTO `json:"breaker,omitempty"`
}

// BreakerDTO is one source's persisted circuit-breaker state, surfaced next to
// its latency/reliability stats so the owner sees WHY a source is being refused
// (an anti-ban cooldown after repeated failures) right where they already look.
// The derived isCoolingDown reflects whether the cooldown is still in the future.
type BreakerDTO struct {
	// ConsecutiveFailures is how many gated fetches failed in a row.
	ConsecutiveFailures int `json:"consecutiveFailures"`
	// CooldownUntil is when the tripped breaker reopens; omitted when not tripped.
	CooldownUntil *time.Time `json:"cooldownUntil,omitempty"`
	// FailingSince marks the start of the current failure streak; omitted when the
	// source is not currently failing. Answers "erroring since when" without an
	// event-log scan — it drives the Source Health Console + Slice 5 alerting.
	FailingSince *time.Time `json:"failingSince,omitempty"`
	// LastError is the most recent gated-fetch failure reason ("" when none).
	LastError string `json:"lastError"`
	// IsCoolingDown is derived at read time: cooldown set and still in the future.
	IsCoolingDown bool `json:"isCoolingDown"`
}

// BreakerResetDTO is the response to POST /api/sources/:sourceId/reset-breaker:
// the source's identity plus its breaker state AFTER the reset (§16 round-trip).
// Breaker is null on a clean reset (the row was cleared), so the owner's UI can
// confirm the source is no longer cooling down.
type BreakerResetDTO struct {
	SourceID   string      `json:"sourceId"`
	SourceName string      `json:"sourceName"`
	Breaker    *BreakerDTO `json:"breaker,omitempty"`
}

// toBreakerDTO maps a sourcegate.BreakerState to its DTO, computing isCoolingDown
// against now. It is the SINGLE breaker mapper both the metrics join and the reset
// round-trip route through, so the two surfaces never drift.
func toBreakerDTO(b sourcegate.BreakerState, now time.Time) BreakerDTO {
	return BreakerDTO{
		ConsecutiveFailures: b.ConsecutiveFailures,
		CooldownUntil:       b.CooldownUntil,
		FailingSince:        b.FailingSince,
		LastError:           b.LastError,
		IsCoolingDown:       b.IsCoolingDown(now),
	}
}

// WarmStartedDTO is the JSON body of POST /api/sources/warmup: an acknowledgement
// that the warm-up pass has been KICKED OFF in the background. It always reports
// started=true — the pass runs detached (it takes minutes over slow anti-bot
// sources) and its per-source outcome surfaces in GET /api/sources/metrics
// (lastWarmedAt / lastError), not in this response.
type WarmStartedDTO struct {
	Started bool `json:"started"`
}

// toSourceMetricDTO maps one ent.SourceMetric to its DTO, computing isSlow
// against thresholdMs and joining in the breaker state keyed by the source's
// trimmed display NAME (the SAME key sourcegate trips — see gateKey /
// canonicalSourceKey). It is the SINGLE mapper the metrics endpoint routes
// through, so no field is dropped.
func toSourceMetricDTO(m *ent.SourceMetric, thresholdMs int, breakers map[string]sourcegate.BreakerState, now time.Time) SourceMetricDTO {
	dto := SourceMetricDTO{
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
	if b, ok := breakers[strings.TrimSpace(m.SourceName)]; ok {
		bd := toBreakerDTO(b, now)
		dto.Breaker = &bd
	}
	return dto
}

// toSourceMetricDTOs maps a slice of metrics through toSourceMetricDTO, joining
// each against the shared breaker snapshot (one batch read, no per-row query).
// The result is always non-nil so the JSON body is [] (not null) for an empty
// list.
func toSourceMetricDTOs(rows []*ent.SourceMetric, thresholdMs int, breakers map[string]sourcegate.BreakerState, now time.Time) []SourceMetricDTO {
	out := make([]SourceMetricDTO, 0, len(rows))
	for _, m := range rows {
		out = append(out, toSourceMetricDTO(m, thresholdMs, breakers, now))
	}
	return out
}
