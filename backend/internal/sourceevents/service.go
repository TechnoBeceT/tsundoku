package sourceevents

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/technobecet/tsundoku/internal/ent"
	entsourceevent "github.com/technobecet/tsundoku/internal/ent/sourceevent"
	"github.com/technobecet/tsundoku/internal/pkg/errorclass"
)

// maxErrorLen caps a stored error_message so a pathologically long upstream
// message can't bloat an event row. Truncated messages keep their prefix (the
// useful part) — mirrors internal/metrics + internal/sourcegate.
const maxErrorLen = 512

// Service is the concrete Recorder plus the retention side of the audit log. It
// owns the Ent client (safe for concurrent use, so the methods are goroutine-
// safe). Writes are best-effort (log + swallow); PurgeOld returns its error.
type Service struct {
	client *ent.Client
}

// NewService builds a sourceevents Service over the Ent client.
func NewService(client *ent.Client) *Service {
	return &Service{client: client}
}

// Log records a single event. It is the convenience one-event form of LogBatch;
// like it, a DB failure is logged and swallowed.
func (s *Service) Log(ctx context.Context, event Event) {
	s.LogBatch(ctx, []Event{event})
}

// LogBatch inserts a slice of events in one bulk create. Best-effort: an empty
// batch is a no-op, and an insert failure is logged and swallowed — audit
// bookkeeping must never break or slow the caller. All rows share one insert;
// since every row is a fresh, generated-UUID append (no upsert, no per-row
// business validation beyond the enum values the callers control), a partial
// failure is not a concern the way it is for the metrics upsert path.
func (s *Service) LogBatch(ctx context.Context, events []Event) {
	if len(events) == 0 {
		return
	}
	builders := make([]*ent.SourceEventCreate, len(events))
	for i, e := range events {
		builders[i] = s.build(e)
	}
	if err := s.client.SourceEvent.CreateBulk(builders...).Exec(ctx); err != nil {
		slog.WarnContext(ctx, "sourceevents: LogBatch failed (best-effort, dropping)",
			"count", len(events), "err", err)
	}
}

// build maps one Event to a SourceEvent create builder. It converts the duration
// to whole milliseconds and, when Err is set, fills error_message (truncated) +
// error_category (errorclass.Classify) — the ONE place that derivation happens.
func (s *Service) build(e Event) *ent.SourceEventCreate {
	c := s.client.SourceEvent.Create().
		SetSourceKey(e.SourceKey).
		SetSourceID(e.SourceID).
		SetSourceName(e.SourceName).
		SetLanguage(e.Language).
		SetEventType(e.Type).
		SetStatus(e.Status).
		SetDurationMs(durationMs(e.Duration))
	if e.Err != nil {
		c.SetErrorMessage(truncateError(e.Err)).SetErrorCategory(errorclass.Classify(e.Err))
	}
	if e.ItemsCount != nil {
		c.SetItemsCount(*e.ItemsCount)
	}
	if len(e.Metadata) > 0 {
		c.SetMetadata(e.Metadata)
	}
	return c
}

// PurgeOld deletes every event strictly older than before and returns how many
// rows it removed. It is the retention sweep (called with now - retention_days).
// Unlike the best-effort writers it RETURNS its error so a scheduled caller can
// log the outcome.
func (s *Service) PurgeOld(ctx context.Context, before time.Time) (int, error) {
	n, err := s.client.SourceEvent.Delete().
		Where(entsourceevent.CreatedAtLT(before)).
		Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("sourceevents.PurgeOld: %w", err)
	}
	return n, nil
}

// durationMs converts a duration to whole milliseconds, clamping a negative value
// to 0 (guards a malformed sample from storing a nonsense duration).
func durationMs(d time.Duration) int64 {
	ms := d.Milliseconds()
	if ms < 0 {
		return 0
	}
	return ms
}

// truncateError renders an error, capping the stored message at maxErrorLen.
func truncateError(err error) string {
	msg := err.Error()
	if len(msg) > maxErrorLen {
		return msg[:maxErrorLen]
	}
	return msg
}
