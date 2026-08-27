package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// SourceBreakerNotificationCursor is the cross-process publication lock for one
// physical source. Sourcegate takes a PostgreSQL write lock on this row while it
// publishes that source's durable notification records, so concurrent processes
// cannot emit later transitions ahead of earlier committed ones.
type SourceBreakerNotificationCursor struct {
	ent.Schema
}

// Fields of the SourceBreakerNotificationCursor.
func (SourceBreakerNotificationCursor) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		field.String("source_key").Unique(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Edges of the SourceBreakerNotificationCursor. None; source_key is its durable
// identity and its unique index is the row-lock target.
func (SourceBreakerNotificationCursor) Edges() []ent.Edge {
	return nil
}
