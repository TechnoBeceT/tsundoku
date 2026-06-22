package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// ImportEntry holds the schema definition for the ImportEntry entity.
type ImportEntry struct {
	ent.Schema
}

// Fields of the ImportEntry.
func (ImportEntry) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		field.String("path").Unique(),
		field.String("status").Default("pending"),
		field.String("error").Default(""),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Edges of the ImportEntry.
func (ImportEntry) Edges() []ent.Edge {
	return nil
}
