package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// GlobalRuntimeIntent records the desired and applied revision of Tsundoku's
// global engine configuration. It is distinct from SourceRuntimeIntent so a
// settings-only deployment with no catalog sources still has durable retry
// state and source-specific reconciliation never receives a synthetic ID.
type GlobalRuntimeIntent struct {
	ent.Schema
}

// Fields of the GlobalRuntimeIntent.
func (GlobalRuntimeIntent) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		field.String("scope").Unique().Immutable(),
		field.Int64("desired_revision").Default(0),
		field.Int64("applied_revision").Default(0),
		field.Time("last_apply_attempt").Optional().Nillable(),
		field.String("last_apply_error").MaxLen(512).Default(""),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Edges of the GlobalRuntimeIntent.
func (GlobalRuntimeIntent) Edges() []ent.Edge { return nil }
