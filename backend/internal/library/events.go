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
