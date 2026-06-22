package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// SourceEvent holds the schema definition for the SourceEvent entity.
type SourceEvent struct {
	ent.Schema
}

// Fields of the SourceEvent.
func (SourceEvent) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		field.String("source"),
		field.String("event_type"),
		field.String("payload").Default(""),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}

// Edges of the SourceEvent.
func (SourceEvent) Edges() []ent.Edge {
	return nil
}
