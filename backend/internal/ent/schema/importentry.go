package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// ImportEntry stages a series discovered on disk by a library scan so the owner
// can review and import it incrementally. `path` (the series directory) is the
// stable identity; a re-scan upserts by it. `found` is the scanned
// disk.SeriesFacts snapshot; `matched_source` records an owner-chosen Suwayomi
// source to attach at import time.
type ImportEntry struct{ ent.Schema }

func (ImportEntry) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		field.String("path").Unique(),
		field.String("title").Default(""),
		field.String("category").Default(""),
		field.String("status").Default("pending"),
		field.Int("chapter_count").Default(0),
		field.JSON("found", map[string]any{}).Optional(),
		field.JSON("matched_source", map[string]any{}).Optional(),
		field.Time("scanned_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (ImportEntry) Edges() []ent.Edge { return nil }

func (ImportEntry) Indexes() []ent.Index {
	return []ent.Index{index.Fields("status")}
}
