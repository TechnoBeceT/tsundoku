package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// SourceRuntimeIntent records the desired and applied engine runtime revision
// for one source. It survives policy-row deletion so an acknowledged runtime
// state is not forgotten when the source returns to inherited defaults.
type SourceRuntimeIntent struct {
	ent.Schema
}

// Fields of the SourceRuntimeIntent.
func (SourceRuntimeIntent) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		field.Int64("source_id").Unique(),
		field.Int64("desired_revision").Default(0),
		field.Int64("applied_revision").Default(0),
		field.Time("last_apply_attempt").Optional().Nillable(),
		field.String("last_apply_error").Default(""),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Edges of the SourceRuntimeIntent. source_id is owned by the live engine
// catalog rather than an Ent entity.
func (SourceRuntimeIntent) Edges() []ent.Edge { return nil }
