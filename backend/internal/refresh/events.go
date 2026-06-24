package refresh

import (
	"encoding/json"

	"github.com/technobecet/tsundoku/internal/sse"
)

// RefreshEvent is the SSE payload for refresh.start / refresh.done. On
// refresh.start only Monitored is set; refresh.done carries the full summary.
type RefreshEvent struct {
	Monitored          int `json:"monitored,omitempty"`
	SeriesRefreshed    int `json:"seriesRefreshed,omitempty"`
	ProvidersRefreshed int `json:"providersRefreshed,omitempty"`
	NewChapters        int `json:"newChapters,omitempty"`
	Errors             int `json:"errors,omitempty"`
}

// broadcast emits a refresh SSE event. JSON-encoding failures are discarded — a
// missing event beats crashing the sweep (mirrors job.Runner.broadcastCycle).
func (s *Service) broadcast(eventType string, data RefreshEvent) {
	raw, err := json.Marshal(data)
	if err != nil {
		// Unreachable: RefreshEvent is all ints; Marshal cannot fail. Documented.
		return
	}
	s.hub.Broadcast(sse.Event{Type: eventType, Data: json.RawMessage(raw)})
}
