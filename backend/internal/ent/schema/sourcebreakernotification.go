package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// SourceBreakerNotification is the durable publication record for one committed
// circuit-breaker transition. The breaker mutation and this row commit in the
// same transaction; sourcegate then publishes pending rows in numeric-ID order
// while holding the matching SourceBreakerNotificationCursor row. Published rows
// remain as compact delivery receipts, avoiding an automatic row-deletion path.
type SourceBreakerNotification struct {
	ent.Schema
}

// Fields of the SourceBreakerNotification. The implicit integer ID is a
// PostgreSQL sequence and supplies the transition order for one source: breaker
// mutations on that source already serialize through SourceCircuitState's row
// lock, so their IDs are allocated in the same order they can commit.
func (SourceBreakerNotification) Fields() []ent.Field {
	return []ent.Field{
		field.String("source_key"),
		field.String("event_type"),
		field.String("status"),
		field.String("error_message").Optional().Nillable(),
		field.String("error_category").Default(""),
		field.Bool("event_requested").Default(false),
		field.Bool("hook_requested").Default(false),
		field.Int("consecutive_failures").Default(0),
		field.Time("cooldown_until").Optional().Nillable(),
		field.Time("failing_since").Optional().Nillable(),
		field.String("last_error").Default(""),
		// Each requested effect has its own receipt. A successful audit insert is
		// never repeated merely because the later summary hook failed, and neither
		// effect can make the aggregate receipt true before both are complete.
		field.Time("event_published_at").Optional().Nillable(),
		field.Time("hook_published_at").Optional().Nillable(),
		// Failed delivery attempts remain ordered at the head of this source's
		// stream. next_attempt_at supplies bounded exponential retry backoff;
		// publication_error is cleared after all requested effects succeed.
		field.Int("publication_attempts").Default(0),
		field.Time("next_attempt_at").Optional().Nillable(),
		field.String("publication_error").Optional().Nillable(),
		field.Time("published_at").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}

// Edges of the SourceBreakerNotification. None: source_key is the same
// denormalized physical-source name used by SourceCircuitState and SourceEvent.
func (SourceBreakerNotification) Edges() []ent.Edge {
	return nil
}

// Indexes support the pending-per-source publication query and startup replay.
func (SourceBreakerNotification) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("source_key", "published_at"),
		index.Fields("published_at"),
	}
}
