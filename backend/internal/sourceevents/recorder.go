// Package sourceevents records and purges the append-only source-operation audit
// log (the SourceEvent entity). It is the write + retention side of the Source
// Health Console substrate; the read/aggregation side lives with the reporting
// API (a later slice).
//
// Recording is BEST-EFFORT and fire-and-forget: a DB failure is logged and
// swallowed, never returned, so audit bookkeeping can never break or slow a
// search, download, refresh, or warm — the exact posture of internal/metrics'
// Recorder. Retention (PurgeOld) is a maintenance operation and DOES return its
// error so a scheduled caller can log the outcome.
package sourceevents

import (
	"context"
	"time"

	entsourceevent "github.com/technobecet/tsundoku/internal/ent/sourceevent"
)

// EventType and Status re-export the generated Ent enum types so callers name
// them through this package (sourceevents.EventSearch) without importing the
// generated ent enum package, while staying the SAME type the Ent setters expect
// (§2 DRY — one enum definition, in the schema).
type (
	// EventType classifies the source operation an event records.
	EventType = entsourceevent.EventType
	// Status is an event's binary outcome.
	Status = entsourceevent.Status
)

// The event-type + status constants, re-exported from the generated enum.
const (
	// EventSearch is a source search (one row per source per fan-out).
	EventSearch = entsourceevent.EventTypeSearch
	// EventDownload is a chapter download attempt (one row per source outcome).
	EventDownload = entsourceevent.EventTypeDownload
	// EventRefresh is a refresh-sweep re-fetch (one row per source-manga group).
	EventRefresh = entsourceevent.EventTypeRefresh
	// EventWarm is an anti-bot warm-up hit (one row per warmed source).
	EventWarm = entsourceevent.EventTypeWarm
	// EventBreakerTrip is a circuit-breaker 0->tripped transition.
	EventBreakerTrip = entsourceevent.EventTypeBreakerTrip
	// EventBreakerReset is a circuit-breaker recovery / manual-reset transition.
	EventBreakerReset = entsourceevent.EventTypeBreakerReset

	// StatusSuccess marks a successful operation.
	StatusSuccess = entsourceevent.StatusSuccess
	// StatusFailed marks a failed operation.
	StatusFailed = entsourceevent.StatusFailed
)

// Event is one source-operation observation to be logged. The writer converts
// Duration to whole milliseconds and, when Err is non-nil, fills error_message
// (truncated) + error_category (via errorclass.Classify) — so the classification
// lives in exactly one place. ItemsCount is optional (nil = not applicable).
type Event struct {
	// SourceKey is the canonical physical-source NAME — the join key across
	// events, breaker, and metrics (download.canonicalSourceKey).
	SourceKey string
	// SourceID is the numeric engine-host source id as a string ("" for disk).
	SourceID string
	// SourceName is the human display name at write time.
	SourceName string
	// Language is the source's language code ("" when unknown).
	Language string
	// Type classifies the operation (search / download / refresh / warm / breaker).
	Type EventType
	// Status is the binary outcome (success / failed).
	Status Status
	// Duration is the operation's wall-clock time (0 when not timed).
	Duration time.Duration
	// Err, when non-nil, sets error_message + error_category on the row.
	Err error
	// ItemsCount is the operation's result cardinality where meaningful; nil when
	// not applicable.
	ItemsCount *int
	// Metadata is small operation-specific forensic context (keyword, url, chapter).
	Metadata map[string]string
}

// Recorder is the write side of the audit log. It is satisfied by *Service and is
// threaded as a nil-guarded optional dependency into every emission site (like
// metrics.Recorder), so existing call sites and tests that do not attach a
// recorder simply record nothing. Both methods are best-effort and return
// nothing.
type Recorder interface {
	// Log records a single event (the convenience one-event form of LogBatch).
	Log(ctx context.Context, event Event)
	// LogBatch records a slice of events in one insert. It is the form the search
	// fan-out and the per-cycle download outcomes use.
	LogBatch(ctx context.Context, events []Event)
}
