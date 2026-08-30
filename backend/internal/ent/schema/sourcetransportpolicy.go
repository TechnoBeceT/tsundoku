package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// SourceTransportPolicy stores optional per-source transport overrides. A nil
// field inherits its runtime default, so a row is created only when at least
// one override is explicit.
type SourceTransportPolicy struct {
	ent.Schema
}

// Fields of the SourceTransportPolicy.
func (SourceTransportPolicy) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		field.Int64("source_id").Unique(),
		field.Bool("reuse_bypass_session").Optional().Nillable(),
		field.Enum("image_connection_mode").Values("fresh", "reuse").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Edges of the SourceTransportPolicy. The source catalog remains owned by the
// engine host, so source_id is deliberately not an Ent edge.
func (SourceTransportPolicy) Edges() []ent.Edge { return nil }
