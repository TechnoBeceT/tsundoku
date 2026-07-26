package library

import (
	"encoding/json"

	"github.com/technobecet/tsundoku/internal/sse"
)

// ScanEvent is the SSE payload for scan.start / scan.progress / scan.done.
// scan.start sets only Total (0 until counted); scan.progress sets Processed/
// Total/Path/Found as each series is staged; scan.done carries the final tally
// (or Error, if the walk itself failed before any tally could be produced).
type ScanEvent struct {
	Processed int    `json:"processed,omitempty"`
	Total     int    `json:"total,omitempty"`
	Path      string `json:"path,omitempty"`
	Found     int    `json:"found,omitempty"`
	Error     string `json:"error,omitempty"`
}

// broadcastScan emits a scan SSE event. JSON-encoding failures are discarded —
// a missing event beats crashing the scan (mirrors refresh.Service.broadcast).
// Unreachable in practice: ScanEvent is ints+strings, which Marshal cannot fail
// on; documented rather than faked for coverage.
func (s *Service) broadcastScan(eventType string, data ScanEvent) {
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	s.hub.Broadcast(sse.Event{Type: eventType, Data: json.RawMessage(raw)})
}

// MergeEvent is the SSE payload for the provider.merged completion event emitted
// by StartMatchDiskProvider (single match) AND StartConsolidateProviders (Part B
// multi-provider consolidation) when an async merge finishes. SeriesID names the
// affected series so the frontend refetches exactly that series' detail; Error is
// set (and non-empty) only when the background merge failed, so the UI can surface
// the failure instead of silently showing stale state. Merged/Skipped carry a
// consolidation's per-provider summary (how many folded / how many fault-isolated
// skips); both are omitted (0) for the single match, which folds exactly one.
type MergeEvent struct {
	SeriesID string `json:"seriesId"`
	Error    string `json:"error,omitempty"`
	Merged   int    `json:"merged,omitempty"`
	Skipped  int    `json:"skipped,omitempty"`
}

// broadcastMerge emits the provider.merged SSE event. JSON-encoding failures are
// discarded — a missing event beats crashing the background goroutine (mirrors
// broadcastScan). Unreachable in practice: MergeEvent is strings only, which
// Marshal cannot fail on; documented rather than faked for coverage.
func (s *Service) broadcastMerge(data MergeEvent) {
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	s.hub.Broadcast(sse.Event{Type: "provider.merged", Data: json.RawMessage(raw)})
}

// DedupSweepEvent is the SSE payload for library.dedup.done — the TERMINAL
// summary of the library-wide provider dedup sweep (DedupAllProviders). The
// endpoint that starts the sweep answers 202 and detaches, so this event is the
// only way its outcome ever reaches the owner.
//
// The three counts are independent and mean different things (see
// DedupAllProviders): SeriesProcessed = series whose dedup ran to completion,
// Skipped = drifted pairs left alone because the linked twin has no chapter feed
// yet, Busy = series skipped because another merge held their latch. Busy is the
// one the owner acts on by simply running the sweep again. Error is set (and
// non-empty) only when the sweep itself failed, and is always a fixed
// caller-safe sentence — never raw error text (see sweepErrorText).
type DedupSweepEvent struct {
	SeriesProcessed int    `json:"seriesProcessed"`
	Merged          int    `json:"merged"`
	Skipped         int    `json:"skipped"`
	Busy            int    `json:"busy"`
	Error           string `json:"error,omitempty"`
}

// broadcastDedupSweep emits the library.dedup.done SSE event. JSON-encoding
// failures are discarded — a missing event beats crashing the background sweep
// (mirrors broadcastMerge). Unreachable in practice: DedupSweepEvent is ints +
// a string, which Marshal cannot fail on; documented rather than faked for
// coverage.
func (s *Service) broadcastDedupSweep(data DedupSweepEvent) {
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	s.hub.Broadcast(sse.Event{Type: "library.dedup.done", Data: json.RawMessage(raw)})
}
