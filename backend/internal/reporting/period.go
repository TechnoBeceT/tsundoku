// Package reporting is the READ / aggregation side of the Source Health Console
// substrate: it turns the append-only SourceEvent audit log (written by
// internal/sourceevents) into the four reporting views the console renders — a
// period KPI overview, a per-source rollup, a paginated raw event feed, and a
// time-bucketed success/fail timeline.
//
// EVERY aggregation is computed IN THE DATABASE (Ent GroupBy/Aggregate + a small
// date_trunc SQL modifier for the timeline buckets) — the whole event table is
// NEVER loaded into Go and folded row-by-row (the mistake this substrate exists
// to avoid; a 30-day log on a 1000-series library is far too large for that). The
// in-memory work is confined to folding an already-aggregated result (at most a
// few rows per source / per bucket) into the display shape, and to joining the
// two rolling side-tables (metrics EWMA, breaker state) that live outside the
// event log.
package reporting

import "time"

// AllSourcesKey is the sentinel :sourceKey path value that selects the GLOBAL
// feed / timeline across every source, instead of one source's rows. The events
// + timeline query builders treat it as "apply no source_key predicate".
const AllSourcesKey = "__all__"

// Period is a validated reporting time-window token. The handler's validator
// rejects any value outside the three constants (a 400) before it reaches the
// service, so Since can assume a known token and never has to error.
type Period string

// The three offered reporting windows.
const (
	// Period24h is the trailing 24 hours.
	Period24h Period = "24h"
	// Period7d is the trailing 7 days.
	Period7d Period = "7d"
	// Period30d is the trailing 30 days (the retention horizon).
	Period30d Period = "30d"
)

// Since returns the inclusive lower bound of the window: now minus the period's
// duration. An unrecognised Period (which the handler validator prevents)
// defaults to the 24h window — a safe, bounded fallback rather than the whole
// table.
func (p Period) Since(now time.Time) time.Time {
	switch p {
	case Period7d:
		return now.Add(-7 * 24 * time.Hour)
	case Period30d:
		return now.Add(-30 * 24 * time.Hour)
	case Period24h:
		return now.Add(-24 * time.Hour)
	default:
		return now.Add(-24 * time.Hour)
	}
}

// Sort selects the ordering of the per-source rollup (Sources).
type Sort string

// The three offered rollup sort orders.
const (
	// SortFailures orders by failed-event count, descending (the default —
	// "which sources are breaking").
	SortFailures Sort = "failures"
	// SortLatency orders by EWMA search latency, descending ("which are slow").
	SortLatency Sort = "latency"
	// SortEvents orders by total event count, descending ("which are busiest").
	SortEvents Sort = "events"
)

// Bucket is a validated timeline granularity.
type Bucket string

// The two offered timeline granularities.
const (
	// BucketHour buckets the timeline by hour (suits the 24h window).
	BucketHour Bucket = "hour"
	// BucketDay buckets the timeline by day (suits the 7d / 30d windows).
	BucketDay Bucket = "day"
)

// SQL returns the date_trunc unit literal for the bucket. Only the two closed
// values are reachable (the handler validator rejects anything else), so the
// returned literal is ALWAYS a safe constant to interpolate into the timeline
// aggregation expression — never user-controlled free text.
func (b Bucket) SQL() string {
	if b == BucketDay {
		return "day"
	}
	return "hour"
}
