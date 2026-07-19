package reporting

import (
	"strings"

	"github.com/technobecet/tsundoku/internal/ent"
	entsourceevent "github.com/technobecet/tsundoku/internal/ent/sourceevent"
	"github.com/technobecet/tsundoku/internal/metrics"
	"github.com/technobecet/tsundoku/internal/sourcegate"
)

// slowestLimit caps the "slowest sources" leaderboard in the overview — enough
// to spot the offenders without turning the KPI card into a full table.
const slowestLimit = 5

// recentErrorsLimit caps the "recent errors" preview in the overview — the last
// handful of failures; the full forensic feed is the events endpoint.
const recentErrorsLimit = 10

// Service aggregates the SourceEvent audit log into the reporting views. It owns
// the Ent client (concurrency-safe, so the read methods are goroutine-safe) plus
// the two rolling side-tables the event log does not carry: the metrics EWMA
// snapshot (search latency) and the circuit-breaker state (failing-since /
// cooldown), both joined in by canonical source key.
type Service struct {
	client  *ent.Client
	metrics *metrics.Service
	gate    *sourcegate.Service
}

// NewService builds a reporting Service over the Ent client, the metrics reader
// (for the latency join + the slowest-sources leaderboard), and the source
// politeness gate (for the failing-since / cooldown join).
func NewService(client *ent.Client, metricsSvc *metrics.Service, gate *sourcegate.Service) *Service {
	return &Service{client: client, metrics: metricsSvc, gate: gate}
}

// trimKey is the canonical source key: TrimSpace of a display name — the SAME
// key the event log (source_key), the breaker, and the metrics side all use, so
// the three tables join on one identity.
func trimKey(name string) string {
	return strings.TrimSpace(name)
}

// metricsByKey keys a metric-snapshot slice by canonical source key so the
// latency stats join onto an event rollup.
func metricsByKey(rows []*ent.SourceMetric) map[string]*ent.SourceMetric {
	out := make(map[string]*ent.SourceMetric, len(rows))
	for _, m := range rows {
		out[trimKey(m.SourceName)] = m
	}
	return out
}

// statusCounts folds one (…, status) aggregate group into running totals. It is
// the ONE place the success/failed/total tally is derived, reused by the
// overview KPIs, the per-type breakdown, and the per-source rollup, so the three
// can never disagree on how a status maps to a bucket.
type statusCounts struct {
	Total   int
	Success int
	Failed  int
}

// add records count events of the given status.
func (c *statusCounts) add(status entsourceevent.Status, count int) {
	c.Total += count
	switch status {
	case entsourceevent.StatusSuccess:
		c.Success += count
	case entsourceevent.StatusFailed:
		c.Failed += count
	}
}

// successRate returns the fraction of events that succeeded (0 when none), as a
// 0..1 value the DTO layer renders as a percentage.
func (c statusCounts) successRate() float64 {
	if c.Total == 0 {
		return 0
	}
	return float64(c.Success) / float64(c.Total)
}
