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
// by StartMatchDiskProvider when an async match/merge finishes. SeriesID names
// the affected series so the frontend refetches exactly that series' detail;
// Error is set (and non-empty) only when the background merge failed, so the UI
// can surface the failure instead of silently showing stale state.
type MergeEvent struct {
	SeriesID string `json:"seriesId"`
	Error    string `json:"error,omitempty"`
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
