package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// Owner holds the schema definition for the Owner entity.
type Owner struct {
	ent.Schema
}

// Fields of the Owner.
func (Owner) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		field.String("username").Unique(),
		field.String("password_hash").Sensitive(),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}

// Edges of the Owner.
func (Owner) Edges() []ent.Edge {
	return nil
}
