package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// SourceThroughputPolicy stores optional download-throughput overrides for one
// immutable numeric engine source ID. A missing field inherits the matching
// runtime global setting; an explicit zero image delay disables image pacing.
//
// The table is an additive zero-data migration. Every non-identity field is
// nullable or defaulted, and existing source/provider rows are not rewritten.
type SourceThroughputPolicy struct {
	ent.Schema
}

// Fields of the SourceThroughputPolicy.
func (SourceThroughputPolicy) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		field.Int64("source_id"),
		field.Int("download_concurrency").Optional().Nillable(),
		field.Int64("image_request_delay_ms").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Indexes of the SourceThroughputPolicy.
func (SourceThroughputPolicy) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("source_id").Unique(),
	}
}

// Edges of the SourceThroughputPolicy.
func (SourceThroughputPolicy) Edges() []ent.Edge {
	return nil
}
