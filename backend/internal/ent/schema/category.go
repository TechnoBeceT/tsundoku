package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// Category holds the schema definition for the Category entity.
//
// A Category is a user-managed, top-level library folder grouping series by
// publication type (e.g. Manga, Manhwa) or any owner-defined bucket. It replaces
// the former fixed Series.category enum: categories can be created, renamed,
// reordered, and deleted (when empty) by the owner. The category name is
// disk-folder-determining (<storage>/<name>/<title>/) and Komga-facing, so a
// rename physically moves the folder (see disk.RenameCategory).
type Category struct {
	ent.Schema
}

// Fields of the Category.
func (Category) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		// name is the human-facing label AND the on-disk folder name (verbatim,
		// not slugified). It is unique and filesystem-safe (validated in the
		// category service before any write).
		field.String("name").Unique(),
		// sort_order controls the owner's preferred display order (ascending);
		// ties break by name. Pure presentation — never disk-determining.
		field.Int("sort_order").Default(0),
		// protected marks a category that can never be RENAMED (the seeded "Other"
		// fallback). It is NOT the delete-guard — deletion is guarded by is_default
		// instead — so a demoted "Other" (protected but no longer the default)
		// becomes deletable while staying unrenameable.
		field.Bool("protected").Default(false),
		// is_default marks the single category that new / uncategorized series land
		// in and that can never be deleted. EXACTLY ONE row carries is_default=true,
		// maintained by category.EnsureDefaults at startup. It replaces the former
		// hardcoded "Other" fallback: the owner can promote any category to the
		// default, which then makes the previous default deletable.
		field.Bool("is_default").Default(false),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Edges of the Category.
func (Category) Edges() []ent.Edge {
	return []ent.Edge{
		// series is the O2M back-reference: every Series filed under this
		// category. The FK lives on Series.category_id (the inverse edge).
		edge.To("series", Series.Type),
	}
}
