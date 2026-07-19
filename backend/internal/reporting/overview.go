package reporting

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/technobecet/tsundoku/internal/ent"
	entsourceevent "github.com/technobecet/tsundoku/internal/ent/sourceevent"
)

// OverviewReport is the period dashboard: headline KPIs, the per-operation
// (event-type) breakdown, the slowest-source and currently-failing-source
// leaderboards, and a preview of the most recent errors. Every slice is non-nil.
type OverviewReport struct {
	Period         Period
	Since          time.Time
	KPIs           KPIStats
	EventsByType   []TypeBreakdown
	SlowestSources []SlowSource
	FailingSources []FailingSource
	RecentErrors   []EventRecord
}

// KPIStats are the headline numbers for the window: how many source operations
// ran, how many succeeded / failed, the success fraction (0..1), and how many
// distinct sources were active.
type KPIStats struct {
	TotalEvents   int
	SuccessEvents int
	FailedEvents  int
	SuccessRate   float64
	ActiveSources int
}

// TypeBreakdown is one operation type's success/fail tally for the window — the
// "search works but downloads fail" signal, one row per event_type present.
type TypeBreakdown struct {
	EventType entsourceevent.EventType
	Total     int
	Success   int
	Failed    int
}

// SlowSource is one entry of the slowest-sources leaderboard, taken from the
// rolling EWMA metric snapshot (not the event log). The list is already ranked
// by EwmaLatencyMs (slowest first), so there is no separate isSlow flag here —
// the whole leaderboard IS the slow tail. The threshold-derived isSlow flag
// lives on GET /api/sources/metrics, which owns the settings threshold.
type SlowSource struct {
	SourceKey     string
	SourceName    string
	EwmaLatencyMs int
}

// FailingSource is one entry of the currently-failing leaderboard, taken from
// the circuit-breaker state (not the event log): the authoritative "erroring
// since when" without a log scan.
type FailingSource struct {
	SourceKey           string
	FailingSince        *time.Time
	ConsecutiveFailures int
	LastError           string
	CooldownUntil       *time.Time
	IsCoolingDown       bool
}

// typeStatusRow is the scan target for the (event_type, status) count aggregate
// that powers BOTH the KPI totals and the per-type breakdown from one query.
type typeStatusRow struct {
	EventType entsourceevent.EventType `json:"event_type"`
	Status    entsourceevent.Status    `json:"status"`
	Count     int                      `json:"count"`
}

// Overview builds the period dashboard. It runs a fixed, small number of queries
// regardless of how many events the window holds: ONE grouped (event_type,
// status) aggregate (KPIs + per-type breakdown), ONE distinct-source-key query
// (active-source count), the metrics snapshot (slowest), the breaker snapshot
// (failing), and ONE bounded recent-errors page. Nothing scales with the row
// count — the event totals come out of the DB already summed.
func (s *Service) Overview(ctx context.Context, period Period, now time.Time) (OverviewReport, error) {
	since := period.Since(now)

	byType, kpis, err := s.overviewCounts(ctx, since)
	if err != nil {
		return OverviewReport{}, err
	}

	active, err := s.activeSourceCount(ctx, since)
	if err != nil {
		return OverviewReport{}, err
	}
	kpis.ActiveSources = active

	slowest, err := s.slowestSources(ctx, now)
	if err != nil {
		return OverviewReport{}, err
	}

	failing, err := s.failingSources(ctx, now)
	if err != nil {
		return OverviewReport{}, err
	}

	failed := entsourceevent.StatusFailed
	recent, err := s.Events(ctx, EventFilter{
		SourceKey: AllSourcesKey,
		Status:    &failed,
		Since:     since,
		Limit:     recentErrorsLimit,
	})
	if err != nil {
		return OverviewReport{}, err
	}

	return OverviewReport{
		Period:         period,
		Since:          since,
		KPIs:           kpis,
		EventsByType:   byType,
		SlowestSources: slowest,
		FailingSources: failing,
		RecentErrors:   recent.Items,
	}, nil
}

