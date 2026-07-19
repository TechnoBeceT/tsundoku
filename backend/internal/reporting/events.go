package reporting

import (
	"context"
	"fmt"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/ent/predicate"
	entsourceevent "github.com/technobecet/tsundoku/internal/ent/sourceevent"
)

// EventFilter is the parsed, validated query for a raw event feed. Every field is
// optional except SourceKey (the path param): a nil Status/EventType means "any",
// a zero Since means "no lower bound" (the full-history feed), and Limit/Offset
// paginate. SourceKey == AllSourcesKey selects the global feed (no source
// predicate).
type EventFilter struct {
	SourceKey string
	Status    *entsourceevent.Status
	EventType *entsourceevent.EventType
	Since     time.Time
	Limit     int
	Offset    int
}

// EventRecord is one audit-log row in service-domain shape (the handler maps it
// to the camelCase DTO). The three pointer fields are nil when the column is
// SQL NULL (a success has no error; a warm has no items count).
type EventRecord struct {
	ID            uuid.UUID
	SourceKey     string
	SourceID      string
	SourceName    string
	Language      string
	EventType     entsourceevent.EventType
	Status        entsourceevent.Status
	DurationMs    int64
	ErrorMessage  *string
	ErrorCategory *string
	ItemsCount    *int
	Metadata      map[string]string
	CreatedAt     time.Time
}

// EventFeed is a page of the event feed plus the total matching count (so the UI
// can render pagination without a second call).
type EventFeed struct {
	Total int
	Items []EventRecord
}

// predicates turns a filter into the Where clause shared by the count and the
// page query, so the total and the page can never diverge. The SourceKey
// predicate is omitted for the AllSourcesKey sentinel (the global feed).
func (f EventFilter) predicates() []predicate.SourceEvent {
	var ps []predicate.SourceEvent
	if f.SourceKey != "" && f.SourceKey != AllSourcesKey {
		ps = append(ps, entsourceevent.SourceKeyEQ(f.SourceKey))
	}
	if f.Status != nil {
		ps = append(ps, entsourceevent.StatusEQ(*f.Status))
	}
	if f.EventType != nil {
		ps = append(ps, entsourceevent.EventTypeEQ(*f.EventType))
	}
	if !f.Since.IsZero() {
		ps = append(ps, entsourceevent.CreatedAtGTE(f.Since))
	}
	return ps
}

// Events returns one filtered, paginated page of the raw event feed plus the
// total matching count. It runs exactly TWO queries regardless of page size — a
// COUNT and a LIMIT/OFFSET page — so it never loads the whole log. Rows are
// ordered newest-first, with id as a stable tiebreak so pagination is
// deterministic when many events share a created_at.
func (s *Service) Events(ctx context.Context, f EventFilter) (EventFeed, error) {
	ps := f.predicates()

	total, err := s.client.SourceEvent.Query().Where(ps...).Count(ctx)
	if err != nil {
		return EventFeed{}, fmt.Errorf("reporting.Events: count: %w", err)
	}

	rows, err := s.client.SourceEvent.Query().
		Where(ps...).
		Order(
			entsourceevent.ByCreatedAt(sql.OrderDesc()),
			entsourceevent.ByID(sql.OrderDesc()),
		).
		Limit(f.Limit).
		Offset(f.Offset).
		All(ctx)
	if err != nil {
		return EventFeed{}, fmt.Errorf("reporting.Events: page: %w", err)
	}

	items := make([]EventRecord, 0, len(rows))
	for _, r := range rows {
		items = append(items, toEventRecord(r))
	}
	return EventFeed{Total: total, Items: items}, nil
}

// toEventRecord maps a generated ent.SourceEvent to the service-domain record,
// preserving the NULL-ness of the three optional columns.
func toEventRecord(r *ent.SourceEvent) EventRecord {
	return EventRecord{
		ID:            r.ID,
		SourceKey:     r.SourceKey,
		SourceID:      r.SourceID,
		SourceName:    r.SourceName,
		Language:      r.Language,
		EventType:     r.EventType,
		Status:        r.Status,
		DurationMs:    r.DurationMs,
		ErrorMessage:  r.ErrorMessage,
		ErrorCategory: r.ErrorCategory,
		ItemsCount:    r.ItemsCount,
		Metadata:      r.Metadata,
		CreatedAt:     r.CreatedAt,
	}
}
