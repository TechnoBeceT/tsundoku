package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// LatestSeries holds the schema definition for the LatestSeries entity.
type LatestSeries struct {
	ent.Schema
}

// Fields of the LatestSeries.
func (LatestSeries) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		field.UUID("series_id", uuid.UUID{}).Unique(),
		field.String("provider"),
		field.Int("rank").Default(0),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Edges of the LatestSeries.
func (LatestSeries) Edges() []ent.Edge {
	return nil
}
