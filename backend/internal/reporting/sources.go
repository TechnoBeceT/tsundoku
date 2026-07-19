package reporting

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/technobecet/tsundoku/internal/ent"
	entsourceevent "github.com/technobecet/tsundoku/internal/ent/sourceevent"
)

// SourceReport is one source's rollup for the window: identity, overall + per-
// operation success/fail tallies (from the event log), the rolling search
// latency (joined from the metrics snapshot), and the current failure streak
// (joined from the breaker). ByType is one entry per event_type the source
// produced, sorted by total desc.
type SourceReport struct {
	SourceKey     string
	SourceID      string
	SourceName    string
	Language      string
	TotalEvents   int
	SuccessEvents int
	FailedEvents  int
	ByType        []TypeBreakdown

	EwmaLatencyMs int
	LastLatencyMs int

	FailingSince        *time.Time
	ConsecutiveFailures int
	LastError           string
	CooldownUntil       *time.Time
	IsCoolingDown       bool
}

// sourceTypeStatusRow is the scan target for the per-source rollup aggregate:
// one row per (source_key, event_type, status) group with its count.
type sourceTypeStatusRow struct {
	SourceKey string                   `json:"source_key"`
	EventType entsourceevent.EventType `json:"event_type"`
	Status    entsourceevent.Status    `json:"status"`
	Count     int                      `json:"count"`
}

// sourceIdentityRow is the scan target for the identity aggregate: the freshest
// display name / id / language captured for each source_key in the window (a
// source_key is a NAME, but the id/language/exact-casing are captured per event,
// so the most recent event's values are the best representative).
type sourceIdentityRow struct {
	SourceKey  string    `json:"source_key"`
	SourceID   string    `json:"source_id"`
	SourceName string    `json:"source_name"`
	Language   string    `json:"language"`
	LastSeen   time.Time `json:"last_seen"`
}

// Sources builds the per-source rollup for the window, sorted per sort. It runs a
// FIXED number of queries regardless of how many sources or events exist: ONE
// grouped (source_key, event_type, status) aggregate (all the counts), ONE
// identity aggregate (freshest name/id/language per source), plus the metrics and
// breaker snapshots joined in memory by canonical key. Nothing scales with the
// event row count — the DB returns the counts already summed.
func (s *Service) Sources(ctx context.Context, period Period, order Sort, now time.Time) ([]SourceReport, error) {
	since := period.Since(now)

	rollups, err := s.sourceRollups(ctx, since)
	if err != nil {
		return nil, err
	}

	identity, err := s.sourceIdentities(ctx, since)
	if err != nil {
		return nil, err
	}

	metricRows, err := s.metrics.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("reporting.Sources: metrics list: %w", err)
	}
	metricsMap := metricsByKey(metricRows)

	breakers, err := s.gate.Snapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("reporting.Sources: breaker snapshot: %w", err)
	}

	reports := make([]SourceReport, 0, len(rollups))
	for key, r := range rollups {
		rep := SourceReport{
			SourceKey:     key,
			TotalEvents:   r.overall.Total,
			SuccessEvents: r.overall.Success,
			FailedEvents:  r.overall.Failed,
			ByType:        r.breakdown(),
		}
		if id, ok := identity[key]; ok {
			rep.SourceID, rep.SourceName, rep.Language = id.SourceID, id.SourceName, id.Language
		}
		if rep.SourceName == "" {
			rep.SourceName = key
		}
		if m, ok := metricsMap[key]; ok {
			rep.EwmaLatencyMs, rep.LastLatencyMs = m.EwmaLatencyMs, m.LastLatencyMs
		}
		if b, ok := breakers[key]; ok {
			rep.FailingSince = b.FailingSince
			rep.ConsecutiveFailures = b.ConsecutiveFailures
			rep.LastError = b.LastError
			rep.CooldownUntil = b.CooldownUntil
			rep.IsCoolingDown = b.IsCoolingDown(now)
		}
		reports = append(reports, rep)
	}

	sortReports(reports, order)
	return reports, nil
}