// overviewCounts runs the single (event_type, status) grouped aggregate and folds
// it into BOTH the per-type breakdown (sorted by total desc, then type) and the
// overall KPI tally — no second round-trip for the totals.
func (s *Service) overviewCounts(ctx context.Context, since time.Time) ([]TypeBreakdown, KPIStats, error) {
	var rows []typeStatusRow
	err := s.client.SourceEvent.Query().
		Where(entsourceevent.CreatedAtGTE(since)).
		GroupBy(entsourceevent.FieldEventType, entsourceevent.FieldStatus).
		Aggregate(ent.As(ent.Count(), "count")).
		Scan(ctx, &rows)
	if err != nil {
		return nil, KPIStats{}, fmt.Errorf("reporting.Overview: type/status aggregate: %w", err)
	}

	perType := make(map[entsourceevent.EventType]*statusCounts)
	var overall statusCounts
	for _, r := range rows {
		overall.add(r.Status, r.Count)
		c := perType[r.EventType]
		if c == nil {
			c = &statusCounts{}
			perType[r.EventType] = c
		}
		c.add(r.Status, r.Count)
	}

	byType := make([]TypeBreakdown, 0, len(perType))
	for t, c := range perType {
		byType = append(byType, TypeBreakdown{EventType: t, Total: c.Total, Success: c.Success, Failed: c.Failed})
	}
	sort.Slice(byType, func(i, j int) bool {
		if byType[i].Total != byType[j].Total {
			return byType[i].Total > byType[j].Total
		}
		return byType[i].EventType < byType[j].EventType
	})

	return byType, KPIStats{
		TotalEvents:   overall.Total,
		SuccessEvents: overall.Success,
		FailedEvents:  overall.Failed,
		SuccessRate:   overall.successRate(),
	}, nil
}

// activeSourceCount returns how many DISTINCT sources produced any event in the
// window — one GroupBy(source_key) that returns the distinct keys (the count is
// their number), never a per-source query.
func (s *Service) activeSourceCount(ctx context.Context, since time.Time) (int, error) {
	keys, err := s.client.SourceEvent.Query().
		Where(entsourceevent.CreatedAtGTE(since)).
		GroupBy(entsourceevent.FieldSourceKey).
		Strings(ctx)
	if err != nil {
		return 0, fmt.Errorf("reporting.Overview: active-source count: %w", err)
	}
	return len(keys), nil
}

// slowestSources reads the rolling metric snapshot (already ordered slowest-first
// by metrics.List) and returns the top slowestLimit entries, each with its
// derived isSlow flag. It is deliberately NOT event-log-derived — latency is an
// EWMA the metrics side already maintains.
func (s *Service) slowestSources(ctx context.Context, now time.Time) ([]SlowSource, error) {
	rows, err := s.metrics.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("reporting.Overview: metrics list: %w", err)
	}
	out := make([]SlowSource, 0, slowestLimit)
	for _, m := range rows {
		if len(out) >= slowestLimit {
			break
		}
		out = append(out, SlowSource{
			SourceKey:     trimKey(m.SourceName),
			SourceName:    m.SourceName,
			EwmaLatencyMs: m.EwmaLatencyMs,
		})
	}
	return out, nil
}

// failingSources reads the breaker snapshot and returns every source currently in
// a failure streak (failing_since set), longest-failing first. This is the "a
// source broke, I need to KNOW" leaderboard — authoritative and log-scan-free.
func (s *Service) failingSources(ctx context.Context, now time.Time) ([]FailingSource, error) {
	breakers, err := s.gate.Snapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("reporting.Overview: breaker snapshot: %w", err)
	}
	out := make([]FailingSource, 0)
	for key, b := range breakers {
		if b.FailingSince == nil {
			continue
		}
		out = append(out, FailingSource{
			SourceKey:           key,
			FailingSince:        b.FailingSince,
			ConsecutiveFailures: b.ConsecutiveFailures,
			LastError:           b.LastError,
			CooldownUntil:       b.CooldownUntil,
			IsCoolingDown:       b.IsCoolingDown(now),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].FailingSince.Before(*out[j].FailingSince)
	})
	return out, nil
}
