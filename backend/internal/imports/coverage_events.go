package imports

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/technobecet/tsundoku/internal/sse"
)

// CoverageDoneEvent is the TERMINAL report of one coverage computation. The
// HTTP request that starts a slow computation returns before it finishes, so
// this is the only channel the outcome reaches the client on — the same
// contract library.dedup.done has (GAP-140).
type CoverageDoneEvent struct {
	SourceID string `json:"sourceId"`
	MangaURL string `json:"mangaUrl"`
	Status   string `json:"status"`
	Total    int    `json:"total,omitempty"`
	Error    string `json:"error,omitempty"`
}

// broadcastCoverageDone emits imports.coverage.done. A nil hub (the many
// read-only/test call sites that never attach one via WithHub) makes this a
// no-op. Encoding failures are discarded — a missing event beats crashing a
// background job (mirrors library.broadcastDedupSweep). Unreachable in
// practice: CoverageDoneEvent is strings + an int, which Marshal cannot fail
// on; documented rather than faked for coverage.
func (s *Service) broadcastCoverageDone(ctx context.Context, ev CoverageDoneEvent) {
	if s.hub == nil {
		return
	}
	raw, err := json.Marshal(ev)
	if err != nil {
		slog.WarnContext(ctx, "imports: could not encode coverage.done", "err", err)
		return
	}
	s.hub.Broadcast(sse.Event{Type: "imports.coverage.done", Data: json.RawMessage(raw)})
}