// sourceRollup accumulates one source's counts while folding the aggregate rows.
type sourceRollup struct {
	overall statusCounts
	perType map[entsourceevent.EventType]*statusCounts
}

// breakdown renders the per-type tallies as a stable slice, sorted by total desc
// then type — the SAME shape the overview uses.
func (r sourceRollup) breakdown() []TypeBreakdown {
	out := make([]TypeBreakdown, 0, len(r.perType))
	for t, c := range r.perType {
		out = append(out, TypeBreakdown{EventType: t, Total: c.Total, Success: c.Success, Failed: c.Failed})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		return out[i].EventType < out[j].EventType
	})
	return out
}

// sourceRollups runs the single (source_key, event_type, status) grouped
// aggregate and folds it into a per-source rollup map.
func (s *Service) sourceRollups(ctx context.Context, since time.Time) (map[string]*sourceRollup, error) {
	var rows []sourceTypeStatusRow
	err := s.client.SourceEvent.Query().
		Where(entsourceevent.CreatedAtGTE(since)).
		GroupBy(entsourceevent.FieldSourceKey, entsourceevent.FieldEventType, entsourceevent.FieldStatus).
		Aggregate(ent.As(ent.Count(), "count")).
		Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("reporting.Sources: rollup aggregate: %w", err)
	}

	out := make(map[string]*sourceRollup)
	for _, r := range rows {
		agg := out[r.SourceKey]
		if agg == nil {
			agg = &sourceRollup{perType: make(map[entsourceevent.EventType]*statusCounts)}
			out[r.SourceKey] = agg
		}
		agg.overall.add(r.Status, r.Count)
		c := agg.perType[r.EventType]
		if c == nil {
			c = &statusCounts{}
			agg.perType[r.EventType] = c
		}
		c.add(r.Status, r.Count)
	}
	return out, nil
}

// sourceIdentities runs the identity aggregate — the freshest (source_id,
// source_name, language) per source_key in the window — via GROUP BY
// source_key,source_id,source_name,language with MAX(created_at), folded to keep
// the most recent tuple per key. Bounded by the distinct-identity count, never a
// per-source query.
func (s *Service) sourceIdentities(ctx context.Context, since time.Time) (map[string]sourceIdentityRow, error) {
	var rows []sourceIdentityRow
	err := s.client.SourceEvent.Query().
		Where(entsourceevent.CreatedAtGTE(since)).
		GroupBy(
			entsourceevent.FieldSourceKey,
			entsourceevent.FieldSourceID,
			entsourceevent.FieldSourceName,
			entsourceevent.FieldLanguage,
		).
		Aggregate(ent.As(ent.Max(entsourceevent.FieldCreatedAt), "last_seen")).
		Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("reporting.Sources: identity aggregate: %w", err)
	}

	out := make(map[string]sourceIdentityRow, len(rows))
	for _, r := range rows {
		if best, ok := out[r.SourceKey]; !ok || r.LastSeen.After(best.LastSeen) {
			out[r.SourceKey] = r
		}
	}
	return out, nil
}

// sortReports orders the rollup per the requested sort, with source name as a
// stable tiebreak so equal primary keys are deterministic.
func sortReports(reports []SourceReport, order Sort) {
	sort.Slice(reports, func(i, j int) bool {
		a, b := reports[i], reports[j]
		switch order {
		case SortLatency:
			if a.EwmaLatencyMs != b.EwmaLatencyMs {
				return a.EwmaLatencyMs > b.EwmaLatencyMs
			}
		case SortEvents:
			if a.TotalEvents != b.TotalEvents {
				return a.TotalEvents > b.TotalEvents
			}
		case SortFailures:
			fallthrough
		default:
			if a.FailedEvents != b.FailedEvents {
				return a.FailedEvents > b.FailedEvents
			}
		}
		return a.SourceName < b.SourceName
	})
}
