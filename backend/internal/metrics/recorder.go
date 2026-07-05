// Package metrics records and reads a rolling per-source search-performance
// snapshot (the SourceMetric entity). It answers "which sources are slow" so the
// anti-bot warm-up job (internal/warmup) can target only the cold/slow ones, and
// it backs the GET /api/sources/metrics screen.
//
// Recording is BEST-EFFORT: a DB failure is logged and swallowed, never returned
// to the caller, so metrics collection can never break or slow down a search or a
// warm pass. Reads (List/Snapshot) return errors normally.
package metrics

import (
	"context"
	"time"
)

// Recorder is the write side of the metrics store. It is satisfied by *Service.
// Both methods are best-effort and return nothing: on any DB failure they log and
// return, so a caller (search fan-out, warm-up job) is never blocked or failed by
// metrics bookkeeping.
type Recorder interface {
	// Record folds one observation (one source's latency + outcome) into that
	// source's rolling snapshot, creating the row on first sight.
	Record(ctx context.Context, sourceID, sourceName string, latency time.Duration, sourceErr error)
	// RecordBatch folds a slice of observations, one per source. It is the form
	// the search fan-out uses: it collects one sample per source in-memory and
	// records them all after the fan-out completes.
	RecordBatch(ctx context.Context, samples []Sample)
}

// Sample is one source's observed search outcome: how long the call took and
// whether it failed. Err is nil on success. A deadline-dropped source still
// carries its (long) latency — that long latency is exactly the signal that
// flags the source slow, so it is fed into the EWMA regardless of Err.
type Sample struct {
	SourceID   string
	SourceName string
	Latency    time.Duration
	Err        error
}
