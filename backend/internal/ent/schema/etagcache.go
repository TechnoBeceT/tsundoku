package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// EtagCache holds the schema definition for the EtagCache entity.
type EtagCache struct {
	ent.Schema
}

// Fields of the EtagCache.
func (EtagCache) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		field.String("url").Unique(),
		field.String("etag").Default(""),
		field.String("last_modified").Default(""),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Edges of the EtagCache.
func (EtagCache) Edges() []ent.Edge {
	return nil
}
